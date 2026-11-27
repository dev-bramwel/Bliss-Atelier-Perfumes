package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/outbox"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/reports"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
)

func testDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GO_TEST_DATABASE_URL not set; skipping database integration test")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := migration.Run(ctx, pool, "init", 15*time.Second); err != nil {
		t.Fatalf("migrate init: %v", err)
	}
	return pool
}

func TestAnalyticsPackage_CannotInitiatePayments(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "/internal/provider") || strings.Contains(path, "/internal/orders") || strings.Contains(path, "/internal/httpapi") {
				t.Fatalf("read-model package analytics must not import payment/order initiation package %q", path)
			}
		}
	}
}

func TestAnalyticsStore_OwnerAuthorizationRequired(t *testing.T) {
	s := NewStore(nil, "")
	f, err := reports.ParseFilter("", "", "Africa/Nairobi", 50, 0)
	if err != nil {
		t.Fatalf("parse filter: %v", err)
	}
	for _, actor := range []*auth.User{nil, {ID: "u1", Role: auth.RoleEmployee}} {
		if _, err := s.SalesSummary(context.Background(), actor, f); err != auth.ErrPermissionDenied {
			t.Fatalf("expected ErrPermissionDenied for actor %+v, got %v", actor, err)
		}
	}
}

func TestAnalyticsStore_DeduplicationReorderingAndCrashSafety(t *testing.T) {
	pool := testDBPool(t)
	ctx := context.Background()
	store := NewStore(pool, "test-group-dedup")
	if err := store.ResetForReplay(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Reordered: order.paid (v2) arrives BEFORE order.created (v1)
	paidEnv, err := events.NewEvent(events.TypeAnalyticsOrderPaid, "ord-reorder-1", 2, "corr-1", events.AnalyticsOrderPaidPayload{
		OrderID:           "ord-reorder-1",
		Currency:          events.CurrencyKES,
		ProductRevenueKes: 3500,
		TransportFeeKes:   300,
		FeeRecipient:      "merchant",
		PaidAt:            now,
	})
	if err != nil {
		t.Fatalf("new paid event: %v", err)
	}
	paidBytes, _ := json.Marshal(paidEnv)

	createdEnv, err := events.NewEvent(events.TypeAnalyticsOrderCreated, "ord-reorder-1", 1, "corr-1", events.AnalyticsOrderCreatedPayload{
		OrderID:   "ord-reorder-1",
		Currency:  events.CurrencyKES,
		TotalKes:  3500,
		ItemCount: 1,
		Items:     []events.OrderItemPayload{{ProductID: "p1", Quantity: 1}},
		CreatedAt: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("new created event: %v", err)
	}
	createdBytes, _ := json.Marshal(createdEnv)

	// Apply v2 first
	applied, err := store.ApplyEvent(ctx, DefaultTopic, 0, 10, paidBytes)
	if err != nil || !applied {
		t.Fatalf("apply paid failed: applied=%v err=%v", applied, err)
	}
	// Simulated crash before Kafka offset commit: duplicate redelivery of same event_id at offset 10
	appliedDup, err := store.ApplyEvent(ctx, DefaultTopic, 0, 10, paidBytes)
	if err != nil || appliedDup {
		t.Fatalf("expected duplicate to be ignored: applied=%v err=%v", appliedDup, err)
	}
	// Stale v1 arrives after v2
	appliedStale, err := store.ApplyEvent(ctx, DefaultTopic, 0, 11, createdBytes)
	if err != nil || !appliedStale {
		t.Fatalf("apply stale created failed: applied=%v err=%v", appliedStale, err)
	}

	// Verify checkpoint recorded offset 11 and order remains PAID with 3500 KES + 300 merchant fee
	cps, err := store.Checkpoints(ctx, DefaultTopic)
	if err != nil {
		t.Fatalf("checkpoints: %v", err)
	}
	if cps[0] != 11 {
		t.Fatalf("expected checkpoint offset 11, got %d", cps[0])
	}

	loc, _ := time.LoadLocation(reports.DefaultTimezone)
	day := now.In(loc).Format("2006-01-02")
	f, err := reports.ParseFilter(day, day, reports.DefaultTimezone, 50, 0)
	if err != nil {
		t.Fatalf("parse filter: %v", err)
	}
	summary, err := store.SalesSummary(ctx, &auth.User{ID: "owner-1", Role: auth.RoleOwner}, f)
	if err != nil {
		t.Fatalf("sales summary: %v", err)
	}
	if summary.PaidOrderCount != 1 || summary.PendingOrderCount != 0 {
		t.Fatalf("expected 1 paid / 0 pending, got %+v", summary)
	}
	if summary.SettledProductRevenueKes != 3500 || summary.TransportFeesMerchantKes != 300 {
		t.Fatalf("unexpected revenue totals: %+v", summary)
	}
}

func TestAnalyticsReplay_ReproducesR22TotalsAndKafkaOutageIsolation(t *testing.T) {
	pool := testDBPool(t)
	ctx := context.Background()

	// Clean relevant tables for deterministic R22 vs Kafka replay comparison
	for _, stmt := range []string{
		`DELETE FROM public.outbox_event WHERE destination = 'kafka'`,
		`DELETE FROM public.payment_audit_ledger`,
		`DELETE FROM public.exchange_request`,
		`DELETE FROM public.delivery_quote`,
		`DELETE FROM public."Order"`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}

	store := NewStore(pool, "test-group-replay")
	if err := store.ResetForReplay(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	orderID := fmt.Sprintf("ord-r25-%d", now.UnixNano())

	// Seed an order + accepted merchant delivery quote + exchange + audit correction
	if _, err := pool.Exec(ctx, `
		INSERT INTO public."Order" (id, "customerName", "customerPhone", "deliveryLocation", items, "totalKes", "paymentStatus", "createdAt", "updatedAt")
		VALUES ($1, 'Alice', '254700000001', 'Mombasa', '[]'::jsonb, 4200, 'PAID', $2, $2)
	`, orderID, now); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.delivery_quote (order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by)
		VALUES ($1, 1, 350, 'merchant', 'rider', 'accepted', $2, 'owner-1')
	`, orderID, now); err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	var exchID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.exchange_request (order_id, status, reason, requested_by, requested_by_role, price_difference_kes, completed_at)
		VALUES ($1, 'completed', 'scent swap', 'emp-1', 'employee', 600, $2)
		RETURNING id::text
	`, orderID, now).Scan(&exchID); err != nil {
		t.Fatalf("insert exchange: %v", err)
	}
	var auditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.payment_audit_ledger (order_id, action, amount_kes, receipt_number, evidence, notes, authorized_by, created_at)
		VALUES ($1, 'manual_settlement', 4200, 'RCPT-R25', 'bank-ref', 'reconciled', 'owner-1', $2)
		RETURNING id::text
	`, orderID, now).Scan(&auditID); err != nil {
		t.Fatalf("insert audit: %v", err)
	}

	// Simulate Kafka outage during business commits: outbox events remain pending in DB while R22/payment state is intact
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	outboxStore := outbox.NewStore(pool)
	ePaid, _ := events.NewEvent(events.TypeAnalyticsOrderPaid, orderID, 2, "corr-r25", events.AnalyticsOrderPaidPayload{
		OrderID: orderID, Currency: events.CurrencyKES, ProductRevenueKes: 4200, TransportFeeKes: 350, FeeRecipient: "merchant", PaidAt: now,
	})
	eExch, _ := events.NewEvent(events.TypeAnalyticsExchangeCompleted, orderID, 1, "corr-r25", events.AnalyticsExchangeCompletedPayload{
		ExchangeID: exchID, OrderID: orderID, Currency: events.CurrencyKES, PriceDifferenceKes: 600, CompletedAt: now,
	})
	eCorr, _ := events.NewEvent(events.TypeAnalyticsPaymentCorrection, orderID, 1, "corr-r25", events.AnalyticsPaymentCorrectionPayload{
		AuditID: auditID, OrderID: orderID, Action: orders.AuditActionManualSettlement, Currency: events.CurrencyKES, AmountKes: 4200, RecordedAt: now,
	})
	for _, ev := range []events.Envelope{ePaid, eExch, eCorr} {
		if err := outboxStore.Enqueue(ctx, tx, ev, outbox.DestinationKafka); err != nil {
			t.Fatalf("enqueue kafka outbox: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit outbox: %v", err)
	}

	// Verify Kafka outage does not block outbox persistence or MarkFailed retries
	pendingCount, err := outboxStore.CountPending(ctx, outbox.DestinationKafka)
	if err != nil || pendingCount != 3 {
		t.Fatalf("expected 3 pending kafka outbox events, got %d (err=%v)", pendingCount, err)
	}

	// Drain/replay those events into analytics projections (or via live Kafka if GO_TEST_KAFKA_BROKERS is set)
	if brokersEnv := os.Getenv("GO_TEST_KAFKA_BROKERS"); brokersEnv != "" {
		brokers := strings.Split(brokersEnv, ",")
		topic := fmt.Sprintf("bliss.analytics.test.%d", now.UnixNano())
		if err := EnsureTopic(ctx, brokers, topic, DefaultRetentionMs); err != nil {
			t.Fatalf("ensure topic: %v", err)
		}
		prod, err := NewProducer(brokers, topic)
		if err != nil {
			t.Fatalf("new producer: %v", err)
		}
		defer prod.Close()
		recs, err := outboxStore.FetchPending(ctx, 10, outbox.DestinationKafka)
		if err != nil {
			t.Fatalf("fetch pending: %v", err)
		}
		for _, rec := range recs {
			env := events.Envelope{
				EventID: rec.ID, EventType: rec.EventType, SchemaVersion: rec.SchemaVersion,
				OccurredAt: rec.CreatedAt, AggregateID: rec.AggregateID, AggregateVersion: rec.AggregateVersion,
				CorrelationID: rec.CorrelationID, Payload: rec.Payload,
			}
			body, _ := json.Marshal(env)
			if err := prod.Publish(ctx, rec.EventType, rec.ID, body); err != nil {
				t.Fatalf("kafka publish: %v", err)
			}
		}
		cons := NewConsumer(brokers, topic, "test-group-replay", store, nil)
		replayed, err := cons.ReplayAll(ctx)
		if err != nil || replayed != 3 {
			t.Fatalf("replay expected 3 events, got %d (err=%v)", replayed, err)
		}
	} else {
		for idx, ev := range []events.Envelope{ePaid, eExch, eCorr} {
			body, _ := json.Marshal(ev)
			if _, err := store.ApplyEvent(ctx, DefaultTopic, 0, int64(idx), body); err != nil {
				t.Fatalf("apply %s: %v", ev.EventType, err)
			}
		}
	}

	loc, _ := time.LoadLocation(reports.DefaultTimezone)
	day := now.In(loc).Format("2006-01-02")
	f, err := reports.ParseFilter(day, day, reports.DefaultTimezone, 50, 0)
	if err != nil {
		t.Fatalf("parse filter: %v", err)
	}
	owner := &auth.User{ID: "owner-1", Role: auth.RoleOwner}

	r22, err := (&reports.Store{Pool: pool}).SalesReport(ctx, owner, f)
	if err != nil {
		t.Fatalf("R22 SalesReport: %v", err)
	}
	r25, err := store.SalesSummary(ctx, owner, f)
	if err != nil {
		t.Fatalf("R25 SalesSummary: %v", err)
	}
	if !reflect.DeepEqual(r22, r25) {
		t.Fatalf("rebuild did not reproduce R22 totals:\nR22=%+v\nR25=%+v", r22, r25)
	}
}
