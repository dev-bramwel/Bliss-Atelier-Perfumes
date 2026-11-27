package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/reports"
)

type ReportService interface {
	SalesReport(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.SalesSummary, error)
	StockMovements(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.StockMovementPage, error)
	RecordFunnelEvent(ctx context.Context, in reports.FunnelEventInput) error
	FunnelSummary(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.FunnelReport, error)
	ExportCSV(ctx context.Context, actor *auth.User, f reports.Filter, w io.Writer) error
}

func registerReportRoutes(mux *http.ServeMux, store ReportService, checker *TrustedProxyChecker) {
	funnelLimiter := newMemoryLimiter(time.Minute, 120)

	parseReportFilter := func(w http.ResponseWriter, r *http.Request) (reports.Filter, bool) {
		limit, offset := pageBounds(r)
		q := r.URL.Query()
		f, err := reports.ParseFilter(q.Get("from"), q.Get("to"), q.Get("tz"), limit, offset)
		if err != nil {
			respond(w, 400, map[string]string{"error": err.Error()})
			return reports.Filter{}, false
		}
		return f, true
	}

	requireOwner := func(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Reports unavailable"})
			return nil, false
		}
		u := UserFromContext(r.Context())
		if u == nil || u.Role != auth.RoleOwner {
			respond(w, 403, map[string]string{"error": "Owner access is required for financial reports."})
			return nil, false
		}
		return u, true
	}

	handleSales := func(w http.ResponseWriter, r *http.Request) {
		u, ok := requireOwner(w, r)
		if !ok {
			return
		}
		f, ok := parseReportFilter(w, r)
		if !ok {
			return
		}
		res, err := store.SalesReport(r.Context(), u, f)
		if err != nil {
			respondReportError(w, err)
			return
		}
		respond(w, 200, res)
	}

	handleStockMovements := func(w http.ResponseWriter, r *http.Request) {
		u, ok := requireOwner(w, r)
		if !ok {
			return
		}
		f, ok := parseReportFilter(w, r)
		if !ok {
			return
		}
		res, err := store.StockMovements(r.Context(), u, f)
		if err != nil {
			respondReportError(w, err)
			return
		}
		respond(w, 200, res)
	}

	handleFunnelSummary := func(w http.ResponseWriter, r *http.Request) {
		u, ok := requireOwner(w, r)
		if !ok {
			return
		}
		f, ok := parseReportFilter(w, r)
		if !ok {
			return
		}
		res, err := store.FunnelSummary(r.Context(), u, f)
		if err != nil {
			respondReportError(w, err)
			return
		}
		respond(w, 200, res)
	}

	handleExportCSV := func(w http.ResponseWriter, r *http.Request) {
		u, ok := requireOwner(w, r)
		if !ok {
			return
		}
		f, ok := parseReportFilter(w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="bliss-reconciled-sales.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		if err := store.ExportCSV(r.Context(), u, f, w); err != nil {
			log.Printf("ERROR: csv export failure: %v", err)
		}
	}

	handleRecordFunnel := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Analytics unavailable"})
			return
		}
		if !funnelLimiter.Allow(r.Context(), SafeClientIP(r, checker)) {
			respond(w, 429, map[string]string{"error": "Too many funnel events"})
			return
		}
		var in reports.FunnelEventInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			respond(w, 400, map[string]string{"error": "Invalid funnel event payload"})
			return
		}
		if err := store.RecordFunnelEvent(r.Context(), in); err != nil {
			if errors.Is(err, reports.ErrInvalidEvent) {
				respond(w, 400, map[string]string{"error": err.Error()})
				return
			}
			log.Printf("ERROR: funnel event failure: %v", err)
			respond(w, 500, map[string]string{"error": "Failed to record funnel event"})
			return
		}
		respond(w, 202, map[string]string{"status": "accepted"})
	}

	for _, prefix := range []string{"", "/api"} {
		mux.HandleFunc("GET "+prefix+"/admin/reports/sales", handleSales)
		mux.HandleFunc("GET "+prefix+"/admin/reports/stock-movements", handleStockMovements)
		mux.HandleFunc("GET "+prefix+"/admin/reports/funnel", handleFunnelSummary)
		mux.HandleFunc("GET "+prefix+"/admin/reports/export.csv", handleExportCSV)
		mux.HandleFunc("POST "+prefix+"/events/funnel", handleRecordFunnel)
	}
}

func respondReportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrPermissionDenied):
		respond(w, 403, map[string]string{"error": "Owner access is required for financial reports."})
	case errors.Is(err, reports.ErrInvalidTimezone),
		errors.Is(err, reports.ErrInvalidDate),
		errors.Is(err, reports.ErrInvalidRange):
		respond(w, 400, map[string]string{"error": err.Error()})
	default:
		log.Printf("ERROR: report query failure: %v", err)
		respond(w, 500, map[string]string{"error": "Failed to load report due to an internal server error."})
	}
}
