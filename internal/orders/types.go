// Package orders implements atomic stock reservations and order lifecycles.
package orders

import (
	"errors"
	"time"
)

var (
	ErrInvalidCustomer         = errors.New("invalid customer information")
	ErrEmptyOrder              = errors.New("order must contain between 1 and 50 items")
	ErrTooManyItems            = errors.New("order must contain between 1 and 50 items")
	ErrInvalidQuantity         = errors.New("invalid item quantity")
	ErrMissingProductID        = errors.New("each order item must include a valid product ID")
	ErrDuplicateProduct        = errors.New("duplicate product in order")
	ErrCatalogUnavailable      = errors.New("catalog unavailable")
	ErrProductNotFound         = errors.New("product not found in catalog")
	ErrProductUnopened         = errors.New("product has not been opened for sale")
	ErrInsufficientStock       = errors.New("insufficient stock")
	ErrOrderNotFound           = errors.New("order not found")
	ErrOrderNotPending         = errors.New("order is not pending")
	ErrReservationNotFound     = errors.New("reservation not found")
	ErrReservationNotActive    = errors.New("reservation is not active")
	ErrLatePaymentResolution   = errors.New("reservation expired; late payment requires explicit resolution")
	ErrPayOnDeliveryProhib     = errors.New("payment on delivery is prohibited; verify payment before dispatch")
	ErrInvalidPaymentMethod    = errors.New("invalid payment method")
	ErrInvalidDeliveryOption   = errors.New("invalid delivery option")
	ErrDeliveryQuoteUnaccepted = errors.New("delivery quote must be accepted before payment")
	ErrQuoteExpired            = errors.New("delivery quote has expired")
	ErrPayableMismatch         = errors.New("payable item and transport snapshots do not reconcile")
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

	DefaultReservationDuration = 15 * time.Minute
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
}

type PaymentStatusResponse struct {
	OrderID           string     `json:"orderId"`
	PaymentStatus     string     `json:"paymentStatus"`
	FulfillmentStatus string     `json:"fulfillmentStatus"`
	ReceiptNumber     string     `json:"receiptNumber,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	IsExpired         bool       `json:"isExpired"`
}
