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

func TestMigrationCommandValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"down"}, {"force"}, {"up", "extra"}, {"init"}, {"adopt-legacy"}, {"up"}, {"status"}} {
		var log bytes.Buffer
		if Run(context.Background(), "migrate", args, func(string) string { return "" }, &log) != 1 {
			t.Fatal("invalid command or missing database accepted")
		}
		if !strings.Contains(log.String(), "usage:") && !strings.Contains(log.String(), "GO_DATABASE_URL is required") {
			t.Fatal(log.String())
		}
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
