package provider

import (
	"context"
	"errors"
)

var (
	// ErrProviderTimeout indicates a network timeout, deadline cancellation, or read failure.
	// When initiation times out, the outcome is UNKNOWN (it may or may not have been accepted by the bank).
	ErrProviderTimeout = errors.New("provider request timed out")

	// ErrProviderRejected indicates the provider explicitly rejected the initiation or query.
	ErrProviderRejected = errors.New("provider rejected request")

	// ErrProviderMalformed indicates the provider returned non-JSON, invalid JSON, or missing required response fields.
	ErrProviderMalformed = errors.New("provider returned malformed response")

	// ErrProviderOversized indicates the provider response exceeded the 64KB bounded safety limit.
	ErrProviderOversized = errors.New("provider response exceeded maximum allowable size")

	// ErrProviderUnavailable indicates the provider service is unreachable or returned HTTP 502/503/504.
	ErrProviderUnavailable = errors.New("provider service unavailable")

	// ErrInvalidAmount indicates the requested transaction amount is invalid.
	ErrInvalidAmount = errors.New("invalid transaction amount")

	// ErrInvalidPhone indicates the phone number could not be standardized to 2547XXXXXXXX.
	ErrInvalidPhone = errors.New("invalid customer phone number")
)

// STKInitiateRequest contains parameters for triggering a customer mobile money push prompt.
type STKInitiateRequest struct {
	OrderID          string
	AttemptID        string
	Phone            string
	AmountKes        int
	CallbackURL      string
	AccountReference string
	Description      string
}

// STKInitiateResponse represents the result of a successful STK Push initiation.
type STKInitiateResponse struct {
	TransactionID     string `json:"transactionId"`
	ReferenceID       string `json:"referenceId"`
	StatusCode        string `json:"statusCode"`
	StatusDescription string `json:"statusDescription"`
	CustomerMessage   string `json:"customerMessage,omitempty"`
}

// STKQueryRequest specifies the transaction to query at the provider.
type STKQueryRequest struct {
	TransactionID string `json:"transactionId"`
}

// STKQueryResponse is the status returned by an STK status query.
type STKQueryResponse struct {
	Status      string `json:"status"` // "SUCCESS", "FAILED", "PENDING"
	Description string `json:"description"`
}

// ReceiptQueryRequest specifies a transaction receipt lookup.
type ReceiptQueryRequest struct {
	Shortcode      string `json:"shortcode"`
	MpesaReference string `json:"mpesaReference"`
}

// ReceiptTransaction represents a single settled transaction record from the provider.
type ReceiptTransaction struct {
	TransID     string  `json:"transId"`
	TransAmount float64 `json:"transAmount"`
	Mobile      string  `json:"mobile"`
	TransTime   string  `json:"transTime"`
	BillRef     string  `json:"billRef"`
	TransType   string  `json:"transType"`
	Name        string  `json:"name"`
}

// ReceiptQueryResponse is the response from a transaction receipt query.
type ReceiptQueryResponse struct {
	Status       int                  `json:"status"`
	Transactions []ReceiptTransaction `json:"transactions"`
}

// PaymentProvider is the typed port for interacting with payment gateways (e.g. NCBA Bank or mock/fake adapters).
type PaymentProvider interface {
	InitiateSTK(ctx context.Context, req STKInitiateRequest) (*STKInitiateResponse, error)
	QuerySTK(ctx context.Context, req STKQueryRequest) (*STKQueryResponse, error)
	QueryReceipt(ctx context.Context, req ReceiptQueryRequest) (*ReceiptQueryResponse, error)
	Close()
}
