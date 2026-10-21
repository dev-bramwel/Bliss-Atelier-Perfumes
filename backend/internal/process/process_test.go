package process

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestConfigFailsWithoutSecretDisclosure(t *testing.T) {
	for _, command := range []string{"api", "worker", "migrate"} {
		var log bytes.Buffer
		code := Run(context.Background(), command, nil, func(k string) string {
			if k == "GO_DATABASE_URL" {
				return "postgres://user:SENTINEL_SECRET@%bad/db"
			}
			return ""
		}, &log)
		if code != 1 || strings.Contains(log.String(), "SENTINEL_SECRET") || !strings.Contains(log.String(), "GO_DATABASE_URL") {
			t.Fatalf("unsafe failure: %d %s", code, &log)
		}
	}
}

func TestMigrationWritesDisabled(t *testing.T) {
	var log bytes.Buffer
	if Run(context.Background(), "migrate", []string{"up"}, func(string) string { return "" }, &log) != 1 {
		t.Fatal("migration writes accepted")
	}
}

func TestWorkerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var log bytes.Buffer
	if Run(ctx, "worker", nil, func(string) string { return "" }, &log) != 0 || !strings.Contains(log.String(), "worker_stopped") {
		t.Fatal(log.String())
	}
}
