// Package analytics implements Kafka event production, idempotent consumption,
// checkpointed replay, and read-only analytics projections.
// Read models in this package never initiate payments.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/reports"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultTopic               = "bliss.analytics.events.v1"
	DefaultConsumerGroup       = "bliss-analytics-consumer"
	DefaultRetentionMs   int64 = 90 * 24 * 60 * 60 * 1000 // 90 days
)

// Store manages durable deduplication, checkpoints, and analytics read models in PostgreSQL.
type Store struct {
	Pool          *pgxpool.Pool
	ConsumerGroup string
}

// NewStore creates an analytics Store with default consumer group if omitted.
func NewStore(pool *pgxpool.Pool, consumerGroup string) *Store {
	if consumerGroup == "" {
		consumerGroup = DefaultConsumerGroup
	}
	return &Store{Pool: pool, ConsumerGroup: consumerGroup}
}

func (s *Store) group() string {
	if s.ConsumerGroup == "" {
		return DefaultConsumerGroup
	}
	return s.ConsumerGroup
}

// ApplyEvent decodes a versioned analytics envelope, deduplicates by (event_id, consumer_group),
// applies the projection with aggregate-version ordering guards, and checkpoints the Kafka offset
// atomically in a single PostgreSQL transaction.
func (s *Store) ApplyEvent(ctx context.Context, topic string, partition int32, offset int64, rawBody []byte) (bool, error) {
	if s.Pool == nil {
		return false, errors.New("analytics: database pool not initialized")
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("analytics: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	applied, err := applyEventTx(ctx, tx, s.group(), topic, partition, offset, rawBody)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analytics: commit tx: %w", err)
	}
	return applied, nil
}

func applyEventTx(ctx context.Context, tx pgx.Tx, group, topic string, partition int32, offset int64, rawBody []byte) (bool, error) {
	var env events.Envelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return false, fmt.Errorf("analytics: invalid event envelope: %w", err)
	}
	if err := env.Validate(); err != nil {
		return false, fmt.Errorf("analytics: envelope validation failed: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO public.analytics_consumer_dedup (
			event_id, consumer_group, event_type, aggregate_id, aggregate_version, topic, partition, kafka_offset
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id, consumer_group) DO NOTHING
	`, env.EventID, group, env.EventType, env.AggregateID, env.AggregateVersion, topic, partition, offset)
	if err != nil {
		return false, fmt.Errorf("analytics: insert dedup: %w", err)
	}

	applied := tag.RowsAffected() > 0
	if applied {
		if err := applyProjection(ctx, tx, env); err != nil {
			return false, err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.analytics_consumer_checkpoint (consumer_group, topic, partition, last_offset, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (consumer_group, topic, partition) DO UPDATE
		SET last_offset = GREATEST(public.analytics_consumer_checkpoint.last_offset, EXCLUDED.last_offset),
		    updated_at = now()
	`, group, topic, partition, offset); err != nil {
		return false, fmt.Errorf("analytics: upsert checkpoint: %w", err)
	}
	return applied, nil
}

func applyProjection(ctx context.Context, tx pgx.Tx, env events.Envelope) error {
	switch env.EventType {
	case events.TypeAnalyticsOrderCreated:
		var p events.AnalyticsOrderCreatedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("analytics: decode %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO public.analytics_order_fact (
				order_id, aggregate_version, payment_status, product_revenue_kes,
				transport_merchant_kes, transport_courier_kes, occurred_at, updated_at
			) VALUES ($1, $2, 'PENDING', $3, 0, 0, $4, now())
			ON CONFLICT (order_id) DO UPDATE
			SET aggregate_version = EXCLUDED.aggregate_version,
			    payment_status = EXCLUDED.payment_status,
			    product_revenue_kes = EXCLUDED.product_revenue_kes,
			    occurred_at = GREATEST(public.analytics_order_fact.occurred_at, EXCLUDED.occurred_at),
			    updated_at = now()
			WHERE public.analytics_order_fact.aggregate_version < EXCLUDED.aggregate_version
		`, p.OrderID, env.AggregateVersion, p.TotalKes, p.CreatedAt.UTC())
		return err

	case events.TypeAnalyticsOrderUpdated:
		var p events.AnalyticsOrderUpdatedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("analytics: decode %s: %w", env.EventType, err)
		}
		if p.PaymentStatus == "" {
			p.PaymentStatus = "PENDING"
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO public.analytics_order_fact (
				order_id, aggregate_version, payment_status, product_revenue_kes,
				transport_merchant_kes, transport_courier_kes, occurred_at, updated_at
			) VALUES ($1, $2, $3, $4, 0, 0, $5, now())
			ON CONFLICT (order_id) DO UPDATE
			SET occurred_at = GREATEST(public.analytics_order_fact.occurred_at, EXCLUDED.occurred_at),
			    product_revenue_kes = EXCLUDED.product_revenue_kes,
			    updated_at = now()
			WHERE public.analytics_order_fact.payment_status <> 'PAID'
		`, p.OrderID, env.AggregateVersion, p.PaymentStatus, p.TotalKes, p.UpdatedAt.UTC())
		return err

	case events.TypeAnalyticsOrderPaid:
		var p events.AnalyticsOrderPaidPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("analytics: decode %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO public.analytics_order_fact (
				order_id, aggregate_version, payment_status, product_revenue_kes,
				transport_merchant_kes, transport_courier_kes, occurred_at, updated_at
			) VALUES ($1, $2, 'PAID', $3, $4, $5, $6, now())
			ON CONFLICT (order_id) DO UPDATE
			SET aggregate_version = EXCLUDED.aggregate_version,
			    payment_status = 'PAID',
			    product_revenue_kes = EXCLUDED.product_revenue_kes,
			    transport_merchant_kes = EXCLUDED.transport_merchant_kes,
			    transport_courier_kes = EXCLUDED.transport_courier_kes,
			    occurred_at = EXCLUDED.occurred_at,
			    updated_at = now()
			WHERE public.analytics_order_fact.aggregate_version < EXCLUDED.aggregate_version
			   OR public.analytics_order_fact.payment_status <> 'PAID'
		`, p.OrderID, env.AggregateVersion, p.ProductRevenueKes, p.TransportMerchantKes, p.TransportCourierKes, p.PaidAt.UTC())
		return err

	case events.TypeAnalyticsExchangeCompleted:
		var p events.AnalyticsExchangeCompletedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("analytics: decode %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO public.analytics_exchange_fact (
				exchange_id, order_id, price_difference_kes, completed_at, updated_at
			) VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (exchange_id) DO NOTHING
		`, p.ExchangeID, p.OrderID, p.PriceDifferenceKes, p.CompletedAt.UTC())
		return err

	case events.TypeAnalyticsPaymentCorrection:
		var p events.AnalyticsPaymentCorrectionPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("analytics: decode %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO public.analytics_payment_correction_fact (
				audit_id, order_id, action, amount_kes, recorded_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (audit_id) DO NOTHING
		`, p.AuditID, p.OrderID, p.Action, p.AmountKes, p.RecordedAt.UTC())
		return err

	default:
		// Forward-compatible: ignore unknown event types after dedup + checkpoint.
		return nil
	}
}

// ResetForReplay clears analytics projections, deduplication records, and checkpoints
// for this consumer group so a clean replay from offset 0 reproduces aggregates.
func (s *Store) ResetForReplay(ctx context.Context) error {
	if s.Pool == nil {
		return errors.New("analytics: database pool not initialized")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := resetForReplayTx(ctx, tx, s.group()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func resetForReplayTx(ctx context.Context, tx pgx.Tx, group string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM public.analytics_consumer_dedup WHERE consumer_group = $1`, group); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public.analytics_consumer_checkpoint WHERE consumer_group = $1`, group); err != nil {
		return err
	}
	for _, stmt := range []string{
		`DELETE FROM public.analytics_order_fact`,
		`DELETE FROM public.analytics_exchange_fact`,
		`DELETE FROM public.analytics_payment_correction_fact`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// seedBaselineTx populates the analytics read models from authoritative operational tables
// for historical rows created before Kafka streaming or outside Kafka retention, so replays
// reproduce full R22 totals while remaining idempotent with subsequent Kafka events.
func seedBaselineTx(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO public.analytics_order_fact (
			order_id, aggregate_version, payment_status, product_revenue_kes,
			transport_merchant_kes, transport_courier_kes, occurred_at, updated_at
		)
		SELECT
			o.id,
			CASE WHEN o."paymentStatus" = 'PAID' THEN 2 ELSE 1 END,
			o."paymentStatus",
			o."totalKes",
			CASE WHEN o."paymentStatus" = 'PAID' THEN coalesce((SELECT sum(dq.amount_kes) FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted' AND dq.fee_recipient = 'merchant'), 0) ELSE 0 END,
			CASE WHEN o."paymentStatus" = 'PAID' THEN coalesce((SELECT sum(dq.amount_kes) FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted' AND dq.fee_recipient = 'courier'), 0) ELSE 0 END,
			o."updatedAt",
			now()
		FROM public."Order" o
		WHERE o."paymentStatus" IN ('PENDING', 'PAID')
		ON CONFLICT (order_id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("analytics: seed order baseline: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.analytics_exchange_fact (
			exchange_id, order_id, price_difference_kes, completed_at, updated_at
		)
		SELECT id::text, order_id, price_difference_kes, completed_at, now()
		FROM public.exchange_request
		WHERE status = 'completed' AND completed_at IS NOT NULL
		ON CONFLICT (exchange_id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("analytics: seed exchange baseline: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.analytics_payment_correction_fact (
			audit_id, order_id, action, amount_kes, recorded_at, updated_at
		)
		SELECT id::text, order_id, action, amount_kes, created_at, now()
		FROM public.payment_audit_ledger
		ON CONFLICT (audit_id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("analytics: seed correction baseline: %w", err)
	}
	return nil
}

// BackfillToOutbox idempotently enqueues deterministic Kafka outbox events for existing
// authoritative records in Order, exchange_request, and payment_audit_ledger that predate
// Kafka streaming. Safe to run repeatedly.
func (s *Store) BackfillToOutbox(ctx context.Context) (int64, error) {
	if s.Pool == nil {
		return 0, errors.New("analytics: database pool not initialized")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var total int64

	t1, err := tx.Exec(ctx, `
		INSERT INTO public.outbox_event (
			id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version,
			payload, destination, status, retry_count, created_at, updated_at
		)
		SELECT
			'bf-ord-' || o.id,
			$1,
			o.id,
			1,
			'backfill',
			1,
			jsonb_build_object(
				'orderId', o.id,
				'currency', 'KES',
				'totalKes', o."totalKes",
				'itemCount', coalesce(jsonb_array_length(o.items), 0),
				'items', '[]'::jsonb,
				'createdAt', o."updatedAt" AT TIME ZONE 'UTC'
			),
			'kafka',
			'pending',
			0,
			o."createdAt",
			now()
		FROM public."Order" o
		WHERE o."paymentStatus" = 'PENDING'
		ON CONFLICT (id) DO NOTHING
	`, events.TypeAnalyticsOrderCreated)
	if err != nil {
		return 0, fmt.Errorf("analytics: backfill pending orders: %w", err)
	}
	total += t1.RowsAffected()

	t2, err := tx.Exec(ctx, `
		INSERT INTO public.outbox_event (
			id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version,
			payload, destination, status, retry_count, created_at, updated_at
		)
		SELECT
			'bf-paid-' || o.id,
			$1,
			o.id,
			2,
			'backfill',
			1,
			jsonb_build_object(
				'orderId', o.id,
				'currency', 'KES',
				'productRevenueKes', o."totalKes",
				'transportMerchantKes', coalesce((SELECT sum(dq.amount_kes) FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted' AND dq.fee_recipient = 'merchant'), 0),
				'transportCourierKes', coalesce((SELECT sum(dq.amount_kes) FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted' AND dq.fee_recipient = 'courier'), 0),
				'paidAt', o."updatedAt" AT TIME ZONE 'UTC'
			),
			'kafka',
			'pending',
			0,
			o."updatedAt",
			now()
		FROM public."Order" o
		WHERE o."paymentStatus" = 'PAID'
		ON CONFLICT (id) DO NOTHING
	`, events.TypeAnalyticsOrderPaid)
	if err != nil {
		return 0, fmt.Errorf("analytics: backfill paid orders: %w", err)
	}
	total += t2.RowsAffected()

	t3, err := tx.Exec(ctx, `
		INSERT INTO public.outbox_event (
			id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version,
			payload, destination, status, retry_count, created_at, updated_at
		)
		SELECT
			'bf-exch-' || er.id::text,
			$1,
			er.order_id,
			1,
			'backfill',
			1,
			jsonb_build_object(
				'exchangeId', er.id::text,
				'orderId', er.order_id,
				'currency', 'KES',
				'priceDifferenceKes', er.price_difference_kes,
				'completedAt', er.completed_at AT TIME ZONE 'UTC'
			),
			'kafka',
			'pending',
			0,
			er.completed_at,
			now()
		FROM public.exchange_request er
		WHERE er.status = 'completed' AND er.completed_at IS NOT NULL
		ON CONFLICT (id) DO NOTHING
	`, events.TypeAnalyticsExchangeCompleted)
	if err != nil {
		return 0, fmt.Errorf("analytics: backfill exchanges: %w", err)
	}
	total += t3.RowsAffected()

	t4, err := tx.Exec(ctx, `
		INSERT INTO public.outbox_event (
			id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version,
			payload, destination, status, retry_count, created_at, updated_at
		)
		SELECT
			'bf-aud-' || pal.id::text,
			$1,
			pal.order_id,
			1,
			'backfill',
			1,
			jsonb_build_object(
				'auditId', pal.id::text,
				'orderId', pal.order_id,
				'action', pal.action,
				'currency', 'KES',
				'amountKes', pal.amount_kes,
				'recordedAt', pal.created_at AT TIME ZONE 'UTC'
			),
			'kafka',
			'pending',
			0,
			pal.created_at,
			now()
		FROM public.payment_audit_ledger pal
		ON CONFLICT (id) DO NOTHING
	`, events.TypeAnalyticsPaymentCorrection)
	if err != nil {
		return 0, fmt.Errorf("analytics: backfill corrections: %w", err)
	}
	total += t4.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return total, nil
}

// Checkpoints returns the last durably processed offset per partition for topic.
func (s *Store) Checkpoints(ctx context.Context, topic string) (map[int32]int64, error) {
	if s.Pool == nil {
		return nil, errors.New("analytics: database pool not initialized")
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT partition, last_offset
		FROM public.analytics_consumer_checkpoint
		WHERE consumer_group = $1 AND topic = $2
	`, s.group(), topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int32]int64)
	for rows.Next() {
		var part int32
		var off int64
		if err := rows.Scan(&part, &off); err != nil {
			return nil, err
		}
		out[part] = off
	}
	return out, rows.Err()
}

// SalesSummary queries the Kafka-materialized analytics read models for owner-only reporting,
// matching the exact R22 SalesReport contract and Africa/Nairobi date semantics.
func (s *Store) SalesSummary(ctx context.Context, actor *auth.User, f reports.Filter) (*reports.SalesSummary, error) {
	if actor == nil || actor.Role != auth.RoleOwner {
		return nil, auth.ErrPermissionDenied
	}
	if s.Pool == nil {
		return nil, errors.New("analytics: database pool not initialized")
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out := &reports.SalesSummary{
		From:               f.From,
		To:                 f.To,
		Timezone:           f.Timezone,
		PaymentCorrections: []reports.CorrectionRow{},
		Daily:              []reports.DailySalesRow{},
	}

	if err := tx.QueryRow(ctx, `
		SELECT
			coalesce(sum(product_revenue_kes) FILTER (WHERE payment_status = 'PAID'), 0),
			coalesce(sum(transport_merchant_kes) FILTER (WHERE payment_status = 'PAID'), 0),
			coalesce(sum(transport_courier_kes) FILTER (WHERE payment_status = 'PAID'), 0),
			count(*) FILTER (WHERE payment_status = 'PAID'),
			count(*) FILTER (WHERE payment_status = 'PENDING')
		FROM public.analytics_order_fact
		WHERE occurred_at >= $1 AND occurred_at < $2
	`, f.StartUTC, f.EndUTC).Scan(
		&out.SettledProductRevenueKes,
		&out.TransportFeesMerchantKes,
		&out.TransportFeesCourierKes,
		&out.PaidOrderCount,
		&out.PendingOrderCount,
	); err != nil {
		return nil, err
	}
	if out.PaidOrderCount > 0 {
		out.AverageOrderValueKes = out.SettledProductRevenueKes / out.PaidOrderCount
	}

	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(price_difference_kes), 0), count(*)
		FROM public.analytics_exchange_fact
		WHERE completed_at >= $1 AND completed_at < $2
	`, f.StartUTC, f.EndUTC).Scan(&out.ExchangeTopupRevenueKes, &out.CompletedExchangeCount); err != nil {
		return nil, err
	}

	corrRows, err := tx.Query(ctx, `
		SELECT action, count(*), coalesce(sum(amount_kes), 0)
		FROM public.analytics_payment_correction_fact
		WHERE recorded_at >= $1 AND recorded_at < $2
		GROUP BY action
		ORDER BY action
	`, f.StartUTC, f.EndUTC)
	if err != nil {
		return nil, err
	}
	defer corrRows.Close()
	for corrRows.Next() {
		var c reports.CorrectionRow
		if err := corrRows.Scan(&c.Action, &c.Count, &c.TotalKes); err != nil {
			return nil, err
		}
		out.PaymentCorrections = append(out.PaymentCorrections, c)
	}
	if err := corrRows.Err(); err != nil {
		return nil, err
	}

	dailyRows, err := tx.Query(ctx, `
		WITH daily_orders AS (
			SELECT
				(occurred_at AT TIME ZONE $3)::date AS day,
				count(*) AS paid_orders,
				coalesce(sum(product_revenue_kes), 0) AS product_rev,
				coalesce(sum(transport_merchant_kes + transport_courier_kes), 0) AS transport_fee
			FROM public.analytics_order_fact
			WHERE payment_status = 'PAID'
			  AND occurred_at >= $1 AND occurred_at < $2
			GROUP BY 1
		),
		daily_exchanges AS (
			SELECT
				(completed_at AT TIME ZONE $3)::date AS day,
				coalesce(sum(price_difference_kes), 0) AS topup_kes
			FROM public.analytics_exchange_fact
			WHERE completed_at >= $1 AND completed_at < $2
			GROUP BY 1
		)
		SELECT
			coalesce(d.day, e.day)::text AS day,
			coalesce(d.paid_orders, 0),
			coalesce(d.product_rev, 0),
			coalesce(d.transport_fee, 0),
			coalesce(e.topup_kes, 0)
		FROM daily_orders d
		FULL OUTER JOIN daily_exchanges e ON e.day = d.day
		ORDER BY 1 DESC
	`, f.StartUTC, f.EndUTC, f.Timezone)
	if err != nil {
		return nil, err
	}
	defer dailyRows.Close()
	for dailyRows.Next() {
		var r reports.DailySalesRow
		if err := dailyRows.Scan(&r.Date, &r.PaidOrders, &r.ProductRevenueKes, &r.TransportFeesKes, &r.ExchangeTopupTotalKes); err != nil {
			return nil, err
		}
		out.Daily = append(out.Daily, r)
	}
	if err := dailyRows.Err(); err != nil {
		return nil, err
	}

	_ = time.UTC
	return out, nil
}
