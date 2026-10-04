package monitor

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "metrics.db"))
	assert.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func nanSample(ts int64) Sample {
	v := make([]float64, len(Metrics))
	for i := range v {
		v[i] = math.NaN()
	}
	return sampleFromValues(ts, v)
}

func cpuSample(ts int64, cpu float64) Sample {
	s := nanSample(ts)
	s.CPU = cpu
	return s
}

func TestStoreUsesIncrementalAutoVacuum(t *testing.T) {
	s := testStore(t)
	var mode int
	assert.NoError(t, s.db.QueryRow("PRAGMA auto_vacuum").Scan(&mode))
	assert.Equal(t, 2, mode)
}

func TestAddSampleRollsUpAverageAndPeak(t *testing.T) {
	s := testStore(t)
	base := int64(1_759_536_000) // on the hour
	for i, cpu := range []float64{10, 20, 60, 30, 30, 90} {
		assert.NoError(t, s.AddSample(cpuSample(base+int64(i)*60, cpu)))
	}

	var n int
	var avg, peak float64
	var temp sql.NullFloat64
	assert.NoError(t, s.db.QueryRow("SELECT n, cpu, cpu_max, temp FROM rollup_5m WHERE ts = ?", base).Scan(&n, &avg, &peak, &temp))
	assert.Equal(t, 5, n)
	assert.Equal(t, 30.0, avg)
	assert.Equal(t, 60.0, peak)
	assert.False(t, temp.Valid, "unknown stays unknown")

	assert.NoError(t, s.db.QueryRow("SELECT n, cpu, cpu_max FROM rollup_5m WHERE ts = ?", base+300).Scan(&n, &avg, &peak))
	assert.Equal(t, 1, n)
	assert.Equal(t, 90.0, peak)

	assert.NoError(t, s.db.QueryRow("SELECT n, cpu, cpu_max FROM rollup_1h WHERE ts = ?", base).Scan(&n, &avg, &peak))
	assert.Equal(t, 6, n)
	assert.Equal(t, 40.0, avg)
	assert.Equal(t, 90.0, peak)
}

func TestAddSampleKeepsOneRowPerMinute(t *testing.T) {
	s := testStore(t)
	assert.NoError(t, s.AddSample(cpuSample(1_759_536_001, 10)))
	assert.NoError(t, s.AddSample(cpuSample(1_759_536_059, 50)))
	var n int
	var cpu float64
	assert.NoError(t, s.db.QueryRow("SELECT COUNT(*), MAX(cpu) FROM samples").Scan(&n, &cpu))
	assert.Equal(t, 1, n)
	assert.Equal(t, 50.0, cpu)
}

func count(t *testing.T, s *Store, table string) int {
	var n int
	assert.NoError(t, s.db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func TestPruneKeepsEachTierToItsRetention(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_759_536_000, 0)
	ago := func(d time.Duration) int64 { return now.Add(-d).Unix() }
	day := 24 * time.Hour

	for _, ts := range []int64{ago(time.Hour), ago(3 * day), ago(40 * day), ago(800 * day)} {
		assert.NoError(t, s.AddSample(cpuSample(ts, 1)))
	}
	assert.NoError(t, s.AddEvent(Event{TS: ago(100 * day), Group: "pm2", Name: "old", Status: StatusUp}))
	assert.NoError(t, s.AddEvent(Event{TS: ago(day), Group: "pm2", Name: "new", Status: StatusUp}))

	assert.NoError(t, s.Prune(now))
	assert.Equal(t, 1, count(t, s, "samples"))   // within 48h
	assert.Equal(t, 2, count(t, s, "rollup_5m")) // within 30 days
	assert.Equal(t, 3, count(t, s, "rollup_1h")) // within 2 years
	assert.Equal(t, 1, count(t, s, "service_events"))
}

func TestHistoryPicksFinestTierCoveringTheSpan(t *testing.T) {
	assert.Equal(t, rawTier, tierFor(time.Hour))
	assert.Equal(t, rawTier, tierFor(48*time.Hour))
	assert.Equal(t, fiveMinute, tierFor(7*24*time.Hour))
	assert.Equal(t, hourly, tierFor(365*24*time.Hour))

	s := testStore(t)
	now := time.Unix(1_759_536_000, 0)
	assert.NoError(t, s.AddSample(cpuSample(now.Unix()-7200, 10)))
	assert.NoError(t, s.AddSample(cpuSample(now.Unix()-60, 20)))

	h, err := s.History(time.Hour, now)
	assert.NoError(t, err)
	assert.Equal(t, "samples", h.Tier)
	assert.Equal(t, []int64{now.Unix() - 60}, h.TS)
	assert.Equal(t, 20.0, *h.Series["cpu"][0])
	assert.Nil(t, h.Series["temp"][0])
	assert.NotContains(t, h.Series, "cpu_max")

	h, err = s.History(7*24*time.Hour, now)
	assert.NoError(t, err)
	assert.Equal(t, "rollup_5m", h.Tier)
	assert.Len(t, h.TS, 2)
	assert.Contains(t, h.Series, "cpu_max")
}

func TestEventsAndLastStatuses(t *testing.T) {
	s := testStore(t)
	assert.NoError(t, s.AddEvent(Event{TS: 1, Group: "pm2", Name: "bot", Status: StatusUp}))
	assert.NoError(t, s.AddEvent(Event{TS: 2, Group: "pm2", Name: "bot", Status: StatusDown, Detail: "errored"}))
	assert.NoError(t, s.AddEvent(Event{TS: 2, Group: "redis", Name: "redis", Status: StatusUp}))

	last, err := s.LastStatuses()
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"pm2/bot": StatusDown, "redis/redis": StatusUp}, last)

	events, err := s.Events(2)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, "redis", events[0].Name)
	assert.Equal(t, "errored", events[1].Detail)
}

func TestUsageReportsRowsAgainstCapacity(t *testing.T) {
	s := testStore(t)
	assert.NoError(t, s.AddSample(cpuSample(time.Now().Unix(), 1)))
	u, err := s.Usage()
	assert.NoError(t, err)
	assert.Greater(t, u.FileBytes, int64(0))
	assert.Equal(t, "samples", u.Tables[0].Table)
	assert.Equal(t, int64(1), u.Tables[0].Rows)
	assert.Equal(t, int64(2880), u.Tables[0].Capacity)
	assert.Equal(t, int64(8640), u.Tables[1].Capacity)
	assert.Equal(t, int64(17520), u.Tables[2].Capacity)
}

func TestImportLegacyRollsUpAndPrunes(t *testing.T) {
	src, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "users.db"))
	assert.NoError(t, err)
	defer src.Close()
	_, err = src.Exec(`CREATE TABLE system_info (id INTEGER PRIMARY KEY, timestamp TIMESTAMP, temperature FLOAT,
		cpu_usage FLOAT, memory_used FLOAT, memory_total FLOAT, disk_used FLOAT, disk_available FLOAT,
		disk_read_speed FLOAT, disk_write_speed FLOAT)`)
	assert.NoError(t, err)
	// Two readings a year ago in the same hour, as pi_health sent them, and
	// one from an hour ago.
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, r := range []struct {
		ts        time.Time
		temp, cpu string
	}{
		{now.AddDate(-1, 0, 0), "70.8°C", "26%"},
		{now.AddDate(-1, 0, 0).Add(time.Minute), "60.2°C", "50%"},
		{now.Add(-time.Hour), "55.0°C", "4%"},
	} {
		_, err := src.Exec("INSERT INTO system_info (timestamp, temperature, cpu_usage, memory_used, memory_total, disk_used, disk_available, disk_read_speed, disk_write_speed) VALUES (?, ?, ?, 3070.09, 4045, '54G', '169G', 2008, 83996)",
			r.ts.Format(time.DateTime), r.temp, r.cpu)
		assert.NoError(t, err)
	}

	s := testStore(t)
	n, err := s.ImportLegacy(src, now)
	assert.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, 1, count(t, s, "samples"))
	assert.Equal(t, 1, count(t, s, "rollup_5m"))
	assert.Equal(t, 2, count(t, s, "rollup_1h"))

	var cpu, cpuMax, temp, diskTotal float64
	assert.NoError(t, s.db.QueryRow("SELECT cpu, cpu_max, temp_max, disk_total FROM rollup_1h ORDER BY ts LIMIT 1").Scan(&cpu, &cpuMax, &temp, &diskTotal))
	assert.Equal(t, 38.0, cpu)
	assert.Equal(t, 50.0, cpuMax)
	assert.Equal(t, 70.8, temp)
	assert.Equal(t, 223.0, diskTotal)
}

func TestParseLegacySize(t *testing.T) {
	assert.Equal(t, 54.0, parseLegacySize("54G"))
	assert.Equal(t, 0.5, parseLegacySize("512M"))
	assert.Equal(t, 1024.0, parseLegacySize("1.0T"))
	assert.True(t, math.IsNaN(parseLegacySize("")))
}

func TestParseSpan(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"15m": 15 * time.Minute, "6h": 6 * time.Hour, "7d": 7 * 24 * time.Hour, "5y": maxSpan,
	} {
		got, err := parseSpan(in)
		assert.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "h", "0h", "-1d", "3w", "1.5h"} {
		_, err := parseSpan(bad)
		assert.Error(t, err, bad)
	}
}

func TestHistoryHandler(t *testing.T) {
	m := &Monitor{store: testStore(t)}
	m.live = []Sample{cpuSample(100, 5), cpuSample(105, 7)}
	assert.NoError(t, m.store.AddSample(cpuSample(time.Now().Unix()-120, 42)))

	get := func(rng string) (int, History) {
		w := httptest.NewRecorder()
		m.HistoryHandler(w, httptest.NewRequest("GET", "/monitor/history?range="+rng, nil))
		var h History
		json.Unmarshal(w.Body.Bytes(), &h)
		return w.Code, h
	}

	code, h := get("15m")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "live", h.Tier)
	assert.Equal(t, []int64{100, 105}, h.TS)

	code, h = get("1h")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "samples", h.Tier)
	assert.Equal(t, 42.0, *h.Series["cpu"][0])

	code, _ = get("forever")
	assert.Equal(t, http.StatusBadRequest, code)
}
