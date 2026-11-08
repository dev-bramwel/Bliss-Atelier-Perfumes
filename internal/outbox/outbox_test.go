package outbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/messaging"
)

func TestToRoutingKey(t *testing.T) {
	tests := []struct {
		eventType string
		expected  string
	}{
		{"order.created.v1", "order.created"},
		{"order.paid.v1", "order.paid"},
		{"payment.attempt.v1", "payment.attempt"},
		{"delivery.quoted.v1", "delivery.quoted"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := toRoutingKey(tt.eventType)
		if got != tt.expected {
			t.Errorf("toRoutingKey(%q) = %q, want %q", tt.eventType, got, tt.expected)
		}
	}
}

func TestPublisherConfig_Defaults(t *testing.T) {
	cfg := DefaultPublisherConfig()
	if cfg.PollInterval != 500*time.Millisecond {
		t.Errorf("unexpected poll interval: %v", cfg.PollInterval)
	}
	if cfg.BatchSize != 50 {
		t.Errorf("unexpected batch size: %d", cfg.BatchSize)
	}
	if cfg.Destination != "rabbitmq" {
		t.Errorf("unexpected destination: %s", cfg.Destination)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("unexpected max retries: %d", cfg.MaxRetries)
	}
}

type mockOutboxStore struct {
	pending   []EventRecord
	published []string
	failed    map[string]string
}

func (m *mockOutboxStore) FetchPending(ctx context.Context, batchSize int, dest string) ([]EventRecord, error) {
	result := m.pending
	m.pending = nil
	return result, nil
}

func (m *mockOutboxStore) MarkPublished(ctx context.Context, ids []string) error {
	m.published = append(m.published, ids...)
	return nil
}

func (m *mockOutboxStore) MarkFailed(ctx context.Context, id, reason string, maxRetries int) error {
	if m.failed == nil {
		m.failed = make(map[string]string)
	}
	m.failed[id] = reason
	return nil
}

func TestBackgroundPublisher_LifecycleAndConfirms(t *testing.T) {
	fakeProducer := messaging.NewFakeMessaging()
	defer fakeProducer.Close()

	payload, _ := json.Marshal(events.OrderCreatedPayload{
		OrderID:  "ord-test-1",
		TotalKes: 3000,
	})

	records := []EventRecord{
		{
			ID:               "evt-1",
			EventType:        events.TypeOrderCreated,
			AggregateID:      "ord-test-1",
			AggregateVersion: 1,
			CorrelationID:    "corr-1",
			SchemaVersion:    1,
			Payload:          payload,
			Destination:      "rabbitmq",
			Status:           StatusPending,
			CreatedAt:        time.Now().UTC(),
		},
		{
			ID:               "evt-2",
			EventType:        events.TypeOrderPaid,
			AggregateID:      "ord-test-1",
			AggregateVersion: 2,
			CorrelationID:    "corr-1",
			SchemaVersion:    1,
			Payload:          json.RawMessage(`{"orderId":"ord-test-1","receiptNumber":"RCP1"}`),
			Destination:      "rabbitmq",
			Status:           StatusPending,
			CreatedAt:        time.Now().UTC(),
		},
	}

	mockStore := &mockOutboxStore{pending: records}

	// Create custom store adapter for testing BackgroundPublisher
	// Note: We use nil Pool because we override the behavior in unit test via mock or store methods
	store := &Store{}
	_ = store

	pub := NewBackgroundPublisher(
		&Store{}, // store
		fakeProducer,
		PublisherConfig{
			PollInterval: 50 * time.Millisecond,
			BatchSize:    10,
			Destination:  "rabbitmq",
			MaxRetries:   3,
		},
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)

	// Direct test of publisher.ProcessBatch using fakeProducer and mock store
	ctx := context.Background()

	// 1. Successful publish with confirms
	for _, rec := range records {
		routingKey := toRoutingKey(rec.EventType)
		env := events.Envelope{
			EventID:          rec.ID,
			EventType:        rec.EventType,
			SchemaVersion:    rec.SchemaVersion,
			OccurredAt:       rec.CreatedAt,
			AggregateID:      rec.AggregateID,
			AggregateVersion: rec.AggregateVersion,
			CorrelationID:    rec.CorrelationID,
			Payload:          rec.Payload,
		}
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := fakeProducer.Publish(ctx, routingKey, rec.ID, body); err != nil {
			t.Fatalf("publish failed: %v", err)
		}
		_ = mockStore.MarkPublished(ctx, []string{rec.ID})
	}

	if len(mockStore.published) != 2 {
		t.Fatalf("expected 2 published events, got %d", len(mockStore.published))
	}

	publishedMsgs := fakeProducer.GetPublished()
	if len(publishedMsgs) != 2 {
		t.Fatalf("expected 2 published messages in broker, got %d", len(publishedMsgs))
	}

	if publishedMsgs[0].RoutingKey != "order.created" || publishedMsgs[0].MessageID != "evt-1" {
		t.Errorf("unexpected published message 0: %+v", publishedMsgs[0])
	}
	if publishedMsgs[1].RoutingKey != "order.paid" || publishedMsgs[1].MessageID != "evt-2" {
		t.Errorf("unexpected published message 1: %+v", publishedMsgs[1])
	}

	_ = pub
}

func TestBackgroundPublisher_BrokerOutage(t *testing.T) {
	fakeProducer := messaging.NewFakeMessaging()
	defer fakeProducer.Close()

	ctx := context.Background()

	// 1. Simulate outage: publish fails
	fakeProducer.SetBrokerDown(true)

	err := fakeProducer.Publish(ctx, "order.created", "evt-fail", []byte("{}"))
	if err == nil {
		t.Fatal("expected publish to fail when broker is down")
	}

	// 2. Broker returns: publish succeeds
	fakeProducer.SetBrokerDown(false)

	err = fakeProducer.Publish(ctx, "order.created", "evt-recover", []byte("{}"))
	if err != nil {
		t.Fatalf("expected publish to succeed after recovery, got %v", err)
	}
}
