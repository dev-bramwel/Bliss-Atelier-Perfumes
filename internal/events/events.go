// Package events defines the canonical event envelope and event payloads
// matching the schema defined in docs/api-and-event-contracts.md.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EventType constants identifying versioned domain events.
const (
	TypeOrderCreated   = "order.created.v1"
	TypeOrderPaid      = "order.paid.v1"
	TypePaymentAttempt = "payment.attempt.v1"
	TypeDeliveryQuoted = "delivery.quoted.v1"

	// No-PII analytics event types streamed to Kafka.
	TypeAnalyticsOrderCreated      = "analytics.order.created.v1"
	TypeAnalyticsOrderPaid         = "analytics.order.paid.v1"
	TypeAnalyticsExchangeCompleted = "analytics.exchange.completed.v1"
	TypeAnalyticsPaymentCorrection = "analytics.payment.correction.v1"

	CurrencyKES = "KES"
)

// Envelope is the standard event envelope required by docs/api-and-event-contracts.md.
type Envelope struct {
	EventID          string          `json:"eventId"`
	EventType        string          `json:"eventType"`
	SchemaVersion    int             `json:"schemaVersion"`
	OccurredAt       time.Time       `json:"occurredAt"`
	AggregateID      string          `json:"aggregateId"`
	AggregateVersion int             `json:"aggregateVersion"`
	CorrelationID    string          `json:"correlationId"`
	Payload          json.RawMessage `json:"payload"`
}

// OrderItemPayload snapshots an item inside an order event.
type OrderItemPayload struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// OrderCreatedPayload is emitted when an order is created.
type OrderCreatedPayload struct {
	OrderID          string             `json:"orderId"`
	CustomerName     string             `json:"customerName"`
	CustomerPhone    string             `json:"customerPhone"`
	DeliveryLocation string             `json:"deliveryLocation"`
	TotalKes         int                `json:"totalKes"`
	Items            []OrderItemPayload `json:"items"`
	CreatedAt        time.Time          `json:"createdAt"`
}

// OrderPaidPayload is emitted when an order is paid and confirmed.
type OrderPaidPayload struct {
	OrderID       string    `json:"orderId"`
	ReceiptNumber string    `json:"receiptNumber"`
	AmountKes     int       `json:"amountKes"`
	PaidAt        time.Time `json:"paidAt"`
}

// PaymentAttemptPayload is emitted when a payment attempt transitions.
type PaymentAttemptPayload struct {
	AttemptID             string    `json:"attemptId"`
	OrderID               string    `json:"orderId"`
	AttemptNumber         int       `json:"attemptNumber"`
	AmountKes             int       `json:"amountKes"`
	Phone                 string    `json:"phone"`
	Provider              string    `json:"provider"`
	Status                string    `json:"status"`
	ProviderTransactionID string    `json:"providerTransactionId,omitempty"`
	ErrorMessage          string    `json:"errorMessage,omitempty"`
	Timestamp             time.Time `json:"timestamp"`
}

// DeliveryQuotedPayload is emitted when a delivery quote is created or updated.
type DeliveryQuotedPayload struct {
	QuoteID   string    `json:"quoteId"`
	OrderID   string    `json:"orderId"`
	FeeKes    int       `json:"feeKes"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// AnalyticsOrderCreatedPayload is a versioned, no-PII analytics event emitted when an order is created.
type AnalyticsOrderCreatedPayload struct {
	OrderID   string             `json:"orderId"`
	Currency  string             `json:"currency"`
	TotalKes  int                `json:"totalKes"`
	ItemCount int                `json:"itemCount"`
	Items     []OrderItemPayload `json:"items"`
	CreatedAt time.Time          `json:"createdAt"`
}

// AnalyticsOrderPaidPayload is a versioned, no-PII analytics event emitted when an order settles as PAID.
type AnalyticsOrderPaidPayload struct {
	OrderID           string    `json:"orderId"`
	Currency          string    `json:"currency"`
	ProductRevenueKes int       `json:"productRevenueKes"`
	TransportFeeKes   int       `json:"transportFeeKes"`
	FeeRecipient      string    `json:"feeRecipient,omitempty"`
	PaidAt            time.Time `json:"paidAt"`
}

// AnalyticsExchangeCompletedPayload is a versioned, no-PII analytics event emitted when an exchange completes.
type AnalyticsExchangeCompletedPayload struct {
	ExchangeID         string    `json:"exchangeId"`
	OrderID            string    `json:"orderId"`
	Currency           string    `json:"currency"`
	PriceDifferenceKes int       `json:"priceDifferenceKes"`
	CompletedAt        time.Time `json:"completedAt"`
}

// AnalyticsPaymentCorrectionPayload is a versioned, no-PII analytics event emitted when a payment audit entry is recorded.
type AnalyticsPaymentCorrectionPayload struct {
	AuditID    string    `json:"auditId"`
	OrderID    string    `json:"orderId"`
	Action     string    `json:"action"`
	Currency   string    `json:"currency"`
	AmountKes  int       `json:"amountKes"`
	RecordedAt time.Time `json:"recordedAt"`
}

// NewEvent creates a new Envelope with generated eventId and UTC occurredAt.
func NewEvent(eventType, aggregateID string, aggregateVersion int, correlationID string, payload any) (Envelope, error) {
	if eventType == "" {
		return Envelope{}, errors.New("eventType is required")
	}
	if aggregateID == "" {
		return Envelope{}, errors.New("aggregateId is required")
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("failed to marshal event payload: %w", err)
	}

	return Envelope{
		EventID:          generateEventID(),
		EventType:        eventType,
		SchemaVersion:    1,
		OccurredAt:       time.Now().UTC(),
		AggregateID:      aggregateID,
		AggregateVersion: aggregateVersion,
		CorrelationID:    correlationID,
		Payload:          rawPayload,
	}, nil
}

// Validate ensures the envelope satisfies required fields and constraints.
func (e Envelope) Validate() error {
	if e.EventID == "" {
		return errors.New("eventId cannot be empty")
	}
	if e.EventType == "" {
		return errors.New("eventType cannot be empty")
	}
	if e.SchemaVersion <= 0 {
		return errors.New("schemaVersion must be positive")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("occurredAt cannot be zero")
	}
	if e.AggregateID == "" {
		return errors.New("aggregateId cannot be empty")
	}
	if len(e.Payload) == 0 {
		return errors.New("payload cannot be empty")
	}
	return nil
}

func generateEventID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "evt_" + hex.EncodeToString(b)
}
