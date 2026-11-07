package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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
	createFn  func(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error)
	getFn     func(ctx context.Context, orderID string) (*orders.Order, error)
	statusFn  func(ctx context.Context, orderID string) (*orders.PaymentStatusResponse, error)
	releaseFn func(ctx context.Context, now time.Time) (int, error)
	recentFn  func(ctx context.Context, limit int) ([]orders.Order, error)
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

	// 3. GET /api/orders/ord-test-123/payment-status
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123/payment-status", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/orders/ord-test-123/payment-status: expected 200, got %d", w.Code)
	}

	// 4. GET /orders/unknown/payment-status
	req = httptest.NewRequest("GET", "/orders/unknown/payment-status", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("GET /orders/unknown/payment-status: expected 404, got %d", w.Code)
	}

	// 5. GET /api/orders/ord-test-123 (sanitized non-PII order inspection)
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123", nil)
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

	// 6. GET /orders/unknown
	req = httptest.NewRequest("GET", "/orders/unknown", nil)
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
	}
	mockDeliv := &mockDeliveryService{
		quotes: make(map[string][]*delivery.DeliveryQuote),
	}

	api := New("127.0.0.1:0", time.Second, nil, mockOrder, mockDeliv)

	// 1. GET /api/orders/{id}/delivery-quote returns 404 when no quote exists
	req := httptest.NewRequest("GET", "/api/orders/"+orderID+"/delivery-quote", nil)
	w := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404 when no quote exists, got %d", w.Code)
	}

	// 2. POST /api/orders/{id}/delivery-quote creates quote (courier collection)
	createPayload := `{"amount_kes":350,"fee_recipient":"courier","arrangement":"Direct Nyali dispatch","expires_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `","created_by":"mgr-1","customer_notes":"Ring bell"}`
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote", bytes.NewReader([]byte(createPayload)))
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201 Created for delivery quote, got %d (body: %s)", w.Code, w.Body.String())
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
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 OK for GET quote, got %d", w.Code)
	}

	// 4. Accept quote
	acceptPayload := `{"version":1}`
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/accept", bytes.NewReader([]byte(acceptPayload)))
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
	createPayload2 := `{"amount_kes":400,"fee_recipient":"merchant","arrangement":"Express with tracking","expires_at":"` + time.Now().Add(2*time.Hour).Format(time.RFC3339) + `","created_by":"mgr-2","customer_notes":"Call ahead"}`
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote", bytes.NewReader([]byte(createPayload2)))
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201 Created for quote v2, got %d", w.Code)
	}

	// Reject quote v2
	rejectPayload := `{"version":2,"reason":"Price too high"}`
	req = httptest.NewRequest("POST", "/api/orders/"+orderID+"/delivery-quote/reject", bytes.NewReader([]byte(rejectPayload)))
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
}
