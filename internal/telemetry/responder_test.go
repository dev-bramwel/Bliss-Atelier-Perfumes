package telemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDrillResponder_LivenessAndAlertCapture(t *testing.T) {
	responder := NewDrillResponder("test-token-123")
	handler := responder.Handler()

	// 1. Health check (unauthenticated)
	healthReq := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /health/live, got %d", healthRec.Code)
	}

	// 2. Alert POST without authentication -> 401
	payload := `{"receiver":"drill_responder_webhook","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"DbPoolSaturation","severity":"critical"},"annotations":{"summary":"Pool Saturated","runbook_url":"docs/runbooks/runbook-database-pool-exhaustion.md"}}]}`
	unauthReq := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(payload))
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without token, got %d", unauthRec.Code)
	}

	// 3. Alert POST with wrong token -> 401
	wrongReq := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(payload))
	wrongReq.Header.Set("Authorization", "Bearer invalid-token")
	wrongRec := httptest.NewRecorder()
	handler.ServeHTTP(wrongRec, wrongReq)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized with wrong token, got %d", wrongRec.Code)
	}

	// 4. Alert POST with valid Bearer token -> 200 OK
	authReq := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(payload))
	authReq.Header.Set("Authorization", "Bearer test-token-123")
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /alerts with valid bearer token, got %d", authRec.Code)
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

	// 5. Query alerts via GET /alerts without token -> 401
	unauthGetReq := httptest.NewRequest(http.MethodGet, "/alerts", nil)
	unauthGetRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthGetRec, unauthGetReq)
	if unauthGetRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized from GET /alerts without token, got %d", unauthGetRec.Code)
	}

	// 6. Query alerts via GET /alerts with valid token -> 200 OK
	authGetReq := httptest.NewRequest(http.MethodGet, "/alerts", nil)
	authGetReq.Header.Set("Authorization", "Bearer test-token-123")
	authGetRec := httptest.NewRecorder()
	handler.ServeHTTP(authGetRec, authGetReq)
	if authGetRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /alerts with token, got %d", authGetRec.Code)
	}
	if !strings.Contains(authGetRec.Body.String(), "DbPoolSaturation") {
		t.Errorf("expected serialized alert payload in GET /alerts response, got %s", authGetRec.Body.String())
	}
}

func TestDrillResponder_EnforcesMaxBodyBytes(t *testing.T) {
	responder := NewDrillResponder("test-token-123")
	handler := responder.Handler()

	// Send an oversized body exceeding 64 KiB
	oversizedBody := strings.Repeat("A", MaxAlertBodyBytes+1024)
	req := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(oversizedBody))
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 413 or 400 for oversized payload, got %d", rec.Code)
	}
}

func TestDrillResponder_RingBufferRetentionCap(t *testing.T) {
	responder := NewDrillResponder("test-token")
	handler := responder.Handler()

	// Push 60 alerts (MaxRetainedAlerts is 50)
	for i := 0; i < 60; i++ {
		payload := fmt.Sprintf(`{"receiver":"drill_responder_webhook","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"Alert-%d","severity":"warning"},"annotations":{"summary":"Alert %d"}}]}`, i, i)
		req := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("failed alert %d: status %d", i, rec.Code)
		}
	}

	alerts := responder.Alerts()
	if len(alerts) != MaxRetainedAlerts {
		t.Fatalf("expected capped retention of %d alerts, got %d", MaxRetainedAlerts, len(alerts))
	}

	// Verify the oldest 10 alerts were evicted; first retained alert should be Alert-10
	firstAlertName := alerts[0].Alerts[0].Labels["alertname"]
	if firstAlertName != "Alert-10" {
		t.Errorf("expected ring buffer eviction of older alerts; oldest retained should be Alert-10, got %s", firstAlertName)
	}
}

func TestRunResponder_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := RunResponder(ctx, "127.0.0.1:0", "test-token")
	if err != nil {
		t.Fatalf("expected clean exit on cancellation, got error: %v", err)
	}
}

func TestDrillResponder_OperationalEscalationRoutingAndForwarding(t *testing.T) {
	forwardReceived := make(chan string, 2)
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		forwardReceived <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamMock.Close()

	responder := NewDrillResponder("test-token-123")
	responder.SetTeamForwardURL("platform-sre", upstreamMock.URL)
	handler := responder.Handler()

	platformPayload := `{"receiver":"platform_sre_escalation","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ApiHigh5xxRate","severity":"critical","team":"platform-sre"},"annotations":{"summary":"High 5xx Rate","owner":"Platform SRE Team","escalation":"platform-oncall","runbook_url":"docs/runbooks/runbook-api-error-surge.md"}}]}`

	req := httptest.NewRequest(http.MethodPost, "/escalations/platform-sre", strings.NewReader(platformPayload))
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /escalations/platform-sre, got %d", rec.Code)
	}

	select {
	case fwd := <-forwardReceived:
		if !strings.Contains(fwd, "ApiHigh5xxRate") {
			t.Errorf("forwarded body did not contain alertname: %s", fwd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded alert at upstream mock")
	}

	alerts := responder.Alerts()
	if len(alerts) != 1 || alerts[0].Receiver != "platform_sre_escalation" {
		t.Fatalf("expected alert retained in responder, got: %+v", alerts)
	}
}

func TestDrillResponder_UpstreamEscalationRejectionReturns502(t *testing.T) {
	rejectingUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal incident paging system error", http.StatusInternalServerError)
	}))
	defer rejectingUpstream.Close()

	responder := NewDrillResponder("test-token-123")
	responder.SetForwardURL(rejectingUpstream.URL)
	handler := responder.Handler()

	payload := `{"receiver":"platform_sre_escalation","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"DbPoolSaturation"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/escalations/platform-sre", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway when upstream rejects notification, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream escalation rejected notification") {
		t.Errorf("expected rejection reason in error body, got %s", rec.Body.String())
	}
}

func TestDrillResponder_UpstreamEscalationUnavailableReturns502(t *testing.T) {
	// Start and immediately close server so destination is unavailable
	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedServer.URL
	closedServer.Close()

	responder := NewDrillResponder("test-token-123")
	responder.SetForwardURL(closedURL)
	handler := responder.Handler()

	payload := `{"receiver":"platform_sre_escalation","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ServiceTargetDown"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/escalations/platform-sre", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway when upstream is unreachable, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream escalation delivery failed") {
		t.Errorf("expected failure message in error body, got %s", rec.Body.String())
	}
}
