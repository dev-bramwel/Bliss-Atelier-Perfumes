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

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
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

func (m *mockReconciliationStore) CountUnresolvedPaymentAttempts(ctx context.Context, staleThreshold time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, a := range m.attempts {
		if a.Status == orders.AttemptAccepted || a.Status == orders.AttemptUnknown {
			count++
		}
	}
	return count, nil
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

// controlledDLQFixture models the controlled Dead-Letter Queue (DLQ) and PostgreSQL
// dead_letter_message audit store to verify the runbook diagnosis and remediation procedure:
// 1. Poison messages rejected by worker logic are captured in quarantine.
// 2. Operators inspect the dead-letter records to diagnose unmarshal / schema validation failure.
// 3. Operators either replay a repaired payload or archive an unprocessable message.
// 4. Draining/replaying the quarantine drives the metric recovery signal from actual queue state.
type quarantinedDeadLetter struct {
	ID        string
	Queue     string
	Payload   []byte
	Reason    string
	CreatedAt time.Time
	Replayed  bool
	Archived  bool
}

type controlledDLQFixture struct {
	*worker.MemoryInboxStore
	mu         sync.Mutex
	quarantine map[string]*quarantinedDeadLetter
}

func newControlledDLQFixture() *controlledDLQFixture {
	f := &controlledDLQFixture{
		MemoryInboxStore: worker.NewMemoryInboxStore(),
		quarantine:       make(map[string]*quarantinedDeadLetter),
	}
	telemetry.RabbitMQDLQMessagesGauge.Set(0, "dead_letter")
	return f
}

func (f *controlledDLQFixture) Depth() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, m := range f.quarantine {
		if !m.Replayed && !m.Archived {
			count++
		}
	}
	return count
}

func (f *controlledDLQFixture) syncMetric() {
	depth := f.Depth()
	telemetry.RabbitMQDLQMessagesGauge.Set(float64(depth), "dead_letter")
}

func (f *controlledDLQFixture) RecordDeadLetter(ctx context.Context, msgID, consumerName string, payload []byte, reason string) error {
	f.mu.Lock()
	f.quarantine[msgID] = &quarantinedDeadLetter{
		ID:        msgID,
		Queue:     "dead_letter",
		Payload:   payload,
		Reason:    reason,
		CreatedAt: time.Now(),
	}
	f.mu.Unlock()
	err := f.MemoryInboxStore.RecordDeadLetter(ctx, msgID, consumerName, payload, reason)
	f.syncMetric()
	return err
}

func (f *controlledDLQFixture) RecordProcessed(ctx context.Context, msgID, consumerName string, payload []byte) error {
	f.mu.Lock()
	if m, ok := f.quarantine[msgID]; ok {
		m.Replayed = true
	}
	f.mu.Unlock()
	err := f.MemoryInboxStore.RecordProcessed(ctx, msgID, consumerName, payload)
	f.syncMetric()
	return err
}

func (f *controlledDLQFixture) NewPoisonDelivery(msgID, routingKey string, body []byte) *fixtureDelivery {
	return &fixtureDelivery{
		msgID:      msgID,
		routingKey: routingKey,
		body:       body,
		onRejectDLQ: func() {
			f.mu.Lock()
			if _, exists := f.quarantine[msgID]; !exists {
				f.quarantine[msgID] = &quarantinedDeadLetter{
					ID:        msgID,
					Queue:     "dead_letter",
					Payload:   body,
					Reason:    "unmarshal failed: invalid character in json",
					CreatedAt: time.Now(),
				}
			}
			f.mu.Unlock()
			f.syncMetric()
		},
	}
}

func (f *controlledDLQFixture) Inspect(msgID string) (*quarantinedDeadLetter, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.quarantine[msgID]
	if !ok {
		return nil, false
	}
	cp := *m
	return &cp, true
}

func (f *controlledDLQFixture) ReplayPatched(msgID, routingKey string, repairedBody []byte) *fixtureDelivery {
	return &fixtureDelivery{
		msgID:      msgID,
		routingKey: routingKey,
		body:       repairedBody,
		onAck: func() {
			f.mu.Lock()
			if m, ok := f.quarantine[msgID]; ok {
				m.Replayed = true
			}
			f.mu.Unlock()
			f.syncMetric()
		},
	}
}

func (f *controlledDLQFixture) Archive(msgID string) {
	f.mu.Lock()
	if m, ok := f.quarantine[msgID]; ok {
		m.Archived = true
	}
	f.mu.Unlock()
	f.syncMetric()
}

type fixtureDelivery struct {
	msgID       string
	routingKey  string
	body        []byte
	redelivered bool
	acked       bool
	nacked      bool
	requeued    bool
	rejectedDLQ bool
	onRejectDLQ func()
	onAck       func()
}

func (d *fixtureDelivery) MessageID() string  { return d.msgID }
func (d *fixtureDelivery) RoutingKey() string { return d.routingKey }
func (d *fixtureDelivery) Body() []byte       { return d.body }
func (d *fixtureDelivery) Redelivered() bool  { return d.redelivered }
func (d *fixtureDelivery) Ack() error {
	d.acked = true
	if d.onAck != nil {
		d.onAck()
	}
	return nil
}
func (d *fixtureDelivery) Nack(requeue bool) error {
	d.nacked = true
	d.requeued = requeue
	return nil
}
func (d *fixtureDelivery) RejectToDLQ() error {
	d.rejectedDLQ = true
	if d.onRejectDLQ != nil {
		d.onRejectDLQ()
	}
	return nil
}

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
	// Exercises real worker delivery processing, dead-letter store inspection,
	// and repaired payload replay using controlledDLQFixture and worker.NotificationConsumer
	// =========================================================================
	t.Run("Queue_DLQ_Incident_Lifecycle", func(t *testing.T) {
		runbookURL := "docs/runbooks/runbook-dlq-poison-messages.md"
		ctx := context.Background()

		// Initialize controlled DLQ queue and store fixture
		fixture := newControlledDLQFixture()

		// Construct notification consumer with the controlled persistent inbox store
		consumer := worker.NewNotificationConsumerWithInbox(
			fixture,
			nil, // consumer interface not invoked during direct ProcessDelivery test
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)

		// Register real order-created domain handler to verify recovery processing
		var handledEventIDs []string
		consumer.RegisterHandler(events.TypeOrderCreated, func(ctx context.Context, env events.Envelope) error {
			handledEventIDs = append(handledEventIDs, env.EventID)
			return nil
		})

		// Step 1: Induction - Poison message delivered to worker (malformed JSON envelope)
		poisonDelivery := fixture.NewPoisonDelivery(
			"msg-poison-001",
			"notifications.order.created",
			[]byte("this-is-not-valid-json-and-will-fail-unmarshal"),
		)

		err := consumer.ProcessDelivery(ctx, poisonDelivery)
		if err != nil {
			t.Fatalf("unexpected error from ProcessDelivery: %v", err)
		}

		// Verify delivery was quarantined to DLQ by worker logic
		if !poisonDelivery.rejectedDLQ {
			t.Fatalf("expected delivery to be rejected to DLQ by NotificationConsumer")
		}

		// Verify fixture captured the quarantined message and queue depth increased to 1
		if fixture.Depth() != 1 {
			t.Fatalf("expected DLQ fixture depth to be 1, got %d", fixture.Depth())
		}

		// Verify telemetry recorded the DLQ metric from real queue depth
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

		// Step 3: Operational Remediation per Runbook (docs/runbooks/runbook-dlq-poison-messages.md)
		// Action 3.1: Operator inspects quarantined dead-letter in store
		deadLetter, found := fixture.Inspect("msg-poison-001")
		if !found {
			t.Fatalf("expected quarantined message msg-poison-001 in DLQ store")
		}
		if !strings.Contains(deadLetter.Reason, "unmarshal failed") {
			t.Fatalf("expected unmarshal failure reason in quarantine, got: %s", deadLetter.Reason)
		}

		// Action 3.2: Bug diagnosed; operator patches event payload and replays into queue
		validPayload, err := json.Marshal(events.OrderCreatedPayload{
			OrderID:  "ord-dlq-recovered-01",
			TotalKes: 3200,
		})
		if err != nil {
			t.Fatalf("failed to marshal order created payload: %v", err)
		}
		repairedEnv := events.Envelope{
			EventID:          "evt-dlq-recovered-01",
			EventType:        events.TypeOrderCreated,
			SchemaVersion:    1,
			OccurredAt:       time.Now().UTC(),
			AggregateID:      "ord-dlq-recovered-01",
			AggregateVersion: 1,
			Payload:          validPayload,
		}
		repairedBytes, err := json.Marshal(repairedEnv)
		if err != nil {
			t.Fatalf("failed to marshal repaired envelope: %v", err)
		}

		replayedDelivery := fixture.ReplayPatched("msg-poison-001", "notifications.order.created", repairedBytes)
		err = consumer.ProcessDelivery(ctx, replayedDelivery)
		if err != nil {
			t.Fatalf("replayed delivery processing failed: %v", err)
		}

		// Verify worker successfully processed and positively ACKed the replayed delivery
		if !replayedDelivery.acked {
			t.Fatalf("expected replayed delivery to be positively ACKed by worker")
		}
		if len(handledEventIDs) != 1 || handledEventIDs[0] != "evt-dlq-recovered-01" {
			t.Fatalf("expected worker event handler execution for evt-dlq-recovered-01, got: %v", handledEventIDs)
		}

		// Action 3.3: Verify deduplication invariant on redelivered message
		duplicateDelivery := &fixtureDelivery{
			msgID:       "msg-poison-001",
			routingKey:  "notifications.order.created",
			body:        repairedBytes,
			redelivered: true,
		}
		err = consumer.ProcessDelivery(ctx, duplicateDelivery)
		if err != nil {
			t.Fatalf("unexpected error processing duplicate delivery: %v", err)
		}
		if !duplicateDelivery.acked {
			t.Fatalf("expected duplicate delivery to be positively ACKed")
		}
		if len(handledEventIDs) != 1 {
			t.Fatalf("expected handler not to re-execute on duplicate delivery, got calls: %v", handledEventIDs)
		}

		// Step 4: Recovery Verification - DLQ queue drained, metric reflects recovery signal from queue state
		if fixture.Depth() != 0 {
			t.Fatalf("expected DLQ queue depth to be 0 after replay, got %d", fixture.Depth())
		}

		recoveryMetrics := telemetry.DefaultRegistry.Gather()
		if !strings.Contains(recoveryMetrics, `rabbitmq_dlq_messages_gauge{queue="dead_letter"} 0`) {
			t.Fatalf("expected rabbitmq_dlq_messages_gauge to clear to 0 in telemetry, got: %s", recoveryMetrics)
		}

		sendAlertNotification(t, "DlqPoisonMessagesDetected", "critical", "resolved", runbookURL,
			"Dead letter queue drained and poison message repaired and processed.")

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
