package exchanges

import (
	"context"
	"errors"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/jackc/pgx/v5/pgxpool"
)

type mockInvalidator struct {
	called atomic.Bool
}

func (m *mockInvalidator) Invalidate(ctx context.Context) error {
	m.called.Store(true)
	return nil
}

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
	if _, err = pool.Exec(ctx, `
		UPDATE public.stock_control SET authority='replacement', reconciled_until=now()+interval '1 hour';
		UPDATE public.catalog_product SET stock=5, opened=true;
		UPDATE public.catalog_product SET price_override_kes=3500 WHERE id='p2';
		UPDATE public.catalog_product SET price_override_kes=2000 WHERE id='p3';
	`); err != nil {
		t.Fatal(err)
	}
	return pool, ctx
}

func seedPaidOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, productID string, qty, unitPrice int, paidAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO public."Order"(id, "createdAt", "updatedAt", paid_at, "customerName", "customerPhone", "deliveryLocation", "deliveryOption", "paymentMethod", "paymentStatus", "totalKes", items)
		VALUES ($1, $2, $2, $2, 'Amina Hassan', '254712345678', 'Nyali', 'pickup', 'mpesa_stk', 'PAID', $3, '[]'::jsonb)`,
		orderID, paidAt, qty*unitPrice); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.order_item(order_id, product_id, product_name, product_size, unit_price_kes, quantity, subtotal_kes)
		VALUES ($1, $2, 'Perfume', '100ml', $3, $4, $5)`,
		orderID, productID, unitPrice, qty, qty*unitPrice); err != nil {
		t.Fatal(err)
	}
}

func TestExchangeLifecycleAndInvariants(t *testing.T) {
	pool, ctx := openTestPool(t)
	inv := &mockInvalidator{}
	store := &Store{Pool: pool, Invalidator: inv}

	users := auth.NewPGStore(pool)
	employee, err := users.UpsertUserByGoogleSub(ctx, "exch-emp", "emp@example.test", "Employee", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.app_user SET role='employee' WHERE id=$1`, employee.ID); err != nil {
		t.Fatal(err)
	}
	employee.Role = auth.RoleEmployee

	// 1. Order paid >48h ago is rejected.
	seedPaidOrder(t, ctx, pool, "ord-expired", "p1", 1, 2500, time.Now().UTC().Add(-49*time.Hour))
	_, err = store.CreateRequest(ctx, employee, CreateRequestInput{
		OrderID: "ord-expired", ReturnedProductID: "p1", ReturnedQuantity: 1,
		ReplacementProductID: "p1", ReplacementQuantity: 1, Reason: "Late exchange attempt",
	}, "key-expired", "127.0.0.1")
	if !errors.Is(err, ErrWindowExpired) {
		t.Fatalf("expected ErrWindowExpired, got %v", err)
	}

	// 2. Lower-value replacement is excluded.
	seedPaidOrder(t, ctx, pool, "ord-fresh", "p1", 2, 2500, time.Now().UTC().Add(-2*time.Hour))
	_, err = store.CreateRequest(ctx, employee, CreateRequestInput{
		OrderID: "ord-fresh", ReturnedProductID: "p1", ReturnedQuantity: 1,
		ReplacementProductID: "p3", ReplacementQuantity: 1, Reason: "Wants cheaper bottle",
	}, "key-lower", "127.0.0.1")
	if !errors.Is(err, ErrLowerValueExcluded) {
		t.Fatalf("expected ErrLowerValueExcluded, got %v", err)
	}

	// 3. Create higher-value exchange (p1 2500 -> p2 3500, diff 1000) with idempotent replay.
	in := CreateRequestInput{
		OrderID: "ord-fresh", ReturnedProductID: "p1", ReturnedQuantity: 1,
		ReplacementProductID: "p2", ReplacementQuantity: 1, Reason: "Upgrade scent at shop",
	}
	created, err := store.CreateRequest(ctx, employee, in, "key-upgrade-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}
	if created.PriceDifferenceKes != 1000 || created.Status != "requested" {
		t.Fatalf("unexpected created exchange: %+v", created)
	}
	replayed, err := store.CreateRequest(ctx, employee, in, "key-upgrade-1", "127.0.0.1")
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("expected idempotent replay of %s, got %+v, err=%v", created.ID, replayed, err)
	}

	// Verify p1 is quarantined (+1) and saleable stock is untouched (5).
	var p1Stock, p1Quarantined int
	if err := pool.QueryRow(ctx, `SELECT stock, quarantined_stock FROM public.catalog_product WHERE id='p1'`).Scan(&p1Stock, &p1Quarantined); err != nil {
		t.Fatal(err)
	}
	if p1Stock != 5 || p1Quarantined != 1 {
		t.Fatalf("expected p1 stock=5 quarantined=1, got stock=%d quarantined=%d", p1Stock, p1Quarantined)
	}

	// 4. Inspect (passed), Approve, and Complete (requires topup for diff > 0).
	if _, err := store.Inspect(ctx, employee, created.ID, true, "Seal intact, unsprayed", "127.0.0.1"); err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if _, err := store.Decide(ctx, employee, created.ID, true, "Eligible within 48h", "127.0.0.1"); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if _, err := store.Complete(ctx, employee, created.ID, "", "", "127.0.0.1"); !errors.Is(err, ErrTopupRequired) {
		t.Fatalf("expected ErrTopupRequired, got %v", err)
	}

	completed, err := store.Complete(ctx, employee, created.ID, "MPESA-TOPUP-001", "Shop counter STK verified", "127.0.0.1")
	if err != nil || completed.Status != "completed" {
		t.Fatalf("Complete failed: %v, res=%+v", err, completed)
	}
	// Replay Complete must not double-move stock.
	if _, err := store.Complete(ctx, employee, created.ID, "MPESA-TOPUP-001", "Shop counter STK verified", "127.0.0.1"); err != nil {
		t.Fatalf("Complete replay failed: %v", err)
	}

	var p2Stock int
	if err := pool.QueryRow(ctx, `SELECT stock, quarantined_stock FROM public.catalog_product WHERE id='p1'`).Scan(&p1Stock, &p1Quarantined); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id='p2'`).Scan(&p2Stock); err != nil {
		t.Fatal(err)
	}
	if p1Stock != 6 || p1Quarantined != 0 || p2Stock != 4 {
		t.Fatalf("unexpected stock after complete: p1=(%d, q=%d) p2=%d", p1Stock, p1Quarantined, p2Stock)
	}

	// 5. Rejected / failed inspection return never becomes saleable.
	created2, err := store.CreateRequest(ctx, employee, CreateRequestInput{
		OrderID: "ord-fresh", ReturnedProductID: "p1", ReturnedQuantity: 1,
		ReplacementProductID: "p1", ReplacementQuantity: 1, Reason: "Second bottle",
	}, "key-reject-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("second CreateRequest failed: %v", err)
	}
	if _, err := store.Inspect(ctx, employee, created2.ID, false, "Cellophane opened and sprayed", "127.0.0.1"); err != nil {
		t.Fatalf("failed inspection failed: %v", err)
	}
	if _, err := store.Decide(ctx, employee, created2.ID, true, "Attempt approve", "127.0.0.1"); !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("expected ErrInspectionFailed, got %v", err)
	}
	if _, err := store.Decide(ctx, employee, created2.ID, false, "Bottle opened", "127.0.0.1"); err != nil {
		t.Fatalf("Reject failed: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT stock, quarantined_stock FROM public.catalog_product WHERE id='p1'`).Scan(&p1Stock, &p1Quarantined); err != nil {
		t.Fatal(err)
	}
	if p1Stock != 6 || p1Quarantined != 1 {
		t.Fatalf("rejected return must not become saleable: got stock=%d quarantined=%d", p1Stock, p1Quarantined)
	}

	// Verify audit trail retained role and reason.
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.admin_audit_log WHERE resource='exchange_request' AND actor_role='employee'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount < 6 {
		t.Fatalf("expected >=6 exchange audit records, got %d", auditCount)
	}
}
