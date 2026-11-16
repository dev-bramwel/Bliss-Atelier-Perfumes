package process

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestWorkerMetricsServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on ephemeral port: %v", err)
	}
	server := startWorkerMetricsServer(listener, 5*time.Second, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer func() {
		_ = server.Close()
	}()

	addr := listener.Addr().String()

	// 1. Verify GET /health/live
	resp, err := http.Get("http://" + addr + "/health/live")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /health/live, got %v (%d)", err, resp.StatusCode)
	}
	_ = resp.Body.Close()

	// 2. Verify GET /health/ready
	resp, err = http.Get("http://" + addr + "/health/ready")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /health/ready, got %v (%d)", err, resp.StatusCode)
	}
	_ = resp.Body.Close()

	// 3. Verify GET /metrics
	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %v (%d)", err, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "http_requests_total") && !strings.Contains(string(body), "unresolved_payments_gauge") {
		t.Fatalf("metrics response missing expected telemetry counters: %s", string(body))
	}
}
