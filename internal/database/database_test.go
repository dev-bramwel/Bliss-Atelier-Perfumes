package database

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestDisposableDatabase(t *testing.T) {
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("run scripts/test-integration.ps1 to start disposable PostgreSQL")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, dsn, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// Temporary table proves write/read capability without changing any persistent schema.
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, "CREATE TEMP TABLE foundation_probe (id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, "INSERT INTO foundation_probe VALUES ($1)", "legacy-cuid-example"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = connection.QueryRow(ctx, "SELECT id FROM foundation_probe").Scan(&id); err != nil || id != "legacy-cuid-example" {
		t.Fatalf("roundtrip failed: %v", err)
	}
	deadline, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err = connection.Exec(deadline, "SELECT pg_sleep(5)"); err == nil {
		t.Fatal("database deadline ignored")
	}
}
