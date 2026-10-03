package models

import (
	"database/sql"
	"time"
)

// createPiTables creates the tables behind the Raspberry Pi's endpoints:
// system health readings, tracked Amazon prices and zigzag high scores.
func createPiTables() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS system_info (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			temperature FLOAT,
			cpu_usage FLOAT,
			memory_used FLOAT,
			memory_total FLOAT,
			disk_used FLOAT,
			disk_available FLOAT,
			disk_read_speed FLOAT,
			disk_write_speed FLOAT
		)`,
		`CREATE TABLE IF NOT EXISTS amazon_prices (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			title TEXT NOT NULL,
			price TEXT NOT NULL,
			timestamp TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS zigzag_scores (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			score INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(score, created_at)
		)`,
		// Readings and prices are always read newest first.
		`CREATE INDEX IF NOT EXISTS system_info_timestamp ON system_info(timestamp)`,
		`CREATE INDEX IF NOT EXISTS amazon_prices_url_timestamp ON amazon_prices(url, timestamp)`,
	} {
		if _, err := DB.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// SystemInfo is one health reading as the reporter sent it. The reporter sends
// values with units ("70.8°C", "54G"), and the columns keep whatever arrived,
// so every reading is passed through untyped.
type SystemInfo struct {
	Temperature    any
	CPUUsage       any
	MemoryUsed     any
	MemoryTotal    any
	DiskUsed       any
	DiskAvailable  any
	DiskReadSpeed  any
	DiskWriteSpeed any
}

func AddSystemInfo(s SystemInfo) (int64, error) {
	result, err := DB.Exec(`
		INSERT INTO system_info (temperature, cpu_usage, memory_used, memory_total, disk_used, disk_available, disk_read_speed, disk_write_speed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Temperature, s.CPUUsage, s.MemoryUsed, s.MemoryTotal, s.DiskUsed, s.DiskAvailable, s.DiskReadSpeed, s.DiskWriteSpeed,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// systemInfoColumns are returned in this order, under these names. The
// timestamp is cast so the driver hands back the stored text instead of
// parsing it into a time.
var systemInfoColumns = []string{
	"id", "timestamp", "temperature", "cpu_usage", "memory_used", "memory_total",
	"disk_used", "disk_available", "disk_read_speed", "disk_write_speed",
}

// GetLastSystemInfo returns the newest limit readings, newest first, each as
// column name to stored value.
func GetLastSystemInfo(limit int) ([]map[string]any, error) {
	rows, err := DB.Query(`
		SELECT id, CAST(timestamp AS TEXT), temperature, cpu_usage, memory_used, memory_total,
			disk_used, disk_available, disk_read_speed, disk_write_speed
		FROM system_info
		ORDER BY timestamp DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(systemInfoColumns))
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		record := make(map[string]any, len(values))
		for i, name := range systemInfoColumns {
			if b, ok := values[i].([]byte); ok {
				values[i] = string(b)
			}
			record[name] = values[i]
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func AddAmazonPrice(url, title, price, timestamp string) (int64, error) {
	result, err := DB.Exec(
		"INSERT INTO amazon_prices (url, title, price, timestamp) VALUES (?, ?, ?, ?)",
		url, title, price, timestamp,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetLastAmazonPrice returns the newest price recorded for url, and nil when
// there is none.
func GetLastAmazonPrice(url string) (*string, error) {
	var price string
	err := DB.QueryRow(
		"SELECT price FROM amazon_prices WHERE url = ? ORDER BY timestamp DESC LIMIT 1", url,
	).Scan(&price)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &price, nil
}

type ZigzagScore struct {
	Score     int    `json:"score"`
	Timestamp string `json:"timestamp"`
}

// zigzagTimeLayout matches JavaScript's Date.toISOString, which the game and
// the scores imported from Redis use.
const zigzagTimeLayout = "2006-01-02T15:04:05.000Z"

func AddZigzagScore(score int) error {
	return AddZigzagScoreAt(score, time.Now())
}

// AddZigzagScoreAt stores a score made at t. The same score at the same moment
// is stored once, so an import can be repeated.
func AddZigzagScoreAt(score int, t time.Time) error {
	_, err := DB.Exec(
		"INSERT OR IGNORE INTO zigzag_scores (score, created_at) VALUES (?, ?)",
		score, t.UTC().Format(zigzagTimeLayout),
	)
	return err
}

// GetTopZigzagScores returns the best limit scores in ascending order, so the
// high score is last.
func GetTopZigzagScores(limit int) ([]ZigzagScore, error) {
	rows, err := DB.Query(`
		SELECT score, created_at FROM (
			SELECT score, created_at FROM zigzag_scores
			ORDER BY score DESC, created_at DESC
			LIMIT ?
		) ORDER BY score ASC, created_at ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scores := []ZigzagScore{}
	for rows.Next() {
		var s ZigzagScore
		if err := rows.Scan(&s.Score, &s.Timestamp); err != nil {
			return nil, err
		}
		scores = append(scores, s)
	}
	return scores, rows.Err()
}
