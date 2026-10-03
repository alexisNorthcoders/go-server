// Command import-webserver copies the old Node webserver's data into
// users.db, once, when the Pi switches over to go-server. Run it from the
// go-server folder:
//
//	redis-cli ZRANGE user:zigzag_highscore:scores 0 -1 WITHSCORES \
//	  | go run ./cmd/import-webserver -from ../clipboard/DB/database.sqlite
//
// -from is the webserver's SQLite database (system_info, amazon_prices);
// stdin carries the zigzag scores from Redis. Running it again copies nothing
// twice.
package main

import (
	"flag"
	"log"
	"os"

	"go-server/models"
)

func main() {
	from := flag.String("from", "", "the old webserver's SQLite database")
	flag.Parse()
	if *from == "" {
		log.Fatal("pass -from with the old webserver's database")
	}
	if _, err := os.Stat(*from); err != nil {
		log.Fatal(err)
	}

	if err := models.InitDB(); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer models.DB.Close()

	copied, err := models.ImportWebserverTables(*from)
	if err != nil {
		log.Fatal(err)
	}
	for _, table := range []string{"system_info", "amazon_prices"} {
		if n, ok := copied[table]; ok {
			log.Printf("%s: copied %d rows", table, n)
		} else {
			log.Printf("%s: already has rows, skipped", table)
		}
	}

	read, err := models.ImportZigzagScores(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("zigzag_scores: read %d scores from stdin", read)
}
