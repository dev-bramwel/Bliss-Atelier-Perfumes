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
