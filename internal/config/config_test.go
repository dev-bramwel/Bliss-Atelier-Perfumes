package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileDatabaseConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(path, []byte("postgres://runtime:SENTINEL_SECRET@db:5432/bliss_dev?sslmode=disable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"GO_DATABASE_URL_FILE": path, "GO_REQUIRE_SCHEMA": "true"}
	get := func(key string) string { return values[key] }
	cfg, err := Load(get)
	if err != nil || !cfg.RequireSchema || !strings.Contains(cfg.DatabaseURL, "SENTINEL_SECRET") {
		t.Fatal("file config failed")
	}
	values["GO_DATABASE_URL"] = "postgres://other:OTHER_SECRET@db/db"
	if _, err = Load(get); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("conflicting settings not rejected safely")
	}
	delete(values, "GO_DATABASE_URL")
	values["GO_DATABASE_URL_FILE"] = path + "-missing"
	if _, err = Load(get); err == nil || strings.Contains(err.Error(), path) {
		t.Fatal("file error not sanitized")
	}
	delete(values, "GO_DATABASE_URL_FILE")
	if _, err = Load(get); err == nil {
		t.Fatal("schema requirement without database accepted")
	}
}

func TestInvalidSettings(t *testing.T) {
	for key, values := range map[string][]string{"GO_HTTP_ADDR": {"secret", "localhost:0", "localhost:70000"}, "GO_IO_TIMEOUT": {"secret", "0s", "2m"}, "GO_SHUTDOWN_TIMEOUT": {"-1s"}, "GO_DB_MAX_CONNECTIONS": {"0", "33", "secret"}, "GO_DATABASE_URL": {"secret", "https://example.com/db", "postgres://localhost/"}} {
		for _, value := range values {
			t.Run(key+value, func(t *testing.T) {
				_, err := Load(func(k string) string {
					if k == key {
						return value
					}
					return ""
				})
				if err == nil {
					t.Fatal("invalid config accepted")
				}
			})
		}
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil || c.Address != "127.0.0.1:5001" || c.MaxConnections != 4 {
		t.Fatalf("unexpected defaults: %+v %v", c, err)
	}
}
