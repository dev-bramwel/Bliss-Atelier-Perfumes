package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/delivery"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/operations"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

func staffFixture(t *testing.T, role string) (*auth.Service, *testAuthStore, *http.Cookie, string) {
	t.Helper()
	store := newTestAuthStore()
	u, err := store.UpsertUserByGoogleSub(context.Background(), "staff-"+role, role+"@example.test", role, "")
	if err != nil {
		t.Fatal(err)
	}
	u.Role = role
	raw, _ := auth.GenerateSessionToken()
	secret, _ := auth.RandomString(32)
	err = store.CreateSession(context.Background(), auth.UserSession{UserID: u.ID, TokenHash: auth.HashToken(raw), CSRFSecret: secret, ExpiresAt: time.Now().Add(time.Hour), MaxExpiresAt: time.Now().Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return auth.NewService(store, auth.NewMockOIDCVerifier(), nil), store, &http.Cookie{Name: "bliss_session", Value: raw}, auth.GenerateCSRFToken(secret)
}
func newSessionAPI(t *testing.T, address string, timeout time.Duration, ping Ping, deps ...any) (*API, *http.Cookie, string) {
	t.Helper()
	service, _, cookie, csrf := staffFixture(t, auth.RoleManager)
	deps = append(deps, service)
	return New(address, timeout, ping, deps...), cookie, csrf
}
func authorizeStaff(r *http.Request, cookie *http.Cookie, csrf string) {
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
}

type mockOperations struct {
	scope         string
	limit, offset int
	fulfilled     string
}

func (m *mockOperations) CanAccess(_ context.Context, id, user string) (bool, error) {
	return id == "assigned", nil
}
func (m *mockOperations) Inventory(context.Context, int, int) (*operations.InventoryPage, error) {
	return &operations.InventoryPage{Products: []operations.InventoryItem{}, CommonPriceKes: 2500}, nil
}
func (m *mockOperations) Orders(_ context.Context, scope string, limit, offset int) (*operations.OrderPage, error) {
	m.scope, m.limit, m.offset = scope, limit, offset
	return &operations.OrderPage{Orders: []operations.OrderRow{}}, nil
}
func (m *mockOperations) Assign(context.Context, string, string, *auth.User, string) error {
	return nil
}
func (m *mockOperations) Fulfill(_ context.Context, id string, u *auth.User, _ string) error {
	if u.Role == auth.RoleEmployee && id != "assigned" {
		return auth.ErrPermissionDenied
	}
	m.fulfilled = id
	return nil
}

func TestOperationalSessionBoundaries(t *testing.T) {
	for _, role := range []string{auth.RoleOwner, auth.RoleManager, auth.RoleEmployee, auth.RoleCustomer} {
		t.Run(role, func(t *testing.T) {
			service, store, cookie, csrf := staffFixture(t, role)
			ops := &mockOperations{}
			orderStore := &mockOrderService{getFn: func(_ context.Context, id string) (*orders.Order, error) {
				return &orders.Order{ID: id, CustomerPhone: "254700111222", Notes: "Private dispatch note"}, nil
			}}
			a := New(":0", time.Second, nil, service, ops, orderStore, SecurityConfig{ManagerSecret: "legacy"})
			call := func(method, path, body string, token bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.AddCookie(cookie)
				if token {
					r.Header.Set("X-CSRF-Token", csrf)
				}
				w := httptest.NewRecorder()
				a.server.Handler.ServeHTTP(w, r)
				return w
			}
			want := 200
			if role == auth.RoleCustomer {
				want = 403
			}
			if w := call("GET", "/api/operations/orders?limit=999&offset=-1", "", false); w.Code != want {
				t.Fatalf("orders status %d: %s", w.Code, w.Body)
			}
			if role == auth.RoleEmployee && ops.scope != "usr_staff-employee" {
				t.Fatalf("employee scope %q", ops.scope)
			}
			if ops.limit != 50 && role != auth.RoleCustomer {
				t.Fatal("unbounded page")
			}
			for _, path := range []string{"/api/admin/users", "/api/admin/audit-logs", "/api/admin/payments/exceptions", "/admin/payments/exceptions"} {
				w := call("GET", path, "", false)
				if role == auth.RoleEmployee || role == auth.RoleCustomer {
					if w.Code != 403 {
						t.Fatalf("%s: expected 403, got %d", path, w.Code)
					}
				} else if w.Code != 200 {
					t.Fatalf("%s: expected 200, got %d: %s", path, w.Code, w.Body)
				}
			}
			if role != auth.RoleCustomer {
				if w := call("POST", "/api/orders/assigned/fulfill", "{}", false); w.Code != 403 {
					t.Fatalf("missing CSRF: %d", w.Code)
				}
				if w := call("POST", "/api/orders/assigned/fulfill", "{}", true); w.Code != 200 {
					t.Fatalf("fulfill: %d %s", w.Code, w.Body)
				}
				if role == auth.RoleEmployee {
					if w := call("POST", "/api/orders/unassigned/fulfill", "{}", true); w.Code != 403 {
						t.Fatal("employee fulfilled unassigned order")
					}
				}
				w := call("GET", "/api/orders/assigned", "", false)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "Private dispatch note") {
					t.Fatalf("staff details unavailable: %d %s", w.Code, w.Body)
				}
			}
			if role == auth.RoleEmployee {
				if w := call("GET", "/api/orders/unassigned", "", false); w.Code == 200 {
					t.Fatal("employee read another order")
				}
			}
			r := httptest.NewRequest("GET", "/api/admin/payments/exceptions", nil)
			r.Header.Set("X-Manager-Secret", "legacy")
			w := httptest.NewRecorder()
			a.server.Handler.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal("shared secret bypassed sessions")
			}
			if err := store.RevokeSessionByTokenHash(context.Background(), auth.HashToken(cookie.Value)); err != nil {
				t.Fatal(err)
			}
			if w := call("GET", "/api/operations/orders", "", false); w.Code != 401 {
				t.Fatalf("revoked session status %d", w.Code)
			}
		})
	}
}

func TestIssuePayloadsAndActorBinding(t *testing.T) {
	service, _, cookie, csrf := staffFixture(t, auth.RoleManager)
	var resolution orders.ResolveExceptionRequest
	var settlement orders.ManualSettlementRequest
	mock := &mockOrderService{resolveExcFn: func(_ context.Context, _ string, r orders.ResolveExceptionRequest) error { resolution = r; return nil }, manualSettleFn: func(_ context.Context, r orders.ManualSettlementRequest) (*orders.Order, *orders.PaymentAuditRecord, error) {
		settlement = r
		return &orders.Order{}, &orders.PaymentAuditRecord{}, nil
	}}
	quotes := &mockDeliveryService{quotes: map[string][]*delivery.DeliveryQuote{}}
	a := New(":0", time.Second, nil, service, mock, quotes)
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/orders/one/delivery-quote", `{"fee_kes":350,"notes":"Nyali"}`, 201},
		{"/api/orders/one/delivery-quote", `{"fee_kes":0}`, 400},
		{"/api/admin/payments/exceptions/exc/resolve", `{"action":"refund_recorded","resolution_notes":"Verified reversal","evidence":"bank-ref","authorizedBy":"spoofed"}`, 200},
		{"/api/admin/payments/manual-settlement", `{"order_id":"one","amount_kes":2500,"receipt_number":"RCPT","evidence":"bank-ref","notes":"verified","authorizedBy":"spoofed"}`, 200},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		authorizeStaff(r, cookie, csrf)
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	if resolution.Notes != "Verified reversal" || resolution.AuthorizedBy != "usr_staff-manager" || settlement.OrderID != "one" || settlement.AmountKes != 2500 || settlement.ReceiptNumber != "RCPT" || settlement.AuthorizedBy != "usr_staff-manager" {
		t.Fatalf("payload or actor binding failed: %+v %+v", resolution, settlement)
	}
	raw, _ := json.Marshal(quotes.quotes["one"][0])
	if !strings.Contains(string(raw), "Nyali") {
		t.Fatal("quote note lost")
	}
}
