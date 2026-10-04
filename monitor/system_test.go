package monitor

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCPUCountsIdleAndIowaitAsNotBusy(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	busy, total := parseCPU("cpu  100 10 50 800 40 0 0 0 99 99\ncpu0 1 1 1 1 1 0 0 0 0 0\n")
	assert.Equal(t, uint64(160), busy)
	assert.Equal(t, uint64(1000), total)
}

func TestParseMeminfoUsesAvailable(t *testing.T) {
	used, total, swap := parseMeminfo("MemTotal:  4142080 kB\nMemFree:  200000 kB\nMemAvailable: 2048000 kB\nSwapTotal: 204800 kB\nSwapFree: 102400 kB\n")
	assert.InDelta(t, (4142080-2048000)/1024.0, used, 0.01)
	assert.InDelta(t, 4045.0, total, 0.01)
	assert.InDelta(t, 100.0, swap, 0.01)
}

func TestParseDiskstatsSkipsPartitionsAndLoops(t *testing.T) {
	stats := ` 7 0 loop0 3045 0 101720 359 0 0 0 0 0 356 359
 259 0 nvme0n1 7322006 2495410 1000 3942888 37590196 19655186 2000 658932146 0 41452800
 259 1 nvme0n1p1 3600 10057 500 886 1680 2260 700 29238 0 952
`
	read, written := parseDiskstats(stats, func(name string) bool { return name == "nvme0n1" })
	assert.Equal(t, uint64(1000), read)
	assert.Equal(t, uint64(2000), written)
}

func TestParseNetDevSkipsLoopbackAndDocker(t *testing.T) {
	netdev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9999 1 0 0 0 0 0 0 9999 1 0 0 0 0 0 0
  eth0: 1000 1 0 0 0 0 0 0 300 1 0 0 0 0 0 0
 wlan0: 24 1 0 0 0 0 0 0 0 1 0 0 0 0 0 0
docker0: 9999 1 0 0 0 0 0 0 9999 1 0 0 0 0 0 0
br-656969655e8d: 9999 1 0 0 0 0 0 0 9999 1 0 0 0 0 0 0
veth81cfe09: 9999 1 0 0 0 0 0 0 9999 1 0 0 0 0 0 0
`
	rx, tx := parseNetDev(netdev)
	assert.Equal(t, uint64(1024), rx)
	assert.Equal(t, uint64(300), tx)
}

func TestParseListeningPortsOnlyAllAddresses(t *testing.T) {
	tcp := `  sl  local_address rem_address   st
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000
   1: 0100007F:18EB 00000000:0000 0A 00000000:00000000
   2: 00000000:0016 00000000:0000 0A 00000000:00000000
   3: 00000000:0050 0A00A8C0:D431 01 00000000:00000000
`
	tcp6 := `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A
   1: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A
`
	// 8080 and 22 on IPv4, 22 again and 80 on IPv6; 6379 is loopback only
	// and the port 80 IPv4 line is an established connection.
	assert.Equal(t, []int{22, 80, 8080}, parseListeningPorts(tcp, tcp6))
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, name)
		assert.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		assert.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func TestSysReaderWorksOutRatesBetweenReads(t *testing.T) {
	root := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "sys/block/sda/device"), 0o755))
	files := map[string]string{
		"sys/class/thermal/thermal_zone0/temp": "60050\n",
		"proc/loadavg":                         "0.52 0.40 0.30 1/500 1234\n",
		"proc/meminfo":                         "MemTotal: 4096 kB\nMemAvailable: 1024 kB\n",
		"proc/stat":                            "cpu  100 0 100 800 0 0 0 0 0 0\n",
		"proc/diskstats":                       "8 0 sda 0 0 0 0 0 0 0 0\n",
		"proc/net/dev":                         "eth0: 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
	}
	writeFiles(t, root, files)
	r := sysReader{root: root}

	first := r.read(1000)
	assert.InDelta(t, 60.05, first.Temp, 0.001)
	assert.Equal(t, 0.52, first.Load1)
	assert.InDelta(t, 3.0, first.MemUsed, 0.001)
	assert.True(t, math.IsNaN(first.CPU), "no rate on the first read")
	assert.Greater(t, first.DiskTotal, 0.0)

	files["proc/stat"] = "cpu  130 0 120 850 0 0 0 0 0 0\n"     // 50 of 100 busy
	files["proc/diskstats"] = "8 0 sda 0 0 2048 0 0 0 4096 0\n" // 1 MiB read, 2 MiB written
	files["proc/net/dev"] = "eth0: 10240 0 0 0 0 0 0 0 5120 0 0 0 0 0 0 0\n"
	writeFiles(t, root, files)

	second := r.read(1010)
	assert.InDelta(t, 50.0, second.CPU, 0.001)
	assert.InDelta(t, 102.4, second.DiskRead, 0.001) // 1024 kB over 10 s
	assert.InDelta(t, 204.8, second.DiskWrite, 0.001)
	assert.InDelta(t, 1.0, second.NetRx, 0.001)
	assert.InDelta(t, 0.5, second.NetTx, 0.001)
}

func TestSampleJSONWritesNullForUnknown(t *testing.T) {
	s := Sample{TS: 60, CPU: 12.345, Temp: math.NaN()}
	b, err := s.MarshalJSON()
	assert.NoError(t, err)
	assert.Contains(t, string(b), `"ts":60,"cpu":12.35,"temp":null`)
}

func TestMeanSampleSkipsUnknown(t *testing.T) {
	nan := math.NaN()
	m := meanSample(60, []Sample{{CPU: 10, Temp: nan}, {CPU: 30, Temp: 50}})
	assert.Equal(t, int64(60), m.TS)
	assert.Equal(t, 20.0, m.CPU)
	assert.Equal(t, 50.0, m.Temp)
}
