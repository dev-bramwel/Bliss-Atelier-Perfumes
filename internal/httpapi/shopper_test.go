package httpapi

import (
	"context"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type shopperOrderStore struct {
	*mockOrderService
	owner    string
	switched bool
}

func (s *shopperOrderStore) CustomerOwnsOrder(_ context.Context, id, user string) (bool, error) {
	return id == "owned" && user == s.owner, nil
}
func (s *shopperOrderStore) SwitchToPickup(_ context.Context, id string) error {
	s.switched = true
	return nil
}
func TestShopperSessionOwnershipAndCSRF(t *testing.T) {
	service, users, cookie, csrf := staffFixture(t, auth.RoleCustomer)
	var owner string
	for id := range users.users {
		owner = id
	}
	var captured orders.CreateOrderRequest
	store := &shopperOrderStore{owner: owner, mockOrderService: &mockOrderService{
		createFn: func(_ context.Context, req orders.CreateOrderRequest) (*orders.Order, error) {
			captured = req
			return &orders.Order{ID: "owned", DeliveryOption: "delivery"}, nil
		},
		statusFn: func(_ context.Context, id string) (*orders.PaymentStatusResponse, error) {
			return &orders.PaymentStatusResponse{OrderID: id, PaymentStatus: orders.PaymentPending}, nil
		},
	}}
	api := New("127.0.0.1:0", time.Second, nil, store, service)
	for _, test := range []struct {
		path, method, body, csrf string
		want                     int
	}{
		{"/api/orders/owned/payment-status", "GET", "", "", 200},
		{"/api/orders/other/payment-status", "GET", "", "", 401},
		{"/api/orders/owned/pickup", "POST", "{}", "", 403},
		{"/api/orders/owned/pickup", "POST", "{}", csrf, 200},
		{"/api/orders", "POST", `{"user_id":"forged","customerName":"Shopper","items":[{"id":"p1","quantity":1}]}`, "", 403},
		{"/api/orders", "POST", `{"user_id":"forged","customerName":"Shopper","items":[{"id":"p1","quantity":1}]}`, csrf, 201},
	} {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", test.csrf)
		recorder := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(recorder, req)
		if recorder.Code != test.want {
			t.Fatalf("%s %s: got %d want %d: %s", test.method, test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
	if captured.UserID != owner {
		t.Fatal("identity was not bound to authoritative session")
	}
	if !store.switched {
		t.Fatal("authorized pickup change did not run")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/orders/owned/payment-status", nil)
	req.AddCookie(cookie)
	for _, session := range users.sessions {
		session.IsRevoked = true
	}
	recorder := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(recorder, req)
	if recorder.Code != 401 {
		t.Fatal("revoked session retained customer order access")
	}
}
