package delivery

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCalculatePayable(t *testing.T) {
	now := time.Now()
	future := now.Add(24 * time.Hour)
	past := now.Add(-1 * time.Hour)

	t.Run("pickup requires no quote", func(t *testing.T) {
		breakdown, err := CalculatePayable(4500, nil, true)
		if err != nil {
			t.Fatalf("expected no error for pickup, got %v", err)
		}
		if breakdown.PayableMerchantKES != 4500 {
			t.Errorf("expected payable merchant 4500, got %d", breakdown.PayableMerchantKES)
		}
		if breakdown.CustomerDirectKES != 0 {
			t.Errorf("expected customer direct 0, got %d", breakdown.CustomerDirectKES)
		}
		if breakdown.TransportFeeKES != 0 {
			t.Errorf("expected transport fee 0, got %d", breakdown.TransportFeeKES)
		}
	})

	t.Run("delivery requires quote", func(t *testing.T) {
		_, err := CalculatePayable(4500, nil, false)
		if err == nil {
			t.Fatal("expected error when delivery has no quote")
		}
	})

	t.Run("unaccepted quote rejected", func(t *testing.T) {
		quote := &DeliveryQuote{
			AmountKES:    400,
			FeeRecipient: FeeRecipientMerchant,
			Status:       QuoteStatusPending,
			ExpiresAt:    future,
		}
		_, err := CalculatePayable(4500, quote, false)
		if err == nil {
			t.Fatal("expected error for pending quote")
		}
	})

	t.Run("expired accepted quote rejected", func(t *testing.T) {
		quote := &DeliveryQuote{
			AmountKES:    400,
			FeeRecipient: FeeRecipientMerchant,
			Status:       QuoteStatusAccepted,
			ExpiresAt:    past,
		}
		_, err := CalculatePayable(4500, quote, false)
		if err == nil {
			t.Fatal("expected error for expired quote")
		}
	})

	t.Run("courier fee excluded from merchant collection", func(t *testing.T) {
		quote := &DeliveryQuote{
			AmountKES:    350,
			FeeRecipient: FeeRecipientCourier,
			Status:       QuoteStatusAccepted,
			ExpiresAt:    future,
			Version:      2,
		}
		breakdown, err := CalculatePayable(6000, quote, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if breakdown.ItemsSubtotalKES != 6000 {
			t.Errorf("expected items subtotal 6000, got %d", breakdown.ItemsSubtotalKES)
		}
		if breakdown.TransportFeeKES != 350 {
			t.Errorf("expected transport fee 350, got %d", breakdown.TransportFeeKES)
		}
		if breakdown.FeeRecipient != FeeRecipientCourier {
			t.Errorf("expected fee recipient courier, got %s", breakdown.FeeRecipient)
		}
		// Customer pays only items to merchant (M-Pesa STK push)
		if breakdown.PayableMerchantKES != 6000 {
			t.Errorf("expected payable merchant 6000, got %d", breakdown.PayableMerchantKES)
		}
		// Customer pays rider directly on arrival
		if breakdown.CustomerDirectKES != 350 {
			t.Errorf("expected customer direct 350, got %d", breakdown.CustomerDirectKES)
		}
	})

	t.Run("merchant transport fee collected upfront", func(t *testing.T) {
		quote := &DeliveryQuote{
			AmountKES:    500,
			FeeRecipient: FeeRecipientMerchant,
			Status:       QuoteStatusAccepted,
			ExpiresAt:    future,
			Version:      1,
		}
		breakdown, err := CalculatePayable(6000, quote, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if breakdown.ItemsSubtotalKES != 6000 {
			t.Errorf("expected items subtotal 6000, got %d", breakdown.ItemsSubtotalKES)
		}
		if breakdown.TransportFeeKES != 500 {
			t.Errorf("expected transport fee 500, got %d", breakdown.TransportFeeKES)
		}
		if breakdown.FeeRecipient != FeeRecipientMerchant {
			t.Errorf("expected fee recipient merchant, got %s", breakdown.FeeRecipient)
		}
		// Merchant collects items + transport
		if breakdown.PayableMerchantKES != 6500 {
			t.Errorf("expected payable merchant 6500, got %d", breakdown.PayableMerchantKES)
		}
		if breakdown.CustomerDirectKES != 0 {
			t.Errorf("expected customer direct 0, got %d", breakdown.CustomerDirectKES)
		}
	})
}

func TestSanitizedPublicQuote(t *testing.T) {
	now := time.Now()
	quote := &DeliveryQuote{
		ID:            "secret-uuid",
		OrderID:       "ord-123",
		Version:       3,
		AmountKES:     400,
		FeeRecipient:  FeeRecipientMerchant,
		Arrangement:   "Mombasa delivery to Nyali",
		Status:        QuoteStatusAccepted,
		ExpiresAt:     now.Add(2 * time.Hour),
		CreatedAt:     now,
		AcceptedAt:    &now,
		CreatedBy:     "manager-user-id-internal",
		CustomerNotes: "Gate 4, call upon arrival",
	}

	pub := quote.ToPublic()
	if pub.OrderID != "ord-123" || pub.Version != 3 || pub.AmountKES != 400 {
		t.Errorf("unexpected public fields: %+v", pub)
	}
	if pub.CustomerNotes != "Gate 4, call upon arrival" {
		t.Errorf("unexpected customer notes: %s", pub.CustomerNotes)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping database test: GO_TEST_DATABASE_URL not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := migration.Run(ctx, pool, "status", 5*time.Second)
	if err != nil || res.State == "empty" {
		_, _ = pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
		if _, err := migration.Run(ctx, pool, "init", 15*time.Second); err != nil {
			t.Fatalf("migration init failed: %v", err)
		}
	} else if res.State == "legacy" || res.State == "pending" {
		if _, err := migration.Run(ctx, pool, "up", 15*time.Second); err != nil {
			t.Fatalf("migration up failed: %v", err)
		}
	}
	return pool
}

func TestDeliveryQuoteStore(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)

	// Seed test order
	orderID := "test-delivery-order-1"
	_, _ = pool.Exec(ctx, `DELETE FROM public."Order" WHERE id = $1`, orderID)
	_, err := pool.Exec(ctx, `
		INSERT INTO public."Order" (id, "updatedAt", "customerName", "customerPhone", "deliveryLocation", "totalKes", items, "paymentStatus")
		VALUES ($1, now(), 'Amina Customer', '254712345678', 'Nyali, Mombasa', 5000, '[]', 'PENDING')
	`, orderID)
	if err != nil {
		t.Fatalf("failed to seed test order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public."Order" WHERE id = $1`, orderID)
	})

	// 1. Create first quote (v1)
	q1, err := store.CreateQuote(ctx, orderID, CreateQuoteParams{
		AmountKES:     350,
		FeeRecipient:  FeeRecipientCourier,
		Arrangement:   "Direct dispatch to Nyali residence",
		ExpiresAt:     time.Now().Add(1 * time.Hour),
		CreatedBy:     "manager-1",
		CustomerNotes: "Call when passing lights",
	})
	if err != nil {
		t.Fatalf("failed to create quote: %v", err)
	}
	if q1.Version != 1 || q1.Status != QuoteStatusPending || q1.AmountKES != 350 {
		t.Fatalf("unexpected quote v1: %+v", q1)
	}

	// 2. Fetch active quote
	active, err := store.GetActiveQuote(ctx, orderID)
	if err != nil {
		t.Fatalf("failed to get active quote: %v", err)
	}
	if active.ID != q1.ID || active.Version != 1 {
		t.Fatalf("unexpected active quote: %+v", active)
	}

	// 3. Create second quote (re-quote -> v2)
	q2, err := store.CreateQuote(ctx, orderID, CreateQuoteParams{
		AmountKES:     400,
		FeeRecipient:  FeeRecipientMerchant,
		Arrangement:   "Express rider with tracking",
		ExpiresAt:     time.Now().Add(2 * time.Hour),
		CreatedBy:     "manager-2",
		CustomerNotes: "Customer requested expedited service",
	})
	if err != nil {
		t.Fatalf("failed to create quote v2: %v", err)
	}
	if q2.Version != 2 || q2.Status != QuoteStatusPending || q2.AmountKES != 400 {
		t.Fatalf("unexpected quote v2: %+v", q2)
	}

	// Active quote is now v2
	active2, err := store.GetActiveQuote(ctx, orderID)
	if err != nil {
		t.Fatalf("failed to get active quote: %v", err)
	}
	if active2.Version != 2 {
		t.Fatalf("expected active quote version 2, got %d", active2.Version)
	}

	// 3b. Accepting or rejecting superseded version 1 must return ErrQuoteConflict
	_, err = store.AcceptQuote(ctx, orderID, 1)
	if err == nil || !errors.Is(err, ErrQuoteConflict) {
		t.Fatalf("expected ErrQuoteConflict when accepting superseded quote v1, got %v", err)
	}
	_, err = store.RejectQuote(ctx, orderID, 1, "superseded test")
	if err == nil || !errors.Is(err, ErrQuoteConflict) {
		t.Fatalf("expected ErrQuoteConflict when rejecting superseded quote v1, got %v", err)
	}

	// 4. Accept quote v2
	accepted, err := store.AcceptQuote(ctx, orderID, 2)
	if err != nil {
		t.Fatalf("failed to accept quote: %v", err)
	}
	if accepted.Status != QuoteStatusAccepted || accepted.AcceptedAt == nil {
		t.Fatalf("expected accepted status with timestamp, got %+v", accepted)
	}

	// 5. Idempotent acceptance
	acceptedAgain, err := store.AcceptQuote(ctx, orderID, 2)
	if err != nil {
		t.Fatalf("idempotent accept failed: %v", err)
	}
	if acceptedAgain.Status != QuoteStatusAccepted {
		t.Fatalf("expected accepted status, got %+v", acceptedAgain)
	}

	// 6. Expired quote rejection on accept
	orderIDExpired := "test-delivery-order-expired"
	_, _ = pool.Exec(ctx, `DELETE FROM public."Order" WHERE id = $1`, orderIDExpired)
	_, err = pool.Exec(ctx, `
		INSERT INTO public."Order" (id, "updatedAt", "customerName", "customerPhone", "deliveryLocation", "totalKes", items, "paymentStatus")
		VALUES ($1, now(), 'Fatuma Customer', '254712345679', 'Bamburi, Mombasa', 3000, '[]', 'PENDING')
	`, orderIDExpired)
	if err != nil {
		t.Fatalf("failed to seed test order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public."Order" WHERE id = $1`, orderIDExpired)
	})

	// Seed directly an expired quote
	_, err = pool.Exec(ctx, `
		INSERT INTO public.delivery_quote (order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by)
		VALUES ($1, 1, 300, 'merchant', 'Standard delivery', 'pending', now() - interval '1 hour', 'mgr')
	`, orderIDExpired)
	if err != nil {
		t.Fatalf("failed to seed expired quote: %v", err)
	}

	_, err = store.AcceptQuote(ctx, orderIDExpired, 1)
	if err != ErrQuoteExpired {
		t.Fatalf("expected ErrQuoteExpired, got %v", err)
	}

	// 7. ExpireQuotes sweep: seed another pending quote that has expired
	_, err = pool.Exec(ctx, `
		INSERT INTO public.delivery_quote (order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by)
		VALUES ($1, 2, 350, 'merchant', 'Express delivery', 'pending', now() - interval '30 minutes', 'mgr')
	`, orderIDExpired)
	if err != nil {
		t.Fatalf("failed to seed pending expired quote: %v", err)
	}

	expiredCount, err := store.ExpireQuotes(ctx, time.Now())
	if err != nil {
		t.Fatalf("failed to run ExpireQuotes: %v", err)
	}
	if expiredCount < 1 {
		t.Errorf("expected at least 1 expired quote swept, got %d", expiredCount)
	}
}
