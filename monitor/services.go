package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Service statuses. Stopped is a service someone turned off on purpose, such
// as a pm2 process or container that is not running, which is not an alarm.
const (
	StatusUp      = "up"
	StatusWarn    = "warn"
	StatusDown    = "down"
	StatusStopped = "stopped"
)

// Service is the state of one thing the Pi runs, as its last check found it.
type Service struct {
	Group    string            `json:"group"` // system, pm2, docker, redis, database
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Detail   string            `json:"detail,omitempty"`
	CPU      *float64          `json:"cpu,omitempty"`    // percent
	MemMB    *float64          `json:"mem_mb,omitempty"` // MiB
	Restarts *int              `json:"restarts,omitempty"`
	Since    int64             `json:"since,omitempty"` // Unix seconds it has been in this state
	Stats    map[string]string `json:"stats,omitempty"`
}

func (s Service) key() string { return s.Group + "/" + s.Name }

// A check finds the state of one group of services. When the group itself
// cannot be read (pm2 is missing, Docker is down) it returns one Service for
// the group, not a guess about each member.
type check func(ctx context.Context) []Service

const commandTimeout = 10 * time.Second

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func groupFailure(group string, err error) []Service {
	return []Service{{Group: group, Name: group, Status: StatusDown, Detail: err.Error()}}
}

// systemdCheck reports the given units' ActiveState.
func systemdCheck(units []string) check {
	return func(ctx context.Context) []Service {
		args := []string{"show", "--timestamp=unix", "-p", "Id", "-p", "ActiveState", "-p", "SubState", "-p", "StateChangeTimestamp"}
		out, err := run(ctx, "systemctl", append(args, units...)...)
		if err != nil && len(out) == 0 {
			return groupFailure("system", err)
		}
		return parseSystemctlShow(string(out), units)
	}
}

// parseSystemctlShow reads `systemctl show` blocks, one per unit, separated
// by blank lines. The Id is the unit's full name, so the name is taken from
// the order the units were asked for.
func parseSystemctlShow(out string, units []string) []Service {
	services := []Service{}
	for i, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		if i >= len(units) {
			break
		}
		props := map[string]string{}
		for line := range strings.Lines(block) {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			props[k] = v
		}
		s := Service{Group: "system", Name: units[i], Detail: props["SubState"]}
		switch props["ActiveState"] {
		case "active":
			s.Status = StatusUp
		case "activating", "reloading", "deactivating":
			s.Status = StatusWarn
		default:
			s.Status = StatusDown
			s.Detail = strings.TrimSpace(props["ActiveState"] + " " + props["SubState"])
		}
		if ts, err := strconv.ParseInt(strings.TrimPrefix(props["StateChangeTimestamp"], "@"), 10, 64); err == nil && ts > 0 {
			s.Since = ts
		}
		services = append(services, s)
	}
	return services
}

// pm2Check lists every pm2 process. pm2 lives in nvm's folder, which is on
// the PATH pm2 starts go-server with.
func pm2Check(ctx context.Context) []Service {
	out, err := run(ctx, "pm2", "jlist")
	if err != nil {
		return groupFailure("pm2", err)
	}
	services, err := parsePM2(out)
	if err != nil {
		return groupFailure("pm2", err)
	}
	return services
}

func parsePM2(out []byte) ([]Service, error) {
	// pm2 can print notices, such as "[PM2] ...", before the JSON, which is
	// on a line of its own.
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "[") && json.Valid([]byte(line)) {
			out = []byte(line)
			break
		}
	}
	// Only these fields are read: jlist also carries each process's
	// environment, secrets included, which must go no further.
	var procs []struct {
		Name  string `json:"name"`
		Monit struct {
			CPU    float64 `json:"cpu"`
			Memory float64 `json:"memory"`
		} `json:"monit"`
		Env struct {
			Status   string `json:"status"`
			Restarts int    `json:"restart_time"`
			Uptime   int64  `json:"pm_uptime"` // Unix ms it last started
		} `json:"pm2_env"`
	}
	if err := json.Unmarshal(out, &procs); err != nil {
		return nil, fmt.Errorf("reading pm2 jlist: %w", err)
	}
	services := []Service{}
	for _, p := range procs {
		s := Service{Group: "pm2", Name: p.Name, Detail: p.Env.Status}
		restarts := p.Env.Restarts
		s.Restarts = &restarts
		switch p.Env.Status {
		case "online":
			s.Status = StatusUp
			cpu, mem := p.Monit.CPU, p.Monit.Memory/(1<<20)
			s.CPU, s.MemMB = &cpu, &mem
			s.Since = p.Env.Uptime / 1000
			s.Detail = ""
		case "stopped":
			s.Status = StatusStopped
		case "launching", "stopping", "waiting restart", "one-launch-status":
			s.Status = StatusWarn
		default: // errored
			s.Status = StatusDown
		}
		services = append(services, s)
	}
	return services, nil
}

// dockerCheck lists every container, running or not.
func dockerCheck(ctx context.Context) []Service {
	out, err := run(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return groupFailure("docker", err)
	}
	return parseDockerPS(out)
}

func parseDockerPS(out []byte) []Service {
	services := []Service{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		var c struct{ Names, State, Status, Image string }
		if json.Unmarshal(sc.Bytes(), &c) != nil {
			continue
		}
		s := Service{Group: "docker", Name: c.Names, Detail: c.Status}
		switch {
		case c.State == "running" && strings.Contains(c.Status, "(unhealthy)"):
			s.Status = StatusWarn
		case c.State == "running":
			s.Status = StatusUp
		case c.State == "restarting" || c.State == "paused" || c.State == "created":
			s.Status = StatusWarn
		case c.State == "exited" && strings.HasPrefix(c.Status, "Exited (0)"):
			s.Status = StatusStopped
		default: // exited with an error, dead, removing
			s.Status = StatusDown
		}
		services = append(services, s)
	}
	return services
}

// redisCheck sends INFO to the Redis server at addr.
func redisCheck(addr string) check {
	return func(ctx context.Context) []Service {
		s := Service{Group: "redis", Name: "redis"}
		info, err := redisInfo(ctx, addr)
		if err != nil {
			s.Status, s.Detail = StatusDown, err.Error()
			return []Service{s}
		}
		s.Status = StatusUp
		keys := 0
		for k, v := range info {
			if strings.HasPrefix(k, "db") {
				// db0:keys=57,expires=3,avg_ttl=0
				n, _ := strconv.Atoi(strings.TrimPrefix(strings.Split(v, ",")[0], "keys="))
				keys += n
			}
		}
		s.Stats = map[string]string{
			"memory":      info["used_memory_human"],
			"peak memory": info["used_memory_peak_human"],
			"clients":     info["connected_clients"],
			"ops/s":       info["instantaneous_ops_per_sec"],
			"keys":        strconv.Itoa(keys),
			"version":     info["redis_version"],
			"hit rate":    redisHitRate(info),
		}
		s.Detail = fmt.Sprintf("%s · %d keys", info["used_memory_human"], keys)
		if up, err := strconv.ParseInt(info["uptime_in_seconds"], 10, 64); err == nil {
			s.Since = time.Now().Unix() - up
		}
		if mem, err := strconv.ParseFloat(info["used_memory"], 64); err == nil {
			mem /= 1 << 20
			s.MemMB = &mem
		}
		return []Service{s}
	}
}

func redisHitRate(info map[string]string) string {
	hits, _ := strconv.ParseFloat(info["keyspace_hits"], 64)
	misses, _ := strconv.ParseFloat(info["keyspace_misses"], 64)
	if hits+misses == 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f%%", 100*hits/(hits+misses))
}

// redisInfo speaks just enough RESP to send INFO and read its bulk reply.
func redisInfo(ctx context.Context, addr string) (map[string]string, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("*1\r\n$4\r\nINFO\r\n")); err != nil {
		return nil, err
	}
	rd := bufio.NewReader(conn)
	header, err := rd.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(header, "$") {
		return nil, fmt.Errorf("redis: %s", strings.TrimSpace(strings.TrimPrefix(header, "-")))
	}
	size, err := strconv.Atoi(strings.TrimSpace(header[1:]))
	if err != nil || size < 0 {
		return nil, fmt.Errorf("redis: unexpected reply %q", header)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(rd, body); err != nil {
		return nil, err
	}
	return parseRedisInfo(string(body)), nil
}

func parseRedisInfo(body string) map[string]string {
	info := map[string]string{}
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			info[k] = v
		}
	}
	return info
}

// sqliteCheck reports the size of each SQLite database file, with its
// write-ahead log.
func sqliteCheck(paths []string) check {
	return func(ctx context.Context) []Service {
		services := []Service{}
		for _, p := range paths {
			s := Service{Group: "database", Name: filepath.Base(p)}
			info, err := os.Stat(p)
			if err != nil {
				s.Status, s.Detail = StatusDown, "missing"
				services = append(services, s)
				continue
			}
			size := info.Size()
			if wal, err := os.Stat(p + "-wal"); err == nil {
				size += wal.Size()
			}
			s.Status = StatusUp
			s.Detail = formatBytes(size)
			s.Stats = map[string]string{"bytes": strconv.FormatInt(size, 10)}
			services = append(services, s)
		}
		return services
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
