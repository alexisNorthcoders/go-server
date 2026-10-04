// Command migrate-system-info moves pi_health's readings out of users.db and
// into the monitor's tiered store, once, when go-server takes over collecting.
// Run it from the go-server folder:
//
//	go run ./cmd/migrate-system-info
//
// Readings are rolled up into the five-minute and hourly tiers, and whatever
// is past those tiers' retention is dropped. Then the system_info table is
// dropped from users.db and users.db is vacuumed to give the space back. Pass
// -keep to leave system_info where it is.
package main

import (
	"database/sql"
	"flag"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"go-server/monitor"
)

func main() {
	users := flag.String("users", "./users.db", "the database holding system_info")
	keep := flag.Bool("keep", false, "copy the readings but leave system_info in place")
	flag.Parse()

	if _, err := os.Stat(*users); err != nil {
		log.Fatal(err)
	}
	src, err := sql.Open("sqlite3", *users+"?_busy_timeout=10000")
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()
	var tables int
	if err := src.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'system_info'").Scan(&tables); err != nil {
		log.Fatal(err)
	}
	if tables == 0 {
		log.Fatal("users.db has no system_info table: nothing to migrate")
	}

	store, err := monitor.OpenStore(monitor.ConfigFromEnv().StorePath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	start := time.Now()
	n, err := store.ImportLegacy(src, time.Now())
	if err != nil {
		log.Fatalf("after %d readings: %v", n, err)
	}
	log.Printf("Rolled up %d readings in %s", n, time.Since(start).Round(time.Millisecond))
	usage, err := store.Usage()
	if err != nil {
		log.Fatal(err)
	}
	for _, t := range usage.Tables {
		log.Printf("  %-15s %7d rows", t.Table, t.Rows)
	}

	if *keep {
		return
	}
	if _, err := src.Exec("DROP TABLE system_info"); err != nil {
		log.Fatal(err)
	}
	before, _ := os.Stat(*users)
	if _, err := src.Exec("VACUUM"); err != nil {
		log.Fatal(err)
	}
	after, _ := os.Stat(*users)
	log.Printf("Dropped system_info; users.db went from %.1f MB to %.1f MB",
		float64(before.Size())/1e6, float64(after.Size())/1e6)
}
