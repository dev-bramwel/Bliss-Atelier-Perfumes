// Package httpapi provides HTTP surfaces for health, catalog, and order lifecycles.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/delivery"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/rediscache"
)

func respond(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

type Ping func(context.Context) error

// PublicOrder provides a sanitized, non-PII view of an order suitable for unauthenticated public inspection.
type PublicOrder struct {
	ID                string                     `json:"id"`
	PaymentStatus     string                     `json:"payment_status"`
	FulfillmentStatus string                     `json:"fulfillment_status"`
	PaymentMethod     string                     `json:"payment_method"`
	DeliveryOption    string                     `json:"delivery_option,omitempty"`
	TotalKes          int                        `json:"total_kes"`
	CreatedAt         time.Time                  `json:"created_at"`
	Items             []PublicOrderItem          `json:"items,omitempty"`
	DeliveryQuote     *delivery.PublicQuote      `json:"delivery_quote,omitempty"`
	PayableBreakdown  *delivery.PayableBreakdown `json:"payable_breakdown,omitempty"`
	LatestAttempt     *orders.PaymentAttempt     `json:"latest_attempt,omitempty"`
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
	ValidateGuestAccess(ctx context.Context, orderID string, rawToken string) (bool, error)
	IssueGuestToken(ctx context.Context, orderID string) (string, error)
	ReleaseExpired(ctx context.Context, now time.Time) (int, error)
	ListRecent(ctx context.Context, limit int) ([]orders.Order, error)
	BeginIdempotent(ctx context.Context, key, callerScope, operation, requestHash string, ttl time.Duration) (*orders.IdempotencyRecord, bool, error)
	CompleteIdempotent(ctx context.Context, key, callerScope, operation string, statusCode int, headers map[string]string, responseBody, resourceID string) error
	FailIdempotent(ctx context.Context, key, callerScope, operation string) error
	CreatePaymentAttempt(ctx context.Context, orderID, phone string, amountKes int, provider string) (*orders.PaymentAttempt, error)
	GetLatestPaymentAttempt(ctx context.Context, orderID string) (*orders.PaymentAttempt, error)
	GetPaymentAttempts(ctx context.Context, orderID string) ([]orders.PaymentAttempt, error)
	ConfirmPayment(ctx context.Context, orderID string, receiptNumber string, paidAmountKes ...int) error
	UpdatePaymentAttempt(ctx context.Context, attemptID, status, providerTxID, providerRefID, errorMsg string) error
	FindPaymentAttemptByProviderTx(ctx context.Context, providerTxID string) (*orders.PaymentAttempt, error)
	FindInFlightPaymentAttempt(ctx context.Context, phone string, amountKes int, within time.Duration) (*orders.PaymentAttempt, error)
	FindReceiptOrder(ctx context.Context, receiptNumber string) (string, error)
}

type DeliveryService interface {
	CreateQuote(ctx context.Context, orderID string, p delivery.CreateQuoteParams) (*delivery.DeliveryQuote, error)
	GetActiveQuote(ctx context.Context, orderID string) (*delivery.DeliveryQuote, error)
	AcceptQuote(ctx context.Context, orderID string, version int) (*delivery.DeliveryQuote, error)
	RejectQuote(ctx context.Context, orderID string, version int, reason string) (*delivery.DeliveryQuote, error)
	ExpireQuotes(ctx context.Context, asOf time.Time) (int64, error)
}

// CatalogCacheProvider defines read-through cache and invalidation capabilities for catalog products.
type CatalogCacheProvider interface {
	Get(ctx context.Context) ([]catalog.Product, error)
	Set(ctx context.Context, products []catalog.Product, ttl ...time.Duration) error
	SetVersioned(ctx context.Context, products []catalog.Product, expectedVersion string, ttl ...time.Duration) error
	Version(ctx context.Context) string
	Invalidate(ctx context.Context) error
}

type API struct {
	server   *http.Server
	draining atomic.Bool
}

func New(address string, timeout time.Duration, ping Ping, deps ...any) *API {
	a := &API{}
	var catalogStore Catalog
	var orderStore OrderService
	var deliveryStore DeliveryService
	var secConfig SecurityConfig
	var paymentProv provider.PaymentProvider
	var ncbaConfig provider.NCBAConfig
	var redisClient *rediscache.Client
	var catalogCache CatalogCacheProvider

	for _, d := range deps {
		switch v := d.(type) {
		case Catalog:
			catalogStore = v
		case OrderService:
			orderStore = v
		case DeliveryService:
			deliveryStore = v
		case SecurityConfig:
			secConfig = v
		case provider.PaymentProvider:
			paymentProv = v
		case provider.NCBAConfig:
			ncbaConfig = v
		case *rediscache.Client:
			redisClient = v
		case CatalogCacheProvider:
			catalogCache = v
		}
	}

	if redisClient != nil && catalogCache == nil {
		catalogCache = rediscache.NewCatalogCache(redisClient, "", 5*time.Minute)
	}

	orderLimiter := newMemoryLimiter(time.Minute, 10)
	paymentRetryLimiter := newMemoryLimiter(time.Minute, 5)
	orderLookupLimiter := newMemoryLimiter(time.Minute, 30)
	managerLimiter := newMemoryLimiter(time.Minute, 20)

	if redisClient != nil {
		orderLimiter = rediscache.NewRateLimiter(redisClient, "order:create:", time.Minute, 10)
		paymentRetryLimiter = rediscache.NewRateLimiter(redisClient, "order:retry:", time.Minute, 5)
		orderLookupLimiter = rediscache.NewRateLimiter(redisClient, "order:lookup:", time.Minute, 30)
		managerLimiter = rediscache.NewRateLimiter(redisClient, "manager:action:", time.Minute, 20)
	}

	proxyChecker := NewTrustedProxyChecker(secConfig.TrustedProxies)
	extractManager := func(r *http.Request) string {
		return ExtractManagerIdentity(r, secConfig.ManagerSecret)
	}

	mux := http.NewServeMux()

	catalogHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if a.draining.Load() || catalogStore == nil {
			respond(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		var cacheVer string
		if catalogCache != nil {
			cachedProducts, err := catalogCache.Get(ctx)
			if err == nil && cachedProducts != nil {
				w.Header().Set("X-Cache", "HIT")
				respond(w, 200, map[string]any{"products": cachedProducts})
				return
			}
			cacheVer = catalogCache.Version(ctx)
		}
		products, err := catalogStore.Products(ctx)
		if err != nil {
			slog.Warn("catalog_unavailable", "reason", err.Error())
			respond(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		if catalogCache != nil {
			_ = catalogCache.SetVersioned(ctx, products, cacheVer)
		}
		w.Header().Set("X-Cache", "MISS")
		respond(w, 200, map[string]any{"products": products})
	}

	mux.HandleFunc("GET /catalog", catalogHandler)
	mux.HandleFunc("GET /api/catalog", catalogHandler)

	handleInvalidateCatalog := func(w http.ResponseWriter, r *http.Request) {
		if a.draining.Load() {
			respond(w, 503, map[string]string{"error": "service draining"})
			return
		}
		ip := SafeClientIP(r, proxyChecker)
		if !managerLimiter.Allow(r.Context(), ip) {
			w.Header().Set("Retry-After", "60")
			respond(w, http.StatusTooManyRequests, map[string]string{
				"error": "too many requests; please wait before retrying",
			})
			return
		}
		if extractManager(r) == "" {
			respond(w, http.StatusUnauthorized, map[string]string{"error": "manager authorization required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if catalogCache != nil {
			if err := catalogCache.Invalidate(ctx); err != nil {
				slog.Warn("catalog_cache_invalidation_failed", "error", err.Error())
			}
		}
		respond(w, http.StatusOK, map[string]string{"status": "invalidated"})
	}

	mux.HandleFunc("POST /catalog/invalidate", handleInvalidateCatalog)
	mux.HandleFunc("POST /api/catalog/invalidate", handleInvalidateCatalog)

	if orderStore != nil {
		handleCreateOrder := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			ip := SafeClientIP(r, proxyChecker)
			if !orderLimiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				respond(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many order creation requests; please wait before retrying",
				})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			var maxBytesErr *http.MaxBytesError
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				if errors.As(err, &maxBytesErr) {
					respond(w, http.StatusRequestEntityTooLarge, map[string]string{
						"error": "request body too large; maximum 64KB allowed",
					})
					return
				}
				respond(w, 400, map[string]string{"error": "failed to read request body"})
				return
			}

			var req orders.CreateOrderRequest
			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				respond(w, 400, map[string]string{"error": "invalid json payload"})
				return
			}

			rawIdempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			idempotencyKey := rawIdempotencyKey
			if idempotencyKey == "" {
				idempotencyKey = "auto_" + orders.CanonicalRequestHash([]byte(ip+":"+string(bodyBytes)))
			}
			callerScope := "guest:" + ip
			if mgr := extractManager(r); mgr != "" {
				callerScope = "manager:" + mgr
			}

			reqHash := orders.CanonicalRequestHash(bodyBytes)
			rec, isReplay, err := orderStore.BeginIdempotent(ctx, idempotencyKey, callerScope, "create_order", reqHash, orders.DefaultIdempotencyTTL)
			if err != nil {
				switch {
				case errors.Is(err, orders.ErrIdempotencyInProgress):
					respond(w, http.StatusConflict, map[string]string{
						"error": "operation currently in progress; please retry shortly",
					})
				case errors.Is(err, orders.ErrIdempotencyConflict):
					respond(w, http.StatusConflict, map[string]string{
						"error": "idempotency key reused with different request payload",
					})
				default:
					slog.Error("idempotency_begin_failed", "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}
			if isReplay {
				// Replay cached response without leaking plaintext tokens from database.
				// Issue a fresh guest token dynamically for the customer session if resource ID is present
				// and the client explicitly provided an idempotency key (preventing cross-session token minting for implicit/auto keys).
				if rec.ResourceID != "" && rawIdempotencyKey != "" {
					freshGuestToken, tokenErr := orderStore.IssueGuestToken(ctx, rec.ResourceID)
					if tokenErr == nil && freshGuestToken != "" {
						var replayData map[string]any
						if err := json.Unmarshal([]byte(rec.ResponseBody), &replayData); err == nil {
							replayData["guest_token"] = freshGuestToken
							if ordMap, ok := replayData["order"].(map[string]any); ok {
								ordMap["guestToken"] = freshGuestToken
							}
							if modifiedBytes, err := json.Marshal(replayData); err == nil {
								w.Header().Set("Content-Type", "application/json")
								w.Header().Set("X-Idempotent-Replay", "true")
								w.WriteHeader(rec.StatusCode)
								_, _ = w.Write(modifiedBytes)
								return
							}
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(rec.StatusCode)
				_, _ = w.Write([]byte(rec.ResponseBody))
				return
			}

			order, err := orderStore.CreateOrder(ctx, req)
			if err != nil {
				_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "create_order")
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

			var stkPushData any
			var orderWarning string

			if order.PaymentMethod == "mpesa" && paymentProv != nil && order.LatestAttempt != nil {
				stkResp, stkErr := paymentProv.InitiateSTK(ctx, provider.STKInitiateRequest{
					OrderID:   order.ID,
					AttemptID: order.LatestAttempt.ID,
					Phone:     order.CustomerPhone,
					AmountKes: order.TotalKes,
				})
				if stkErr == nil && stkResp != nil {
					_ = orderStore.UpdatePaymentAttempt(ctx, order.LatestAttempt.ID, orders.AttemptAccepted, stkResp.TransactionID, stkResp.ReferenceID, "")
					order.LatestAttempt.Status = orders.AttemptAccepted
					order.LatestAttempt.ProviderTransactionID = stkResp.TransactionID
					order.LatestAttempt.ProviderReferenceID = stkResp.ReferenceID
					stkPushData = map[string]string{
						"merchantRequestId":   stkResp.ReferenceID,
						"checkoutRequestId":   stkResp.TransactionID,
						"responseDescription": stkResp.StatusDescription,
					}
				} else if errors.Is(stkErr, provider.ErrProviderTimeout) {
					_ = orderStore.UpdatePaymentAttempt(ctx, order.LatestAttempt.ID, orders.AttemptUnknown, "", "", "provider initiation timed out")
					order.LatestAttempt.Status = orders.AttemptUnknown
					order.LatestAttempt.ErrorMessage = "provider initiation timed out"
					orderWarning = "M-Pesa accepted the payment request. The callback will recover its transaction mapping."
				} else if stkErr != nil {
					_ = orderStore.UpdatePaymentAttempt(ctx, order.LatestAttempt.ID, orders.AttemptFailed, "", "", stkErr.Error())
					order.LatestAttempt.Status = orders.AttemptFailed
					order.LatestAttempt.ErrorMessage = stkErr.Error()
					orderWarning = "Order saved but M-Pesa STK initiation failed. Check your Daraja credentials."
				}
			}

			respData := map[string]any{
				"order":       order,
				"guest_token": order.GuestToken,
				"stkPush":     stkPushData,
			}
			if orderWarning != "" {
				respData["warning"] = orderWarning
			}
			respBytes, err := json.Marshal(respData)
			if err != nil {
				slog.Error("marshal_create_order_response_failed", "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}

			// Redact raw guest token in persistent idempotency storage so plaintext tokens are never written to disk or DB.
			cachedOrder := *order
			cachedOrder.GuestToken = ""
			cachedRespData := map[string]any{
				"order":       cachedOrder,
				"guest_token": "",
				"stkPush":     stkPushData,
			}
			if orderWarning != "" {
				cachedRespData["warning"] = orderWarning
			}
			cachedRespBytes, err := json.Marshal(cachedRespData)
			if err != nil {
				slog.Error("marshal_cached_order_response_failed", "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}

			if err := orderStore.CompleteIdempotent(ctx, idempotencyKey, callerScope, "create_order", http.StatusCreated, map[string]string{"Content-Type": "application/json"}, string(cachedRespBytes), order.ID); err != nil {
				slog.Error("complete_idempotent_failed", "key", idempotencyKey, "reason", err.Error())
				_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "create_order")
				respond(w, 500, map[string]string{"error": "internal error; please retry order submission"})
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(respBytes)
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
			ip := SafeClientIP(r, proxyChecker)
			if !orderLookupLimiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				respond(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many requests; please wait before retrying",
				})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_payment_status_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

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
			ip := SafeClientIP(r, proxyChecker)
			if !orderLookupLimiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				respond(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many order lookup requests; please wait before retrying",
				})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_get_order_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

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
			opt := strings.ToLower(strings.TrimSpace(order.DeliveryOption))
			isPickup := opt == "pickup" || opt == "showroom_pickup"
			var pubQuote *delivery.PublicQuote
			var breakdown *delivery.PayableBreakdown
			if deliveryStore != nil {
				if q, err := deliveryStore.GetActiveQuote(ctx, id); err == nil {
					pq := q.ToPublic()
					pubQuote = &pq
					if bd, err := delivery.CalculatePayable(order.TotalKes, q, isPickup); err == nil {
						breakdown = bd
					}
				} else if isPickup {
					if bd, err := delivery.CalculatePayable(order.TotalKes, nil, true); err == nil {
						breakdown = bd
					}
				}
			} else if isPickup {
				if bd, err := delivery.CalculatePayable(order.TotalKes, nil, true); err == nil {
					breakdown = bd
				}
			}

			respond(w, 200, map[string]any{
				"order": PublicOrder{
					ID:                order.ID,
					PaymentStatus:     order.PaymentStatus,
					FulfillmentStatus: order.FulfillmentStatus,
					PaymentMethod:     order.PaymentMethod,
					DeliveryOption:    order.DeliveryOption,
					TotalKes:          order.TotalKes,
					CreatedAt:         order.CreatedAt,
					Items:             publicItems,
					DeliveryQuote:     pubQuote,
					PayableBreakdown:  breakdown,
					LatestAttempt:     order.LatestAttempt,
				},
			})
		}

		handleRetryPayment := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			id := r.PathValue("id")
			if id == "" {
				respond(w, 400, map[string]string{"error": "order ID is required"})
				return
			}
			ip := SafeClientIP(r, proxyChecker)
			if !paymentRetryLimiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				respond(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many requests; please wait before retrying",
				})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_retry_payment_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

			var maxBytesErr *http.MaxBytesError
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				if errors.As(err, &maxBytesErr) {
					respond(w, http.StatusRequestEntityTooLarge, map[string]string{
						"error": "request body too large; maximum 64KB allowed",
					})
					return
				}
				respond(w, 400, map[string]string{"error": "failed to read request body"})
				return
			}

			var retryReq orders.RetryPaymentRequest
			if len(bodyBytes) > 0 {
				if err := json.Unmarshal(bodyBytes, &retryReq); err != nil {
					respond(w, 400, map[string]string{"error": "invalid json payload"})
					return
				}
			}

			idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if idempotencyKey == "" {
				respond(w, 400, map[string]string{
					"error": "Idempotency-Key header is required for payment retry",
				})
				return
			}
			callerScope := "order:" + id
			if mgr := extractManager(r); mgr != "" {
				callerScope = "manager:" + mgr + ":order:" + id
			}

			reqHash := orders.CanonicalRequestHash(bodyBytes)
			rec, isReplay, err := orderStore.BeginIdempotent(ctx, idempotencyKey, callerScope, "retry_payment", reqHash, orders.DefaultIdempotencyTTL)
			if err != nil {
				switch {
				case errors.Is(err, orders.ErrIdempotencyInProgress):
					respond(w, http.StatusConflict, map[string]string{
						"error": "operation currently in progress; please retry shortly",
					})
				case errors.Is(err, orders.ErrIdempotencyConflict):
					respond(w, http.StatusConflict, map[string]string{
						"error": "idempotency key reused with different request payload",
					})
				default:
					slog.Error("idempotency_retry_begin_failed", "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}
			if isReplay {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(rec.StatusCode)
				_, _ = w.Write([]byte(rec.ResponseBody))
				return
			}

			order, err := orderStore.GetOrder(ctx, id)
			if err != nil {
				_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
				if errors.Is(err, orders.ErrOrderNotFound) {
					respond(w, 404, map[string]string{"error": "order not found"})
					return
				}
				slog.Error("get_order_for_retry_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}

			payableAmount := order.TotalKes
			opt := strings.ToLower(strings.TrimSpace(order.DeliveryOption))
			isPickup := opt == "pickup" || opt == "showroom_pickup"
			if deliveryStore != nil {
				quote, qErr := deliveryStore.GetActiveQuote(ctx, id)
				if !isPickup && qErr != nil {
					_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
					if errors.Is(qErr, delivery.ErrQuoteNotFound) {
						respond(w, http.StatusConflict, map[string]string{
							"error": "delivery quote must be accepted before payment",
						})
						return
					}
					slog.Error("get_active_quote_for_retry_failed", "order_id", id, "reason", qErr.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
					return
				}
				bd, bdErr := delivery.CalculatePayable(order.TotalKes, quote, isPickup)
				if bdErr != nil {
					_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
					respond(w, http.StatusConflict, map[string]string{
						"error": bdErr.Error(),
					})
					return
				}
				payableAmount = bd.PayableMerchantKES
			}

			phone := order.CustomerPhone
			if retryReq.Phone != "" {
				normalizedPhone, pErr := orders.ValidatePhone(retryReq.Phone)
				if pErr != nil {
					_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
					respond(w, 400, map[string]string{"error": pErr.Error()})
					return
				}
				phone = normalizedPhone
			}

			attempt, err := orderStore.CreatePaymentAttempt(ctx, id, phone, payableAmount, "mpesa_stk")
			if err != nil {
				_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
				switch {
				case errors.Is(err, orders.ErrUnresolvedPaymentAttempt):
					respond(w, http.StatusConflict, map[string]string{
						"error": "active payment attempt in progress; please wait for resolution",
					})
				case errors.Is(err, orders.ErrOrderAlreadyPaid):
					respond(w, http.StatusConflict, map[string]string{
						"error": "order is already paid",
					})
				case errors.Is(err, orders.ErrOrderReservationExpired):
					respond(w, http.StatusConflict, map[string]string{
						"error": "stock reservation for this order has expired",
					})
				case errors.Is(err, orders.ErrOrderNotFound):
					respond(w, 404, map[string]string{"error": "order not found"})
				default:
					slog.Error("create_payment_attempt_failed", "order_id", id, "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}

			var stkPushData any
			var retryWarning string

			if paymentProv != nil {
				stkResp, stkErr := paymentProv.InitiateSTK(ctx, provider.STKInitiateRequest{
					OrderID:   id,
					AttemptID: attempt.ID,
					Phone:     phone,
					AmountKes: payableAmount,
				})
				if stkErr == nil && stkResp != nil {
					_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptAccepted, stkResp.TransactionID, stkResp.ReferenceID, "")
					attempt.Status = orders.AttemptAccepted
					attempt.ProviderTransactionID = stkResp.TransactionID
					attempt.ProviderReferenceID = stkResp.ReferenceID
					stkPushData = map[string]string{
						"merchantRequestId":   stkResp.ReferenceID,
						"checkoutRequestId":   stkResp.TransactionID,
						"responseDescription": stkResp.StatusDescription,
					}
				} else if errors.Is(stkErr, provider.ErrProviderTimeout) {
					_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptUnknown, "", "", "provider initiation timed out")
					attempt.Status = orders.AttemptUnknown
					attempt.ErrorMessage = "provider initiation timed out"
					retryWarning = "M-Pesa accepted the payment request. The callback will recover its transaction mapping."
				} else if stkErr != nil {
					_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptFailed, "", "", stkErr.Error())
					attempt.Status = orders.AttemptFailed
					attempt.ErrorMessage = stkErr.Error()
					retryWarning = "Payment retry saved but STK initiation failed."
				}
			}

			respData := map[string]any{
				"orderId": id,
				"attempt": attempt,
				"message": "payment retry initiated",
			}
			if stkPushData != nil {
				respData["stkPush"] = stkPushData
			}
			if retryWarning != "" {
				respData["warning"] = retryWarning
			}
			respBytes, err := json.Marshal(respData)
			if err != nil {
				_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
				slog.Error("marshal_retry_payment_response_failed", "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}

			if err := orderStore.CompleteIdempotent(ctx, idempotencyKey, callerScope, "retry_payment", http.StatusCreated, map[string]string{"Content-Type": "application/json"}, string(respBytes), attempt.ID); err != nil {
				slog.Error("complete_idempotent_retry_failed", "key", idempotencyKey, "reason", err.Error())
				_ = orderStore.FailIdempotent(ctx, idempotencyKey, callerScope, "retry_payment")
				respond(w, 500, map[string]string{"error": "internal error; please retry payment submission"})
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(respBytes)
		}

		handleGetAttempts := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			id := r.PathValue("id")
			if id == "" {
				respond(w, 400, map[string]string{"error": "order ID is required"})
				return
			}
			ip := SafeClientIP(r, proxyChecker)
			if !orderLookupLimiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				respond(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many requests; please wait before retrying",
				})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_get_attempts_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

			attempts, err := orderStore.GetPaymentAttempts(ctx, id)
			if err != nil {
				slog.Error("get_payment_attempts_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			respond(w, 200, map[string]any{
				"orderId":  id,
				"attempts": attempts,
			})
		}

		mux.HandleFunc("POST /orders", handleCreateOrder)
		mux.HandleFunc("POST /api/orders", handleCreateOrder)
		mux.HandleFunc("GET /orders/{id}/payment-status", handlePaymentStatus)
		mux.HandleFunc("GET /api/orders/{id}/payment-status", handlePaymentStatus)
		mux.HandleFunc("GET /orders/{id}", handleGetOrder)
		mux.HandleFunc("GET /api/orders/{id}", handleGetOrder)
		mux.HandleFunc("POST /orders/{id}/retry-payment", handleRetryPayment)
		mux.HandleFunc("POST /api/orders/{id}/retry-payment", handleRetryPayment)
		mux.HandleFunc("GET /orders/{id}/attempts", handleGetAttempts)
		mux.HandleFunc("GET /api/orders/{id}/attempts", handleGetAttempts)

		ncbaHandler, mpesaHandler := newPaymentCallbackHandlers(orderStore, ncbaConfig, secConfig.MpesaCallbackSecret, timeout)
		mux.HandleFunc("POST /api/payments/ncba/ipn", ncbaHandler)
		mux.HandleFunc("POST /payments/ncba/ipn", ncbaHandler)
		mux.HandleFunc("POST /api/mpesa/callback", mpesaHandler)
		mux.HandleFunc("POST /mpesa/callback", mpesaHandler)
	}

	if deliveryStore != nil {
		handleCreateQuote := func(w http.ResponseWriter, r *http.Request) {
			if a.draining.Load() {
				respond(w, 503, map[string]string{"error": "service draining"})
				return
			}
			managerID := extractManager(r)
			if managerID == "" {
				respond(w, 401, map[string]string{"error": "manager authorization required"})
				return
			}
			id := r.PathValue("id")
			if id == "" {
				respond(w, 400, map[string]string{"error": "order ID is required"})
				return
			}
			var p delivery.CreateQuoteParams
			var maxBytesErr *http.MaxBytesError
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				if errors.As(err, &maxBytesErr) {
					respond(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large; maximum 64KB allowed"})
					return
				}
				respond(w, 400, map[string]string{"error": "invalid json payload"})
				return
			}
			p.CreatedBy = managerID
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			quote, err := deliveryStore.CreateQuote(ctx, id, p)
			if err != nil {
				switch {
				case errors.Is(err, delivery.ErrOrderNotFound):
					respond(w, 404, map[string]string{"error": "order not found"})
				case errors.Is(err, delivery.ErrOrderNotPending):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, delivery.ErrInvalidQuote):
					respond(w, 400, map[string]string{"error": err.Error()})
				default:
					slog.Error("create_delivery_quote_failed", "order_id", id, "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}
			pub := quote.ToPublic()
			respond(w, 201, map[string]any{"quote": pub})
		}

		handleGetQuote := func(w http.ResponseWriter, r *http.Request) {
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

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_get_quote_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

			quote, err := deliveryStore.GetActiveQuote(ctx, id)
			if err != nil {
				if errors.Is(err, delivery.ErrQuoteNotFound) {
					respond(w, 404, map[string]string{"error": "delivery quote not found"})
					return
				}
				slog.Error("get_delivery_quote_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			pub := quote.ToPublic()
			resp := map[string]any{"quote": pub}
			if orderStore != nil {
				if order, err := orderStore.GetOrder(ctx, id); err == nil {
					opt := strings.ToLower(strings.TrimSpace(order.DeliveryOption))
					isPickup := opt == "pickup" || opt == "showroom_pickup"
					if breakdown, err := delivery.CalculatePayable(order.TotalKes, quote, isPickup); err == nil {
						resp["breakdown"] = breakdown
					}
				}
			}
			respond(w, 200, resp)
		}

		handleAcceptQuote := func(w http.ResponseWriter, r *http.Request) {
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

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_accept_quote_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

			var req struct {
				Version int `json:"version"`
			}
			var maxBytesErr *http.MaxBytesError
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				if errors.As(err, &maxBytesErr) {
					respond(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large; maximum 64KB allowed"})
					return
				}
				respond(w, 400, map[string]string{"error": "invalid json payload"})
				return
			}

			quote, err := deliveryStore.AcceptQuote(ctx, id, req.Version)
			if err != nil {
				switch {
				case errors.Is(err, delivery.ErrOrderNotFound):
					respond(w, 404, map[string]string{"error": "order not found"})
				case errors.Is(err, delivery.ErrQuoteNotFound):
					respond(w, 404, map[string]string{"error": "delivery quote not found"})
				case errors.Is(err, delivery.ErrQuoteExpired):
					respond(w, 409, map[string]string{"error": "delivery quote has expired"})
				case errors.Is(err, delivery.ErrQuoteConflict):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, delivery.ErrQuoteNotPending):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, delivery.ErrInvalidQuote):
					respond(w, 400, map[string]string{"error": err.Error()})
				default:
					slog.Error("accept_delivery_quote_failed", "order_id", id, "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}
			pub := quote.ToPublic()
			respond(w, 200, map[string]any{"quote": pub})
		}

		handleRejectQuote := func(w http.ResponseWriter, r *http.Request) {
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

			ok, err := AuthorizeOrderAccess(ctx, r, id, orderStore, secConfig.ManagerSecret)
			if err != nil {
				slog.Error("authorize_reject_quote_failed", "order_id", id, "reason", err.Error())
				respond(w, 500, map[string]string{"error": "internal error"})
				return
			}
			if !ok {
				if ExtractGuestToken(r) == "" && extractManager(r) == "" {
					respond(w, 401, map[string]string{"error": "guest authorization required"})
				} else {
					respond(w, 403, map[string]string{"error": "access denied for this order"})
				}
				return
			}

			var req struct {
				Version int    `json:"version"`
				Reason  string `json:"reason"`
			}
			var maxBytesErr *http.MaxBytesError
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				if errors.As(err, &maxBytesErr) {
					respond(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large; maximum 64KB allowed"})
					return
				}
				respond(w, 400, map[string]string{"error": "invalid json payload"})
				return
			}

			quote, err := deliveryStore.RejectQuote(ctx, id, req.Version, req.Reason)
			if err != nil {
				switch {
				case errors.Is(err, delivery.ErrQuoteNotFound):
					respond(w, 404, map[string]string{"error": "delivery quote not found"})
				case errors.Is(err, delivery.ErrQuoteExpired):
					respond(w, 409, map[string]string{"error": "delivery quote has expired"})
				case errors.Is(err, delivery.ErrQuoteConflict):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, delivery.ErrQuoteNotPending):
					respond(w, 409, map[string]string{"error": err.Error()})
				case errors.Is(err, delivery.ErrInvalidQuote):
					respond(w, 400, map[string]string{"error": err.Error()})
				default:
					slog.Error("reject_delivery_quote_failed", "order_id", id, "reason", err.Error())
					respond(w, 500, map[string]string{"error": "internal error"})
				}
				return
			}
			pub := quote.ToPublic()
			respond(w, 200, map[string]any{"quote": pub})
		}

		mux.HandleFunc("POST /orders/{id}/delivery-quote", handleCreateQuote)
		mux.HandleFunc("POST /api/orders/{id}/delivery-quote", handleCreateQuote)
		mux.HandleFunc("GET /orders/{id}/delivery-quote", handleGetQuote)
		mux.HandleFunc("GET /api/orders/{id}/delivery-quote", handleGetQuote)
		mux.HandleFunc("POST /orders/{id}/delivery-quote/accept", handleAcceptQuote)
		mux.HandleFunc("POST /api/orders/{id}/delivery-quote/accept", handleAcceptQuote)
		mux.HandleFunc("POST /orders/{id}/delivery-quote/reject", handleRejectQuote)
		mux.HandleFunc("POST /api/orders/{id}/delivery-quote/reject", handleRejectQuote)
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

	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		mux.ServeHTTP(w, r)
	})

	a.server = &http.Server{
		Addr:              address,
		Handler:           CORSHandler(secConfig.AllowedOrigins, innerHandler),
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
