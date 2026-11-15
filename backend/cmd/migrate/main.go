package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"bms/db"
)

func main() {
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatal("usage: migrate [up|down|version]")
	}
	url := os.Getenv("MIGRATE_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		log.Fatal("MIGRATE_DATABASE_URL is required")
	}
	if err := run(url, flag.Arg(0)); err != nil {
		log.Fatal(err)
	}
}

func run(url, cmd string) error {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return err
	}
	// One simple-protocol batch per file so function bodies keep their semicolons.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()

	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		return err
	}
	src, err := iofs.New(db.Migrations, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return err
	}
	switch cmd {
	case "up":
		err = m.Up()
	case "down":
		err = m.Steps(-1)
	case "version":
		v, dirty, verr := m.Version()
		if verr != nil {
			return verr
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return nil
	default:
		return fmt.Errorf("unknown command %s", cmd)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("no change")
		return nil
	}
	if err != nil {
		return err
	}
	v, dirty, _ := m.Version()
	fmt.Printf("version=%d dirty=%v\n", v, dirty)
	_ = sqlDB
	return nil
}

// keep database/sql referenced for the driver instance
var _ = sql.ErrNoRows
