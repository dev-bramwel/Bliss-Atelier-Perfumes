// Package orders implements atomic stock reservations and order lifecycles.
package orders

import (
	"errors"
	"time"
)

var (
	ErrInvalidCustomer          = errors.New("invalid customer information")
	ErrEmptyOrder               = errors.New("order must contain between 1 and 50 items")
	ErrTooManyItems             = errors.New("order must contain between 1 and 50 items")
	ErrInvalidQuantity          = errors.New("invalid item quantity")
	ErrMissingProductID         = errors.New("each order item must include a valid product ID")
	ErrDuplicateProduct         = errors.New("duplicate product in order")
	ErrCatalogUnavailable       = errors.New("catalog unavailable")
	ErrProductNotFound          = errors.New("product not found in catalog")
	ErrProductUnopened          = errors.New("product has not been opened for sale")
	ErrInsufficientStock        = errors.New("insufficient stock")
	ErrOrderNotFound            = errors.New("order not found")
	ErrOrderNotPending          = errors.New("order is not pending")
	ErrReservationNotFound      = errors.New("reservation not found")
	ErrReservationNotActive     = errors.New("reservation is not active")
	ErrLatePaymentResolution    = errors.New("reservation expired; late payment requires explicit resolution")
	ErrPayOnDeliveryProhib      = errors.New("payment on delivery is prohibited; verify payment before dispatch")
	ErrInvalidPaymentMethod     = errors.New("invalid payment method")
	ErrInvalidDeliveryOption    = errors.New("invalid delivery option")
	ErrDeliveryQuoteUnaccepted  = errors.New("delivery quote must be accepted before payment")
	ErrQuoteExpired             = errors.New("delivery quote has expired")
	ErrPayableMismatch          = errors.New("payable item and transport snapshots do not reconcile")
	ErrUnauthorizedGuest        = errors.New("unauthorized guest access")
	ErrGuestTokenExpired        = errors.New("guest token has expired")
	ErrPayloadTooLarge          = errors.New("request payload too large")
	ErrIdempotencyConflict      = errors.New("idempotency key reused with different request payload")
	ErrIdempotencyInProgress    = errors.New("operation currently in progress; please retry shortly")
	ErrUnresolvedPaymentAttempt = errors.New("active payment attempt in progress; please wait for resolution")
	ErrAttemptNotFound          = errors.New("payment attempt not found")
	ErrOrderAlreadyPaid         = errors.New("order is already paid")
	ErrOrderReservationExpired  = errors.New("stock reservation for this order has expired")
	ErrDuplicateReceipt         = errors.New("receipt has already been utilized on another order")
	ErrInvalidSettlement        = errors.New("invalid manual settlement parameters")
	ErrInvalidCorrection        = errors.New("invalid payment correction parameters")
	ErrExceptionNotFound        = errors.New("payment exception not found")
	ErrExceptionAlreadyResolved = errors.New("payment exception is already resolved")
)

const (
	PaymentPending            = "PENDING"
	PaymentPaid               = "PAID"
	PaymentFailed             = "FAILED"
	PaymentExpired            = "EXPIRED"
	PaymentResolutionRequired = "RESOLUTION_REQUIRED"

	FulfillmentUnfulfilled = "unfulfilled"
	FulfillmentFulfilled   = "fulfilled"
	FulfillmentCancelled   = "cancelled"

	ReservationActive    = "active"
	ReservationReleased  = "released"
	ReservationCommitted = "committed"

	AttemptCreated    = "created"
	AttemptInitiating = "initiating"
	AttemptAccepted   = "accepted"
	AttemptSucceeded  = "succeeded"
	AttemptFailed     = "failed"
	AttemptCancelled  = "cancelled"
	AttemptUnknown    = "unknown"

	ExceptionUnmappedTransaction    = "UNMAPPED_TRANSACTION"
	ExceptionAmountMismatch         = "AMOUNT_MISMATCH"
	ExceptionDuplicateReceipt       = "DUPLICATE_RECEIPT"
	ExceptionStaleAttemptUnresolved = "STALE_ATTEMPT_UNRESOLVED"
	ExceptionLatePayment            = "LATE_PAYMENT"
	ExceptionManualResolutionReq    = "MANUAL_RESOLUTION_REQUIRED"

	ExceptionStatusOpen      = "open"
	ExceptionStatusResolved  = "resolved"
	ExceptionStatusDismissed = "dismissed"

	AuditActionManualSettlement     = "manual_settlement"
	AuditActionDuplicateCorrection  = "duplicate_correction"
	AuditActionAmountCorrection     = "amount_correction"
	AuditActionRefundReconciliation = "refund_reconciliation"

	IdempotencyInProgress = "in_progress"
	IdempotencyCompleted  = "completed"
	IdempotencyFailed     = "failed"

	DefaultReservationDuration = 15 * time.Minute
	DefaultIdempotencyTTL      = 24 * time.Hour
)

type CustomerInput struct {
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Location string `json:"location"`
	Delivery string `json:"delivery"`
	Notes    string `json:"notes"`
	Payment  string `json:"payment"`
}

type ItemInput struct {
	ID  string `json:"id"`
	Qty int    `json:"qty"`
}

type CreateOrderRequest struct {
	Customer CustomerInput `json:"customer"`
	Items    []ItemInput   `json:"items"`
}

type OrderItem struct {
	ID           string    `json:"id"`
	OrderID      string    `json:"orderId"`
	ProductID    string    `json:"productId"`
	ProductName  string    `json:"productName"`
	Name         string    `json:"name"`
	ProductSize  string    `json:"productSize"`
	UnitPriceKes int       `json:"unitPriceKes"`
	Quantity     int       `json:"qty"`
	SubtotalKes  int       `json:"subtotalKes"`
	CreatedAt    time.Time `json:"createdAt"`
}

type StockReservation struct {
	ID         string     `json:"id"`
	OrderID    string     `json:"orderId"`
	Status     string     `json:"status"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	ReleasedAt *time.Time `json:"releasedAt,omitempty"`
}

type PaymentAttempt struct {
	ID                    string     `json:"id"`
	OrderID               string     `json:"orderId"`
	AttemptNumber         int        `json:"attemptNumber"`
	AmountKes             int        `json:"amountKes"`
	Phone                 string     `json:"phone"`
	Status                string     `json:"status"`
	Provider              string     `json:"provider"`
	ProviderTransactionID string     `json:"providerTransactionId,omitempty"`
	ProviderReferenceID   string     `json:"providerReferenceId,omitempty"`
	ErrorMessage          string     `json:"errorMessage,omitempty"`
	QueryCount            int        `json:"queryCount"`
	LastQueriedAt         *time.Time `json:"lastQueriedAt,omitempty"`
	NextQueryAt           *time.Time `json:"nextQueryAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type IdempotencyRecord struct {
	Key             string            `json:"key"`
	CallerScope     string            `json:"callerScope"`
	Operation       string            `json:"operation"`
	RequestHash     string            `json:"requestHash"`
	Status          string            `json:"status"`
	StatusCode      int               `json:"statusCode"`
	ResponseHeaders map[string]string `json:"responseHeaders"`
	ResponseBody    string            `json:"responseBody"`
	ResourceID      string            `json:"resourceId,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	ExpiresAt       time.Time         `json:"expiresAt"`
}

type RetryPaymentRequest struct {
	Phone string `json:"phone,omitempty"`
}

type Order struct {
	ID                string            `json:"id"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	CustomerName      string            `json:"customerName"`
	CustomerPhone     string            `json:"customerPhone"`
	DeliveryLocation  string            `json:"deliveryLocation"`
	DeliveryOption    string            `json:"deliveryOption"`
	Notes             string            `json:"notes,omitempty"`
	PaymentMethod     string            `json:"paymentMethod"`
	PaymentStatus     string            `json:"paymentStatus"`
	FulfillmentStatus string            `json:"fulfillmentStatus"`
	TotalKes          int               `json:"totalKes"`
	Items             []OrderItem       `json:"items"`
	Reservation       *StockReservation `json:"reservation,omitempty"`
	GuestToken        string            `json:"guestToken,omitempty"`
	LatestAttempt     *PaymentAttempt   `json:"latestAttempt,omitempty"`
}

type PaymentStatusResponse struct {
	OrderID           string          `json:"orderId"`
	PaymentStatus     string          `json:"paymentStatus"`
	FulfillmentStatus string          `json:"fulfillmentStatus"`
	ReceiptNumber     string          `json:"receiptNumber,omitempty"`
	ExpiresAt         *time.Time      `json:"expiresAt,omitempty"`
	IsExpired         bool            `json:"isExpired"`
	LatestAttempt     *PaymentAttempt `json:"latestAttempt,omitempty"`
	AttemptsCount     int             `json:"attemptsCount,omitempty"`
}

type PaymentException struct {
	ID                    string         `json:"id"`
	OrderID               *string        `json:"orderId,omitempty"`
	AttemptID             *string        `json:"attemptId,omitempty"`
	ExceptionType         string         `json:"exceptionType"`
	Provider              string         `json:"provider"`
	ProviderTransactionID *string        `json:"providerTransactionId,omitempty"`
	ReceiptNumber         *string        `json:"receiptNumber,omitempty"`
	ReceivedAmountKes     *int           `json:"receivedAmountKes,omitempty"`
	ExpectedAmountKes     *int           `json:"expectedAmountKes,omitempty"`
	CustomerPhone         *string        `json:"customerPhone,omitempty"`
	Status                string         `json:"status"`
	ResolutionAction      *string        `json:"resolutionAction,omitempty"`
	ResolutionNotes       *string        `json:"resolutionNotes,omitempty"`
	ResolvedBy            *string        `json:"resolvedBy,omitempty"`
	Evidence              *string        `json:"evidence,omitempty"`
	Metadata              map[string]any `json:"metadata,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	UpdatedAt             time.Time      `json:"updatedAt"`
	ResolvedAt            *time.Time     `json:"resolvedAt,omitempty"`
}

type PaymentAuditRecord struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"orderId"`
	Action        string    `json:"action"`
	AmountKes     int       `json:"amountKes"`
	ReceiptNumber string    `json:"receiptNumber"`
	Provider      string    `json:"provider"`
	Evidence      string    `json:"evidence"`
	Notes         string    `json:"notes"`
	AuthorizedBy  string    `json:"authorizedBy"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ManualSettlementRequest struct {
	OrderID       string `json:"orderId"`
	ReceiptNumber string `json:"receiptNumber"`
	AmountKes     int    `json:"amountKes"`
	Provider      string `json:"provider,omitempty"`
	Evidence      string `json:"evidence"`
	AuthorizedBy  string `json:"authorizedBy"`
	Notes         string `json:"notes"`
}

type PaymentCorrectionRequest struct {
	OrderID       string  `json:"orderId"`
	Action        string  `json:"action"`
	ReceiptNumber string  `json:"receiptNumber"`
	AmountKes     int     `json:"amountKes"`
	Provider      string  `json:"provider,omitempty"`
	Evidence      string  `json:"evidence"`
	AuthorizedBy  string  `json:"authorizedBy"`
	Notes         string  `json:"notes"`
	ExceptionID   *string `json:"exceptionId,omitempty"`
}

type ExceptionFilter struct {
	Status string
	Limit  int
	Offset int
}

type ResolveExceptionRequest struct {
	Action       string `json:"action"`
	Notes        string `json:"notes"`
	AuthorizedBy string `json:"authorizedBy"`
	Evidence     string `json:"evidence,omitempty"`
	OrderID      string `json:"orderId,omitempty"`
}
