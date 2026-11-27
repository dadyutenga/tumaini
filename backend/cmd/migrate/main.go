// Command migrate applies the schema in backend/database/migrations.
//
//	migrate up       apply pending migrations (each file atomically)
//	migrate status   list applied and pending migrations
//	migrate verify   exit non-zero unless everything is applied and unchanged
//
// There is no "down": the approved schema is forward-only. Recovery is a restore from backup or a new
// forward migration (see docs/backup-and-dr.md).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"bms/database"
	"bms/internal/platform/migrator"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: migrate [up|status|verify]")
	}
	url := os.Getenv("MIGRATE_DATABASE_URL")
	if url == "" {
		log.Fatal("MIGRATE_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	switch os.Args[1] {
	case "up":
		applied, err := migrator.Up(ctx, url, database.Migrations, "migrations")
		for _, v := range applied {
			fmt.Println("applied", v)
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("migrations complete")
	case "status", "verify":
		st, err := migrator.Check(ctx, url, database.Migrations, "migrations")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("applied=%d pending=[%s] changed=[%s] unknown=[%s]\n", len(st.Applied),
			strings.Join(st.Pending, ","), strings.Join(st.Mismatch, ","), strings.Join(st.Unknown, ","))
		if os.Args[1] == "verify" {
			if err := migrator.Verify(st); err != nil {
				log.Fatal(err)
			}
		}
	default:
		log.Fatalf("unknown command %s", os.Args[1])
	}
}
