package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/delivery"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

func TestHealth(t *testing.T) {
	var failing atomic.Bool
	a := New("127.0.0.1:0", 50*time.Millisecond, func(ctx context.Context) error {
		if failing.Load() {
			return errors.New("private DB details")
		}
		return nil
	})
	for _, tc := range []struct {
		path        string
		fail, drain bool
		status      int
	}{{"/", false, false, 200}, {"/health/live", true, false, 200}, {"/health/ready", false, false, 200}, {"/health/ready", true, false, 503}, {"/health/ready", false, true, 503}, {"/api/orders", false, false, 404}} {
		failing.Store(tc.fail)
		a.draining.Store(tc.drain)
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if tc.status != 404 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: expected Cache-Control: no-store, got %q", tc.path, w.Header().Get("Cache-Control"))
		}
	}
}

func TestReadinessDeadline(t *testing.T) {
	a := New("127.0.0.1:0", 10*time.Millisecond, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := New(listener.Addr().String(), time.Second, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, listener, time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil))) }()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown stuck")
	}
	if !a.draining.Load() {
		t.Fatal("readiness not disabled")
	}
}

func TestShutdownDeadlineClosesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := New(listener.Addr().String(), time.Second, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- a.Serve(ctx, listener, 20*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := &http.Client{Timeout: time.Second}
		response, _ := client.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected drain deadline failure")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown exceeded bound")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("connection stayed open")
	}
}

type mockOrderService struct {
	createFn         func(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error)
	getFn            func(ctx context.Context, orderID string) (*orders.Order, error)
	statusFn         func(ctx context.Context, orderID string) (*orders.PaymentStatusResponse, error)
	releaseFn        func(ctx context.Context, now time.Time) (int, error)
	recentFn         func(ctx context.Context, limit int) ([]orders.Order, error)
	validateAccessFn func(ctx context.Context, orderID string, rawToken string) (bool, error)
}

func (m *mockOrderService) CreateOrder(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error) {
	if m.createFn != nil {
		return m.createFn(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (m *mockOrderService) GetOrder(ctx context.Context, orderID string) (*orders.Order, error) {
	if m.getFn != nil {
		return m.getFn(ctx, orderID)
	}
	return nil, orders.ErrOrderNotFound
}

func (m *mockOrderService) GetPaymentStatus(ctx context.Context, orderID string) (*orders.PaymentStatusResponse, error) {
	if m.statusFn != nil {
		return m.statusFn(ctx, orderID)
	}
	return nil, orders.ErrOrderNotFound
}

func (m *mockOrderService) ValidateGuestAccess(ctx context.Context, orderID string, rawToken string) (bool, error) {
	if m.validateAccessFn != nil {
		return m.validateAccessFn(ctx, orderID, rawToken)
	}
	return true, nil
}

func (m *mockOrderService) ReleaseExpired(ctx context.Context, now time.Time) (int, error) {
	if m.releaseFn != nil {
		return m.releaseFn(ctx, now)
	}
	return 0, nil
}

func (m *mockOrderService) ListRecent(ctx context.Context, limit int) ([]orders.Order, error) {
	if m.recentFn != nil {
		return m.recentFn(ctx, limit)
	}
	return []orders.Order{}, nil
}

func TestOrderRoutes(t *testing.T) {
	validGuestToken, _, _ := orders.GenerateGuestToken()
	mock := &mockOrderService{
		createFn: func(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error) {
			if len(req.Items) > 0 && req.Items[0].ID == "out-of-stock" {
				return nil, orders.ErrInsufficientStock
			}
			return &orders.Order{
				ID:                "ord-test-123",
				CustomerName:      req.Customer.Name,
				PaymentStatus:     orders.PaymentPending,
				FulfillmentStatus: orders.FulfillmentUnfulfilled,
				TotalKes:          5000,
				GuestToken:        validGuestToken,
			}, nil
		},
		getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
			if orderID == "ord-test-123" {
				return &orders.Order{
					ID:                "ord-test-123",
					CustomerName:      "Alice",
					PaymentStatus:     orders.PaymentPending,
					FulfillmentStatus: orders.FulfillmentUnfulfilled,
					TotalKes:          5000,
				}, nil
			}
			return nil, orders.ErrOrderNotFound
		},
		statusFn: func(ctx context.Context, orderID string) (*orders.PaymentStatusResponse, error) {
			if orderID == "ord-test-123" {
				return &orders.PaymentStatusResponse{
					OrderID:           "ord-test-123",
					PaymentStatus:     orders.PaymentPending,
					FulfillmentStatus: orders.FulfillmentUnfulfilled,
				}, nil
			}
			return nil, orders.ErrOrderNotFound
		},
		validateAccessFn: func(ctx context.Context, orderID string, rawToken string) (bool, error) {
			if orderID == "ord-test-123" && rawToken == validGuestToken {
				return true, nil
			}
			return false, nil
		},
		recentFn: func(ctx context.Context, limit int) ([]orders.Order, error) {
			return []orders.Order{
				{ID: "ord-test-123", CustomerName: "Alice"},
			}, nil
		},
	}

	api := New("127.0.0.1:0", time.Second, nil, mock)

	// 1. POST /api/orders success
	validBody, _ := json.Marshal(map[string]any{
		"customer": map[string]string{
			"name":     "Alice Wanjiku",
			"phone":    "254712345678",
			"location": "Mombasa",
			"delivery": "delivery",
			"payment":  "pay_now",
		},
		"items": []map[string]any{
			{"id": "p1", "qty": 2},
		},
	})
	req := httptest.NewRequest("POST", "/api/orders", bytes.NewReader(validBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("POST /api/orders: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var createResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &createResp)
	if createResp["guest_token"] != validGuestToken {
		t.Fatalf("expected guest_token in create order response, got %v", createResp["guest_token"])
	}

	// 2. POST /orders insufficient stock
	oosBody, _ := json.Marshal(map[string]any{
		"customer": map[string]string{
			"name":     "Alice Wanjiku",
			"phone":    "254712345678",
			"location": "Mombasa",
			"delivery": "delivery",
			"payment":  "pay_now",
		},
		"items": []map[string]any{
			{"id": "out-of-stock", "qty": 1},
		},
	})
	req = httptest.NewRequest("POST", "/orders", bytes.NewReader(oosBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("POST /orders insufficient stock: expected 409, got %d", w.Code)
	}

	// 3a. GET /api/orders/ord-test-123/payment-status unauthenticated fails with 401
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123/payment-status", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("GET unauthenticated payment-status: expected 401, got %d", w.Code)
	}

	// 3b. GET /api/orders/ord-test-123/payment-status with wrong guest token fails with 403
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123/payment-status", nil)
	req.Header.Set("X-Guest-Token", "gt_0000000000000000000000000000000000000000000000000000000000000000")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("GET wrong guest token payment-status: expected 403, got %d", w.Code)
	}

	// 3c. GET /api/orders/ord-test-123/payment-status with valid guest token succeeds (200)
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123/payment-status", nil)
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/orders/ord-test-123/payment-status: expected 200, got %d", w.Code)
	}

	// 3d. GET /api/orders/ord-test-123/payment-status with manager auth succeeds (200)
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123/payment-status", nil)
	req.Header.Set("X-Manager-Id", "mgr-1")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/orders/ord-test-123/payment-status with manager auth: expected 200, got %d", w.Code)
	}

	// 4. GET /orders/unknown/payment-status with manager auth returns 404
	req = httptest.NewRequest("GET", "/orders/unknown/payment-status", nil)
	req.Header.Set("X-Manager-Id", "mgr-1")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("GET /orders/unknown/payment-status: expected 404, got %d", w.Code)
	}

	// 5a. GET /api/orders/ord-test-123 unauthenticated returns 401
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("GET unauthenticated order: expected 401, got %d", w.Code)
	}

	// 5b. GET /api/orders/ord-test-123 with valid guest token returns 200 (sanitized non-PII)
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123", nil)
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/orders/ord-test-123: expected 200, got %d", w.Code)
	}
	var getResp struct {
		Order map[string]any `json:"order"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode get order response: %v", err)
	}
	if getResp.Order["id"] != "ord-test-123" {
		t.Fatalf("expected order id ord-test-123, got %v", getResp.Order["id"])
	}
	if getResp.Order["customer_name"] != nil || getResp.Order["customer_phone"] != nil ||
		getResp.Order["delivery_location"] != nil || getResp.Order["notes"] != nil {
		t.Fatalf("GET /api/orders/{id} leaked customer PII: %v", getResp.Order)
	}

	// 6. GET /orders/unknown with manager auth returns 404
	req = httptest.NewRequest("GET", "/orders/unknown", nil)
	req.Header.Set("X-Manager-Id", "mgr-1")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("GET /orders/unknown: expected 404, got %d", w.Code)
	}

	// 7. GET /api/orders must not be allowed (PII protection; method not allowed)
	req = httptest.NewRequest("GET", "/api/orders", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed && w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/orders should not be allowed: expected 404 or 405, got %d", w.Code)
	}

	// 8. POST /api/orders rate limit throttles excessive order creations from same client
	var throttled bool
	for i := 0; i < 15; i++ {
		req = httptest.NewRequest("POST", "/api/orders", bytes.NewReader(validBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:12345"
		w = httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			throttled = true
			if w.Header().Get("Retry-After") == "" {
				t.Fatalf("expected Retry-After header on 429 response")
			}
			break
		}
	}
	if !throttled {
		t.Fatalf("expected excessive order creations to be throttled with 429 Too Many Requests")
	}
}

type mockDeliveryService struct {
	quotes map[string][]*delivery.DeliveryQuote
}

func (m *mockDeliveryService) CreateQuote(ctx context.Context, orderID string, p delivery.CreateQuoteParams) (*delivery.DeliveryQuote, error) {
	if p.AmountKES < 0 || p.FeeRecipient == "" || p.Arrangement == "" || p.CreatedBy == "" || p.ExpiresAt.IsZero() {
		return nil, delivery.ErrInvalidQuote
	}
	quotes := m.quotes[orderID]
	version := len(quotes) + 1
	q := &delivery.DeliveryQuote{
		ID:            "dq-uuid-1",
		OrderID:       orderID,
		Version:       version,
		AmountKES:     p.AmountKES,
		FeeRecipient:  p.FeeRecipient,
		Arrangement:   p.Arrangement,
		Status:        delivery.QuoteStatusPending,
		ExpiresAt:     p.ExpiresAt,
		CreatedAt:     time.Now(),
		CreatedBy:     p.CreatedBy,
		CustomerNotes: p.CustomerNotes,
	}
	m.quotes[orderID] = append(quotes, q)
	return q, nil
}

func (m *mockDeliveryService) GetActiveQuote(ctx context.Context, orderID string) (*delivery.DeliveryQuote, error) {
	quotes := m.quotes[orderID]
	if len(quotes) == 0 {
		return nil, delivery.ErrQuoteNotFound
	}
	return quotes[len(quotes)-1], nil
}

func (m *mockDeliveryService) AcceptQuote(ctx context.Context, orderID string, version int) (*delivery.DeliveryQuote, error) {
	quotes := m.quotes[orderID]
	if len(quotes) == 0 || version > len(quotes) || version <= 0 {
		return nil, delivery.ErrQuoteNotFound
	}
	if version < len(quotes) {
		return nil, delivery.ErrQuoteConflict
	}
	q := quotes[version-1]
	if q.Status == delivery.QuoteStatusAccepted {
		return q, nil
	}
	if q.Status != delivery.QuoteStatusPending {
		return nil, delivery.ErrQuoteNotPending
	}
	if time.Now().After(q.ExpiresAt) {
		q.Status = delivery.QuoteStatusExpired
		return nil, delivery.ErrQuoteExpired
	}
	now := time.Now()
	q.Status = delivery.QuoteStatusAccepted
	q.AcceptedAt = &now
	return q, nil
}

func (m *mockDeliveryService) RejectQuote(ctx context.Context, orderID string, version int, reason string) (*delivery.DeliveryQuote, error) {
	quotes := m.quotes[orderID]
	if len(quotes) == 0 || version > len(quotes) || version <= 0 {
		return nil, delivery.ErrQuoteNotFound
	}
	if version < len(quotes) {
		return nil, delivery.ErrQuoteConflict
	}
	q := quotes[version-1]
	if q.Status == delivery.QuoteStatusRejected {
		return q, nil
	}
	if q.Status != delivery.QuoteStatusPending {
		return nil, delivery.ErrQuoteNotPending
	}
	now := time.Now()
	q.Status = delivery.QuoteStatusRejected
	q.RejectedAt = &now
	q.CustomerNotes += " | " + reason
	return q, nil
}

func (m *mockDeliveryService) ExpireQuotes(ctx context.Context, asOf time.Time) (int64, error) {
	var count int64
	for _, qs := range m.quotes {
		for _, q := range qs {
			if q.Status == delivery.QuoteStatusPending && asOf.After(q.ExpiresAt) {
				q.Status = delivery.QuoteStatusExpired
				count++
			}
		}
	}
	return count, nil
}

func TestDeliveryQuoteEndpoints(t *testing.T) {
	orderID := "ord-deliv-test-1"
	validGuestToken, _, _ := orders.GenerateGuestToken()
	wrongGuestToken, _, _ := orders.GenerateGuestToken()

	mockOrder := &mockOrderService{
		getFn: func(ctx context.Context, id string) (*orders.Order, error) {
			if id == orderID {
				return &orders.Order{
					ID:             orderID,
					TotalKes:       5000,
					DeliveryOption: "delivery",
					PaymentStatus:  orders.PaymentPending,
				}, nil
			}
			return nil, orders.ErrOrderNotFound
		},
		validateAccessFn: func(ctx context.Context, id, token string) (bool, error) {
			if id == orderID && token == validGuestToken {
				return true, nil
			}
			return false, nil
		},
	}
	mockDeliv := &mockDeliveryService{
		quotes: make(map[string][]*delivery.DeliveryQuote),
	}

	api := New("127.0.0.1:0", time.Second, nil, mockOrder, mockDeliv)

	// 1a. GET /api/orders/{id}/delivery-quote unauthenticated fails with 401
	unauthGetReq := httptest.NewRequest("GET", "/api/orders/"+orderID+"/delivery-quote", nil)
	w := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, unauthGetReq)
	if w.Code != 401 {
		t.Fatalf("expected 401 Unauthorized for unauthenticated GET quote, got %d", w.Code)
	}

	// 1b. GET /api/orders/{id}/delivery-quote with guest token returns 404 when no quote exists
	req := httptest.NewRequest("GET", "/api/orders/"+orderID+"/delivery-quote", nil)
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404 when no quote exists, got %d", w.Code)
	}

	// 2a. POST /api/orders/{id}/delivery-quote without manager auth fails with 401
	createPayload := `{"amount_kes":350,"fee_recipient":"courier","arrangement":"Direct Nyali dispatch","expires_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `","created_by":"spoofed-user","customer_notes":"Ring bell"}`
	unauthReq := httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote", bytes.NewReader([]byte(createPayload)))
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, unauthReq)
	if w.Code != 401 {
		t.Fatalf("expected 401 Unauthorized for unauthenticated quote creation, got %d", w.Code)
	}

	// 2b. POST /api/orders/{id}/delivery-quote with X-Manager-Id creates quote (courier collection)
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote", bytes.NewReader([]byte(createPayload)))
	req.Header.Set("X-Manager-Id", "mgr-1")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201 Created for delivery quote, got %d (body: %s)", w.Code, w.Body.String())
	}
	if len(mockDeliv.quotes[orderID]) == 0 || mockDeliv.quotes[orderID][0].CreatedBy != "mgr-1" {
		t.Fatalf("expected created_by to be derived from authenticated manager identity mgr-1, got %v", mockDeliv.quotes[orderID][0].CreatedBy)
	}

	var createResp struct {
		Quote delivery.PublicQuote `json:"quote"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to decode create quote response: %v", err)
	}
	if createResp.Quote.Version != 1 || createResp.Quote.AmountKES != 350 || createResp.Quote.FeeRecipient != delivery.FeeRecipientCourier {
		t.Fatalf("unexpected quote in create response: %+v", createResp.Quote)
	}

	// 3. GET /api/orders/{id}/delivery-quote returns quote and breakdown
	req = httptest.NewRequest("GET", "/api/orders/"+orderID+"/delivery-quote", nil)
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 OK for GET quote, got %d", w.Code)
	}

	// 4a. Accept quote with wrong guest token fails with 403
	acceptPayload := `{"version":1}`
	wrongGuestReq := httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/accept", bytes.NewReader([]byte(acceptPayload)))
	wrongGuestReq.Header.Set("X-Guest-Token", wrongGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, wrongGuestReq)
	if w.Code != 403 {
		t.Fatalf("expected 403 Forbidden for cross-guest accept quote, got %d", w.Code)
	}

	// 4b. Accept quote with valid guest token succeeds
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/accept", bytes.NewReader([]byte(acceptPayload)))
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 OK for accept quote, got %d (body: %s)", w.Code, w.Body.String())
	}
	var acceptResp struct {
		Quote delivery.PublicQuote `json:"quote"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &acceptResp); err != nil {
		t.Fatalf("failed to decode accept response: %v", err)
	}
	if acceptResp.Quote.Status != delivery.QuoteStatusAccepted {
		t.Fatalf("expected accepted status, got %s", acceptResp.Quote.Status)
	}

	// 5. GET /api/orders/{id} includes sanitized delivery_quote and payable_breakdown
	req = httptest.NewRequest("GET", "/api/orders/"+orderID, nil)
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 OK for GET order, got %d", w.Code)
	}
	var getOrderResp struct {
		Order PublicOrder `json:"order"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getOrderResp); err != nil {
		t.Fatalf("failed to decode get order response: %v", err)
	}
	if getOrderResp.Order.DeliveryQuote == nil || getOrderResp.Order.DeliveryQuote.Status != delivery.QuoteStatusAccepted {
		t.Fatalf("expected order to have accepted delivery quote: %+v", getOrderResp.Order.DeliveryQuote)
	}
	if getOrderResp.Order.PayableBreakdown == nil {
		t.Fatalf("expected payable breakdown to be present")
	}
	// Direct courier transport fee is excluded from merchant collection!
	if getOrderResp.Order.PayableBreakdown.PayableMerchantKES != 5000 {
		t.Fatalf("expected payable merchant KES 5000 (items only), got %d", getOrderResp.Order.PayableBreakdown.PayableMerchantKES)
	}
	if getOrderResp.Order.PayableBreakdown.CustomerDirectKES != 350 {
		t.Fatalf("expected customer direct KES 350 (paid to rider), got %d", getOrderResp.Order.PayableBreakdown.CustomerDirectKES)
	}

	// 6. Create quote v2 (re-quote) resets to pending
	createPayload2 := `{"amount_kes":400,"fee_recipient":"merchant","arrangement":"Express with tracking","expires_at":"` + time.Now().Add(2*time.Hour).Format(time.RFC3339) + `","customer_notes":"Call ahead"}`
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote", bytes.NewReader([]byte(createPayload2)))
	req.Header.Set("Authorization", "Bearer mgr-2")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201 Created for quote v2, got %d", w.Code)
	}

	// Attempting to accept superseded quote v1 must fail with 409 Conflict
	supersededReq := httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/accept", bytes.NewReader([]byte(`{"version":1}`)))
	supersededReq.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, supersededReq)
	if w.Code != 409 {
		t.Fatalf("expected 409 Conflict when accepting superseded quote v1, got %d", w.Code)
	}

	// Reject quote v2 with wrong guest token fails with 403
	rejectPayload := `{"version":2,"reason":"Price too high"}`
	wrongRejectReq := httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/reject", bytes.NewReader([]byte(rejectPayload)))
	wrongRejectReq.Header.Set("X-Guest-Token", wrongGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, wrongRejectReq)
	if w.Code != 403 {
		t.Fatalf("expected 403 Forbidden for wrong guest reject quote, got %d", w.Code)
	}

	// Reject quote v2 with valid guest token succeeds
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/reject", bytes.NewReader([]byte(rejectPayload)))
	req.Header.Set("X-Guest-Token", validGuestToken)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 OK for reject quote, got %d (body: %s)", w.Code, w.Body.String())
	}
	var rejectResp struct {
		Quote delivery.PublicQuote `json:"quote"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rejectResp); err != nil {
		t.Fatalf("failed to decode reject response: %v", err)
	}
	if rejectResp.Quote.Status != delivery.QuoteStatusRejected {
		t.Fatalf("expected rejected status, got %s", rejectResp.Quote.Status)
	}

	// 7. Manager can also accept and reject quotes on behalf of customer
	createPayload3 := `{"amount_kes":450,"fee_recipient":"merchant","arrangement":"Standard dispatch","expires_at":"` + time.Now().Add(2*time.Hour).Format(time.RFC3339) + `"}`
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote", bytes.NewReader([]byte(createPayload3)))
	req.Header.Set("X-Manager-Id", "mgr-3")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201 Created for quote v3, got %d", w.Code)
	}

	// Manager accepts quote v3
	acceptMgrReq := httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/accept", bytes.NewReader([]byte(`{"version":3}`)))
	acceptMgrReq.Header.Set("X-Manager-Id", "mgr-3")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, acceptMgrReq)
	if w.Code != 200 {
		t.Fatalf("expected manager quote acceptance to succeed (200), got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestSecurityHeadersAndCORS(t *testing.T) {
	secConfig := SecurityConfig{
		AllowedOrigins: []string{"https://blissatelier.co.ke", "https://checkout.blissatelier.co.ke"},
	}
	api := New("127.0.0.1:0", time.Second, nil, secConfig)

	// 1. Standard security headers on GET /catalog
	req := httptest.NewRequest("GET", "/catalog", nil)
	w := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)

	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", w.Header().Get("X-Content-Type-Options"))
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("expected X-Frame-Options: DENY, got %q", w.Header().Get("X-Frame-Options"))
	}
	if w.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Errorf("expected Referrer-Policy: strict-origin-when-cross-origin, got %q", w.Header().Get("Referrer-Policy"))
	}
	if w.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("expected strict CSP, got %q", w.Header().Get("Content-Security-Policy"))
	}

	// 2. CORS Preflight OPTIONS
	preflightReq := httptest.NewRequest("OPTIONS", "/api/orders", nil)
	preflightReq.Header.Set("Origin", "https://blissatelier.co.ke")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, preflightReq)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for preflight, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://blissatelier.co.ke" {
		t.Errorf("expected CORS allow origin header, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-Guest-Token") {
		t.Errorf("expected X-Guest-Token in allowed headers, got %q", w.Header().Get("Access-Control-Allow-Headers"))
	}

	// 3. CORS disallowed origin
	badOriginReq := httptest.NewRequest("OPTIONS", "/api/orders", nil)
	badOriginReq.Header.Set("Origin", "https://malicious-site.example")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, badOriginReq)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("disallowed origin should not receive Access-Control-Allow-Origin")
	}
}

func TestRequestBodySizeEnforcementAndMalformedJSON(t *testing.T) {
	mock := &mockOrderService{
		createFn: func(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error) {
			return &orders.Order{ID: "ord-test"}, nil
		},
	}
	api := New("127.0.0.1:0", time.Second, nil, mock)

	// 1. Request body > 64KB returns 413 Payload Too Large
	largePayload := fmt.Sprintf(`{"customer":{"name":"Alice","phone":"254712345678","location":"Nairobi","delivery":"delivery","notes":"%s"},"items":[{"id":"p1","qty":1}]}`, strings.Repeat("x", 70*1024))
	req := httptest.NewRequest("POST", "/api/orders", strings.NewReader(largePayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d", w.Code)
	}
	var errResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &errResp)
	if !strings.Contains(errResp["error"], "too large") {
		t.Errorf("expected safe too large error message, got %v", errResp)
	}

	// 2. Malformed JSON returns 400 Bad Request
	req = httptest.NewRequest("POST", "/api/orders", bytes.NewReader([]byte(`{"customer": { invalid json`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed json, got %d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "invalid json payload" {
		t.Errorf("expected 'invalid json payload', got %v", errResp)
	}
}
