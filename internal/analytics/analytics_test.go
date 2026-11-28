package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/url"
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

func TestKafkaDialOpts_TLSAndSASL(t *testing.T) {
	for _, mech := range []string{"plain", "scram-sha-256", "scram-sha-512"} {
		opts, err := kafkaDialOpts(ClientConfig{
			Brokers:       []string{"kafka.example.com:9093"},
			TLS:           true,
			SASLMechanism: mech,
			SASLUsername:  "analytics",
			SASLPassword:  "secret",
		})
		if err != nil {
			t.Fatalf("mechanism %s failed: %v", mech, err)
		}
		if len(opts) < 3 {
			t.Fatalf("expected seed, tls, and sasl opts for %s, got %d", mech, len(opts))
		}
	}
	if _, err := kafkaDialOpts(ClientConfig{Brokers: []string{"b:9092"}, SASLMechanism: "invalid", SASLUsername: "u", SASLPassword: "p"}); err == nil {
		t.Fatal("expected error for unsupported SASL mechanism")
	}
	if _, err := kafkaDialOpts(ClientConfig{Brokers: []string{"b:9092"}, SASLMechanism: "plain"}); err == nil {
		t.Fatal("expected error for missing SASL credentials")
	}
}

func TestAnalyticsStore_DeduplicationReorderingAndPendingUpdate(t *testing.T) {
	pool := testDBPool(t)
	ctx := context.Background()
	store := NewStore(pool, "test-group-dedup")
	if err := store.ResetForReplay(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	day1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

	// Reordered: order.paid (v2) arrives BEFORE order.created (v1)
	paidEnv, err := events.NewEvent(events.TypeAnalyticsOrderPaid, "ord-reorder-1", 2, "corr-1", events.AnalyticsOrderPaidPayload{
		OrderID:              "ord-reorder-1",
		Currency:             events.CurrencyKES,
		ProductRevenueKes:    3500,
		TransportMerchantKes: 300,
		TransportCourierKes:  200,
		PaidAt:               day1,
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
		CreatedAt: day1.Add(-2 * time.Minute),
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

	// Pending order created on day1, then SwitchToPickup updated on day2
	pendCreated, _ := events.NewEvent(events.TypeAnalyticsOrderCreated, "ord-pend-1", 1, "corr-2", events.AnalyticsOrderCreatedPayload{
		OrderID:   "ord-pend-1",
		Currency:  events.CurrencyKES,
		TotalKes:  1800,
		ItemCount: 1,
		Items:     []events.OrderItemPayload{{ProductID: "p1", Quantity: 1}},
		CreatedAt: day1,
	})
	pendUpdated, _ := events.NewEvent(events.TypeAnalyticsOrderUpdated, "ord-pend-1", 1, "corr-2", events.AnalyticsOrderUpdatedPayload{
		OrderID:       "ord-pend-1",
		Currency:      events.CurrencyKES,
		TotalKes:      1800,
		PaymentStatus: "PENDING",
		UpdatedAt:     day2,
	})
	b1, _ := json.Marshal(pendCreated)
	b2, _ := json.Marshal(pendUpdated)
	if _, err := store.ApplyEvent(ctx, DefaultTopic, 0, 12, b1); err != nil {
		t.Fatalf("apply pend created: %v", err)
	}
	if _, err := store.ApplyEvent(ctx, DefaultTopic, 0, 13, b2); err != nil {
		t.Fatalf("apply pend updated: %v", err)
	}

	cps, err := store.Checkpoints(ctx, DefaultTopic)
	if err != nil {
		t.Fatalf("checkpoints: %v", err)
	}
	if cps[0] != 13 {
		t.Fatalf("expected checkpoint offset 13, got %d", cps[0])
	}

	owner := &auth.User{ID: "owner-1", Role: auth.RoleOwner}
	fDay1, _ := reports.ParseFilter("2026-11-20", "2026-11-20", reports.DefaultTimezone, 50, 0)
	s1, err := store.SalesSummary(ctx, owner, fDay1)
	if err != nil {
		t.Fatalf("summary day1: %v", err)
	}
	if s1.PaidOrderCount != 1 || s1.PendingOrderCount != 0 {
		t.Fatalf("day1 expected 1 paid / 0 pending, got %+v", s1)
	}
	if s1.SettledProductRevenueKes != 3500 || s1.TransportFeesMerchantKes != 300 || s1.TransportFeesCourierKes != 200 {
		t.Fatalf("unexpected day1 revenue/transport totals: %+v", s1)
	}

	fDay2, _ := reports.ParseFilter("2026-11-21", "2026-11-21", reports.DefaultTimezone, 50, 0)
	s2, err := store.SalesSummary(ctx, owner, fDay2)
	if err != nil {
		t.Fatalf("summary day2: %v", err)
	}
	if s2.PendingOrderCount != 1 {
		t.Fatalf("day2 expected pending order count 1 after SwitchToPickup date update, got %+v", s2)
	}

	// Preflight failure on unavailable Kafka broker must NOT erase existing projections!
	badCons := NewConsumer([]string{"127.0.0.1:1"}, DefaultTopic, "test-group-dedup", store, nil)
	preflightCtx, cancelPreflight := context.WithTimeout(ctx, 2*time.Second)
	defer cancelPreflight()
	if _, err := badCons.ReplayAll(preflightCtx); err == nil {
		t.Fatal("expected ReplayAll to fail when broker is unavailable")
	}
	afterErr, err := store.SalesSummary(ctx, owner, fDay1)
	if err != nil || afterErr.SettledProductRevenueKes != 3500 {
		t.Fatalf("expected existing projections to remain intact on replay failure, got %+v (err=%v)", afterErr, err)
	}
}

func TestAnalyticsReplay_ReproducesR22TotalsAndKafkaOutageIsolation(t *testing.T) {
	pool := testDBPool(t)
	ctx := context.Background()

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

	// Seed paid order + BOTH merchant AND courier accepted quotes + exchange + audit correction
	if _, err := pool.Exec(ctx, `
		INSERT INTO public."Order" (id, "customerName", "customerPhone", "deliveryLocation", items, "totalKes", "paymentStatus", "createdAt", "updatedAt")
		VALUES ($1, 'Alice', '254700000001', 'Mombasa', '[]'::jsonb, 4200, 'PAID', $2, $2)
	`, orderID, now); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.delivery_quote (order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by)
		VALUES
		($1, 1, 350, 'merchant', 'rider', 'accepted', $2, 'owner-1'),
		($1, 2, 450, 'courier', 'rider', 'accepted', $2, 'owner-1')
	`, orderID, now); err != nil {
		t.Fatalf("insert quotes: %v", err)
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

	// Verify BackfillToOutbox idempotently seeds historical rows into Kafka outbox
	bf1, err := store.BackfillToOutbox(ctx)
	if err != nil || bf1 != 3 {
		t.Fatalf("expected 3 backfilled kafka outbox events, got %d (err=%v)", bf1, err)
	}
	bf2, err := store.BackfillToOutbox(ctx)
	if err != nil || bf2 != 0 {
		t.Fatalf("expected 0 on idempotent second backfill, got %d (err=%v)", bf2, err)
	}

	outboxStore := outbox.NewStore(pool)
	pendingCount, err := outboxStore.CountPending(ctx, outbox.DestinationKafka)
	if err != nil || pendingCount != 3 {
		t.Fatalf("expected 3 pending kafka outbox events, got %d (err=%v)", pendingCount, err)
	}

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
		ePaid, _ := events.NewEvent(events.TypeAnalyticsOrderPaid, orderID, 2, "corr-r25", events.AnalyticsOrderPaidPayload{
			OrderID: orderID, Currency: events.CurrencyKES, ProductRevenueKes: 4200,
			TransportMerchantKes: 350, TransportCourierKes: 450, PaidAt: now,
		})
		eExch, _ := events.NewEvent(events.TypeAnalyticsExchangeCompleted, orderID, 1, "corr-r25", events.AnalyticsExchangeCompletedPayload{
			ExchangeID: exchID, OrderID: orderID, Currency: events.CurrencyKES, PriceDifferenceKes: 600, CompletedAt: now,
		})
		eCorr, _ := events.NewEvent(events.TypeAnalyticsPaymentCorrection, orderID, 1, "corr-r25", events.AnalyticsPaymentCorrectionPayload{
			AuditID: auditID, OrderID: orderID, Action: orders.AuditActionManualSettlement, Currency: events.CurrencyKES, AmountKes: 4200, RecordedAt: now,
		})
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
