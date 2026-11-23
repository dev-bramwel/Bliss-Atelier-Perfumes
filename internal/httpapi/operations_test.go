package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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
	service, _, cookie, csrf := staffFixture(t, auth.RoleOwner)
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
func (m *mockOperations) Restock(_ context.Context, u *auth.User, batchID, productID string, qty int, ref string, _ string) error {
	if u == nil || !auth.CanManageInventory(*u) {
		return auth.ErrPermissionDenied
	}
	if strings.TrimSpace(batchID) == "" {
		return operations.ErrBatchIDRequired
	}
	if qty == 0 {
		return operations.ErrInvalidQuantity
	}
	if productID == "notfound" {
		return operations.ErrProductNotFound
	}
	if productID == "dberror" {
		return errors.New("pq: connection reset by peer")
	}
	return nil
}

func TestOperationalSessionBoundaries(t *testing.T) {
	for _, role := range []string{auth.RoleOwner, auth.RoleEmployee, auth.RoleCustomer} {
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
	service, _, cookie, csrf := staffFixture(t, auth.RoleOwner)
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
	if resolution.Notes != "Verified reversal" || resolution.AuthorizedBy != "usr_staff-owner" || settlement.OrderID != "one" || settlement.AmountKes != 2500 || settlement.ReceiptNumber != "RCPT" || settlement.AuthorizedBy != "usr_staff-owner" {
		t.Fatalf("payload or actor binding failed: %+v %+v", resolution, settlement)
	}
	raw, _ := json.Marshal(quotes.quotes["one"][0])
	if !strings.Contains(string(raw), "Nyali") {
		t.Fatal("quote note lost")
	}
}

func TestOperationsRestockEndpoint(t *testing.T) {
	mock := &mockOperations{}
	service, store, ownerCookie, ownerCSRF := staffFixture(t, auth.RoleOwner)
	api := New("127.0.0.1:0", time.Second, nil, mock, service)

	ctx := context.Background()
	empAllowed, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp-allowed", "emp-allowed@blissatelier.com", "Emp Allowed", "")
	_ = store.UpdateUserRole(ctx, empAllowed.ID, auth.RoleEmployee)
	_, _ = store.UpdateUserPermissions(ctx, "", empAllowed.ID, true, nil)

	empDenied, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp-denied", "emp-denied@blissatelier.com", "Emp Denied", "")
	_ = store.UpdateUserRole(ctx, empDenied.ID, auth.RoleEmployee)

	makeSession := func(u *auth.User) (*http.Cookie, string) {
		raw, _ := auth.GenerateSessionToken()
		secret, _ := auth.RandomString(32)
		_ = store.CreateSession(ctx, auth.UserSession{
			ID:           "ses-" + u.ID,
			UserID:       u.ID,
			TokenHash:    auth.HashToken(raw),
			CSRFSecret:   secret,
			ExpiresAt:    time.Now().Add(time.Hour),
			MaxExpiresAt: time.Now().Add(24 * time.Hour),
		})
		return &http.Cookie{Name: "bliss_session", Value: raw}, auth.GenerateCSRFToken(secret)
	}

	empAllowedCookie, empAllowedCSRF := makeSession(empAllowed)
	empDeniedCookie, empDeniedCSRF := makeSession(empDenied)

	// 1. Owner can restock (200 OK)
	req := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-1","product_id":"p1","quantity":10,"reference":"BATCH-01"}`))
	authorizeStaff(req, ownerCookie, ownerCSRF)
	rr := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for owner restock, got %d: %s", rr.Code, rr.Body.String())
	}

	// 2. Employee with permission can restock (200 OK)
	req2 := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-2","product_id":"p1","quantity":5,"reference":"BATCH-02"}`))
	authorizeStaff(req2, empAllowedCookie, empAllowedCSRF)
	rr2 := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for permitted employee restock, got %d: %s", rr2.Code, rr2.Body.String())
	}

	// 3. Employee with permission can make negative stock adjustment (200 OK)
	reqAdj := httptest.NewRequest("POST", "/api/operations/inventory/adjust", strings.NewReader(`{"batch_id":"b-adj","product_id":"p1","quantity":-2,"reference":"DAMAGED-01"}`))
	authorizeStaff(reqAdj, empAllowedCookie, empAllowedCSRF)
	rrAdj := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rrAdj, reqAdj)
	if rrAdj.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for permitted employee stock adjustment, got %d: %s", rrAdj.Code, rrAdj.Body.String())
	}

	// 4. Employee without permission is rejected (403 Forbidden)
	req3 := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-3","product_id":"p1","quantity":5,"reference":"BATCH-03"}`))
	authorizeStaff(req3, empDeniedCookie, empDeniedCSRF)
	rr3 := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for employee without permission, got %d: %s", rr3.Code, rr3.Body.String())
	}

	// 5. Invalid input: zero quantity (400 Bad Request)
	req4 := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-4","product_id":"p1","quantity":0,"reference":"BATCH-04"}`))
	authorizeStaff(req4, empAllowedCookie, empAllowedCSRF)
	rr4 := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for zero quantity, got %d", rr4.Code)
	}

	// 6. Product not found (404 Not Found)
	reqNotFound := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-nf","product_id":"notfound","quantity":5,"reference":"BATCH-NF"}`))
	authorizeStaff(reqNotFound, empAllowedCookie, empAllowedCSRF)
	rrNotFound := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rrNotFound, reqNotFound)
	if rrNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for non-existent product, got %d", rrNotFound.Code)
	}

	// 7. Sanitized 500 error on unexpected database failure
	reqErr := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-err","product_id":"dberror","quantity":5,"reference":"BATCH-ERR"}`))
	authorizeStaff(reqErr, empAllowedCookie, empAllowedCSRF)
	rrErr := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rrErr, reqErr)
	if rrErr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error for storage failure, got %d", rrErr.Code)
	}
	if strings.Contains(rrErr.Body.String(), "connection reset") {
		t.Fatalf("database details leaked to client: %s", rrErr.Body.String())
	}

	// 8. Missing batch ID (400 Bad Request)
	reqNoBatch := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"product_id":"p1","quantity":5,"reference":"BATCH-NOID"}`))
	authorizeStaff(reqNoBatch, empAllowedCookie, empAllowedCSRF)
	rrNoBatch := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rrNoBatch, reqNoBatch)
	if rrNoBatch.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing batch ID, got %d", rrNoBatch.Code)
	}

	// 9. Invalid CSRF token (403 Forbidden)
	req5 := httptest.NewRequest("POST", "/api/operations/inventory/restock", strings.NewReader(`{"batch_id":"b-5","product_id":"p1","quantity":5,"reference":"BATCH-05"}`))
	authorizeStaff(req5, empAllowedCookie, "bad-csrf-token")
	rr5 := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rr5, req5)
	if rr5.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for invalid CSRF token, got %d", rr5.Code)
	}
}
