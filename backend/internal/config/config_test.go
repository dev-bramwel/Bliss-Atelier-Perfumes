package config

import "testing"

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
