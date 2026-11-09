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
	if err != nil || c.Address != "127.0.0.1:5001" || c.MaxConnections != 4 || c.ManagerSecret != "" {
		t.Fatalf("unexpected defaults: %+v %v", c, err)
	}
}

func TestManagerSecretConfiguration(t *testing.T) {
	// 1. Direct env variable
	c, err := Load(func(k string) string {
		if k == "GO_MANAGER_SECRET" {
			return "my-test-manager-secret"
		}
		return ""
	})
	if err != nil || c.ManagerSecret != "my-test-manager-secret" {
		t.Fatalf("expected manager secret from env, got %s (err: %v)", c.ManagerSecret, err)
	}

	// 2. Secret file
	path := filepath.Join(t.TempDir(), "mgr-secret")
	if err := os.WriteFile(path, []byte("file-mgr-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(func(k string) string {
		if k == "GO_MANAGER_SECRET_FILE" {
			return path
		}
		return ""
	})
	if err != nil || c.ManagerSecret != "file-mgr-secret" {
		t.Fatalf("expected manager secret from file, got %s (err: %v)", c.ManagerSecret, err)
	}

	// 3. Conflict rejection
	_, err = Load(func(k string) string {
		if k == "GO_MANAGER_SECRET" {
			return "sec1"
		}
		if k == "GO_MANAGER_SECRET_FILE" {
			return path
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected conflict error when both secret and file provided")
	}
}

func TestRedisConfiguration(t *testing.T) {
	// 1. Valid env
	c, err := Load(func(k string) string {
		if k == "GO_REDIS_URL" {
			return "redis://127.0.0.1:6379/0"
		}
		return ""
	})
	if err != nil || c.RedisURL != "redis://127.0.0.1:6379/0" {
		t.Fatalf("expected redis URL from env, got %s (err: %v)", c.RedisURL, err)
	}

	// 2. Valid file
	path := filepath.Join(t.TempDir(), "redis-url")
	if err := os.WriteFile(path, []byte("redis://:secret@redis.internal:6380/2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(func(k string) string {
		if k == "GO_REDIS_URL_FILE" {
			return path
		}
		return ""
	})
	if err != nil || c.RedisURL != "redis://:secret@redis.internal:6380/2" {
		t.Fatalf("expected redis URL from file, got %s (err: %v)", c.RedisURL, err)
	}

	// 3. Conflict rejection
	_, err = Load(func(k string) string {
		if k == "GO_REDIS_URL" {
			return "redis://localhost:6379/0"
		}
		if k == "GO_REDIS_URL_FILE" {
			return path
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected conflict error when both GO_REDIS_URL and GO_REDIS_URL_FILE provided")
	}

	// 4. Invalid scheme / empty host
	for _, bad := range []string{"http://localhost:6379", "redis://", "postgres://localhost:5432/db", "not-a-url"} {
		_, err := Load(func(k string) string {
			if k == "GO_REDIS_URL" {
				return bad
			}
			return ""
		})
		if err == nil {
			t.Fatalf("expected error for invalid redis url: %s", bad)
		}
	}

	// 5. Empty file rejection
	emptyPath := filepath.Join(t.TempDir(), "empty-redis")
	if err := os.WriteFile(emptyPath, []byte("   \n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(func(k string) string {
		if k == "GO_REDIS_URL_FILE" {
			return emptyPath
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected error for empty redis url file")
	}
}
