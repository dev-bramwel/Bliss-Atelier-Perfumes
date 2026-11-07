// Package httpapi provides HTTP surfaces for health, catalog, and order lifecycles.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

type Ping func(context.Context) error

// PublicOrder provides a sanitized, non-PII view of an order suitable for unauthenticated public inspection.
type PublicOrder struct {
	ID                string            `json:"id"`
	PaymentStatus     string            `json:"payment_status"`
	FulfillmentStatus string            `json:"fulfillment_status"`
	PaymentMethod     string            `json:"payment_method"`
	TotalKes          int               `json:"total_kes"`
	CreatedAt         time.Time         `json:"created_at"`
	Items             []PublicOrderItem `json:"items,omitempty"`
}

// PublicOrderItem represents an order item in the sanitized public order view.
type PublicOrderItem struct {
	ProductID    string `json:"product_id"`
	ProductName  string `json:"product_name"`
	ProductSize  string `json:"product_size"`
	UnitPriceKes int    `json:"unit_price_kes"`
	Quantity     int    `json:"quantity"`
	SubtotalKes  int    `json:"subtotal_kes"`
}

type Catalog interface {
	Products(context.Context) ([]catalog.Product, error)
}

type OrderService interface {
	CreateOrder(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error)
	GetOrder(ctx context.Context, orderID string) (*orders.Order, error)
	GetPaymentStatus(ctx context.Context, orderID string) (*orders.PaymentStatusResponse, error)
	ReleaseExpired(ctx context.Context, now time.Time) (int, error)
	ListRecent(ctx context.Context, limit int) ([]orders.Order, error)
}

type API struct {
	server   *http.Server
	draining atomic.Bool
}

func New(address string, timeout time.Duration, ping Ping, deps ...any) *API {
	a := &API{}
	var catalogStore Catalog
	var orderStore OrderService

	for _, d := range deps {
		switch v := d.(type) {
		case Catalog:
			catalogStore = v
		case OrderService:
			orderStore = v
		}
	}

	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, code int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}

	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if a.draining.Load() || catalogStore == nil {
			respond(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		products, err := catalogStore.Products(ctx)
		if err != nil {
			slog.Warn("catalog_unavailable", "reason", err.Error())
			respond(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		respond(w, 200, map[string]any{"products": products})
	})

	if orderStore != nil {
		orderLimiter := newIPLimiter(time.Minute, 10)
		handleCreateOrder := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			ip := clientIP(r)
			if !orderLimiter.Allow(ip, time.Now()) {
				w.Header().Set("Retry-After", "60")
				respond(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many order creation requests; please wait before retrying",
				})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			var req orders.CreateOrderRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				respond(w, 400, map[string]string{"error": "invalid json payload"})
				return
			}

			order, err := orderStore.CreateOrder(ctx, req)
			if err != nil {
				switch {
				case errors.Is(err, orders.ErrCatalogUnavailable):
					respond(w, 503, map[string]string{"error": "catalog unavailable"})
				case errors.Is(err, orders.ErrInsufficientStock):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, orders.ErrProductUnopened):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, orders.ErrProductNotFound):
					respond(w, 404, map[string]string{"error": err.Error()})
				case errors.Is(err, orders.ErrInvalidCustomer),
					errors.Is(err, orders.ErrEmptyOrder),
					errors.Is(err, orders.ErrTooManyItems),
					errors.Is(err, orders.ErrInvalidQuantity),
					errors.Is(err, orders.ErrMissingProductID),
					errors.Is(err, orders.ErrDuplicateProduct),
					errors.Is(err, orders.ErrPayOnDeliveryProhib),
					errors.Is(err, orders.ErrInvalidPaymentMethod),
					errors.Is(err, orders.ErrInvalidDeliveryOption):
					respond(w, 400, map[string]string{"error": err.Error()})
				default:
					slog.Error("create_order_failed", "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}

			respond(w, 201, map[string]any{
				"order":   order,
				"stkPush": nil,
			})
		}

		handlePaymentStatus := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			id := r.PathValue("id")
			if id == "" {
				respond(w, 400, map[string]string{"error": "order ID is required"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			statusResp, err := orderStore.GetPaymentStatus(ctx, id)
			if err != nil {
				if errors.Is(err, orders.ErrOrderNotFound) {
					respond(w, 404, map[string]string{"error": "order not found"})
					return
				}
				slog.Error("payment_status_failed", "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			respond(w, 200, statusResp)
		}

		handleGetOrder := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			id := r.PathValue("id")
			if id == "" {
				respond(w, 400, map[string]string{"error": "order ID is required"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			order, err := orderStore.GetOrder(ctx, id)
			if err != nil {
				if errors.Is(err, orders.ErrOrderNotFound) {
					respond(w, 404, map[string]string{"error": "order not found"})
					return
				}
				slog.Error("get_order_failed", "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}

			// Sanitize order: return only non-PII fields for unauthenticated lookup.
			// Customer phone, delivery location, name, and notes are excluded.
			publicItems := make([]PublicOrderItem, 0, len(order.Items))
			for _, item := range order.Items {
				publicItems = append(publicItems, PublicOrderItem{
					ProductID:    item.ProductID,
					ProductName:  item.ProductName,
					ProductSize:  item.ProductSize,
					UnitPriceKes: item.UnitPriceKes,
					Quantity:     item.Quantity,
					SubtotalKes:  item.SubtotalKes,
				})
			}
			respond(w, 200, map[string]any{
				"order": PublicOrder{
					ID:                order.ID,
					PaymentStatus:     order.PaymentStatus,
					FulfillmentStatus: order.FulfillmentStatus,
					PaymentMethod:     order.PaymentMethod,
					TotalKes:          order.TotalKes,
					CreatedAt:         order.CreatedAt,
					Items:             publicItems,
				},
			})
		}

		mux.HandleFunc("POST /orders", handleCreateOrder)
		mux.HandleFunc("POST /api/orders", handleCreateOrder)
		mux.HandleFunc("GET /orders/{id}/payment-status", handlePaymentStatus)
		mux.HandleFunc("GET /api/orders/{id}/payment-status", handlePaymentStatus)
		mux.HandleFunc("GET /orders/{id}", handleGetOrder)
		mux.HandleFunc("GET /api/orders/{id}", handleGetOrder)
	}

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]string{"status": "Bliss Atelier API running"})
	})
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if a.draining.Load() {
			respond(w, 503, map[string]string{"status": "draining"})
			return
		}
		if ping != nil {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			if ping(ctx) != nil {
				respond(w, 503, map[string]string{"status": "unavailable"})
				return
			}
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})

	a.server = &http.Server{
		Addr: address,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			mux.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      timeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	return a
}

// Serve drains in-flight requests and closes remaining connections at the deadline.
func (a *API) Serve(ctx context.Context, listener net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	done := make(chan error, 1)
	go func() { done <- a.server.Serve(listener) }()
	logger.Info("http_started")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("HTTP server failed")
	case <-ctx.Done():
		a.draining.Store(true)
		drainCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		err := a.server.Shutdown(drainCtx)
		if err != nil {
			_ = a.server.Close()
			<-done
			return errors.New("HTTP shutdown deadline exceeded")
		}
		<-done
		logger.Info("http_stopped")
		return nil
	}
}
