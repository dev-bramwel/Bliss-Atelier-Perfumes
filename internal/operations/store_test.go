package operations

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Uses the same explicitly disposable database guard as the other integration suites.
func TestAssignedFulfillmentAndInventory(t *testing.T) {
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GO_TEST_DATABASE_URL to the disposable integration database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Run(ctx, pool, "init", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE public.stock_control SET authority='replacement',reconciled_until=now()+interval '1 hour'; UPDATE public.catalog_product SET stock=5,opened=true`); err != nil {
		t.Fatal(err)
	}
	users := auth.NewPGStore(pool)
	makeUser := func(role string) *auth.User {
		user, err := users.UpsertUserByGoogleSub(ctx, "ops-"+role, role+"@example.test", role, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = users.UpdateUserRole(ctx, user.ID, role); err != nil {
			t.Fatal(err)
		}
		user.Role = role
		return user
	}
	owner, employee := makeUser(auth.RoleOwner), makeUser(auth.RoleEmployee)
	osStore := &orders.Store{Pool: pool}
	s := &Store{Pool: pool}
	order, err := osStore.CreateOrder(ctx, orders.CreateOrderRequest{Customer: orders.CustomerInput{Name: "Test buyer", Phone: "0712345678", Location: "Mombasa", Delivery: "pickup", Payment: "pay_now"}, Items: []orders.ItemInput{{ID: "p1", Qty: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.Inventory(ctx, 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Products[0].Stock != 5 || inv.Products[0].Held != 2 || inv.Products[0].Available != 3 {
		t.Fatalf("holds not reflected: %+v", inv.Products[0])
	}
	if err = s.Assign(ctx, order.ID, employee.ID, employee, "127.0.0.1"); !errors.Is(err, auth.ErrPermissionDenied) {
		t.Fatal("employee assigned an order")
	}
	if err = s.Assign(ctx, order.ID, employee.ID, owner, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	allowed, err := s.CanAccess(ctx, order.ID, employee.ID)
	if err != nil || !allowed {
		t.Fatal("employee cannot access assignment")
	}
	page, err := s.Orders(ctx, employee.ID, 25, 0)
	if err != nil || page.Total != 1 || len(page.Orders) != 1 {
		t.Fatalf("scoped queue: %+v %v", page, err)
	}
	if err = s.Fulfill(ctx, order.ID, employee, "127.0.0.1"); !errors.Is(err, ErrConflict) {
		t.Fatal("unpaid order dispatched")
	}
	if err = osStore.ConfirmPayment(ctx, order.ID, "OPS-RECEIPT", order.TotalKes); err != nil {
		t.Fatal(err)
	}
	if err = s.Fulfill(ctx, order.ID, employee, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Fulfill(ctx, order.ID, employee, "127.0.0.1"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate fulfillment accepted")
	}
	var audits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM public.admin_audit_log WHERE resource_id=$1 AND action IN ('assign_order','fulfill_order')`, order.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit entries: %d %v", audits, err)
	}
	inv, err = s.Inventory(ctx, 25, 0)
	if err != nil || inv.Products[0].Stock != 3 || inv.Products[0].Held != 0 {
		t.Fatalf("paid stock: %+v %v", inv, err)
	}
}

func TestRestockPermissionsAndLedger(t *testing.T) {
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GO_TEST_DATABASE_URL to the disposable integration database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Run(ctx, pool, "init", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	users := auth.NewPGStore(pool)
	makeUser := func(role string) *auth.User {
		user, err := users.UpsertUserByGoogleSub(ctx, "restock-"+role, role+"@restock.test", role, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = users.UpdateUserRole(ctx, user.ID, role); err != nil {
			t.Fatal(err)
		}
		user.Role = role
		return user
	}
	owner := makeUser(auth.RoleOwner)
	empDenied := makeUser(auth.RoleEmployee)
	empAllowed := makeUser(auth.RoleEmployee)
	if _, err = users.UpdateUserPermissions(ctx, owner.ID, empAllowed.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	empAllowed.CanManageInventory = true

	s := &Store{Pool: pool}

	// 1. Employee without permission is rejected
	if err := s.Restock(ctx, empDenied, "p1", 10, "INV-100", "127.0.0.1"); !errors.Is(err, auth.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for unpermitted employee, got: %v", err)
	}

	// 2. Negative or zero quantity rejected
	if err := s.Restock(ctx, empAllowed, "p1", 0, "INV-100", "127.0.0.1"); err == nil {
		t.Fatal("expected error for zero quantity, got nil")
	}

	// 3. Employee with permission restocks p1
	var initialStock int
	_ = pool.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id='p1'`).Scan(&initialStock)
	if err := s.Restock(ctx, empAllowed, "p1", 5, "INV-ARRIVAL-1", "127.0.0.1"); err != nil {
		t.Fatalf("unexpected error during restock: %v", err)
	}
	var newStock int
	_ = pool.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id='p1'`).Scan(&newStock)
	if newStock != initialStock+5 {
		t.Fatalf("expected stock %d, got %d", initialStock+5, newStock)
	}

	// Verify ledger entry
	var delta int
	if err := pool.QueryRow(ctx, `SELECT e.delta FROM public.stock_entry e JOIN public.stock_batch b ON e.batch_id = b.id WHERE e.product_id='p1' ORDER BY b.created_at DESC LIMIT 1`).Scan(&delta); err != nil || delta != 5 {
		t.Fatalf("expected stock_entry delta 5, got %d (err: %v)", delta, err)
	}

	// Verify audit log
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.admin_audit_log WHERE action='restock_inventory' AND resource_id='p1'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("expected 1 audit log entry for restock, got %d (err: %v)", auditCount, err)
	}

	// 4. Owner restocks p2
	if err := s.Restock(ctx, owner, "p2", 3, "OWNER-ARRIVAL", "127.0.0.1"); err != nil {
		t.Fatalf("owner restock failed: %v", err)
	}
}
