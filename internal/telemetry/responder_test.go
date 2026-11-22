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

func TestDrillResponder_MetricsAndDrillInduction(t *testing.T) {
	responder := NewDrillResponder("test-drill-token")
	handler := responder.Handler()

	// 1. Verify GET /metrics exposes Prometheus metrics
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recMetrics := httptest.NewRecorder()
	handler.ServeHTTP(recMetrics, reqMetrics)
	if recMetrics.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", recMetrics.Code)
	}

	// 2. Unauthenticated induction returns 401
	unauthReq := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(`{"metric":"payment_exceptions_total"}`))
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without token on /drill/induce-metric, got %d", unauthRec.Code)
	}

	// 3. Induce payment_exceptions_total
	induceReq := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(`{"metric":"payment_exceptions_total","label":"AMOUNT_MISMATCH"}`))
	induceReq.Header.Set("Authorization", "Bearer test-drill-token")
	induceRec := httptest.NewRecorder()
	handler.ServeHTTP(induceRec, induceReq)
	if induceRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /drill/induce-metric, got %d", induceRec.Code)
	}

	// 4. Induce rabbitmq_dlq_messages_gauge to 5
	induceDLQ := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(`{"metric":"rabbitmq_dlq_messages_gauge","label":"notifications","value":5}`))
	induceDLQ.Header.Set("Authorization", "Bearer test-drill-token")
	induceDLQRec := httptest.NewRecorder()
	handler.ServeHTTP(induceDLQRec, induceDLQ)
	if induceDLQRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /drill/induce-metric for DLQ, got %d", induceDLQRec.Code)
	}

	metricsOutput := DefaultRegistry.Gather()
	if !strings.Contains(metricsOutput, `rabbitmq_dlq_messages_gauge{queue="notifications"} 5`) {
		t.Errorf("expected dlq gauge 5 in metrics output, got:\n%s", metricsOutput)
	}

	// 5. Clear DLQ metric back to 0
	clearDLQ := httptest.NewRequest(http.MethodPost, "/drill/clear-metric", strings.NewReader(`{"metric":"rabbitmq_dlq_messages_gauge","label":"notifications"}`))
	clearDLQ.Header.Set("Authorization", "Bearer test-drill-token")
	clearDLQRec := httptest.NewRecorder()
	handler.ServeHTTP(clearDLQRec, clearDLQ)
	if clearDLQRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /drill/clear-metric, got %d", clearDLQRec.Code)
	}

	metricsOutputAfter := DefaultRegistry.Gather()
	if !strings.Contains(metricsOutputAfter, `rabbitmq_dlq_messages_gauge{queue="notifications"} 0`) {
		t.Errorf("expected dlq gauge 0 in metrics output after clear, got:\n%s", metricsOutputAfter)
	}
}

func TestDrillResponder_TestSinkMockUpstream(t *testing.T) {
	responder := NewDrillResponder("test-drill-token")
	testServer := httptest.NewServer(responder.Handler())
	defer testServer.Close()

	// Route platform escalation to responder's own test sink
	responder.SetTeamForwardURL("platform-sre", testServer.URL+"/test-sink")
	handler := responder.Handler()

	payload := `{"receiver":"platform_sre_escalation","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ServiceTargetDown","team":"platform-sre"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/escalations/platform-sre", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer test-drill-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when forwarding to /test-sink, got %d", rec.Code)
	}

	sinkAlerts := responder.TestSinkAlerts()
	if len(sinkAlerts) != 1 || sinkAlerts[0].Receiver != "platform_sre_escalation" {
		t.Fatalf("expected test sink to capture alert, got: %+v", sinkAlerts)
	}

	// Verify unauthenticated access to /test-sink is rejected with 401
	unauthGet := httptest.NewRequest(http.MethodGet, "/test-sink", nil)
	unauthGetRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthGetRec, unauthGet)
	if unauthGetRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated GET /test-sink, got %d", unauthGetRec.Code)
	}

	unauthPost := httptest.NewRequest(http.MethodPost, "/test-sink", strings.NewReader(payload))
	unauthPostRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthPostRec, unauthPost)
	if unauthPostRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated POST /test-sink, got %d", unauthPostRec.Code)
	}

	// Verify authenticated GET /test-sink returns captured alert
	reqGetSink := httptest.NewRequest(http.MethodGet, "/test-sink", nil)
	reqGetSink.Header.Set("Authorization", "Bearer test-drill-token")
	recGetSink := httptest.NewRecorder()
	handler.ServeHTTP(recGetSink, reqGetSink)
	if recGetSink.Code != http.StatusOK || !strings.Contains(recGetSink.Body.String(), "ServiceTargetDown") {
		t.Fatalf("expected GET /test-sink to return captured alert, got code %d body %s", recGetSink.Code, recGetSink.Body.String())
	}

	// Verify ?fail=500 triggers 502 Bad Gateway
	responder.SetTeamForwardURL("platform-sre", testServer.URL+"/test-sink?fail=500")
	reqFail := httptest.NewRequest(http.MethodPost, "/escalations/platform-sre", strings.NewReader(payload))
	reqFail.Header.Set("Authorization", "Bearer test-drill-token")
	recFail := httptest.NewRecorder()
	handler.ServeHTTP(recFail, reqFail)

	if recFail.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway when test sink fails with 500, got %d", recFail.Code)
	}
}

func TestDrillResponder_InduceMetricLabelValidation(t *testing.T) {
	responder := NewDrillResponder("test-drill-token")
	handler := responder.Handler()

	// 1. Rejects invalid payment exception labels
	invalidExceptionPayloads := []string{
		`{"metric":"payment_exceptions_total","label":"UNKNOWN_EXCEPTION"}`,
		`{"metric":"payment_exceptions_total","label":"user_phone_0712345678"}`,
		`{"metric":"payment_exceptions_total","label":"token_12345"}`,
	}
	for _, p := range invalidExceptionPayloads {
		req := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(p))
		req.Header.Set("Authorization", "Bearer test-drill-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for payload %s, got %d", p, rec.Code)
		}
	}

	// 2. Rejects invalid queue names
	invalidQueuePayloads := []string{
		`{"metric":"rabbitmq_dlq_messages_gauge","label":"custom_random_queue","value":3}`,
		`{"metric":"rabbitmq_dlq_messages_gauge","label":"ord_123456789","value":1}`,
	}
	for _, p := range invalidQueuePayloads {
		req := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(p))
		req.Header.Set("Authorization", "Bearer test-drill-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for payload %s, got %d", p, rec.Code)
		}
	}

	// 2b. Rejects invalid HTTP status codes for http_requests_total
	invalidStatusPayloads := []string{
		`{"metric":"http_requests_total","label":"999","value":1}`,
		`{"metric":"http_requests_total","label":"unknown_status","value":1}`,
	}
	for _, p := range invalidStatusPayloads {
		req := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(p))
		req.Header.Set("Authorization", "Bearer test-drill-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for status payload %s, got %d", p, rec.Code)
		}
	}

	// 3. Rejects negative values
	negativeValuePayloads := []string{
		`{"metric":"rabbitmq_dlq_messages_gauge","label":"notifications","value":-1}`,
		`{"metric":"unresolved_payments_gauge","value":-5}`,
	}
	for _, p := range negativeValuePayloads {
		req := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(p))
		req.Header.Set("Authorization", "Bearer test-drill-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for negative payload %s, got %d", p, rec.Code)
		}
	}

	// 4. Rejects invalid queue in /drill/clear-metric
	clearInvalidReq := httptest.NewRequest(http.MethodPost, "/drill/clear-metric", strings.NewReader(`{"metric":"rabbitmq_dlq_messages_gauge","label":"invalid_queue"}`))
	clearInvalidReq.Header.Set("Authorization", "Bearer test-drill-token")
	clearInvalidRec := httptest.NewRecorder()
	handler.ServeHTTP(clearInvalidRec, clearInvalidReq)
	if clearInvalidRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request from /drill/clear-metric with invalid queue, got %d", clearInvalidRec.Code)
	}

	// 5. Accepts valid whitelisted labels
	validPayloads := []string{
		`{"metric":"payment_exceptions_total","label":"AMOUNT_MISMATCH"}`,
		`{"metric":"payment_exceptions_total","label":"UNMAPPED_TRANSACTION"}`,
		`{"metric":"rabbitmq_dlq_messages_gauge","label":"notifications","value":2}`,
		`{"metric":"rabbitmq_dlq_messages_gauge","label":"dead_letter","value":1}`,
		`{"metric":"unresolved_payments_gauge","value":0}`,
		`{"metric":"http_requests_total","label":"500","value":10}`,
		`{"metric":"http_requests_total","label":"200","value":5}`,
	}
	for _, p := range validPayloads {
		req := httptest.NewRequest(http.MethodPost, "/drill/induce-metric", strings.NewReader(p))
		req.Header.Set("Authorization", "Bearer test-drill-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for valid payload %s, got %d", p, rec.Code)
		}
	}
}
