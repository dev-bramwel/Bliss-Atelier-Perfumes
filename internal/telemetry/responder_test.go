package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDrillResponder_LivenessAndAlertCapture(t *testing.T) {
	responder := NewDrillResponder()
	handler := responder.Handler()

	// 1. Health check
	healthReq := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /health/live, got %d", healthRec.Code)
	}

	// 2. Alert POST
	payload := `{"receiver":"drill_responder_webhook","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"DbPoolSaturation","severity":"critical"},"annotations":{"summary":"Pool Saturated","runbook_url":"docs/runbooks/runbook-database-pool-exhaustion.md"}}]}`
	alertReq := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(payload))
	alertRec := httptest.NewRecorder()
	handler.ServeHTTP(alertRec, alertReq)
	if alertRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /alerts, got %d", alertRec.Code)
	}

	alerts := responder.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 captured alert notification, got %d", len(alerts))
	}
	if alerts[0].Receiver != "drill_responder_webhook" {
		t.Errorf("expected receiver drill_responder_webhook, got %s", alerts[0].Receiver)
	}
	if len(alerts[0].Alerts) != 1 || alerts[0].Alerts[0].Labels["alertname"] != "DbPoolSaturation" {
		t.Errorf("unexpected alert payload contents: %+v", alerts[0])
	}
}

func TestRunResponder_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := RunResponder(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("expected clean exit on cancellation, got error: %v", err)
	}
}
