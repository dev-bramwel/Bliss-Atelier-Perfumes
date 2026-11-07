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
	createFn func(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error)
	getFn    func(ctx context.Context, orderID string) (*orders.Order, error)
	statusFn func(ctx context.Context, orderID string) (*orders.PaymentStatusResponse, error)
	recentFn func(ctx context.Context, limit int) ([]orders.Order, error)
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

	// 5. GET /api/orders/ord-test-123
	req = httptest.NewRequest("GET", "/api/orders/ord-test-123", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/orders/ord-test-123: expected 200, got %d", w.Code)
	}

	// 6. GET /orders/unknown
	req = httptest.NewRequest("GET", "/orders/unknown", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("GET /orders/unknown: expected 404, got %d", w.Code)
	}

	// 7. GET /api/orders
	req = httptest.NewRequest("GET", "/api/orders", nil)
	w = httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/orders: expected 200, got %d", w.Code)
	}
}
