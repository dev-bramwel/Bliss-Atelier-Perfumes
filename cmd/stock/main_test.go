package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping database test; set GO_TEST_DATABASE_URL to disposable test postgres")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn, 5, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestStrictImportInput(t *testing.T) {
	for _, raw := range []string{
		`{"lines":[{"productId":"p1","quantity":null}]}`,
		`{"lines":[{"productId":"p1"}]}`,
		`{"unexpected":true}`,
		`{} {}`,
		`{}` + strings.Repeat(" ", 1<<20),
	} {
		var b catalog.Batch
		if decode(strings.NewReader(raw), &b) == nil {
			t.Fatal("malformed input accepted")
		}
	}
	var b catalog.Batch
	if err := decode(strings.NewReader(`{"id":"count","kind":"opening","actor":"owner-id","evidence":"signoff-reference","lines":[{"productId":"p1","quantity":0}]}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.Validate() != nil {
		t.Fatal("explicit zero opening rejected")
	}
}

func TestVerifyActorPermission(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var hasAppUser bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.app_user') IS NOT NULL`).Scan(&hasAppUser); err != nil || !hasAppUser {
		t.Skip("app_user table not present in test database")
	}

	_, _ = pool.Exec(ctx, `DELETE FROM public.app_user WHERE email LIKE '%@stocktest.blissatelier.com'`)
	_, err := pool.Exec(ctx, `
		INSERT INTO public.app_user (id, google_sub, email, name, role, is_active, can_manage_inventory)
		VALUES 
			('usr_stk_owner', 'sub_stk_owner', 'owner@stocktest.blissatelier.com', 'Stock Owner', 'owner', true, false),
			('usr_stk_emp_allowed', 'sub_stk_emp1', 'emp1@stocktest.blissatelier.com', 'Allowed Emp', 'employee', true, true),
			('usr_stk_emp_denied', 'sub_stk_emp2', 'emp2@stocktest.blissatelier.com', 'Denied Emp', 'employee', true, false),
			('usr_stk_emp_inactive', 'sub_stk_emp3', 'emp3@stocktest.blissatelier.com', 'Inactive Emp', 'employee', false, true),
			('usr_stk_cust', 'sub_stk_cust', 'cust@stocktest.blissatelier.com', 'Customer User', 'customer', true, true);
	`)
	if err != nil {
		t.Fatalf("failed to insert test users: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.app_user WHERE email LIKE '%@stocktest.blissatelier.com'`)
	})

	// 1. Owner is permitted
	if err := verifyActorPermission(ctx, pool, "usr_stk_owner"); err != nil {
		t.Errorf("expected owner to be permitted, got: %v", err)
	}
	// 2. Employee with can_manage_inventory=true is permitted
	if err := verifyActorPermission(ctx, pool, "usr_stk_emp_allowed"); err != nil {
		t.Errorf("expected employee with can_manage_inventory to be permitted, got: %v", err)
	}
	// 3. Employee with can_manage_inventory=false is rejected
	if err := verifyActorPermission(ctx, pool, "usr_stk_emp_denied"); err == nil {
		t.Error("expected employee without permission to be rejected, got nil")
	}
	// 4. Inactive employee is rejected even with permission
	if err := verifyActorPermission(ctx, pool, "usr_stk_emp_inactive"); err == nil {
		t.Error("expected inactive employee to be rejected, got nil")
	}
	// 5. Customer is rejected
	if err := verifyActorPermission(ctx, pool, "usr_stk_cust"); err == nil {
		t.Error("expected customer to be rejected, got nil")
	}
	// 6. Unknown actor is rejected
	if err := verifyActorPermission(ctx, pool, "unknown_actor_id"); err == nil {
		t.Error("expected unknown actor to be rejected, got nil")
	}
	// 7. Empty actor is rejected
	if err := verifyActorPermission(ctx, pool, ""); err == nil {
		t.Error("expected empty actor to be rejected, got nil")
	}

	// Test verifyOwnerPermission:
	if err := verifyOwnerPermission(ctx, pool, "usr_stk_owner"); err != nil {
		t.Errorf("expected owner to be permitted for replacement approval, got: %v", err)
	}
	if err := verifyOwnerPermission(ctx, pool, "usr_stk_emp_allowed"); err == nil {
		t.Error("expected employee to be rejected for replacement approval, got nil")
	}
	if err := verifyOwnerPermission(ctx, pool, "unknown_owner_id"); err == nil {
		t.Error("expected unknown owner to be rejected for replacement approval, got nil")
	}
}
