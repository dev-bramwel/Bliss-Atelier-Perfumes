package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/messaging"
)

type mockDelivery struct {
	msgID       string
	routingKey  string
	body        []byte
	redelivered bool
	acked       bool
	nacked      bool
	requeued    bool
	dlq         bool
}

func (m *mockDelivery) MessageID() string       { return m.msgID }
func (m *mockDelivery) RoutingKey() string      { return m.routingKey }
func (m *mockDelivery) Body() []byte            { return m.body }
func (m *mockDelivery) Redelivered() bool       { return m.redelivered }
func (m *mockDelivery) Ack() error              { m.acked = true; return nil }
func (m *mockDelivery) Nack(requeue bool) error { m.nacked = true; m.requeued = requeue; return nil }
func (m *mockDelivery) RejectToDLQ() error      { m.dlq = true; return nil }

func TestNotificationConsumer_Deduplication(t *testing.T) {
	fakeMessaging := messaging.NewFakeMessaging()
	defer fakeMessaging.Close()

	consumer := NewNotificationConsumer(
		nil, // in-memory test without pool
		fakeMessaging,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)

	var handlerCalls int32
	consumer.RegisterHandler(events.TypeOrderCreated, func(ctx context.Context, env events.Envelope) error {
		atomic.AddInt32(&handlerCalls, 1)
		return nil
	})

	// Create valid order created event
	payload, _ := json.Marshal(events.OrderCreatedPayload{
		OrderID:  "ord-dedup-1",
		TotalKes: 1200,
	})
	env := events.Envelope{
		EventID:          "evt-dedup-1",
		EventType:        events.TypeOrderCreated,
		SchemaVersion:    1,
		OccurredAt:       time.Now().UTC(),
		AggregateID:      "ord-dedup-1",
		AggregateVersion: 1,
		Payload:          payload,
	}
	envBytes, _ := json.Marshal(env)

	d1 := &mockDelivery{
		msgID:      "evt-dedup-1",
		routingKey: "order.created",
		body:       envBytes,
	}

	// 1. First delivery executes handler and ACKs
	err := consumer.ProcessDelivery(context.Background(), d1)
	if err != nil {
		t.Fatalf("first delivery failed: %v", err)
	}
	if !d1.acked {
		t.Error("expected first delivery to be ACKed")
	}
	if atomic.LoadInt32(&handlerCalls) != 1 {
		t.Errorf("expected 1 handler call, got %d", atomic.LoadInt32(&handlerCalls))
	}
}

func TestNotificationConsumer_PoisonMessageToDLQ(t *testing.T) {
	fakeMessaging := messaging.NewFakeMessaging()
	defer fakeMessaging.Close()

	consumer := NewNotificationConsumer(
		nil,
		fakeMessaging,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)

	// 1. Completely unparseable JSON
	dPoison1 := &mockDelivery{
		msgID:      "poison-msg-1",
		routingKey: "order.created",
		body:       []byte("{corrupted-json-not-valid"),
	}

	err := consumer.ProcessDelivery(context.Background(), dPoison1)
	if err != nil {
		t.Fatalf("expected nil error on poison message handling, got %v", err)
	}
	if !dPoison1.dlq {
		t.Error("expected unparseable poison message to be rejected directly to DLQ")
	}

	// 2. Missing required event envelope fields (schema violation)
	invalidEnv := events.Envelope{
		EventID:       "", // missing ID
		EventType:     "", // missing Type
		SchemaVersion: 0,
	}
	invalidBytes, _ := json.Marshal(invalidEnv)

	dPoison2 := &mockDelivery{
		msgID:      "poison-msg-2",
		routingKey: "order.created",
		body:       invalidBytes,
	}

	err = consumer.ProcessDelivery(context.Background(), dPoison2)
	if err != nil {
		t.Fatalf("expected nil error on poison message handling, got %v", err)
	}
	if !dPoison2.dlq {
		t.Error("expected invalid envelope to be rejected directly to DLQ")
	}

	// 3. Missing MessageID on delivery
	dPoison3 := &mockDelivery{
		msgID:      "",
		routingKey: "order.created",
		body:       []byte("{}"),
	}

	_ = consumer.ProcessDelivery(context.Background(), dPoison3)
	if !dPoison3.dlq {
		t.Error("expected missing message ID delivery to be rejected to DLQ")
	}
}

func TestNotificationConsumer_TransientFailureRequeued(t *testing.T) {
	fakeMessaging := messaging.NewFakeMessaging()
	defer fakeMessaging.Close()

	consumer := NewNotificationConsumer(
		nil,
		fakeMessaging,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)

	// Register handler that simulates transient error (e.g. database network glitch)
	consumer.RegisterHandler(events.TypeOrderCreated, func(ctx context.Context, env events.Envelope) error {
		return errors.New("transient database failure")
	})

	payload, _ := json.Marshal(events.OrderCreatedPayload{
		OrderID:  "ord-transient-1",
		TotalKes: 1000,
	})
	env := events.Envelope{
		EventID:          "evt-transient-1",
		EventType:        events.TypeOrderCreated,
		SchemaVersion:    1,
		OccurredAt:       time.Now().UTC(),
		AggregateID:      "ord-transient-1",
		AggregateVersion: 1,
		Payload:          payload,
	}
	envBytes, _ := json.Marshal(env)

	d := &mockDelivery{
		msgID:      "evt-transient-1",
		routingKey: "order.created",
		body:       envBytes,
	}

	err := consumer.ProcessDelivery(context.Background(), d)
	if err == nil {
		t.Fatal("expected error on transient failure")
	}
	if !d.nacked || !d.requeued {
		t.Error("expected message to be NACKed with requeue=true on transient handler failure")
	}
	if d.dlq {
		t.Error("message should not be routed to DLQ on transient failure")
	}
}

func TestNotificationConsumer_PoisonPayloadQuarantined(t *testing.T) {
	fakeMessaging := messaging.NewFakeMessaging()
	defer fakeMessaging.Close()

	consumer := NewNotificationConsumer(
		nil,
		fakeMessaging,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)

	// Valid envelope but corrupt payload JSON that default handler cannot unmarshal
	env := events.Envelope{
		EventID:          "evt-poison-payload-1",
		EventType:        events.TypeOrderCreated,
		SchemaVersion:    1,
		OccurredAt:       time.Now().UTC(),
		AggregateID:      "ord-poison-1",
		AggregateVersion: 1,
		Payload:          []byte(`{not-json}`),
	}
	envBytes, _ := json.Marshal(env)

	d := &mockDelivery{
		msgID:      "evt-poison-payload-1",
		routingKey: "order.created",
		body:       envBytes,
	}

	err := consumer.ProcessDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected nil error on quarantined poison payload, got %v", err)
	}
	if !d.dlq {
		t.Error("expected message with unparseable payload to be routed to DLQ")
	}
	if d.requeued {
		t.Error("message with unparseable payload should not be requeued")
	}
}

func TestSyncDLQGauge_NilPool(t *testing.T) {
	count, err := SyncDLQGauge(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected nil error on nil pool SyncDLQGauge, got %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 count on nil pool SyncDLQGauge, got %d", count)
	}

	consumer := NewNotificationConsumer(nil, nil, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	cCount, cErr := consumer.SyncDLQGauge(context.Background())
	if cErr != nil {
		t.Fatalf("expected nil error on consumer SyncDLQGauge, got %v", cErr)
	}
	if cCount != 0 {
		t.Fatalf("expected 0 count on consumer SyncDLQGauge, got %d", cCount)
	}
}

func TestNotificationConsumer_PersistentInboxReplayAndDeduplication(t *testing.T) {
	inbox := NewMemoryInboxStore()
	fakeMessaging := messaging.NewFakeMessaging()
	defer fakeMessaging.Close()

	consumer := NewNotificationConsumerWithInbox(
		inbox,
		fakeMessaging,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)

	var handlerCalls int32
	var lastHandledOrderID string
	consumer.RegisterHandler(events.TypeOrderCreated, func(ctx context.Context, env events.Envelope) error {
		atomic.AddInt32(&handlerCalls, 1)
		var p events.OrderCreatedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return err
		}
		lastHandledOrderID = p.OrderID
		return nil
	})

	ctx := context.Background()
	msgID := "msg-replay-test-001"

	// 1. Initial delivery is a poison message (corrupted JSON)
	poisonDelivery := &mockDelivery{
		msgID:      msgID,
		routingKey: "order.created",
		body:       []byte("{corrupt-json-payload"),
	}

	err := consumer.ProcessDelivery(ctx, poisonDelivery)
	if err != nil {
		t.Fatalf("unexpected error processing poison delivery: %v", err)
	}
	if !poisonDelivery.dlq {
		t.Fatalf("expected poison message to be routed to DLQ")
	}

	// Verify persistent inbox recorded dead_lettered status and gauge reflects DLQ depth 1
	rec, exists := inbox.Get(msgID, ConsumerName)
	if !exists {
		t.Fatalf("expected record in inbox for %s", msgID)
	}
	if rec.Status != InboxDeadLettered {
		t.Fatalf("expected status %s, got %s", InboxDeadLettered, rec.Status)
	}
	dlqCount, err := consumer.SyncDLQGauge(ctx)
	if err != nil || dlqCount != 1 {
		t.Fatalf("expected DLQ count 1, got %d (err: %v)", dlqCount, err)
	}

	// 2. Operator repairs payload and replays message with same message ID
	repairedPayload, _ := json.Marshal(events.OrderCreatedPayload{
		OrderID:  "ord-repaired-001",
		TotalKes: 4500,
	})
	repairedEnv := events.Envelope{
		EventID:          "evt-repaired-001",
		EventType:        events.TypeOrderCreated,
		SchemaVersion:    1,
		OccurredAt:       time.Now().UTC(),
		AggregateID:      "ord-repaired-001",
		AggregateVersion: 1,
		Payload:          repairedPayload,
	}
	repairedBytes, _ := json.Marshal(repairedEnv)

	replayedDelivery := &mockDelivery{
		msgID:      msgID,
		routingKey: "order.created",
		body:       repairedBytes,
	}

	err = consumer.ProcessDelivery(ctx, replayedDelivery)
	if err != nil {
		t.Fatalf("unexpected error processing replayed delivery: %v", err)
	}
	if !replayedDelivery.acked {
		t.Fatalf("expected replayed delivery to be positively ACKed")
	}

	// Verify event handler ACTUALLY executed during replay
	if atomic.LoadInt32(&handlerCalls) != 1 {
		t.Fatalf("expected 1 handler call after replay, got %d", atomic.LoadInt32(&handlerCalls))
	}
	if lastHandledOrderID != "ord-repaired-001" {
		t.Fatalf("expected order ID ord-repaired-001, got %s", lastHandledOrderID)
	}

	// Verify persistent inbox status transitioned to processed and DLQ depth cleared to 0
	recAfterReplay, exists := inbox.Get(msgID, ConsumerName)
	if !exists {
		t.Fatalf("expected record in inbox after replay")
	}
	if recAfterReplay.Status != InboxProcessed {
		t.Fatalf("expected status %s after replay, got %s", InboxProcessed, recAfterReplay.Status)
	}
	dlqCountAfterReplay, err := consumer.SyncDLQGauge(ctx)
	if err != nil || dlqCountAfterReplay != 0 {
		t.Fatalf("expected DLQ count 0 after replay, got %d (err: %v)", dlqCountAfterReplay, err)
	}

	// 3. Duplicate delivery of the already processed message arrives
	duplicateDelivery := &mockDelivery{
		msgID:       msgID,
		routingKey:  "order.created",
		body:        repairedBytes,
		redelivered: true,
	}

	err = consumer.ProcessDelivery(ctx, duplicateDelivery)
	if err != nil {
		t.Fatalf("unexpected error processing duplicate delivery: %v", err)
	}
	if !duplicateDelivery.acked {
		t.Fatalf("expected duplicate delivery to be ACKed")
	}

	// Verify deduplication invariant: handler was NOT called again
	if atomic.LoadInt32(&handlerCalls) != 1 {
		t.Fatalf("expected handler calls to remain 1 after duplicate delivery, got %d", atomic.LoadInt32(&handlerCalls))
	}
}
