package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewEvent_OrderCreated(t *testing.T) {
	payload := OrderCreatedPayload{
		OrderID:          "ord-123",
		CustomerName:     "Test User",
		CustomerPhone:    "254712345678",
		DeliveryLocation: "Mombasa",
		TotalKes:         2500,
		Items: []OrderItemPayload{
			{ProductID: "prod-1", Quantity: 2},
		},
		CreatedAt: time.Now().UTC(),
	}

	evt, err := NewEvent(TypeOrderCreated, payload.OrderID, 1, "corr-abc", payload)
	if err != nil {
		t.Fatalf("failed to create event: %v", err)
	}

	if err := evt.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	if evt.EventType != TypeOrderCreated {
		t.Errorf("expected TypeOrderCreated, got %s", evt.EventType)
	}
	if evt.AggregateID != "ord-123" {
		t.Errorf("expected aggregateId ord-123, got %s", evt.AggregateID)
	}
	if evt.CorrelationID != "corr-abc" {
		t.Errorf("expected correlationId corr-abc, got %s", evt.CorrelationID)
	}
	if evt.SchemaVersion != 1 {
		t.Errorf("expected schemaVersion 1, got %d", evt.SchemaVersion)
	}

	// Verify serialization and deserialization
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("failed to marshal envelope: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}

	var decodedPayload OrderCreatedPayload
	if err := json.Unmarshal(decoded.Payload, &decodedPayload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if decodedPayload.OrderID != payload.OrderID || decodedPayload.TotalKes != payload.TotalKes {
		t.Errorf("payload mismatch: %+v vs %+v", decodedPayload, payload)
	}
}

func TestEnvelope_Validate(t *testing.T) {
	tests := []struct {
		name    string
		env     Envelope
		wantErr bool
	}{
		{
			name: "Valid",
			env: Envelope{
				EventID:       "evt_1",
				EventType:     "test.event.v1",
				SchemaVersion: 1,
				OccurredAt:    time.Now().UTC(),
				AggregateID:   "agg_1",
				Payload:       json.RawMessage(`{}`),
			},
			wantErr: false,
		},
		{
			name: "MissingEventID",
			env: Envelope{
				EventType:     "test.event.v1",
				SchemaVersion: 1,
				OccurredAt:    time.Now().UTC(),
				AggregateID:   "agg_1",
				Payload:       json.RawMessage(`{}`),
			},
			wantErr: true,
		},
		{
			name: "MissingEventType",
			env: Envelope{
				EventID:       "evt_1",
				SchemaVersion: 1,
				OccurredAt:    time.Now().UTC(),
				AggregateID:   "agg_1",
				Payload:       json.RawMessage(`{}`),
			},
			wantErr: true,
		},
		{
			name: "ZeroSchemaVersion",
			env: Envelope{
				EventID:       "evt_1",
				EventType:     "test.event.v1",
				SchemaVersion: 0,
				OccurredAt:    time.Now().UTC(),
				AggregateID:   "agg_1",
				Payload:       json.RawMessage(`{}`),
			},
			wantErr: true,
		},
		{
			name: "MissingAggregateID",
			env: Envelope{
				EventID:       "evt_1",
				EventType:     "test.event.v1",
				SchemaVersion: 1,
				OccurredAt:    time.Now().UTC(),
				Payload:       json.RawMessage(`{}`),
			},
			wantErr: true,
		},
		{
			name: "EmptyPayload",
			env: Envelope{
				EventID:       "evt_1",
				EventType:     "test.event.v1",
				SchemaVersion: 1,
				OccurredAt:    time.Now().UTC(),
				AggregateID:   "agg_1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.env.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
