package models

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ImportWebserverTables copies system_info and amazon_prices from the old Node
// webserver's database at sourcePath. A table that already has rows here is
// skipped, so running it twice copies nothing twice. It returns how many rows
// it copied per table.
func ImportWebserverTables(sourcePath string) (map[string]int64, error) {
	ctx := context.Background()
	// ATTACH applies to one connection, so everything runs on the same one.
	conn, err := DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS src", sourcePath); err != nil {
		return nil, err
	}
	defer conn.ExecContext(ctx, "DETACH DATABASE src")

	copies := []struct{ table, insert string }{
		// The old table never filled its ids, so new ones are assigned.
		{"system_info", `INSERT INTO system_info (timestamp, temperature, cpu_usage, memory_used, memory_total,
			disk_used, disk_available, disk_read_speed, disk_write_speed)
			SELECT timestamp, temperature, cpu_usage, memory_used, memory_total,
			disk_used, disk_available, disk_read_speed, disk_write_speed
			FROM src.system_info ORDER BY rowid`},
		{"amazon_prices", `INSERT INTO amazon_prices (id, url, title, price, timestamp)
			SELECT id, url, title, price, timestamp FROM src.amazon_prices`},
	}

	copied := map[string]int64{}
	for _, c := range copies {
		var existing int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM main."+c.table).Scan(&existing); err != nil {
			return copied, err
		}
		if existing > 0 {
			continue
		}
		result, err := conn.ExecContext(ctx, c.insert)
		if err != nil {
			return copied, fmt.Errorf("copying %s: %w", c.table, err)
		}
		copied[c.table], _ = result.RowsAffected()
	}
	return copied, nil
}

// ImportZigzagScores reads the output of
//
//	redis-cli ZRANGE user:zigzag_highscore:scores 0 -1 WITHSCORES
//
// alternating lines of member (the score's time in Unix milliseconds) and
// score, and stores each score. It returns how many it read; a score already
// stored is not stored again.
func ImportZigzagScores(r io.Reader) (int, error) {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if len(lines)%2 != 0 {
		return 0, fmt.Errorf("expected member and score pairs, got %d lines", len(lines))
	}

	for i := 0; i < len(lines); i += 2 {
		ms, err := strconv.ParseInt(lines[i], 10, 64)
		if err != nil {
			return i / 2, fmt.Errorf("line %d: member %q is not a time in milliseconds", i+1, lines[i])
		}
		score, err := strconv.ParseFloat(lines[i+1], 64)
		if err != nil {
			return i / 2, fmt.Errorf("line %d: score %q is not a number", i+2, lines[i+1])
		}
		if err := AddZigzagScoreAt(int(score), time.UnixMilli(ms)); err != nil {
			return i / 2, err
		}
	}
	return len(lines) / 2, nil
}
