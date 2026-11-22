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
