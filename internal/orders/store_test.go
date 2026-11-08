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
			Delivery: "pickup",
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

	// Verify guest access token was generated and persisted
	if order.GuestToken == "" || !ValidateTokenFormat(order.GuestToken) {
		t.Fatalf("expected valid GuestToken on created order, got %q", order.GuestToken)
	}
	var count int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM public.guest_order_access WHERE order_id = $1 AND token_hash = $2", order.ID, HashToken(order.GuestToken)).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("expected guest_order_access entry, count=%d err=%v", count, err)
	}

	// ValidateGuestAccess with correct token
	ok, err := store.ValidateGuestAccess(ctx, order.ID, order.GuestToken)
	if err != nil || !ok {
		t.Fatalf("expected ValidateGuestAccess with valid token to return true, got ok=%v err=%v", ok, err)
	}

	// ValidateGuestAccess with wrong token
	wrongToken, _, _ := GenerateGuestToken()
	ok, err = store.ValidateGuestAccess(ctx, order.ID, wrongToken)
	if err != nil || ok {
		t.Fatalf("expected ValidateGuestAccess with wrong token to return false, got ok=%v err=%v", ok, err)
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

func TestConfirmPaymentDeliveryQuoteGate(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	productID := "p10"
	setupCatalogFixture(t, pool, productID, 10, true, nil)

	// 1. Create order with delivery (requires accepted quote)
	delivReq := CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Mombasa Client",
			Phone:    "254700112233",
			Location: "Nyali Links, Mombasa",
			Delivery: "delivery",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: productID, Qty: 1},
		},
	}
	order, err := store.CreateOrder(ctx, delivReq)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// 2. Confirm payment before any quote exists: must fail
	err = store.ConfirmPayment(ctx, order.ID, "MPESAGATE1")
	if !errors.Is(err, ErrDeliveryQuoteUnaccepted) {
		t.Fatalf("expected ErrDeliveryQuoteUnaccepted, got %v", err)
	}

	// 3. Create a pending quote
	_, err = pool.Exec(ctx, `
		INSERT INTO public.delivery_quote (order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by)
		VALUES ($1, 1, 400, 'merchant', 'Express Nyali Dispatch', 'pending', now() + interval '1 hour', 'manager-gate')
	`, order.ID)
	if err != nil {
		t.Fatalf("failed to insert pending quote: %v", err)
	}

	// Confirm payment with pending quote: must fail
	err = store.ConfirmPayment(ctx, order.ID, "MPESAGATE1")
	if !errors.Is(err, ErrDeliveryQuoteUnaccepted) {
		t.Fatalf("expected ErrDeliveryQuoteUnaccepted for pending quote, got %v", err)
	}

	// 4. Create an expired accepted quote
	_, err = pool.Exec(ctx, `
		UPDATE public.delivery_quote
		SET status = 'accepted', expires_at = now() - interval '5 minutes', accepted_at = now() - interval '10 minutes'
		WHERE order_id = $1 AND version = 1
	`, order.ID)
	if err != nil {
		t.Fatalf("failed to update quote to expired: %v", err)
	}

	err = store.ConfirmPayment(ctx, order.ID, "MPESAGATE1")
	if !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("expected ErrQuoteExpired for expired quote, got %v", err)
	}

	// 5. Update quote to valid accepted quote (fee_recipient = 'merchant', amount = 400, items = 2500 -> payable total = 2900)
	_, err = pool.Exec(ctx, `
		UPDATE public.delivery_quote
		SET status = 'accepted', expires_at = now() + interval '2 hours', accepted_at = now()
		WHERE order_id = $1 AND version = 1
	`, order.ID)
	if err != nil {
		t.Fatalf("failed to update quote to accepted: %v", err)
	}

	// Underpayment: paying only item subtotal (order.TotalKes) when merchant delivery fee (400) is due must fail
	err = store.ConfirmPayment(ctx, order.ID, "MPESAGATE1", order.TotalKes)
	if !errors.Is(err, ErrPayableMismatch) {
		t.Fatalf("expected ErrPayableMismatch on underpayment, got %v", err)
	}

	// Confirm payment with exact reconciled payable amount (order.TotalKes + 400): must succeed
	err = store.ConfirmPayment(ctx, order.ID, "MPESAGATE1", order.TotalKes+400)
	if err != nil {
		t.Fatalf("expected successful ConfirmPayment with exact reconciled amount, got %v", err)
	}

	// Verify order is PAID
	ord, err := store.GetOrder(ctx, order.ID)
	if err != nil || ord.PaymentStatus != PaymentPaid {
		t.Fatalf("expected order PAID, got %+v (err: %v)", ord, err)
	}
}

func TestIdempotencyLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	setupCatalogFixture(t, pool, "p1", 10, true, nil)
	store := &Store{Pool: pool}

	key := "idemp-key-test-1"
	scope := "guest"
	op := "create_order"
	hash1 := CanonicalRequestHash([]byte(`{"items":[{"id":"p1","qty":1}]}`))
	hash2 := CanonicalRequestHash([]byte(`{"items":[{"id":"p1","qty":2}]}`))

	// 1. Initial BeginIdempotent: should allow new operation
	rec, isReplay, err := store.BeginIdempotent(ctx, key, scope, op, hash1, time.Hour)
	if err != nil || isReplay || rec == nil || rec.Status != IdempotencyInProgress {
		t.Fatalf("expected initial BeginIdempotent to succeed, got rec=%+v, replay=%v, err=%v", rec, isReplay, err)
	}

	// 2. Concurrent request with same key while in_progress: must return ErrIdempotencyInProgress
	_, _, err = store.BeginIdempotent(ctx, key, scope, op, hash1, time.Hour)
	if !errors.Is(err, ErrIdempotencyInProgress) {
		t.Fatalf("expected ErrIdempotencyInProgress for concurrent call, got %v", err)
	}

	// 3. Complete the operation
	err = store.CompleteIdempotent(ctx, key, scope, op, 201, map[string]string{"Content-Type": "application/json"}, `{"orderId":"ord-123"}`, "ord-123")
	if err != nil {
		t.Fatalf("failed to complete idempotency key: %v", err)
	}

	// 4. Identical replay: same hash must return isReplay=true with cached body
	recReplay, isReplay, err := store.BeginIdempotent(ctx, key, scope, op, hash1, time.Hour)
	if err != nil || !isReplay || recReplay.StatusCode != 201 || recReplay.ResponseBody != `{"orderId":"ord-123"}` {
		t.Fatalf("expected successful replay, got rec=%+v, replay=%v, err=%v", recReplay, isReplay, err)
	}

	// 5. Changed payload: different hash must return ErrIdempotencyConflict (HTTP 409)
	_, _, err = store.BeginIdempotent(ctx, key, scope, op, hash2, time.Hour)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict on changed body, got %v", err)
	}

	// 5b. Calling FailIdempotent on an already completed key must NOT downgrade it to failed
	err = store.FailIdempotent(ctx, key, scope, op)
	if err != nil {
		t.Fatalf("FailIdempotent returned error: %v", err)
	}
	recAfterFail, isReplay, err := store.BeginIdempotent(ctx, key, scope, op, hash1, time.Hour)
	if err != nil || !isReplay || recAfterFail.Status != IdempotencyCompleted {
		t.Fatalf("expected completed key to remain completed despite FailIdempotent call, got %+v (replay=%v, err=%v)", recAfterFail, isReplay, err)
	}

	// 6. Failed operation handling: when marked failed, subsequent request with that key can retry
	failKey := "idemp-fail-key"
	_, _, err = store.BeginIdempotent(ctx, failKey, scope, op, hash1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	err = store.FailIdempotent(ctx, failKey, scope, op)
	if err != nil {
		t.Fatal(err)
	}
	retryRec, isReplay, err := store.BeginIdempotent(ctx, failKey, scope, op, hash1, time.Hour)
	if err != nil || isReplay || retryRec.Status != IdempotencyInProgress {
		t.Fatalf("expected failed key to permit retry, got rec=%+v, replay=%v, err=%v", retryRec, isReplay, err)
	}
}

func TestPaymentAttemptLifecycleAndExplicitRetry(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	setupCatalogFixture(t, pool, "p1", 10, true, nil)
	store := &Store{Pool: pool}

	// 1. Create order
	req := CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Bob Test",
			Phone:    "254711223344",
			Location: "Mombasa Nyali",
			Delivery: "pickup",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: "p1", Qty: 1},
		},
	}
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// Verify order has initial attempt #1
	if order.LatestAttempt == nil {
		t.Fatal("expected order to have LatestAttempt populated")
	}
	if order.LatestAttempt.AttemptNumber != 1 || order.LatestAttempt.Status != AttemptCreated {
		t.Fatalf("unexpected initial attempt: %+v", order.LatestAttempt)
	}

	// 2. Query attempts
	attempts, err := store.GetPaymentAttempts(ctx, order.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("expected 1 payment attempt, got %d (err: %v)", len(attempts), err)
	}

	// 2b. Attempt to create a second attempt while attempt 1 is 'created': must fail with ErrUnresolvedPaymentAttempt
	_, err = store.CreatePaymentAttempt(ctx, order.ID, "254711223344", order.TotalKes, "mpesa_stk")
	if !errors.Is(err, ErrUnresolvedPaymentAttempt) {
		t.Fatalf("expected ErrUnresolvedPaymentAttempt while attempt is created, got %v", err)
	}

	// 3. Mark attempt 1 as 'initiating'
	err = store.UpdatePaymentAttempt(ctx, attempts[0].ID, AttemptInitiating, "TX12345", "REF67890", "")
	if err != nil {
		t.Fatalf("failed to update attempt: %v", err)
	}

	// 4. Attempt to create a second attempt while attempt 1 is active (initiating): must fail with ErrUnresolvedPaymentAttempt
	_, err = store.CreatePaymentAttempt(ctx, order.ID, "254711223344", order.TotalKes, "mpesa_stk")
	if !errors.Is(err, ErrUnresolvedPaymentAttempt) {
		t.Fatalf("expected ErrUnresolvedPaymentAttempt while active attempt in-flight, got %v", err)
	}

	// 4b. Mark attempt 1 as 'unknown' (e.g. provider timeout): must also fail with ErrUnresolvedPaymentAttempt
	err = store.UpdatePaymentAttempt(ctx, attempts[0].ID, AttemptUnknown, "TX12345", "REF67890", "Provider timeout")
	if err != nil {
		t.Fatalf("failed to update attempt to unknown: %v", err)
	}
	_, err = store.CreatePaymentAttempt(ctx, order.ID, "254711223344", order.TotalKes, "mpesa_stk")
	if !errors.Is(err, ErrUnresolvedPaymentAttempt) {
		t.Fatalf("expected ErrUnresolvedPaymentAttempt while attempt is unknown, got %v", err)
	}

	// 5. Mark attempt 1 as 'failed'
	err = store.UpdatePaymentAttempt(ctx, attempts[0].ID, AttemptFailed, "", "", "User cancelled on phone")
	if err != nil {
		t.Fatalf("failed to mark attempt failed: %v", err)
	}

	// 6. Explicit retry creates attempt #2
	att2, err := store.CreatePaymentAttempt(ctx, order.ID, "254711223344", order.TotalKes, "mpesa_stk")
	if err != nil {
		t.Fatalf("failed to create retry payment attempt: %v", err)
	}
	if att2.AttemptNumber != 2 || att2.Status != AttemptCreated {
		t.Fatalf("expected attempt #2 created, got %+v", att2)
	}

	// 7. GetLatestPaymentAttempt returns attempt #2
	latest, err := store.GetLatestPaymentAttempt(ctx, order.ID)
	if err != nil || latest.ID != att2.ID || latest.AttemptNumber != 2 {
		t.Fatalf("expected latest attempt to be #2, got %+v (err: %v)", latest, err)
	}

	// 8. Confirm payment for order
	err = store.ConfirmPayment(ctx, order.ID, "MPESARECEIPT99", order.TotalKes)
	if err != nil {
		t.Fatalf("failed to confirm payment: %v", err)
	}

	// Verify that latest attempt was updated to SUCCEEDED with provider transaction ID
	latestAfterPaid, err := store.GetLatestPaymentAttempt(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to get latest attempt after payment: %v", err)
	}
	if latestAfterPaid.Status != AttemptSucceeded || latestAfterPaid.ProviderTransactionID != "MPESARECEIPT99" {
		t.Fatalf("expected latest attempt SUCCEEDED with receipt MPESARECEIPT99, got %+v", latestAfterPaid)
	}

	// 9. Retry on PAID order must fail with ErrOrderAlreadyPaid
	_, err = store.CreatePaymentAttempt(ctx, order.ID, "254711223344", order.TotalKes, "mpesa_stk")
	if !errors.Is(err, ErrOrderAlreadyPaid) {
		t.Fatalf("expected ErrOrderAlreadyPaid on paid order, got %v", err)
	}
}

func TestFindPaymentAttemptHelpers(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	setupCatalogFixture(t, pool, "p1", 10, true, nil)
	store := &Store{Pool: pool}

	req := CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Alice Helper",
			Phone:    "254700112233",
			Location: "Westlands",
			Delivery: "pickup",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: "p1", Qty: 1},
		},
	}
	order, err := store.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// 1. Initial attempt is 'created'. FindInFlightPaymentAttempt finds 'created', 'initiating', 'accepted', 'unknown'
	inFlight, err := store.FindInFlightPaymentAttempt(ctx, "254700112233", order.TotalKes, 10*time.Minute)
	if err != nil || inFlight == nil || inFlight.ID != order.LatestAttempt.ID {
		t.Fatalf("expected in-flight attempt found for created status, got %+v (err: %v)", inFlight, err)
	}

	// 2. Update attempt to initiating with provider tx & ref IDs
	err = store.UpdatePaymentAttempt(ctx, order.LatestAttempt.ID, AttemptInitiating, "NCBA_TX_999", "NCBA_REF_888", "")
	if err != nil {
		t.Fatalf("failed to update attempt: %v", err)
	}

	// Now in-flight attempt should still be found
	inFlight, err = store.FindInFlightPaymentAttempt(ctx, "254700112233", order.TotalKes, 10*time.Minute)
	if err != nil || inFlight == nil || inFlight.ID != order.LatestAttempt.ID {
		t.Fatalf("expected in-flight attempt %+v, got %+v (err: %v)", order.LatestAttempt, inFlight, err)
	}

	// 3. FindPaymentAttemptByProviderTx by transaction ID and reference ID
	byTx, err := store.FindPaymentAttemptByProviderTx(ctx, "NCBA_TX_999")
	if err != nil || byTx == nil || byTx.ID != order.LatestAttempt.ID {
		t.Fatalf("expected attempt found by provider tx ID, got %+v (err: %v)", byTx, err)
	}
	byRef, err := store.FindPaymentAttemptByProviderTx(ctx, "NCBA_REF_888")
	if err != nil || byRef == nil || byRef.ID != order.LatestAttempt.ID {
		t.Fatalf("expected attempt found by provider ref ID, got %+v (err: %v)", byRef, err)
	}
	nonExistent, err := store.FindPaymentAttemptByProviderTx(ctx, "DOES_NOT_EXIST")
	if !errors.Is(err, ErrAttemptNotFound) || nonExistent != nil {
		t.Fatalf("expected ErrAttemptNotFound for non-existent provider tx, got %+v (err: %v)", nonExistent, err)
	}

	// 4. FindReceiptOrder should be empty string before confirmation
	receiptOrder, err := store.FindReceiptOrder(ctx, "NCBA_RECEIPT_777")
	if err != nil || receiptOrder != "" {
		t.Fatalf("expected empty receipt order, got %q (err: %v)", receiptOrder, err)
	}

	// 5. Confirm payment with receipt
	err = store.ConfirmPayment(ctx, order.ID, "NCBA_RECEIPT_777", order.TotalKes)
	if err != nil {
		t.Fatalf("failed to confirm payment: %v", err)
	}

	// FindReceiptOrder should now return the order ID
	receiptOrder, err = store.FindReceiptOrder(ctx, "NCBA_RECEIPT_777")
	if err != nil || receiptOrder != order.ID {
		t.Fatalf("expected receipt order ID %q, got %q (err: %v)", order.ID, receiptOrder, err)
	}
}
