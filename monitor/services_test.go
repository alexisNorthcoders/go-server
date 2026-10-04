package monitor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSystemctlShow(t *testing.T) {
	out := `Id=nginx.service
ActiveState=active
SubState=running
StateChangeTimestamp=@1759532401

Id=redis-server.service
ActiveState=failed
SubState=failed
StateChangeTimestamp=@1759532500

Id=docker.service
ActiveState=activating
SubState=start
StateChangeTimestamp=
`
	got := parseSystemctlShow(out, []string{"nginx", "redis-server", "docker"})
	assert.Len(t, got, 3)
	assert.Equal(t, Service{Group: "system", Name: "nginx", Status: StatusUp, Detail: "running", Since: 1759532401}, got[0])
	assert.Equal(t, StatusDown, got[1].Status)
	assert.Equal(t, "failed failed", got[1].Detail)
	assert.Equal(t, StatusWarn, got[2].Status)
	assert.Zero(t, got[2].Since)
}

func TestParsePM2KeepsOnlyWhatItNeeds(t *testing.T) {
	out := "[PM2] notice\n" +
		`[{"name":"reddit-bot","monit":{"cpu":2.5,"memory":104857600},` +
		`"pm2_env":{"status":"online","restart_time":3,"pm_uptime":1759532401000,"DATABASE_SECRET":"hunter2"}},` +
		`{"name":"clipboard","monit":{"cpu":0,"memory":0},"pm2_env":{"status":"stopped","restart_time":0}},` +
		`{"name":"broken","monit":{"cpu":0,"memory":0},"pm2_env":{"status":"errored","restart_time":15}}]` + "\n"
	got, err := parsePM2([]byte(out))
	assert.NoError(t, err)
	assert.Len(t, got, 3)

	assert.Equal(t, StatusUp, got[0].Status)
	assert.Equal(t, 2.5, *got[0].CPU)
	assert.Equal(t, 100.0, *got[0].MemMB)
	assert.Equal(t, 3, *got[0].Restarts)
	assert.Equal(t, int64(1759532401), got[0].Since)
	assert.NotContains(t, fmt.Sprintf("%+v", got), "hunter2")

	assert.Equal(t, StatusStopped, got[1].Status)
	assert.Nil(t, got[1].CPU)
	assert.Equal(t, StatusDown, got[2].Status)
	assert.Equal(t, "errored", got[2].Detail)
}

func TestParseDockerPS(t *testing.T) {
	out := `{"Names":"joplin_server","State":"running","Status":"Up 44 minutes"}
{"Names":"sick","State":"running","Status":"Up 2 hours (unhealthy)"}
{"Names":"done","State":"exited","Status":"Exited (0) 3 days ago"}
{"Names":"crashed","State":"exited","Status":"Exited (137) 1 hour ago"}
{"Names":"looping","State":"restarting","Status":"Restarting (1) 5 seconds ago"}
`
	got := parseDockerPS([]byte(out))
	statuses := map[string]string{}
	for _, s := range got {
		statuses[s.Name] = s.Status
	}
	assert.Equal(t, map[string]string{
		"joplin_server": StatusUp, "sick": StatusWarn, "done": StatusStopped,
		"crashed": StatusDown, "looping": StatusWarn,
	}, statuses)
}

// fakeRedis answers one INFO with body.
func fakeRedis(t *testing.T, body string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		rd := bufio.NewReader(conn)
		for range 3 { // *1, $4, INFO
			rd.ReadString('\n')
		}
		fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(body), body)
	}()
	return ln.Addr().String()
}

func TestRedisCheck(t *testing.T) {
	body := strings.Join([]string{
		"# Server", "redis_version:7.0.15", "uptime_in_seconds:100",
		"# Clients", "connected_clients:2",
		"# Memory", "used_memory:1352640", "used_memory_human:1.29M", "used_memory_peak_human:2.00M",
		"# Stats", "instantaneous_ops_per_sec:4", "keyspace_hits:90", "keyspace_misses:10",
		"# Keyspace", "db0:keys=50,expires=3,avg_ttl=0", "db1:keys=7,expires=0,avg_ttl=0",
	}, "\r\n")
	got := redisCheck(fakeRedis(t, body))(context.Background())
	assert.Len(t, got, 1)
	s := got[0]
	assert.Equal(t, StatusUp, s.Status)
	assert.Equal(t, "1.29M · 57 keys", s.Detail)
	assert.Equal(t, "57", s.Stats["keys"])
	assert.Equal(t, "90.0%", s.Stats["hit rate"])
	assert.Equal(t, "2", s.Stats["clients"])
	assert.InDelta(t, 1.29, *s.MemMB, 0.01)
}

func TestRedisCheckDown(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	got := redisCheck(addr)(context.Background())
	assert.Equal(t, StatusDown, got[0].Status)
}

func TestFormatBytes(t *testing.T) {
	assert.Equal(t, "512 B", formatBytes(512))
	assert.Equal(t, "1.5 KiB", formatBytes(1536))
	assert.Equal(t, "5.0 MiB", formatBytes(5<<20))
}
