package monitor

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// Sample is one reading of the machine. Rates are averages since the previous
// reading. A value that could not be read is NaN and is stored as NULL.
type Sample struct {
	TS        int64   // Unix seconds
	CPU       float64 // percent busy, all cores
	Temp      float64 // °C
	Load1     float64
	MemUsed   float64 // MiB, total minus available
	MemTotal  float64 // MiB
	SwapUsed  float64 // MiB
	DiskUsed  float64 // GiB on /
	DiskTotal float64 // GiB on /
	DiskRead  float64 // kB/s, physical disks
	DiskWrite float64 // kB/s
	NetRx     float64 // kB/s, physical interfaces
	NetTx     float64 // kB/s
}

// Metrics names a Sample's values, in the order values returns them. They are
// the column names in every table and the keys in every response.
var Metrics = []string{
	"cpu", "temp", "load1", "mem_used", "mem_total", "swap_used",
	"disk_used", "disk_total", "disk_read", "disk_write", "net_rx", "net_tx",
}

func (s Sample) values() []float64 {
	return []float64{
		s.CPU, s.Temp, s.Load1, s.MemUsed, s.MemTotal, s.SwapUsed,
		s.DiskUsed, s.DiskTotal, s.DiskRead, s.DiskWrite, s.NetRx, s.NetTx,
	}
}

func sampleFromValues(ts int64, v []float64) Sample {
	return Sample{
		TS: ts, CPU: v[0], Temp: v[1], Load1: v[2], MemUsed: v[3], MemTotal: v[4], SwapUsed: v[5],
		DiskUsed: v[6], DiskTotal: v[7], DiskRead: v[8], DiskWrite: v[9], NetRx: v[10], NetTx: v[11],
	}
}

// MarshalJSON writes the sample as metric name to value, with null for NaN.
func (s Sample) MarshalJSON() ([]byte, error) {
	b := []byte(`{"ts":` + strconv.FormatInt(s.TS, 10))
	for i, v := range s.values() {
		b = append(b, `,"`+Metrics[i]+`":`...)
		b = appendNumber(b, v)
	}
	return append(b, '}'), nil
}

func appendNumber(b []byte, v float64) []byte {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return append(b, "null"...)
	}
	return strconv.AppendFloat(b, math.Round(v*100)/100, 'f', -1, 64)
}

// meanSample averages samples metric by metric, skipping unknown values.
func meanSample(ts int64, samples []Sample) Sample {
	sums := make([]float64, len(Metrics))
	counts := make([]int, len(Metrics))
	for _, s := range samples {
		for i, v := range s.values() {
			if !math.IsNaN(v) {
				sums[i] += v
				counts[i]++
			}
		}
	}
	for i := range sums {
		if counts[i] == 0 {
			sums[i] = math.NaN()
		} else {
			sums[i] /= float64(counts[i])
		}
	}
	return sampleFromValues(ts, sums)
}

// counters are the cumulative kernel counters rates are worked out from.
type counters struct {
	cpuBusy, cpuTotal   uint64
	diskRead, diskWrite uint64 // sectors
	netRx, netTx        uint64 // bytes
}

// sysReader reads the machine through root, "/" outside tests.
type sysReader struct {
	root   string
	prev   *counters
	prevTS float64 // seconds, sub-second precision
}

func (r *sysReader) path(p string) string { return filepath.Join(r.root, p) }

func (r *sysReader) readFile(p string) string {
	b, err := os.ReadFile(r.path(p))
	if err != nil {
		return ""
	}
	return string(b)
}

// read takes a Sample at now (Unix seconds). The first read has no previous
// counters, so its rates are NaN.
func (r *sysReader) read(now float64) Sample {
	s := Sample{TS: int64(now)}
	nan := math.NaN()

	s.Temp = nan
	if t, err := strconv.ParseFloat(strings.TrimSpace(r.readFile("sys/class/thermal/thermal_zone0/temp")), 64); err == nil {
		s.Temp = t / 1000
	}

	s.Load1 = nan
	if f := strings.Fields(r.readFile("proc/loadavg")); len(f) > 0 {
		if l, err := strconv.ParseFloat(f[0], 64); err == nil {
			s.Load1 = l
		}
	}

	s.MemUsed, s.MemTotal, s.SwapUsed = parseMeminfo(r.readFile("proc/meminfo"))

	s.DiskUsed, s.DiskTotal = nan, nan
	var fs syscall.Statfs_t
	if err := syscall.Statfs(r.path("."), &fs); err == nil {
		const gib = 1 << 30
		bsize := float64(fs.Bsize)
		s.DiskTotal = float64(fs.Blocks) * bsize / gib
		s.DiskUsed = float64(fs.Blocks-fs.Bfree) * bsize / gib
	}

	c := counters{}
	c.cpuBusy, c.cpuTotal = parseCPU(r.readFile("proc/stat"))
	c.diskRead, c.diskWrite = parseDiskstats(r.readFile("proc/diskstats"), r.isPhysicalDisk)
	c.netRx, c.netTx = parseNetDev(r.readFile("proc/net/dev"))

	s.CPU, s.DiskRead, s.DiskWrite, s.NetRx, s.NetTx = nan, nan, nan, nan, nan
	if r.prev != nil && now > r.prevTS {
		dt := now - r.prevTS
		p := r.prev
		if total := delta(c.cpuTotal, p.cpuTotal); total > 0 {
			s.CPU = 100 * delta(c.cpuBusy, p.cpuBusy) / total
		}
		// A sector is 512 bytes whatever the disk's own sector size.
		s.DiskRead = delta(c.diskRead, p.diskRead) * 512 / 1024 / dt
		s.DiskWrite = delta(c.diskWrite, p.diskWrite) * 512 / 1024 / dt
		s.NetRx = delta(c.netRx, p.netRx) / 1024 / dt
		s.NetTx = delta(c.netTx, p.netTx) / 1024 / dt
	}
	r.prev, r.prevTS = &c, now
	return s
}

// delta is how far a counter moved, 0 if it went backwards (a reset).
func delta(now, before uint64) float64 {
	if now < before {
		return 0
	}
	return float64(now - before)
}

// parseCPU reads the busy and total jiffies from /proc/stat's "cpu" line.
// Idle and iowait count as not busy.
func parseCPU(stat string) (busy, total uint64) {
	for line := range strings.Lines(stat) {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		for i, field := range f[1:] {
			// guest and guest_nice are already counted in user and nice.
			if i >= 8 {
				break
			}
			n, _ := strconv.ParseUint(field, 10, 64)
			total += n
			if i != 3 && i != 4 {
				busy += n
			}
		}
		return busy, total
	}
	return 0, 0
}

// parseMeminfo returns used memory (total minus available), total memory and
// used swap, in MiB.
func parseMeminfo(meminfo string) (used, total, swapUsed float64) {
	kb := map[string]float64{}
	sc := bufio.NewScanner(strings.NewReader(meminfo))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 {
			if n, err := strconv.ParseFloat(f[0], 64); err == nil {
				kb[name] = n
			}
		}
	}
	total, ok := kb["MemTotal"]
	if !ok {
		return math.NaN(), math.NaN(), math.NaN()
	}
	return (total - kb["MemAvailable"]) / 1024, total / 1024, (kb["SwapTotal"] - kb["SwapFree"]) / 1024
}

// parseDiskstats sums sectors read and written over the disks physical
// accepts, leaving out partitions, which would count the same I/O twice.
func parseDiskstats(diskstats string, physical func(string) bool) (read, written uint64) {
	for line := range strings.Lines(diskstats) {
		f := strings.Fields(line)
		if len(f) < 10 || !physical(f[2]) {
			continue
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		read += r
		written += w
	}
	return read, written
}

// isPhysicalDisk reports whether name is a whole disk backed by a device, so
// not a partition, loop or ram disk.
func (r *sysReader) isPhysicalDisk(name string) bool {
	_, err := os.Stat(r.path(filepath.Join("sys/block", name, "device")))
	return err == nil
}

// parseNetDev sums bytes received and sent over the real interfaces, leaving
// out loopback and Docker's bridges and veths, whose traffic also crosses a
// real interface or never leaves the machine.
func parseNetDev(netdev string) (rx, tx uint64) {
	for line := range strings.Lines(netdev) {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// Host is what the dashboard shows about the machine itself.
type Host struct {
	Hostname string  `json:"hostname"`
	Kernel   string  `json:"kernel"`
	Cores    int     `json:"cores"`
	Uptime   float64 `json:"uptime"` // seconds since boot
	Ports    []int   `json:"ports"`  // TCP ports listening on any address
}

func (r *sysReader) host() Host {
	h := Host{Ports: []int{}}
	h.Hostname, _ = os.Hostname()
	h.Kernel = strings.TrimSpace(r.readFile("proc/sys/kernel/osrelease"))
	for line := range strings.Lines(r.readFile("proc/stat")) {
		if strings.HasPrefix(line, "cpu") && !strings.HasPrefix(line, "cpu ") {
			h.Cores++
		}
	}
	if f := strings.Fields(r.readFile("proc/uptime")); len(f) > 0 {
		h.Uptime, _ = strconv.ParseFloat(f[0], 64)
	}
	h.Ports = parseListeningPorts(r.readFile("proc/net/tcp"), r.readFile("proc/net/tcp6"))
	return h
}

// parseListeningPorts returns the sorted, distinct ports in LISTEN state that
// are bound to every address, not just loopback, so reachable from the LAN.
func parseListeningPorts(tables ...string) []int {
	seen := map[int]bool{}
	ports := []int{}
	for _, table := range tables {
		for line := range strings.Lines(table) {
			f := strings.Fields(line)
			// sl local_address rem_address st ...; 0A is LISTEN.
			if len(f) < 4 || f[3] != "0A" {
				continue
			}
			addr, portHex, ok := strings.Cut(f[1], ":")
			if !ok || strings.Trim(addr, "0") != "" {
				continue
			}
			port, err := strconv.ParseUint(portHex, 16, 16)
			if err != nil || seen[int(port)] {
				continue
			}
			seen[int(port)] = true
			ports = append(ports, int(port))
		}
	}
	slices.Sort(ports)
	return ports
}
