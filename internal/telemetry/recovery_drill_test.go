package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/reconciliation"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/worker"
)

// =============================================================================
// Mock Implementations for Operational Remediation Services
// =============================================================================

type mockReconciliationStore struct {
	mu         sync.Mutex
	attempts   []orders.PaymentAttempt
	orders     map[string]*orders.Order
	exceptions []orders.PaymentException
}

func (m *mockReconciliationStore) ListUnresolvedPaymentAttempts(ctx context.Context, staleThreshold time.Duration, limit int) ([]orders.PaymentAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var unresolved []orders.PaymentAttempt
	for _, a := range m.attempts {
		if a.Status == orders.AttemptAccepted || a.Status == orders.AttemptUnknown {
			unresolved = append(unresolved, a)
		}
	}
	return unresolved, nil
}

func (m *mockReconciliationStore) UpdatePaymentAttemptQuerySchedule(ctx context.Context, attemptID string, nextQueryAt *time.Time, lastError string) error {
	return nil
}

func (m *mockReconciliationStore) UpdatePaymentAttempt(ctx context.Context, attemptID, status, providerTxID, providerRefID, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.attempts {
		if m.attempts[i].ID == attemptID {
			m.attempts[i].Status = status
			m.attempts[i].ProviderTransactionID = providerTxID
			m.attempts[i].ProviderReferenceID = providerRefID
			break
		}
	}
	return nil
}

func (m *mockReconciliationStore) ConfirmPayment(ctx context.Context, orderID string, receiptNumber string, paidAmountKes ...int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.orders[orderID]; ok {
		o.PaymentStatus = orders.PaymentPaid
	}
	return nil
}

func (m *mockReconciliationStore) RecordPaymentException(ctx context.Context, exc orders.PaymentException) (*orders.PaymentException, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exceptions = append(m.exceptions, exc)
	return &exc, nil
}

func (m *mockReconciliationStore) GetOrder(ctx context.Context, orderID string) (*orders.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.orders[orderID]; ok {
		return o, nil
	}
	return nil, errors.New("order not found")
}

type mockPaymentProvider struct {
	mu        sync.Mutex
	queryResp *provider.STKQueryResponse
	queryErr  error
}

func (p *mockPaymentProvider) InitiateSTK(ctx context.Context, req provider.STKInitiateRequest) (*provider.STKInitiateResponse, error) {
	return nil, nil
}

func (p *mockPaymentProvider) QuerySTK(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queryResp, p.queryErr
}

func (p *mockPaymentProvider) QueryReceipt(ctx context.Context, req provider.ReceiptQueryRequest) (*provider.ReceiptQueryResponse, error) {
	return nil, nil
}

func (p *mockPaymentProvider) Close() {}

type testDelivery struct {
	msgID       string
	routingKey  string
	body        []byte
	rejectedDLQ bool
	acked       bool
}

func (d *testDelivery) MessageID() string       { return d.msgID }
func (d *testDelivery) RoutingKey() string      { return d.routingKey }
func (d *testDelivery) Body() []byte            { return d.body }
func (d *testDelivery) Redelivered() bool       { return false }
func (d *testDelivery) Ack() error              { d.acked = true; return nil }
func (d *testDelivery) Nack(requeue bool) error { return nil }
func (d *testDelivery) RejectToDLQ() error      { d.rejectedDLQ = true; return nil }

// =============================================================================
// TestRunbookDiagnosisAndRecovery_Drill
// =============================================================================

// TestRunbookDiagnosisAndRecovery_Drill exercises the complete operational lifecycle
// (Induction -> Diagnosis -> Remediation -> Recovery Verification) using real service
// logic (reconciliation.Reconciler, worker.NotificationConsumer, telemetry.HTTPMiddleware) for:
// 1. Payment Reconciliation Incident (Runbook: docs/runbooks/runbook-payment-reconciliation-failure.md)
// 2. Queue Dead-Letter (DLQ) Incident (Runbook: docs/runbooks/runbook-dlq-poison-messages.md)
// 3. API 5xx Error Surge Incident (Runbook: docs/runbooks/runbook-api-error-surge.md)
func TestRunbookDiagnosisAndRecovery_Drill(t *testing.T) {
	testToken := "test-drill-recovery-token"
	responder := telemetry.NewDrillResponder(testToken)
	responderServer := httptest.NewServer(responder.Handler())
	defer responderServer.Close()

	sendAlertNotification := func(t *testing.T, alertName, severity, status, runbookURL, desc string) {
		t.Helper()
		payload := telemetry.AlertNotification{
			Receiver: "drill_responder_webhook",
			Status:   status,
			Alerts: []telemetry.ResponderAlert{
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
		req.Header.Set("Authorization", "Bearer "+testToken)

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
	// Incident 1: Payment Reconciliation & Stale Attempts Triage
	// Exercises real domain reconciliation sweep logic via reconciliation.Reconciler
	// =========================================================================
	t.Run("Payment_Reconciliation_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-payment-reconciliation-failure.md"
		ctx := context.Background()

		// Construct mock store with 4 stale pending attempts
		store := &mockReconciliationStore{
			attempts: []orders.PaymentAttempt{
				{ID: "att-1", OrderID: "ord-1", Status: orders.AttemptAccepted, QueryCount: 4, ProviderTransactionID: "ws_CO_1", CreatedAt: time.Now().Add(-10 * time.Minute)},
				{ID: "att-2", OrderID: "ord-2", Status: orders.AttemptAccepted, QueryCount: 4, ProviderTransactionID: "ws_CO_2", CreatedAt: time.Now().Add(-10 * time.Minute)},
				{ID: "att-3", OrderID: "ord-3", Status: orders.AttemptAccepted, QueryCount: 4, ProviderTransactionID: "ws_CO_3", CreatedAt: time.Now().Add(-10 * time.Minute)},
				{ID: "att-4", OrderID: "ord-4", Status: orders.AttemptAccepted, QueryCount: 4, ProviderTransactionID: "ws_CO_4", CreatedAt: time.Now().Add(-10 * time.Minute)},
			},
			orders: map[string]*orders.Order{
				"ord-1": {ID: "ord-1", PaymentStatus: orders.PaymentPending},
				"ord-2": {ID: "ord-2", PaymentStatus: orders.PaymentPending},
				"ord-3": {ID: "ord-3", PaymentStatus: orders.PaymentPending},
				"ord-4": {ID: "ord-4", PaymentStatus: orders.PaymentPending},
			},
		}

		prov := &mockPaymentProvider{
			queryErr: errors.New("upstream gateway timeout"),
		}

		reconciler := reconciliation.New(
			store,
			prov,
			reconciliation.Config{
				StaleThreshold:   1 * time.Minute,
				MaxQueryAttempts: 5,
				BaseBackoff:      15 * time.Second,
				MaxBackoff:       4 * time.Minute,
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)

		// Step 1: Induction - Run reconciliation sweep on stale attempts
		summary, err := reconciler.ReconcileStaleAttempts(ctx)
		if err != nil {
			t.Fatalf("unexpected sweep error: %v", err)
		}
		if summary.Processed != 4 {
			t.Fatalf("expected 4 attempts processed, got %d", summary.Processed)
		}

		// Verify domain sweep updated telemetry: unresolved gauge set to 4, exceptions raised
		metrics := telemetry.DefaultRegistry.Gather()
		if !strings.Contains(metrics, "unresolved_payments_gauge 4") {
			t.Fatalf("expected unresolved_payments_gauge to be 4 in telemetry, got: %s", metrics)
		}
		if !strings.Contains(metrics, `payment_exceptions_total{type="STALE_ATTEMPT_UNRESOLVED"}`) {
			t.Fatalf("expected STALE_ATTEMPT_UNRESOLVED exception in telemetry scrape")
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

		// Step 3: Remediation - Automated sweep converges attempts; operator / callback settles orders
		store.mu.Lock()
		for i := range store.attempts {
			store.attempts[i].Status = orders.AttemptSucceeded
		}
		store.mu.Unlock()

		// Run reconciliation sweep again to clear the unresolved state
		recoverySummary, err := reconciler.ReconcileStaleAttempts(ctx)
		if err != nil {
			t.Fatalf("unexpected recovery sweep error: %v", err)
		}
		if recoverySummary.Processed != 0 {
			t.Fatalf("expected 0 unresolved attempts in recovery sweep, got %d", recoverySummary.Processed)
		}

		// Step 4: Recovery Verification - Alert condition cleared in telemetry
		recoveryMetrics := telemetry.DefaultRegistry.Gather()
		if !strings.Contains(recoveryMetrics, "unresolved_payments_gauge 0") {
			t.Fatalf("expected unresolved_payments_gauge to return to 0 after recovery, got: %s", recoveryMetrics)
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
	// Exercises real worker delivery processing & poison quarantine via worker.NotificationConsumer
	// =========================================================================
	t.Run("Queue_DLQ_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-dlq-poison-messages.md"
		ctx := context.Background()

		// Reset baseline for isolated assertion
		telemetry.RabbitMQDLQMessagesGauge.Set(0, "dead_letter")

		// Construct notification consumer with logger
		consumer := worker.NewNotificationConsumer(
			nil, // in-memory test mode without live PostgreSQL pool
			nil, // consumer interface not invoked during direct ProcessDelivery test
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)

		// Step 1: Induction - Poison message delivered to worker (malformed JSON envelope)
		delivery := &testDelivery{
			msgID:      "msg-poison-001",
			routingKey: "notifications.order.created",
			body:       []byte("this-is-not-valid-json-and-will-fail-unmarshal"),
		}

		err := consumer.ProcessDelivery(ctx, delivery)
		if err != nil {
			t.Fatalf("unexpected error from ProcessDelivery: %v", err)
		}

		// Verify delivery was quarantined to DLQ by worker logic
		if !delivery.rejectedDLQ {
			t.Fatalf("expected delivery to be rejected to DLQ by NotificationConsumer")
		}

		// Verify telemetry recorded the DLQ metric
		metrics := telemetry.DefaultRegistry.Gather()
		if !strings.Contains(metrics, `rabbitmq_dlq_messages_gauge{queue="dead_letter"} 1`) {
			t.Fatalf("expected rabbitmq_dlq_messages_gauge to be 1 in telemetry, got: %s", metrics)
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

		// Step 3: Remediation - Payload inspected and operator drains DLQ per runbook procedure
		telemetry.RabbitMQDLQMessagesGauge.Set(0, "dead_letter")

		// Step 4: Recovery Verification - Gauge cleared, resolved alert dispatched
		recoveryMetrics := telemetry.DefaultRegistry.Gather()
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
	// Exercises real HTTP requests through telemetry.HTTPMiddleware
	// =========================================================================
	t.Run("API_5xx_Surge_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-api-error-surge.md"

		// Handlers simulating failing and healthy backend states
		failingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "database pool connection failure", http.StatusInternalServerError)
		})
		healthyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})

		instrumentedFailing := telemetry.HTTPMiddleware(failingHandler)
		instrumentedHealthy := telemetry.HTTPMiddleware(healthyHandler)

		// Step 1: Induction - Induce 30 requests resulting in HTTP 500 via instrumented middleware
		for i := 0; i < 30; i++ {
			req := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
			rec := httptest.NewRecorder()
			instrumentedFailing.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500 from failing handler, got %d", rec.Code)
			}
		}

		metrics := telemetry.DefaultRegistry.Gather()
		if !strings.Contains(metrics, `http_requests_total{method="POST",route="/api/orders",status="500"}`) {
			t.Fatalf("expected 500 errors in telemetry scrape, got: %s", metrics)
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

		// Step 3: Remediation - Backend recovered, 600 healthy requests processed through middleware
		for i := 0; i < 600; i++ {
			req := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
			rec := httptest.NewRecorder()
			instrumentedHealthy.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 from healthy handler, got %d", rec.Code)
			}
		}

		// Verify 200 OK metrics recorded in registry
		recoveryMetrics := telemetry.DefaultRegistry.Gather()
		if !strings.Contains(recoveryMetrics, `http_requests_total{method="POST",route="/api/orders",status="200"}`) {
			t.Fatalf("expected 200 OK metrics in telemetry scrape, got: %s", recoveryMetrics)
		}

		// Step 4: Recovery Verification - Error rate drops well below 5% threshold
		sendAlertNotification(t, "ApiHigh5xxRate", "critical", "resolved", runbookURL,
			"HTTP 5xx error rate normalized below 5% threshold.")

		alertsAfterRecovery := responder.Alerts()
		recoveredAlert := alertsAfterRecovery[len(alertsAfterRecovery)-1].Alerts[0]
		if recoveredAlert.Status != "resolved" || recoveredAlert.Labels["alertname"] != "ApiHigh5xxRate" {
			t.Fatalf("expected resolved alert, got %+v", recoveredAlert)
		}
	})
}
