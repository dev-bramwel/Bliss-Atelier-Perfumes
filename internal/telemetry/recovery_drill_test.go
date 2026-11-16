package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunbookDiagnosisAndRecovery_Drill exercises the complete operational lifecycle
// (Induction -> Diagnosis -> Remediation -> Recovery Verification) for:
// 1. Payment Reconciliation Incident (Runbook: docs/runbooks/runbook-payment-reconciliation-failure.md)
// 2. Queue Dead-Letter (DLQ) Incident (Runbook: docs/runbooks/runbook-dlq-poison-messages.md)
// 3. API 5xx Error Surge Incident (Runbook: docs/runbooks/runbook-api-error-surge.md)
//
// In each incident scenario, the drill verifies that:
// - Incident symptoms produce firing alerts pointing to the correct actionable runbook URL.
// - Remediating the root cause emits recovery signals that clear the alert condition (status "resolved").
// - The designated drill responder receives both "firing" and "resolved" state notifications with token auth.
func TestRunbookDiagnosisAndRecovery_Drill(t *testing.T) {
	responder := NewDrillResponder()
	responderServer := httptest.NewServer(responder.Handler())
	defer responderServer.Close()

	sendAlertNotification := func(t *testing.T, alertName, severity, status, runbookURL, desc string) {
		t.Helper()
		payload := AlertNotification{
			Receiver: "drill_responder_webhook",
			Status:   status,
			Alerts: []ResponderAlert{
				{
					Status: status,
					Labels: map[string]string{
						"alertname": alertName,
						"severity":  severity,
					},
					Annotations: map[string]string{
						"runbook_url": runbookURL,
						"description": desc,
					},
				},
			},
		}

		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("failed to marshal payload: %v", err)
		}

		req, err := http.NewRequest(http.MethodPost, responderServer.URL+"/alerts", strings.NewReader(string(payloadBytes)))
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+DefaultDrillResponderToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to deliver alert: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK from responder, got %d", resp.StatusCode)
		}
	}

	// =========================================================================
	// Incident 1: Payment Reconciliation & Exception Triage
	// =========================================================================
	t.Run("Payment_Reconciliation_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-payment-reconciliation-failure.md"

		// Step 1: Induction - Payment attempts get stuck & exception occurs
		UnresolvedPaymentsGauge.Set(4) // Alert threshold: > 3
		PaymentExceptionsTotal.Inc("AMOUNT_MISMATCH")

		metrics := DefaultRegistry.Gather()
		if !strings.Contains(metrics, "unresolved_payments_gauge 4") {
			t.Fatalf("expected unresolved_payments_gauge to be 4")
		}
		if !strings.Contains(metrics, `payment_exceptions_total{type="AMOUNT_MISMATCH"}`) {
			t.Fatalf("expected AMOUNT_MISMATCH exception in telemetry scrape")
		}

		// Dispatch "firing" alert to responder
		sendAlertNotification(t, "UnresolvedPaymentSpike", "critical", "firing", runbookURL,
			"4 payment attempts are pending reconciliation beyond threshold.")

		// Step 2: Diagnosis - Verify responder received firing alert pointing to runbook
		alerts := responder.Alerts()
		if len(alerts) == 0 {
			t.Fatalf("responder received no alerts")
		}
		lastAlert := alerts[len(alerts)-1].Alerts[0]
		if lastAlert.Status != "firing" || lastAlert.Labels["alertname"] != "UnresolvedPaymentSpike" {
			t.Fatalf("expected firing UnresolvedPaymentSpike alert, got %+v", lastAlert)
		}
		if lastAlert.Annotations["runbook_url"] != runbookURL {
			t.Fatalf("expected runbook %s, got %s", runbookURL, lastAlert.Annotations["runbook_url"])
		}

		// Step 3: Remediation - Automated sweep runs; operator settles unresolved payments
		ReconciliationRunsTotal.Inc("success")
		UnresolvedPaymentsGauge.Set(0) // Safe action drops unresolved count to 0

		// Step 4: Recovery Verification - Alert condition cleared
		recoveryMetrics := DefaultRegistry.Gather()
		if !strings.Contains(recoveryMetrics, "unresolved_payments_gauge 0") {
			t.Fatalf("expected unresolved_payments_gauge to return to 0 after recovery")
		}

		// Dispatch "resolved" alert to responder
		sendAlertNotification(t, "UnresolvedPaymentSpike", "critical", "resolved", runbookURL,
			"All pending payment attempts successfully reconciled.")

		// Verify responder logged the resolved notification
		alertsAfterRecovery := responder.Alerts()
		recoveredAlert := alertsAfterRecovery[len(alertsAfterRecovery)-1].Alerts[0]
		if recoveredAlert.Status != "resolved" || recoveredAlert.Labels["alertname"] != "UnresolvedPaymentSpike" {
			t.Fatalf("expected resolved alert, got %+v", recoveredAlert)
		}
	})

	// =========================================================================
	// Incident 2: Dead-Letter Queue (DLQ) Poison Messages
	// =========================================================================
	t.Run("Queue_DLQ_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-dlq-poison-messages.md"

		// Step 1: Induction - Poison message quarantined to DLQ
		RabbitMQDLQMessagesGauge.Set(1, "dead_letter") // Alert threshold: > 0

		metrics := DefaultRegistry.Gather()
		if !strings.Contains(metrics, `rabbitmq_dlq_messages_gauge{queue="dead_letter"} 1`) {
			t.Fatalf("expected rabbitmq_dlq_messages_gauge to be 1")
		}

		sendAlertNotification(t, "DlqPoisonMessagesDetected", "critical", "firing", runbookURL,
			"One unprocessable poison message quarantined in dead_letter queue.")

		// Step 2: Diagnosis - Inspect alert and runbook URL
		alerts := responder.Alerts()
		lastAlert := alerts[len(alerts)-1].Alerts[0]
		if lastAlert.Status != "firing" || lastAlert.Labels["alertname"] != "DlqPoisonMessagesDetected" {
			t.Fatalf("expected firing DlqPoisonMessagesDetected alert, got %+v", lastAlert)
		}
		if lastAlert.Annotations["runbook_url"] != runbookURL {
			t.Fatalf("expected runbook %s, got %s", runbookURL, lastAlert.Annotations["runbook_url"])
		}

		// Step 3: Remediation - Payload inspected and replayed/quarantine cleared
		RabbitMQDLQMessagesGauge.Set(0, "dead_letter")

		// Step 4: Recovery Verification - Gauge cleared, resolved alert dispatched
		recoveryMetrics := DefaultRegistry.Gather()
		if !strings.Contains(recoveryMetrics, `rabbitmq_dlq_messages_gauge{queue="dead_letter"} 0`) {
			t.Fatalf("expected rabbitmq_dlq_messages_gauge to clear to 0")
		}

		sendAlertNotification(t, "DlqPoisonMessagesDetected", "critical", "resolved", runbookURL,
			"Dead letter queue drained and poison message quarantined.")

		alertsAfterRecovery := responder.Alerts()
		recoveredAlert := alertsAfterRecovery[len(alertsAfterRecovery)-1].Alerts[0]
		if recoveredAlert.Status != "resolved" || recoveredAlert.Labels["alertname"] != "DlqPoisonMessagesDetected" {
			t.Fatalf("expected resolved alert, got %+v", recoveredAlert)
		}
	})

	// =========================================================================
	// Incident 3: API 5xx Error Surge
	// =========================================================================
	t.Run("API_5xx_Surge_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-api-error-surge.md"

		// Step 1: Induction - Induce 5xx spike exceeding 5% threshold
		for i := 0; i < 30; i++ {
			HTTPRequestsTotal.Inc("POST", "/api/orders", "500")
		}

		metrics := DefaultRegistry.Gather()
		if !strings.Contains(metrics, `http_requests_total{method="POST",route="/api/orders",status="500"}`) {
			t.Fatalf("expected 500 error metric in telemetry scrape")
		}

		sendAlertNotification(t, "ApiHigh5xxRate", "critical", "firing", runbookURL,
			"HTTP 5xx error rate is exceeding 5% over the last minute.")

		// Step 2: Diagnosis - Verify firing alert reaching responder
		alerts := responder.Alerts()
		lastAlert := alerts[len(alerts)-1].Alerts[0]
		if lastAlert.Status != "firing" || lastAlert.Labels["alertname"] != "ApiHigh5xxRate" {
			t.Fatalf("expected firing ApiHigh5xxRate alert, got %+v", lastAlert)
		}
		if lastAlert.Annotations["runbook_url"] != runbookURL {
			t.Fatalf("expected runbook %s, got %s", runbookURL, lastAlert.Annotations["runbook_url"])
		}

		// Step 3: Remediation - Backend recovered, healthy requests resume
		for i := 0; i < 600; i++ {
			HTTPRequestsTotal.Inc("POST", "/api/orders", "200")
		}

		// Step 4: Recovery Verification - Error rate drops well below 5% (30 / 630 = 4.7%)
		sendAlertNotification(t, "ApiHigh5xxRate", "critical", "resolved", runbookURL,
			"HTTP 5xx error rate normalized below 1% threshold.")

		alertsAfterRecovery := responder.Alerts()
		recoveredAlert := alertsAfterRecovery[len(alertsAfterRecovery)-1].Alerts[0]
		if recoveredAlert.Status != "resolved" || recoveredAlert.Labels["alertname"] != "ApiHigh5xxRate" {
			t.Fatalf("expected resolved alert, got %+v", recoveredAlert)
		}
	})
}
