package models

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// oldWebserverDB builds a database shaped like the Node webserver's, whose
// system_info was created with "id SERIAL" and so has no ids.
func oldWebserverDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database.sqlite")
	src, err := sql.Open("sqlite3", path)
	assert.NoError(t, err)
	defer src.Close()
	for _, stmt := range []string{
		`CREATE TABLE system_info (id SERIAL PRIMARY KEY, timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			temperature FLOAT, cpu_usage FLOAT, memory_used FLOAT, memory_total FLOAT, disk_used FLOAT,
			disk_available FLOAT, disk_read_speed FLOAT, disk_write_speed FLOAT)`,
		`INSERT INTO system_info (timestamp, temperature, cpu_usage, memory_used, memory_total, disk_used, disk_available, disk_read_speed, disk_write_speed)
			VALUES ('2026-04-21 17:18:01', '68.1°C', '50%', 2718.84, 4045.0, '53G', '170G', 1640.0, 28.0),
			       ('2026-04-21 17:19:02', '70.8°C', '26%', 3070.09, 4045.0, '54G', '169G', 2008.0, 83996.0)`,
		`CREATE TABLE amazon_prices (id INTEGER PRIMARY KEY AUTOINCREMENT, url TEXT NOT NULL, title TEXT NOT NULL, price TEXT NOT NULL, timestamp TEXT NOT NULL)`,
		`INSERT INTO amazon_prices (id, url, title, price, timestamp) VALUES
			(41, 'https://www.amazon.co.uk/dp/B01D8KOZF4', 'ELEGOO UNO R3', '£42.99', '2026-09-06T10:34:33.896Z')`,
		// Tables the webserver shared with the clipboard app stay behind.
		`CREATE TABLE files (id INTEGER PRIMARY KEY, name TEXT)`,
	} {
		_, err := src.Exec(stmt)
		assert.NoError(t, err, stmt)
	}
	return path
}

func TestImportWebserverTablesCopiesOnce(t *testing.T) {
	inTempDir(t)
	assert.NoError(t, InitDB())
	source := oldWebserverDB(t)

	copied, err := ImportWebserverTables(source)
	assert.NoError(t, err)
	assert.Equal(t, map[string]int64{"system_info": 2, "amazon_prices": 1}, copied)

	records, err := GetLastSystemInfo(10)
	assert.NoError(t, err)
	assert.Len(t, records, 2)
	assert.Equal(t, "2026-04-21 17:19:02", records[0]["timestamp"])
	assert.Equal(t, "70.8°C", records[0]["temperature"])
	assert.Equal(t, 3070.09, records[0]["memory_used"])
	assert.NotNil(t, records[0]["id"])

	price, err := GetLastAmazonPrice("https://www.amazon.co.uk/dp/B01D8KOZF4")
	assert.NoError(t, err)
	assert.Equal(t, "£42.99", *price)
	id, err := AddAmazonPrice("u", "t", "£1", "2026-09-07T00:00:00.000Z")
	assert.NoError(t, err)
	assert.Equal(t, int64(42), id)

	copied, err = ImportWebserverTables(source)
	assert.NoError(t, err)
	assert.Empty(t, copied)
	records, _ = GetLastSystemInfo(10)
	assert.Len(t, records, 2)

	var files int
	DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'files'").Scan(&files)
	assert.Zero(t, files)
}

func TestImportZigzagScores(t *testing.T) {
	inTempDir(t)
	assert.NoError(t, InitDB())

	// redis-cli ZRANGE ... WITHSCORES: member (Unix ms), then score.
	dump := "1736972749012\n18190\n1737801325524\n16655\n"
	n, err := ImportZigzagScores(strings.NewReader(dump))
	assert.NoError(t, err)
	assert.Equal(t, 2, n)
	_, err = ImportZigzagScores(strings.NewReader(dump))
	assert.NoError(t, err)

	scores, err := GetTopZigzagScores(10)
	assert.NoError(t, err)
	assert.Equal(t, []ZigzagScore{
		{Score: 16655, Timestamp: "2025-01-25T10:35:25.524Z"},
		{Score: 18190, Timestamp: "2025-01-15T20:25:49.012Z"},
	}, scores)

	_, err = ImportZigzagScores(strings.NewReader("1736972749012\n"))
	assert.Error(t, err)
	_, err = ImportZigzagScores(strings.NewReader("yesterday\n5\n"))
	assert.Error(t, err)
}
