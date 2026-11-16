package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestSyntheticIncidentDrill_ReachesDesignatedResponder(t *testing.T) {
	testToken := "test-drill-responder-token"
	// 1. Set up Designated Responder Webhook using the actual DrillResponder implementation
	responder := NewDrillResponder(testToken)
	responderServer := httptest.NewServer(responder.Handler())
	defer responderServer.Close()

	// Verify responder liveness endpoint
	liveResp, err := http.Get(responderServer.URL + "/health/live")
	if err != nil {
		t.Fatalf("failed to query responder liveness: %v", err)
	}
	if liveResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /health/live, got %d", liveResp.StatusCode)
	}
	_ = liveResp.Body.Close()

	// 2. Simulate Synthetic Incident 1: Unresolved Payment Exception
	PaymentExceptionsTotal.Inc("AMOUNT_MISMATCH")
	UnresolvedPaymentsGauge.Set(5)

	// Simulate Synthetic Incident 2: Queue DLQ Poison Message
	RabbitMQDLQMessagesGauge.Inc("dead_letter")

	// Simulate Synthetic Incident 3: API 5xx Error Surge
	for i := 0; i < 20; i++ {
		HTTPRequestsTotal.Inc("POST", "/api/orders", "500")
	}

	// 3. Scrape Metrics & Verify State
	metricsOutput := DefaultRegistry.Gather()

	if !strings.Contains(metricsOutput, `payment_exceptions_total{type="AMOUNT_MISMATCH"}`) {
		t.Errorf("expected AMOUNT_MISMATCH metric in scrape output")
	}
	if !strings.Contains(metricsOutput, `unresolved_payments_gauge 5`) {
		t.Errorf("expected unresolved_payments_gauge 5 in scrape output")
	}
	if !strings.Contains(metricsOutput, `rabbitmq_dlq_messages_gauge{queue="dead_letter"} 1`) {
		t.Errorf("expected rabbitmq_dlq_messages_gauge in scrape output")
	}
	if !strings.Contains(metricsOutput, `http_requests_total{method="POST",route="/api/orders",status="500"} 20`) {
		t.Errorf("expected http_requests_total 500 in scrape output")
	}

	// 4. Simulate Alertmanager Evaluation & Webhook Dispatch to Designated Responder
	simulatedAlerts := []struct {
		AlertName   string
		Severity    string
		RunbookURL  string
		Description string
	}{
		{
			AlertName:   "UnresolvedPaymentSpike",
			Severity:    "critical",
			RunbookURL:  "docs/runbooks/runbook-payment-reconciliation-failure.md",
			Description: "5 payment attempts are pending reconciliation beyond threshold.",
		},
		{
			AlertName:   "DlqPoisonMessagesDetected",
			Severity:    "critical",
			RunbookURL:  "docs/runbooks/runbook-dlq-poison-messages.md",
			Description: "One or more unprocessable messages rejected to Dead Letter Queue.",
		},
		{
			AlertName:   "ApiHigh5xxRate",
			Severity:    "critical",
			RunbookURL:  "docs/runbooks/runbook-api-error-surge.md",
			Description: "HTTP 5xx error rate is exceeding 5% over the last minute.",
		},
	}

	for _, sa := range simulatedAlerts {
		payload := AlertNotification{
			Receiver: "drill_responder_webhook",
			Status:   "firing",
			Alerts: []ResponderAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": sa.AlertName,
						"severity":  sa.Severity,
					},
					Annotations: map[string]string{
						"runbook_url": sa.RunbookURL,
						"description": sa.Description,
					},
				},
			},
		}

		payloadBytes, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, responderServer.URL+"/alerts", strings.NewReader(string(payloadBytes)))
		if err != nil {
			t.Fatalf("failed to build alert request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to deliver alert to responder: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK from responder, got %d", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// 5. Verify all synthetic alerts reached designated responder
	receivedAlerts := responder.Alerts()
	if len(receivedAlerts) != 3 {
		t.Fatalf("expected 3 delivered alerts at responder sink, got %d", len(receivedAlerts))
	}

	alertNamesFound := make(map[string]bool)
	for _, notif := range receivedAlerts {
		for _, a := range notif.Alerts {
			alertNamesFound[a.Labels["alertname"]] = true

			// Assert each alert points to an actionable operational runbook
			runbook := a.Annotations["runbook_url"]
			if runbook == "" || !strings.HasPrefix(runbook, "docs/runbooks/") {
				t.Errorf("alert %s missing valid runbook_url: got %s", a.Labels["alertname"], runbook)
			}
		}
	}

	for _, expected := range []string{"UnresolvedPaymentSpike", "DlqPoisonMessagesDetected", "ApiHigh5xxRate"} {
		if !alertNamesFound[expected] {
			t.Errorf("expected alert %s was not delivered to responder", expected)
		}
	}

	// 6. Strict PII & High-Cardinality Telemetry Audit
	// Telemetry metrics and alert payloads must NEVER leak customer phone numbers, addresses, tokens, or order IDs.
	phonePattern := regexp.MustCompile(`(?:\+?254|0)[17]\d{8}`)
	tokenPattern := regexp.MustCompile(`\bgt_[0-9a-fA-F]{64}\b`)
	orderIDPattern := regexp.MustCompile(`\bord_[a-zA-Z0-9_\-]+\b`)

	for _, m := range []string{metricsOutput} {
		if phonePattern.MatchString(m) {
			t.Errorf("PII Leak detected: customer phone number in metrics output: %s", m)
		}
		if tokenPattern.MatchString(m) {
			t.Errorf("Security Leak detected: guest token in metrics output: %s", m)
		}
		if orderIDPattern.MatchString(m) {
			t.Errorf("High-Cardinality violation: dynamic order ID found in metric labels: %s", m)
		}
	}
}
