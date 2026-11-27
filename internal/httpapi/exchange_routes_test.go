package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/exchanges"
)

type stubExchangeService struct {
	listErr     error
	createErr   error
	inspectErr  error
	decideErr   error
	completeErr error
	lastIdemKey string
}

func (s *stubExchangeService) List(ctx context.Context, statusFilter string, limit, offset int) (*exchanges.ExchangePage, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return &exchanges.ExchangePage{Exchanges: []exchanges.ExchangeRequest{}, Total: 0}, nil
}

func (s *stubExchangeService) CreateRequest(ctx context.Context, actor *auth.User, in exchanges.CreateRequestInput, idempotencyKey, ip string) (*exchanges.ExchangeRequest, error) {
	s.lastIdemKey = idempotencyKey
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &exchanges.ExchangeRequest{ID: "exch-1", OrderID: in.OrderID, Status: "requested"}, nil
}

func (s *stubExchangeService) Inspect(ctx context.Context, actor *auth.User, exchangeID string, passed bool, notes, ip string) (*exchanges.ExchangeRequest, error) {
	if s.inspectErr != nil {
		return nil, s.inspectErr
	}
	return &exchanges.ExchangeRequest{ID: exchangeID, Status: "inspected"}, nil
}

func (s *stubExchangeService) Decide(ctx context.Context, actor *auth.User, exchangeID string, approve bool, reason, ip string) (*exchanges.ExchangeRequest, error) {
	if s.decideErr != nil {
		return nil, s.decideErr
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	return &exchanges.ExchangeRequest{ID: exchangeID, Status: status}, nil
}

func (s *stubExchangeService) Complete(ctx context.Context, actor *auth.User, exchangeID, topupReceipt, topupEvidence, ip string) (*exchanges.ExchangeRequest, error) {
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	return &exchanges.ExchangeRequest{ID: exchangeID, Status: "completed"}, nil
}

func TestExchangeRoutesAuthAndErrorMapping(t *testing.T) {
	stub := &stubExchangeService{}
	mux := http.NewServeMux()
	registerExchangeRoutes(mux, stub, NewTrustedProxyChecker(""))

	withStaff := func(handler http.Handler, role string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := &auth.User{ID: "usr-1", Role: role}
			handler.ServeHTTP(w, r.WithContext(ContextWithAuth(r.Context(), u, nil)))
		})
	}

	// Create succeeds with Idempotency-Key header.
	body, _ := json.Marshal(exchanges.CreateRequestInput{
		OrderID: "ord-1", ReturnedProductID: "p1", ReturnedQuantity: 1,
		ReplacementProductID: "p2", ReplacementQuantity: 1, Reason: "Swap",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/operations/exchanges", bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", "idem-123")
	rec := httptest.NewRecorder()
	withStaff(mux, auth.RoleEmployee).ServeHTTP(rec, req)
	if rec.Code != 201 || stub.lastIdemKey != "idem-123" {
		t.Fatalf("expected 201 and idem-123, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Window expired maps to 400.
	stub.createErr = exchanges.ErrWindowExpired
	req2 := httptest.NewRequest(http.MethodPost, "/api/operations/exchanges", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	withStaff(mux, auth.RoleEmployee).ServeHTTP(rec2, req2)
	if rec2.Code != 400 {
		t.Fatalf("expected 400 for expired window, got %d", rec2.Code)
	}

	// Failed inspection approval maps to 409.
	stub.decideErr = exchanges.ErrInspectionFailed
	req3 := httptest.NewRequest(http.MethodPost, "/api/operations/exchanges/exch-1/approve", bytes.NewBufferString(`{"reason":"ok"}`))
	rec3 := httptest.NewRecorder()
	withStaff(mux, auth.RoleOwner).ServeHTTP(rec3, req3)
	if rec3.Code != 409 {
		t.Fatalf("expected 409 for failed inspection approval, got %d", rec3.Code)
	}
}
