package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/reports"
)

type stubReportService struct{}

func (s *stubReportService) SalesReport(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.SalesSummary, error) {
	return &reports.SalesSummary{From: f.From, To: f.To, Timezone: f.Timezone, SettledProductRevenueKes: 2500}, nil
}

func (s *stubReportService) StockMovements(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.StockMovementPage, error) {
	return &reports.StockMovementPage{Movements: []reports.StockMovementRow{}, Total: 0}, nil
}

func (s *stubReportService) RecordFunnelEvent(ctx context.Context, in reports.FunnelEventInput) error {
	if in.EventID == "" {
		return reports.ErrInvalidEvent
	}
	return nil
}

func (s *stubReportService) FunnelSummary(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.FunnelReport, error) {
	return &reports.FunnelReport{From: f.From, To: f.To, Timezone: f.Timezone}, nil
}

func (s *stubReportService) ExportCSV(ctx context.Context, actor *auth.User, f reports.Filter, w io.Writer) error {
	_, err := io.WriteString(w, "order_id,paid_at_local,payment_status,product_revenue_kes,transport_fee_kes,fee_recipient,exchange_topup_kes\n")
	return err
}

func TestReportRoutesAuthTimezoneAndCSV(t *testing.T) {
	mux := http.NewServeMux()
	registerReportRoutes(mux, &stubReportService{}, NewTrustedProxyChecker(""))

	withRole := func(h http.Handler, role string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := &auth.User{ID: "usr-1", Role: role}
			h.ServeHTTP(w, r.WithContext(ContextWithAuth(r.Context(), u, nil)))
		})
	}

	// Employee is rejected with 403.
	empReq := httptest.NewRequest(http.MethodGet, "/api/admin/reports/sales", nil)
	empRec := httptest.NewRecorder()
	withRole(mux, auth.RoleEmployee).ServeHTTP(empRec, empReq)
	if empRec.Code != 403 {
		t.Fatalf("expected 403 for employee, got %d", empRec.Code)
	}

	// Invalid timezone is rejected with 400.
	badTZ := httptest.NewRequest(http.MethodGet, "/api/admin/reports/sales?tz=Mars/Olympus", nil)
	badTZRec := httptest.NewRecorder()
	withRole(mux, auth.RoleOwner).ServeHTTP(badTZRec, badTZ)
	if badTZRec.Code != 400 {
		t.Fatalf("expected 400 for invalid timezone, got %d", badTZRec.Code)
	}

	// Owner CSV export returns text/csv.
	csvReq := httptest.NewRequest(http.MethodGet, "/api/admin/reports/export.csv?from=2026-11-20&to=2026-11-21", nil)
	csvRec := httptest.NewRecorder()
	withRole(mux, auth.RoleOwner).ServeHTTP(csvRec, csvReq)
	if csvRec.Code != 200 || !strings.Contains(csvRec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("expected 200 text/csv, got %d %q", csvRec.Code, csvRec.Header().Get("Content-Type"))
	}

	// Public funnel event ingestion returns 202.
	evtReq := httptest.NewRequest(http.MethodPost, "/api/events/funnel", bytes.NewBufferString(`{"event_id":"e1","session_id":"s1","event_type":"catalog_view"}`))
	evtRec := httptest.NewRecorder()
	mux.ServeHTTP(evtRec, evtReq)
	if evtRec.Code != 202 {
		t.Fatalf("expected 202 for funnel event, got %d", evtRec.Code)
	}
}
