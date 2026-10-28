package migration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCatalogStock(t *testing.T) {
	pool := testPool(t)
	reset(t, pool)
	run(t, pool, "init")
	s := catalog.Store{Pool: pool}
	ctx := context.Background()
	products, err := s.Products(ctx)
	if err != nil || len(products) != 23 {
		t.Fatalf("catalog: %d %v", len(products), err)
	}
	for _, p := range products {
		if p.PriceKes != 2500 || p.Available || p.Slug == "" || p.CategorySlug == "" || len(p.Images) != 1 {
			t.Fatalf("seed: %+v", p)
		}
		path, err := url.PathUnescape(p.Images[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join("../../../frontend", path)); err != nil {
			t.Fatal(err)
		}
	}
	b := catalog.Batch{ID: "count-1", Kind: "opening", Actor: "synthetic-owner", Evidence: "synthetic-count-signoff"}
	for i := 1; i <= 23; i++ {
		b.Lines = append(b.Lines, catalog.Line{ProductID: fmt.Sprintf("p%d", i), Quantity: 2})
	}
	if _, err = s.Apply(ctx, b); err == nil {
		t.Fatal("unapproved authority accepted")
	}
	approve := func() {
		t.Helper()
		if err := s.Approve(ctx, "synthetic-owner", "synthetic-reconciliation", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	approve()
	bad := b
	bad.ID = "bad"
	bad.Lines = append([]catalog.Line(nil), b.Lines...)
	bad.Lines[22].ProductID = "unknown"
	if _, err = s.Apply(ctx, bad); err == nil {
		t.Fatal("unknown mapping accepted")
	}
	var batches int
	if pool.QueryRow(ctx, "SELECT count(*) FROM public.stock_batch").Scan(&batches) != nil || batches != 0 {
		t.Fatal("partial batch persisted")
	}
	if replay, err := s.Apply(ctx, b); err != nil || replay {
		t.Fatalf("opening: %v %v", replay, err)
	}
	// Input order has no semantic effect on idempotency.
	b.Lines[0], b.Lines[22] = b.Lines[22], b.Lines[0]
	if replay, err := s.Apply(ctx, b); err != nil || !replay {
		t.Fatalf("repeat: %v %v", replay, err)
	}
	changed := b
	changed.Lines = append([]catalog.Line(nil), b.Lines...)
	changed.Lines[0].Quantity = 3
	if _, err = s.Apply(ctx, changed); err == nil {
		t.Fatal("changed retry accepted")
	}
	second := b
	second.ID = "count-2"
	if _, err = s.Apply(ctx, second); err == nil {
		t.Fatal("second opening accepted")
	}
	products, _ = s.Products(ctx)
	if products[0].Available {
		t.Fatal("import did not revoke reconciliation")
	}
	approve()
	products, _ = s.Products(ctx)
	if !products[0].Available {
		t.Fatal("approved stock unavailable")
	}
	execSQL(t, pool, "UPDATE public.catalog_settings SET common_price_kes=2700; UPDATE public.catalog_product SET price_override_kes=3000 WHERE id='p1'")
	products, _ = s.Products(ctx)
	if products[0].PriceKes != 3000 || products[1].PriceKes != 2700 {
		t.Fatal("DB price precedence")
	}
	sale := catalog.Batch{ID: "sale-1", Kind: "local_sale", Actor: "synthetic-operator", Evidence: "synthetic-verified-sale", Lines: []catalog.Line{{ProductID: "p1", Quantity: 1}}}
	if _, err = s.Apply(ctx, sale); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.Apply(ctx, sale); err != nil || !replay {
		t.Fatal("sale replay", err)
	}
	products, _ = s.Products(ctx)
	if !products[0].Available {
		t.Fatal("sale revoked valid approval")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := sale
			b.ID = fmt.Sprintf("last-bottle-%d", i)
			_, err := s.Apply(ctx, b)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent oversell: %d succeeded", success)
	}
	var stock, ledger int
	if err := pool.QueryRow(ctx, `SELECT stock,(SELECT sum(delta) FROM public.stock_entry WHERE product_id='p1') FROM public.catalog_product WHERE id='p1'`).Scan(&stock, &ledger); err != nil || stock != 0 || ledger != 0 {
		t.Fatal("ledger mismatch", stock, ledger, err)
	}
	expected := 1
	correction := catalog.Batch{ID: "fix-1", Kind: "adjustment", Actor: "synthetic-owner", Evidence: "synthetic-correction", Lines: []catalog.Line{{ProductID: "p1", Quantity: 1, ExpectedBalance: &expected}}}
	if _, err = s.Apply(ctx, correction); err == nil {
		t.Fatal("stale correction accepted")
	}
	expected = 0
	if _, err = s.Apply(ctx, correction); err != nil {
		t.Fatal(err)
	}
	receipt := catalog.Batch{ID: "receipt-1", Kind: "receipt", Actor: "synthetic-owner", Evidence: "synthetic-delivery", Lines: []catalog.Line{{ProductID: "p2", Quantity: 1}}}
	if _, err = s.Apply(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	recount := catalog.Batch{ID: "recount-1", Kind: "reconcile", Actor: "synthetic-owner", Evidence: "synthetic-physical-recount"}
	for i := 1; i <= 23; i++ {
		expected := 2
		if i == 1 {
			expected = 1
		}
		if i == 2 {
			expected = 3
		}
		recount.Lines = append(recount.Lines, catalog.Line{ProductID: fmt.Sprintf("p%d", i), Quantity: expected, ExpectedBalance: &expected})
	}
	if _, err = s.Apply(ctx, recount); err != nil {
		t.Fatal(err)
	}
	execSQL(t, pool, "UPDATE public.catalog_product SET stock=stock+1 WHERE id='p3'")
	if s.Approve(ctx, "synthetic-owner", "mismatch-must-fail", time.Now().Add(time.Hour)) == nil {
		t.Fatal("ledger mismatch approved")
	}
	execSQL(t, pool, "UPDATE public.catalog_product SET stock=stock-1 WHERE id='p3'")
	approve()
	execSQL(t, pool, "UPDATE public.stock_control SET reconciled_until=now()-interval '1 second'")
	products, _ = s.Products(ctx)
	for _, p := range products {
		if p.Available {
			t.Fatal("expired stock exposed")
		}
	}
	// Simulate upstream/database failure; callers receive no stale products.
	execSQL(t, pool, "ALTER TABLE public.catalog_settings RENAME TO unavailable_settings")
	if products, err = s.Products(ctx); err == nil || products != nil {
		t.Fatal("database failure served fallback catalog")
	}
}

func TestStockOperatorGrants(t *testing.T) {
	pool := testPool(t)
	reset(t, pool)
	run(t, pool, "init")
	role := pgx.Identifier{fmt.Sprintf("r09_operator_%d", time.Now().UnixNano())}.Sanitize()
	execSQL(t, pool, "CREATE ROLE "+role+" NOLOGIN NOSUPERUSER NOINHERIT")
	t.Cleanup(func() { execSQL(t, pool, "DROP OWNED BY "+role); execSQL(t, pool, "DROP ROLE "+role) })
	execSQL(t, pool, `GRANT USAGE ON SCHEMA public TO `+role+`;
 GRANT SELECT ON public.catalog_product,public.stock_control,public.stock_batch,public.stock_entry,public.stock_approval TO `+role+`;
 GRANT UPDATE(stock,opened) ON public.catalog_product TO `+role+`;
 GRANT UPDATE ON public.stock_control TO `+role+`;
 GRANT INSERT ON public.stock_batch,public.stock_entry,public.stock_approval TO `+role)
	cfg := pool.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE "+role); return err }
	operator, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	s := catalog.Store{Pool: operator}
	if err = s.Approve(context.Background(), "synthetic-owner", "synthetic-approval", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	b := catalog.Batch{ID: "operator-opening", Kind: "opening", Actor: "synthetic-owner", Evidence: "synthetic-count"}
	for i := 1; i <= 23; i++ {
		b.Lines = append(b.Lines, catalog.Line{ProductID: fmt.Sprintf("p%d", i), Quantity: 1})
	}
	if _, err = s.Apply(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if _, err = operator.Exec(context.Background(), "UPDATE public.catalog_product SET price_override_kes=1"); err == nil {
		t.Fatal("stock operator changed price")
	}
	if _, err = operator.Exec(context.Background(), "DELETE FROM public.stock_entry"); err == nil {
		t.Fatal("stock operator deleted ledger")
	}
}

func TestCatalogUpgradeFromV1(t *testing.T) {
	pool := testPool(t)
	reset(t, pool)
	if _, err := execute(context.Background(), pool, "init", steps()[:1]); err != nil {
		t.Fatal(err)
	}
	if got := run(t, pool, "status"); got.State != "pending" {
		t.Fatal(got)
	}
	run(t, pool, "up")
	run(t, pool, "up")
	run(t, pool, "status")
	execSQL(t, pool, "DROP TABLE public.stock_entry")
	fails(t, pool, "up", "incomplete version schema")
}
