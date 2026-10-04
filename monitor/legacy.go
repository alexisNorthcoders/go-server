package monitor

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// parseLegacyNumber reads a value pi_health sent, units and all: "70.8°C",
// "26%", 3070.09.
func parseLegacyNumber(v any) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case []byte:
		return parseLegacyNumber(string(v))
	case string:
		s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(v), "°C%"))
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return math.NaN()
}

// parseLegacySize reads a `df -h` size such as "54G" or "980M" in GiB.
func parseLegacySize(v any) float64 {
	s, ok := v.(string)
	if !ok {
		if b, isBytes := v.([]byte); isBytes {
			s = string(b)
		} else {
			return parseLegacyNumber(v)
		}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return math.NaN()
	}
	scale := map[byte]float64{'K': 1.0 / (1 << 20), 'M': 1.0 / 1024, 'G': 1, 'T': 1024, 'P': 1 << 20}
	factor, hasUnit := scale[s[len(s)-1]]
	if hasUnit {
		s = s[:len(s)-1]
	} else {
		factor = 1.0 / (1 << 30) // plain bytes
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f * factor
}

// legacySample turns one system_info row into a Sample. Disk speeds were
// iostat's kB/s already; the total disk is used plus available, as df counts
// them. pi_health never read load, swap or network.
func legacySample(timestamp string, temp, cpu, memUsed, memTotal, diskUsed, diskAvail, diskRead, diskWrite any) (Sample, error) {
	t, err := time.Parse(time.DateTime, timestamp)
	if err != nil {
		return Sample{}, fmt.Errorf("timestamp %q: %w", timestamp, err)
	}
	nan := math.NaN()
	used, avail := parseLegacySize(diskUsed), parseLegacySize(diskAvail)
	return Sample{
		TS:        t.Unix(),
		CPU:       parseLegacyNumber(cpu),
		Temp:      parseLegacyNumber(temp),
		Load1:     nan,
		MemUsed:   parseLegacyNumber(memUsed),
		MemTotal:  parseLegacyNumber(memTotal),
		SwapUsed:  nan,
		DiskUsed:  used,
		DiskTotal: used + avail,
		DiskRead:  parseLegacyNumber(diskRead),
		DiskWrite: parseLegacyNumber(diskWrite),
		NetRx:     nan,
		NetTx:     nan,
	}, nil
}

// ImportLegacy copies every reading in src's system_info table (users.db,
// written by pi_health until go-server took over collecting) into the store,
// rolls them up and prunes what is past retention, so only rollups of old
// readings remain. It returns how many rows it read.
func (s *Store) ImportLegacy(src *sql.DB, now time.Time) (int, error) {
	rows, err := src.Query(`SELECT CAST(timestamp AS TEXT), temperature, cpu_usage, memory_used, memory_total,
		disk_used, disk_available, disk_read_speed, disk_write_speed
		FROM system_info ORDER BY timestamp`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	n := 0
	batch := []Sample{}
	for rows.Next() {
		var ts string
		var temp, cpu, memUsed, memTotal, diskUsed, diskAvail, diskRead, diskWrite any
		if err := rows.Scan(&ts, &temp, &cpu, &memUsed, &memTotal, &diskUsed, &diskAvail, &diskRead, &diskWrite); err != nil {
			return n, err
		}
		sample, err := legacySample(ts, temp, cpu, memUsed, memTotal, diskUsed, diskAvail, diskRead, diskWrite)
		if err != nil {
			return n, fmt.Errorf("row %d: %w", n+1, err)
		}
		batch = append(batch, sample)
		n++
		if len(batch) == 10000 {
			if err := s.AddSamples(batch); err != nil {
				return n, err
			}
			batch = batch[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	if err := s.AddSamples(batch); err != nil {
		return n, err
	}
	return n, s.Prune(now)
}
