package reports

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GO_TEST_DATABASE_URL to the disposable integration database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Run(ctx, pool, "init", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	return pool, ctx
}

func TestReconciledReportsFunnelAndPrivacy(t *testing.T) {
	pool, ctx := openTestPool(t)
	store := &Store{Pool: pool}
	owner := &auth.User{ID: "usr-owner", Role: auth.RoleOwner}
	employee := &auth.User{ID: "usr-emp", Role: auth.RoleEmployee}

	// Non-owner access must be denied.
	f, err := ParseFilter("2026-11-20", "2026-11-21", "Africa/Nairobi", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SalesReport(ctx, employee, f); !errors.Is(err, auth.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for employee, got %v", err)
	}

	// 2026-11-20 21:30 UTC == 2026-11-21 00:30 Africa/Nairobi (+03:00) -> must bucket into 2026-11-21.
	nairobiBoundary := time.Date(2026, 11, 20, 21, 30, 0, 0, time.UTC)
	dayOne := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(ctx, `
		INSERT INTO public."Order"(id, "createdAt", "updatedAt", "customerName", "customerPhone", "deliveryLocation", "deliveryOption", "paymentMethod", "paymentStatus", "totalKes", items) VALUES
		('ord-paid-1', $1, $1, 'Secret Buyer One', '254711111111', 'Secret Address 1', 'delivery', 'mpesa_stk', 'PAID', 2500, '[]'::jsonb),
		('ord-paid-2', $2, $2, 'Secret Buyer Two', '254722222222', 'Secret Address 2', 'delivery', 'mpesa_stk', 'PAID', 5000, '[]'::jsonb),
		('ord-pending', $2, $2, 'Pending Buyer', '254733333333', 'Pending Address', 'delivery', 'mpesa_stk', 'PENDING', 9000, '[]'::jsonb)
	`, dayOne, nairobiBoundary); err != nil {
		t.Fatal(err)
	}

	// Accepted delivery quotes on paid orders (merchant vs courier) + rejected quote excluded.
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.delivery_quote(order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by) VALUES
		('ord-paid-1', 1, 300, 'merchant', 'rider', 'accepted', now(), 'usr-owner'),
		('ord-paid-2', 1, 450, 'courier', 'rider', 'accepted', now(), 'usr-owner'),
		('ord-pending', 1, 500, 'merchant', 'rider', 'accepted', now(), 'usr-owner')
	`); err != nil {
		t.Fatal(err)
	}

	// Completed exchange top-up (1000 KES) must be tracked separately from product sales.
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.exchange_request(order_id, status, reason, requested_by, requested_by_role, price_difference_kes, completed_at)
		VALUES ('ord-paid-1', 'completed', 'Upgrade', 'usr-emp', 'employee', 1000, $1)
	`, nairobiBoundary); err != nil {
		t.Fatal(err)
	}

	// Payment audit correction.
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.payment_audit_ledger(order_id, action, amount_kes, receipt_number, evidence, notes, authorized_by, created_at)
		VALUES ('ord-paid-1', 'duplicate_correction', 2500, 'RCPT-1', 'Bank stmt', 'Duplicate reversed', 'usr-owner', $1)
	`, dayOne); err != nil {
		t.Fatal(err)
	}

	// Stock batch + entry.
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.stock_batch(id, digest, kind, actor, evidence, created_at)
		VALUES ('batch-r22-1', 'dig-1', 'receipt', 'usr-owner', 'Opening', $1)`, dayOne); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.stock_entry(batch_id, product_id, delta, balance)
		VALUES ('batch-r22-1', 'p1', 10, 10)`); err != nil {
		t.Fatal(err)
	}

	report, err := store.SalesReport(ctx, owner, f)
	if err != nil {
		t.Fatalf("SalesReport failed: %v", err)
	}
	if report.SettledProductRevenueKes != 7500 {
		t.Fatalf("expected settled revenue 7500 (excluding pending 9000), got %d", report.SettledProductRevenueKes)
	}
	if report.PaidOrderCount != 2 || report.PendingOrderCount != 1 || report.AverageOrderValueKes != 3750 {
		t.Fatalf("unexpected counts/AOV: %+v", report)
	}
	if report.TransportFeesMerchantKes != 300 || report.TransportFeesCourierKes != 450 {
		t.Fatalf("unexpected transport fees: merchant=%d courier=%d", report.TransportFeesMerchantKes, report.TransportFeesCourierKes)
	}
	if report.ExchangeTopupRevenueKes != 1000 || report.CompletedExchangeCount != 1 {
		t.Fatalf("unexpected exchange topup: %+v", report)
	}
	if len(report.Daily) != 2 || report.Daily[0].Date != "2026-11-21" || report.Daily[0].ProductRevenueKes != 5000 {
		t.Fatalf("unexpected Africa/Nairobi daily bucketing: %+v", report.Daily)
	}

	// Stock movements pagination.
	movements, err := store.StockMovements(ctx, owner, f)
	if err != nil || movements.Total != 1 || len(movements.Movements) != 1 || movements.Movements[0].Kind != "receipt" {
		t.Fatalf("unexpected stock movements: %+v, err=%v", movements, err)
	}

	// Duplicate funnel events must not inflate counts, and never establish payment settlement.
	for i := 0; i < 3; i++ {
		if err := store.RecordFunnelEvent(ctx, FunnelEventInput{
			EventID:   "evt-dup-1",
			SessionID: "anon-sess-1",
			EventType: "payment_initiated",
			ProductID: "p1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE public.analytics_funnel_event SET occurred_at = $1 WHERE event_id = 'evt-dup-1'`, dayOne); err != nil {
		t.Fatal(err)
	}
	funnel, err := store.FunnelSummary(ctx, owner, f)
	if err != nil {
		t.Fatalf("FunnelSummary failed: %v", err)
	}
	if funnel.PaymentInitiated != 1 || funnel.SettledOrders != 2 {
		t.Fatalf("expected deduplicated payment_initiated=1 and settled_orders=2, got %+v", funnel)
	}

	// CSV export must omit customer PII.
	var csvBuf bytes.Buffer
	if err := store.ExportCSV(ctx, owner, f, &csvBuf); err != nil {
		t.Fatalf("ExportCSV failed: %v", err)
	}
	csvText := csvBuf.String()
	for _, pii := range []string{"Secret Buyer", "254711111111", "Secret Address"} {
		if strings.Contains(csvText, pii) {
			t.Fatalf("CSV export leaked PII %q:\n%s", pii, csvText)
		}
	}
	if !strings.Contains(csvText, "ord-paid-1") || !strings.Contains(csvText, "ord-paid-2") || strings.Contains(csvText, "ord-pending") {
		t.Fatalf("unexpected CSV rows:\n%s", csvText)
	}

	// Cross-period exchange: order paid in earlier window, exchange completed in later window.
	earlyDay := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	lateDay := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO public."Order"(id, "createdAt", "updatedAt", "customerName", "customerPhone", "deliveryLocation", "deliveryOption", "paymentMethod", "paymentStatus", "totalKes", items)
		VALUES ('ord-early', $1, $1, 'Early Buyer', '254744444444', 'Early Loc', 'pickup', 'mpesa_stk', 'PAID', 2500, '[]'::jsonb)`, earlyDay); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.exchange_request(order_id, status, reason, requested_by, requested_by_role, price_difference_kes, completed_at)
		VALUES ('ord-early', 'completed', 'Late upgrade', 'usr-emp', 'employee', 1500, $1)`, lateDay); err != nil {
		t.Fatal(err)
	}
	earlyFilter, _ := ParseFilter("2026-11-10", "2026-11-10", "Africa/Nairobi", 50, 0)
	var earlyCSV bytes.Buffer
	if err := store.ExportCSV(ctx, owner, earlyFilter, &earlyCSV); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(earlyCSV.String(), "1500") {
		t.Fatalf("earlier period CSV must not include later-period exchange top-up:\n%s", earlyCSV.String())
	}
	lateFilter, _ := ParseFilter("2026-11-25", "2026-11-25", "Africa/Nairobi", 50, 0)
	var lateCSV bytes.Buffer
	if err := store.ExportCSV(ctx, owner, lateFilter, &lateCSV); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lateCSV.String(), "exchange_topup,ord-early") || !strings.Contains(lateCSV.String(), ",1500") {
		t.Fatalf("later period CSV missing cross-period exchange_topup row:\n%s", lateCSV.String())
	}

	// Funnel retention purge removes expired events while keeping recent ones.
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.analytics_funnel_event (event_id, session_id, event_type, occurred_at) VALUES
		('evt-expired', 'sess-old', 'catalog_view', now() - interval '91 days'),
		('evt-fresh', 'sess-new', 'catalog_view', now() - interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	purged, err := store.PurgeExpiredFunnelEvents(ctx, DefaultFunnelRetention)
	if err != nil || purged != 1 {
		t.Fatalf("expected 1 purged event, got purged=%d err=%v", purged, err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.analytics_funnel_event WHERE event_id = 'evt-fresh'`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("expected fresh funnel event to remain, got %d err=%v", remaining, err)
	}
}
