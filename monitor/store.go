package monitor

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// A tier is one resolution readings are kept at. Each minute's reading goes
// into samples; the rollups are recomputed from samples as the minutes come
// in, keeping the average and the peak of each metric. Each tier is pruned to
// its retention, so the database stops growing once every tier is full.
type tier struct {
	Table     string        `json:"table"`
	Step      int64         `json:"step"` // seconds per row
	Retention time.Duration `json:"-"`
}

var (
	rawTier    = tier{"samples", 60, 48 * time.Hour}
	fiveMinute = tier{"rollup_5m", 300, 30 * 24 * time.Hour}
	hourly     = tier{"rollup_1h", 3600, 2 * 365 * 24 * time.Hour}
	tiers      = []tier{rawTier, fiveMinute, hourly}
)

// eventRetention is how long service status changes are kept.
const eventRetention = 90 * 24 * time.Hour

// Store keeps readings and service status changes in their own SQLite file,
// apart from users.db, so monitoring never bloats or locks the game data.
type Store struct {
	db   *sql.DB
	path string
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_auto_vacuum=incremental&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	// auto_vacuum (set on connecting) only takes effect on a database with no
	// tables yet; Prune's incremental vacuum then hands pruned pages back.
	stmts := []string{}
	cols := []string{}
	rollupCols := []string{"n INTEGER NOT NULL"}
	for _, m := range Metrics {
		cols = append(cols, m+" REAL")
		rollupCols = append(rollupCols, m+" REAL", m+"_max REAL")
	}
	stmts = append(stmts,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS samples (ts INTEGER PRIMARY KEY, %s)`, strings.Join(cols, ", ")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rollup_5m (ts INTEGER PRIMARY KEY, %s)`, strings.Join(rollupCols, ", ")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rollup_1h (ts INTEGER PRIMARY KEY, %s)`, strings.Join(rollupCols, ", ")),
		`CREATE TABLE IF NOT EXISTS service_events (
			ts INTEGER NOT NULL,
			grp TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS service_events_ts ON service_events(ts)`,
	)
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func nullable(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return math.Round(v*100) / 100
}

// AddSample stores a minute's reading, replacing any already stored for that
// minute, and brings the rollups covering it up to date.
func (s *Store) AddSample(sample Sample) error {
	return s.AddSamples([]Sample{sample})
}

// AddSamples stores readings in one transaction, then rolls up the buckets
// they fall in.
func (s *Store) AddSamples(samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(fmt.Sprintf(`INSERT OR REPLACE INTO samples (ts, %s) VALUES (?%s)`,
		strings.Join(Metrics, ", "), strings.Repeat(", ?", len(Metrics))))
	if err != nil {
		return err
	}
	defer stmt.Close()
	from, to := samples[0].TS, samples[0].TS
	for _, sample := range samples {
		args := []any{sample.TS / 60 * 60}
		for _, v := range sample.values() {
			args = append(args, nullable(v))
		}
		if _, err := stmt.Exec(args...); err != nil {
			return err
		}
		from, to = min(from, sample.TS), max(to, sample.TS)
	}
	if err := rollup(tx, from, to+1); err != nil {
		return err
	}
	return tx.Commit()
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// rollup recomputes every rollup bucket overlapping [from, to) from samples.
func rollup(db execer, from, to int64) error {
	aggs := []string{}
	for _, m := range Metrics {
		aggs = append(aggs, fmt.Sprintf("ROUND(AVG(%[1]s), 2), MAX(%[1]s)", m))
	}
	cols := []string{"ts", "n"}
	for _, m := range Metrics {
		cols = append(cols, m, m+"_max")
	}
	for _, t := range tiers[1:] {
		start := from / t.Step * t.Step
		end := (to + t.Step - 1) / t.Step * t.Step
		_, err := db.Exec(fmt.Sprintf(`INSERT OR REPLACE INTO %s (%s)
			SELECT ts / %d * %d, COUNT(*), %s FROM samples
			WHERE ts >= ? AND ts < ? GROUP BY ts / %d`,
			t.Table, strings.Join(cols, ", "), t.Step, t.Step, strings.Join(aggs, ", "), t.Step),
			start, end)
		if err != nil {
			return fmt.Errorf("rolling up %s: %w", t.Table, err)
		}
	}
	return nil
}

// Prune deletes what has outlived its tier's retention and hands the freed
// pages back to the file system.
func (s *Store) Prune(now time.Time) error {
	for _, t := range tiers {
		cutoff := now.Add(-t.Retention).Unix()
		if _, err := s.db.Exec("DELETE FROM "+t.Table+" WHERE ts < ?", cutoff); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec("DELETE FROM service_events WHERE ts < ?", now.Add(-eventRetention).Unix()); err != nil {
		return err
	}
	// incremental_vacuum frees one page per step, so it is read to the end;
	// Exec would step it once.
	rows, err := s.db.Query("PRAGMA incremental_vacuum")
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// History is a stretch of readings in columns, the shape charts take. Rollup
// tiers also carry each metric's peak, under "<metric>_max".
type History struct {
	Tier   string                `json:"tier"`
	Step   int64                 `json:"step"`
	TS     []int64               `json:"ts"`
	Series map[string][]*float64 `json:"series"`
}

// tierFor picks the finest tier that still holds the whole span.
func tierFor(span time.Duration) tier {
	for _, t := range tiers {
		if span <= t.Retention {
			return t
		}
	}
	return hourly
}

// History returns the readings from the last span before now, at the finest
// tier that still covers it.
func (s *Store) History(span time.Duration, now time.Time) (History, error) {
	t := tierFor(span)
	cols := []string{}
	for _, m := range Metrics {
		cols = append(cols, m)
		if t != rawTier {
			cols = append(cols, m+"_max")
		}
	}
	rows, err := s.db.Query(fmt.Sprintf("SELECT ts, %s FROM %s WHERE ts >= ? ORDER BY ts",
		strings.Join(cols, ", "), t.Table), now.Add(-span).Unix())
	if err != nil {
		return History{}, err
	}
	defer rows.Close()

	h := History{Tier: t.Table, Step: t.Step, TS: []int64{}, Series: map[string][]*float64{}}
	for _, c := range cols {
		h.Series[c] = []*float64{}
	}
	vals := make([]sql.NullFloat64, len(cols))
	ptrs := []any{new(int64)}
	for i := range vals {
		ptrs = append(ptrs, &vals[i])
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return History{}, err
		}
		h.TS = append(h.TS, *ptrs[0].(*int64))
		for i, c := range cols {
			var v *float64
			if vals[i].Valid {
				f := vals[i].Float64
				v = &f
			}
			h.Series[c] = append(h.Series[c], v)
		}
	}
	return h, rows.Err()
}

// Event is a service changing status.
type Event struct {
	TS     int64  `json:"ts"`
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (s *Store) AddEvent(e Event) error {
	_, err := s.db.Exec("INSERT INTO service_events (ts, grp, name, status, detail) VALUES (?, ?, ?, ?, ?)",
		e.TS, e.Group, e.Name, e.Status, e.Detail)
	return err
}

// Events returns the newest limit status changes, newest first.
func (s *Store) Events(limit int) ([]Event, error) {
	rows, err := s.db.Query("SELECT ts, grp, name, status, detail FROM service_events ORDER BY ts DESC, rowid DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.TS, &e.Group, &e.Name, &e.Status, &e.Detail); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// LastStatuses returns each service's most recent recorded status, keyed by
// group/name, so a restart does not record every service again.
func (s *Store) LastStatuses() (map[string]string, error) {
	rows, err := s.db.Query("SELECT grp, name, status FROM service_events ORDER BY ts, rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	last := map[string]string{}
	for rows.Next() {
		var grp, name, status string
		if err := rows.Scan(&grp, &name, &status); err != nil {
			return nil, err
		}
		last[grp+"/"+name] = status
	}
	return last, rows.Err()
}

// TableUsage is how full one table is.
type TableUsage struct {
	Table     string `json:"table"`
	Step      int64  `json:"step,omitempty"`
	Rows      int64  `json:"rows"`
	Capacity  int64  `json:"capacity"`  // rows once the retention is full
	Retention int64  `json:"retention"` // seconds
	Oldest    *int64 `json:"oldest"`
	Newest    *int64 `json:"newest"`
}

// Usage is how much the monitor is storing, against what it will level off at.
type Usage struct {
	Path      string       `json:"path"`
	FileBytes int64        `json:"file_bytes"`
	WALBytes  int64        `json:"wal_bytes"`
	FreeBytes int64        `json:"free_bytes"` // pages freed but not yet handed back
	Tables    []TableUsage `json:"tables"`
}

func (s *Store) Usage() (Usage, error) {
	u := Usage{Path: s.path}
	if info, err := os.Stat(s.path); err == nil {
		u.FileBytes = info.Size()
	}
	if info, err := os.Stat(s.path + "-wal"); err == nil {
		u.WALBytes = info.Size()
	}
	var pageSize, freePages int64
	s.db.QueryRow("PRAGMA page_size").Scan(&pageSize)
	s.db.QueryRow("PRAGMA freelist_count").Scan(&freePages)
	u.FreeBytes = pageSize * freePages

	for _, t := range tiers {
		tu := TableUsage{Table: t.Table, Step: t.Step, Retention: int64(t.Retention.Seconds()),
			Capacity: int64(t.Retention.Seconds()) / t.Step}
		if err := s.db.QueryRow("SELECT COUNT(*), MIN(ts), MAX(ts) FROM "+t.Table).Scan(&tu.Rows, &tu.Oldest, &tu.Newest); err != nil {
			return u, err
		}
		u.Tables = append(u.Tables, tu)
	}
	ev := TableUsage{Table: "service_events", Retention: int64(eventRetention.Seconds())}
	if err := s.db.QueryRow("SELECT COUNT(*), MIN(ts), MAX(ts) FROM service_events").Scan(&ev.Rows, &ev.Oldest, &ev.Newest); err != nil {
		return u, err
	}
	u.Tables = append(u.Tables, ev)
	return u, nil
}
