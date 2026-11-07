package orders

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping database test; set GO_TEST_DATABASE_URL to disposable test postgres")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := database.Open(ctx, dsn, 20, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// Clean and run migrations up to latest version
	_, err = pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
	if err != nil {
		t.Fatal(err)
	}
	_, err = migration.Run(ctx, pool, "init", 15*time.Second)
	if err != nil {
		t.Fatalf("migration init failed: %v", err)
	}

	return pool
}

func setupCatalogFixture(t *testing.T, pool *pgxpool.Pool, productID string, stock int, opened bool, priceOverride *int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ensure stock_control authority
	_, err := pool.Exec(ctx, `UPDATE public.stock_control 
		SET authority = 'replacement', reconciled_until = now() + interval '1 hour'
		WHERE singleton = true`)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure common price
	_, err = pool.Exec(ctx, `UPDATE public.catalog_settings 
		SET common_price_kes = 2500
		WHERE singleton = true`)
	if err != nil {
		t.Fatal(err)
	}

	// Update test product (already seeded as p1..p23)
	_, err = pool.Exec(ctx, `UPDATE public.catalog_product
		SET stock = $2, opened = $3, price_override_kes = $4
		WHERE id = $1`,
		productID, stock, opened, priceOverride)
	if err != nil {
		t.Fatal(err)
	}

	// Insert or update stock_batch and stock_entry so ledger matches catalog_product.stock
	batchID := "init_batch_" + productID
	_, _ = pool.Exec(ctx, `INSERT INTO public.stock_batch (id, digest, kind, actor, evidence)
		VALUES ($1, 'digest', 'opening', 'tester', 'test-fixture')
		ON CONFLICT (id) DO NOTHING`, batchID)
	_, _ = pool.Exec(ctx, `INSERT INTO public.stock_entry (batch_id, product_id, delta, balance)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (batch_id, product_id) DO UPDATE SET delta = $3, balance = $3`, batchID, productID, stock)
}

func validReq(productID string, qty int) CreateOrderRequest {
	return CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Alice Wanjiku",
			Phone:    "254712345678",
			Location: "Nairobi CBD",
			Delivery: "delivery",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: productID, Qty: qty},
		},
	}
}

func TestCreateOrderAndPriceSnapshot(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	catStore := &catalog.Store{Pool: pool}
	ctx := context.Background()

	setupCatalogFixture(t, pool, "p1", 10, true, nil)

	// Create order at common price 2500
	req := validReq("p1", 2)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	if order.TotalKes != 5000 {
		t.Fatalf("expected totalKes 5000, got %d", order.TotalKes)
	}
	if order.PaymentStatus != PaymentPending {
		t.Fatalf("expected status %s, got %s", PaymentPending, order.PaymentStatus)
	}
	if order.FulfillmentStatus != FulfillmentUnfulfilled {
		t.Fatalf("expected fulfillment %s, got %s", FulfillmentUnfulfilled, order.FulfillmentStatus)
	}
	if order.Reservation == nil || order.Reservation.Status != ReservationActive {
		t.Fatalf("expected active reservation, got %+v", order.Reservation)
	}
	if len(order.Items) != 1 || order.Items[0].UnitPriceKes != 2500 || order.Items[0].SubtotalKes != 5000 {
		t.Fatalf("invalid snapshotted items: %+v", order.Items)
	}

	// Verify on-hand stock is preserved while reservation is active (maintaining ledger consistency)
	var onHandStock int
	err = pool.QueryRow(ctx, "SELECT stock FROM public.catalog_product WHERE id = $1", "p1").Scan(&onHandStock)
	if err != nil || onHandStock != 10 {
		t.Fatalf("expected on-hand stock 10, got %d (err: %v)", onHandStock, err)
	}

	// Verify available stock reflects active reservation hold (10 - 2 = 8)
	avail, err := store.GetAvailableStock(ctx, "p1")
	if err != nil || avail != 8 {
		t.Fatalf("expected available stock 8, got %d (err: %v)", avail, err)
	}

	// Verify catalog store approval succeeds while reservation is active (proving no ledger mismatch)
	err = catStore.Approve(ctx, "owner", "evidence", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("catalog Approve failed during active reservation: %v", err)
	}

	// Change catalog settings common price to 3500
	_, err = pool.Exec(ctx, "UPDATE public.catalog_settings SET common_price_kes = 3500 WHERE singleton = true")
	if err != nil {
		t.Fatal(err)
	}

	// Retrieve order from DB and assert historical snapshot survived unchanged
	retrieved, err := store.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("GetOrder failed: %v", err)
	}
	if retrieved.TotalKes != 5000 {
		t.Fatalf("expected historical order totalKes 5000 after catalog price hike, got %d", retrieved.TotalKes)
	}
	if len(retrieved.Items) != 1 || retrieved.Items[0].UnitPriceKes != 2500 || retrieved.Items[0].SubtotalKes != 5000 {
		t.Fatalf("historical item prices were mutated: %+v", retrieved.Items)
	}
}

func TestConcurrentLastItemCannotOversell(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	productID := "p2"
	setupCatalogFixture(t, pool, productID, 1, true, nil)

	concurrency := 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	var successCount int
	var failCount int
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			req := validReq(productID, 1)
			req.Customer.Phone = "2547120000" + string(rune('0'+idx))
			_, err := store.CreateOrder(ctx, req)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				if errors.Is(err, ErrInsufficientStock) {
					failCount++
				} else {
					t.Errorf("unexpected error on concurrent checkout: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful checkout for last item, got %d", successCount)
	}
	if failCount != concurrency-1 {
		t.Fatalf("expected %d checkouts to fail with insufficient stock, got %d", concurrency-1, failCount)
	}

	// Available stock must be exactly 0, never negative
	avail, err := store.GetAvailableStock(ctx, productID)
	if err != nil || avail != 0 {
		t.Fatalf("expected available stock 0, got %d (err: %v)", avail, err)
	}

	// Active reservations count must be exactly 1
	var activeReservations int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM public.stock_reservation r
		JOIN public.stock_reservation_item ri ON ri.reservation_id = r.id
		WHERE ri.product_id = $1 AND r.status = 'active'`, productID).Scan(&activeReservations)
	if err != nil || activeReservations != 1 {
		t.Fatalf("expected 1 active reservation, got %d (err: %v)", activeReservations, err)
	}
}

func TestIdempotentExpiryRelease(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	productID := "p3"
	setupCatalogFixture(t, pool, productID, 5, true, nil)

	req := validReq(productID, 2)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Check available stock decreased from 5 to 3
	availAfterOrder, err := store.GetAvailableStock(ctx, productID)
	if err != nil || availAfterOrder != 3 {
		t.Fatalf("expected available stock 3 after reservation, got %d", availAfterOrder)
	}

	// Backdate reservation expires_at to 10 minutes ago
	past := time.Now().UTC().Add(-10 * time.Minute)
	_, err = pool.Exec(ctx, "UPDATE public.stock_reservation SET expires_at = $1 WHERE id = $2", past, order.Reservation.ID)
	if err != nil {
		t.Fatal(err)
	}

	// First release: must release exactly 1
	released, err := store.ReleaseExpired(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReleaseExpired failed: %v", err)
	}
	if released != 1 {
		t.Fatalf("expected 1 released reservation, got %d", released)
	}

	// Available stock must be restored to 5
	restoredAvail, err := store.GetAvailableStock(ctx, productID)
	if err != nil || restoredAvail != 5 {
		t.Fatalf("expected restored available stock 5, got %d", restoredAvail)
	}

	// Order status must be EXPIRED and fulfillment cancelled
	retrieved, err := store.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.PaymentStatus != PaymentExpired {
		t.Fatalf("expected order paymentStatus %s, got %s", PaymentExpired, retrieved.PaymentStatus)
	}
	if retrieved.FulfillmentStatus != FulfillmentCancelled {
		t.Fatalf("expected fulfillmentStatus %s, got %s", FulfillmentCancelled, retrieved.FulfillmentStatus)
	}

	// Second release: idempotent, must release 0 and NOT double-restore stock
	releasedAgain, err := store.ReleaseExpired(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReleaseExpired rerun failed: %v", err)
	}
	if releasedAgain != 0 {
		t.Fatalf("expected 0 released on rerun, got %d", releasedAgain)
	}

	availAfterRerun, err := store.GetAvailableStock(ctx, productID)
	if err != nil || availAfterRerun != 5 {
		t.Fatalf("stock was double-restored! Expected 5, got %d", availAfterRerun)
	}
}

func TestCancelOrder(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	productID := "p4"
	setupCatalogFixture(t, pool, productID, 4, true, nil)

	req := validReq(productID, 3)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Available stock should be 1
	avail, err := store.GetAvailableStock(ctx, productID)
	if err != nil || avail != 1 {
		t.Fatalf("expected available stock 1, got %d", avail)
	}

	// Cancel the order
	if err := store.CancelOrder(ctx, order.ID); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}

	// Verify available stock restored
	restoredAvail, err := store.GetAvailableStock(ctx, productID)
	if err != nil || restoredAvail != 4 {
		t.Fatalf("expected available stock restored to 4, got %d", restoredAvail)
	}

	// Verify status
	retrieved, err := store.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.PaymentStatus != PaymentFailed {
		t.Fatalf("expected paymentStatus %s, got %s", PaymentFailed, retrieved.PaymentStatus)
	}
	if retrieved.FulfillmentStatus != FulfillmentCancelled {
		t.Fatalf("expected fulfillmentStatus %s, got %s", FulfillmentCancelled, retrieved.FulfillmentStatus)
	}

	// Second cancel fails because not pending
	err = store.CancelOrder(ctx, order.ID)
	if !errors.Is(err, ErrOrderNotPending) {
		t.Fatalf("expected ErrOrderNotPending on repeated cancel, got %v", err)
	}
}

func TestLatePaymentExplicitResolution(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	productID := "p5"
	setupCatalogFixture(t, pool, productID, 2, true, nil)

	req := validReq(productID, 2)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Expire the reservation
	past := time.Now().UTC().Add(-10 * time.Minute)
	_, _ = pool.Exec(ctx, "UPDATE public.stock_reservation SET expires_at = $1 WHERE id = $2", past, order.Reservation.ID)
	_, _ = store.ReleaseExpired(ctx, time.Now().UTC())

	// Another customer buys the restocked product!
	otherReq := validReq(productID, 2)
	_, err = store.CreateOrder(ctx, otherReq)
	if err != nil {
		t.Fatalf("second customer checkout failed: %v", err)
	}

	// Now late payment arrives for the first order
	receiptNo := "TESTLATE999"
	err = store.ConfirmPayment(ctx, order.ID, receiptNo)
	if !errors.Is(err, ErrLatePaymentResolution) {
		t.Fatalf("expected ErrLatePaymentResolution, got %v", err)
	}

	// Order must be in RESOLUTION_REQUIRED, NOT fulfilled silently!
	retrieved, err := store.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.PaymentStatus != PaymentResolutionRequired {
		t.Fatalf("expected paymentStatus %s, got %s", PaymentResolutionRequired, retrieved.PaymentStatus)
	}
	if retrieved.FulfillmentStatus != FulfillmentCancelled {
		t.Fatalf("expected fulfillmentStatus %s, got %s", FulfillmentCancelled, retrieved.FulfillmentStatus)
	}

	// Payment status polling must reflect RESOLUTION_REQUIRED and receipt
	statusResp, err := store.GetPaymentStatus(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if statusResp.PaymentStatus != PaymentResolutionRequired || statusResp.ReceiptNumber != receiptNo {
		t.Fatalf("unexpected payment status response: %+v", statusResp)
	}
}

func TestPromptPaymentConfirmation(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	catStore := &catalog.Store{Pool: pool}
	ctx := context.Background()

	productID := "p6"
	setupCatalogFixture(t, pool, productID, 3, true, nil)

	req := validReq(productID, 1)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	receiptNo := "MPESAOK123"
	if err := store.ConfirmPayment(ctx, order.ID, receiptNo); err != nil {
		t.Fatalf("ConfirmPayment failed: %v", err)
	}

	// Verify order PAID
	retrieved, err := store.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.PaymentStatus != PaymentPaid {
		t.Fatalf("expected status %s, got %s", PaymentPaid, retrieved.PaymentStatus)
	}
	if retrieved.Reservation.Status != ReservationCommitted {
		t.Fatalf("expected reservation status %s, got %s", ReservationCommitted, retrieved.Reservation.Status)
	}

	// Verify MpesaTransaction recorded
	var mpesaStatus, storedReceipt string
	err = pool.QueryRow(ctx, `SELECT status, "mpesaReceiptNumber" FROM public."MpesaTransaction" WHERE "orderId" = $1`, order.ID).Scan(&mpesaStatus, &storedReceipt)
	if err != nil || mpesaStatus != "SUCCESS" || storedReceipt != receiptNo {
		t.Fatalf("unexpected MpesaTransaction record: %s %s (err: %v)", mpesaStatus, storedReceipt, err)
	}

	// Verify stock was decremented in catalog_product and recorded in stock_entry ledger
	var stockInCatalog int
	err = pool.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id = $1`, productID).Scan(&stockInCatalog)
	if err != nil || stockInCatalog != 2 {
		t.Fatalf("expected catalog stock decremented to 2, got %d (err: %v)", stockInCatalog, err)
	}

	var ledgerDeltaSum int
	err = pool.QueryRow(ctx, `SELECT coalesce(sum(delta), 0) FROM public.stock_entry WHERE product_id = $1`, productID).Scan(&ledgerDeltaSum)
	if err != nil || ledgerDeltaSum != 2 {
		t.Fatalf("expected ledger delta sum 2, got %d (err: %v)", ledgerDeltaSum, err)
	}

	// Verify catalog Approve passes cleanly after sale (ledger matches stock)
	if err := catStore.Approve(ctx, "owner", "evidence", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("catalog Approve rejected after payment confirmation: %v", err)
	}

	// Duplicate payment confirmation is idempotent
	if err := store.ConfirmPayment(ctx, order.ID, receiptNo); err != nil {
		t.Fatalf("duplicate ConfirmPayment failed: %v", err)
	}
}

func TestListRecent(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	setupCatalogFixture(t, pool, "p1", 10, true, nil)
	setupCatalogFixture(t, pool, "p2", 10, true, nil)

	_, err := store.CreateOrder(ctx, validReq("p1", 1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateOrder(ctx, validReq("p2", 2))
	if err != nil {
		t.Fatal(err)
	}

	recent, err := store.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent failed: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent orders, got %d", len(recent))
	}
	if len(recent[0].Items) != 1 || recent[0].Items[0].Name == "" {
		t.Fatalf("expected item name populated, got %+v", recent[0].Items)
	}
}

func TestLatePaymentBeforeWorkerSweep(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	productID := "p7"
	setupCatalogFixture(t, pool, productID, 2, true, nil)

	req := validReq(productID, 2)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Backdate expires_at to 5 seconds ago, but DO NOT run ReleaseExpired
	past := time.Now().UTC().Add(-5 * time.Second)
	_, err = pool.Exec(ctx, "UPDATE public.stock_reservation SET expires_at = $1 WHERE id = $2", past, order.Reservation.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Attempt payment confirmation: deadline check must detect hold expiration and reject silent fulfillment
	receiptNo := "LATEBEFORESWEEP"
	err = store.ConfirmPayment(ctx, order.ID, receiptNo)
	if !errors.Is(err, ErrLatePaymentResolution) {
		t.Fatalf("expected ErrLatePaymentResolution when expires_at has passed before worker sweep, got: %v", err)
	}

	// Order must be marked RESOLUTION_REQUIRED
	retrieved, err := store.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.PaymentStatus != PaymentResolutionRequired {
		t.Fatalf("expected status %s, got %s", PaymentResolutionRequired, retrieved.PaymentStatus)
	}
	if retrieved.Reservation.Status != ReservationReleased {
		t.Fatalf("expected reservation released, got %s", retrieved.Reservation.Status)
	}
}

func TestActiveReservationPreventsOperatorSale(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	catStore := &catalog.Store{Pool: pool}
	ctx := context.Background()

	productID := "p8"
	setupCatalogFixture(t, pool, productID, 2, true, nil)

	// Online customer reserves all 2 units
	req := validReq(productID, 2)
	_, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Operator tries to apply offline local sale for 1 unit
	saleBatch := catalog.Batch{
		ID:       "operator_oversell_attempt",
		Kind:     "local_sale",
		Actor:    "operator",
		Evidence: "counter-sale-1",
		Lines: []catalog.Line{
			{ProductID: productID, Quantity: 1},
		},
	}
	_, err = catStore.Apply(ctx, saleBatch)
	if err == nil {
		t.Fatal("expected operator sale to fail when stock is held by active reservation, but it succeeded")
	}
	if !strings.Contains(err.Error(), "insufficient unreserved stock") {
		t.Fatalf("expected insufficient unreserved stock error, got: %v", err)
	}
}

func TestActiveReservationReflectedInCatalogAvailability(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	catStore := &catalog.Store{Pool: pool}
	ctx := context.Background()

	productID := "p9"
	setupCatalogFixture(t, pool, productID, 1, true, nil)

	// Before reservation: catalog reports product as available
	products, err := catStore.Products(ctx)
	if err != nil {
		t.Fatalf("Products failed: %v", err)
	}
	var p9Available bool
	for _, p := range products {
		if p.ID == productID {
			p9Available = p.Available
			break
		}
	}
	if !p9Available {
		t.Fatalf("expected product %s to be available initially", productID)
	}

	// Customer reserves the single unit
	req := validReq(productID, 1)
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// After reservation: catalog must report product as unavailable
	products, err = catStore.Products(ctx)
	if err != nil {
		t.Fatalf("Products failed: %v", err)
	}
	for _, p := range products {
		if p.ID == productID {
			p9Available = p.Available
			break
		}
	}
	if p9Available {
		t.Fatalf("expected product %s to be unavailable when all stock is held by active reservation", productID)
	}

	// Release reservation: catalog must report product as available again
	if err := store.CancelOrder(ctx, order.ID); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}

	products, err = catStore.Products(ctx)
	if err != nil {
		t.Fatalf("Products failed: %v", err)
	}
	for _, p := range products {
		if p.ID == productID {
			p9Available = p.Available
			break
		}
	}
	if !p9Available {
		t.Fatalf("expected product %s to become available again after reservation cancellation", productID)
	}
}
