package delivery

import (
	"errors"
	"time"
)

// FeeRecipient designates who collects the transport fee.
// When 'courier', customer pays the courier directly upon arrival, and transport fee
// is excluded from merchant collection and M-Pesa STK push.
// When 'merchant', the merchant collects transport fee upfront alongside items.
type FeeRecipient string

const (
	FeeRecipientMerchant FeeRecipient = "merchant"
	FeeRecipientCourier  FeeRecipient = "courier"
)

// QuoteStatus represents the lifecycle state of a versioned quote.
type QuoteStatus string

const (
	QuoteStatusPending  QuoteStatus = "pending"
	QuoteStatusAccepted QuoteStatus = "accepted"
	QuoteStatusRejected QuoteStatus = "rejected"
	QuoteStatusExpired  QuoteStatus = "expired"
)

var (
	ErrQuoteNotFound             = errors.New("delivery quote not found")
	ErrQuoteExpired              = errors.New("delivery quote has expired")
	ErrQuoteConflict             = errors.New("delivery quote version conflict")
	ErrQuoteNotPending           = errors.New("delivery quote is not pending")
	ErrInvalidQuote              = errors.New("invalid delivery quote parameters")
	ErrDeliveryQuoteUnaccepted   = errors.New("delivery quote must be accepted before payment")
	ErrPayableMismatch           = errors.New("payable item and transport snapshots do not reconcile")
	ErrOrderNotFound             = errors.New("order not found")
	ErrOrderNotPending           = errors.New("order is not in pending state")
)

// DeliveryQuote is the domain model corresponding to public.delivery_quote.
type DeliveryQuote struct {
	ID            string       `json:"id"`
	OrderID       string       `json:"order_id"`
	Version       int          `json:"version"`
	AmountKES     int          `json:"amount_kes"`
	FeeRecipient  FeeRecipient `json:"fee_recipient"`
	Arrangement   string       `json:"arrangement"`
	Status        QuoteStatus  `json:"status"`
	ExpiresAt     time.Time    `json:"expires_at"`
	CreatedAt     time.Time    `json:"created_at"`
	AcceptedAt    *time.Time   `json:"accepted_at,omitempty"`
	RejectedAt    *time.Time   `json:"rejected_at,omitempty"`
	CreatedBy     string       `json:"created_by"`
	CustomerNotes string       `json:"customer_notes,omitempty"`
}

// CreateQuoteParams contains parameters for manager quoting.
type CreateQuoteParams struct {
	AmountKES     int          `json:"amount_kes"`
	FeeRecipient  FeeRecipient `json:"fee_recipient"`
	Arrangement   string       `json:"arrangement"`
	ExpiresAt     time.Time    `json:"expires_at"`
	CreatedBy     string       `json:"created_by"`
	CustomerNotes string       `json:"customer_notes,omitempty"`
}

// PublicQuote is a sanitized customer-facing delivery quote representation (no actor PII).
type PublicQuote struct {
	OrderID       string       `json:"order_id"`
	Version       int          `json:"version"`
	AmountKES     int          `json:"amount_kes"`
	FeeRecipient  FeeRecipient `json:"fee_recipient"`
	Arrangement   string       `json:"arrangement"`
	Status        QuoteStatus  `json:"status"`
	ExpiresAt     time.Time    `json:"expires_at"`
	CreatedAt     time.Time    `json:"created_at"`
	AcceptedAt    *time.Time   `json:"accepted_at,omitempty"`
	CustomerNotes string       `json:"customer_notes,omitempty"`
}

// PayableBreakdown captures the reconciled totals for checkout/STK initiation.
type PayableBreakdown struct {
	ItemsSubtotalKES   int          `json:"items_subtotal_kes"`
	TransportFeeKES    int          `json:"transport_fee_kes"`
	FeeRecipient       FeeRecipient `json:"fee_recipient"`
	PayableMerchantKES int          `json:"payable_merchant_kes"`
	CustomerDirectKES  int          `json:"customer_direct_kes"`
	QuoteVersion       int          `json:"quote_version"`
	QuoteStatus        QuoteStatus  `json:"quote_status"`
}
