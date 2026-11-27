// Package reports provides owner-scoped reconciled sales, stock movement,
// privacy-aware funnel reporting, and PII-free CSV exports in Africa/Nairobi.
package reports

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const DefaultTimezone = "Africa/Nairobi"

var (
	ErrInvalidTimezone = errors.New("invalid IANA timezone")
	ErrInvalidDate     = errors.New("invalid date format; expected YYYY-MM-DD")
	ErrInvalidRange    = errors.New("from date must not exceed to date or span more than 366 days")
	ErrInvalidEvent    = errors.New("event_id, session_id, and valid event_type are required")
)

type Store struct {
	Pool *pgxpool.Pool
}

type Filter struct {
	From     string
	To       string
	Timezone string
	StartUTC time.Time
	EndUTC   time.Time
	Limit    int
	Offset   int
}

func ParseFilter(fromStr, toStr, tzStr string, limit, offset int) (Filter, error) {
	tzStr = strings.TrimSpace(tzStr)
	if tzStr == "" {
		tzStr = DefaultTimezone
	}
	loc, err := time.LoadLocation(tzStr)
	if err != nil {
		return Filter{}, ErrInvalidTimezone
	}

	nowLocal := time.Now().In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	var fromDate, toDate time.Time

	if strings.TrimSpace(fromStr) == "" {
		fromDate = today.AddDate(0, 0, -29)
	} else {
		parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(fromStr), loc)
		if err != nil {
			return Filter{}, ErrInvalidDate
		}
		fromDate = parsed
	}
	if strings.TrimSpace(toStr) == "" {
		toDate = today
	} else {
		parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(toStr), loc)
		if err != nil {
			return Filter{}, ErrInvalidDate
		}
		toDate = parsed
	}

	if fromDate.After(toDate) || toDate.Sub(fromDate) > 366*24*time.Hour {
		return Filter{}, ErrInvalidRange
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	return Filter{
		From:     fromDate.Format("2006-01-02"),
		To:       toDate.Format("2006-01-02"),
		Timezone: tzStr,
		StartUTC: fromDate.UTC(),
		EndUTC:   toDate.AddDate(0, 0, 1).UTC(),
		Limit:    limit,
		Offset:   offset,
	}, nil
}

type CorrectionRow struct {
	Action   string `json:"action"`
	Count    int    `json:"count"`
	TotalKes int    `json:"total_kes"`
}

type DailySalesRow struct {
	Date                  string `json:"date"`
	PaidOrders            int    `json:"paid_orders"`
	ProductRevenueKes     int    `json:"product_revenue_kes"`
	TransportFeesKes      int    `json:"transport_fees_kes"`
	ExchangeTopupTotalKes int    `json:"exchange_topup_kes"`
}

type SalesSummary struct {
	From                     string          `json:"from"`
	To                       string          `json:"to"`
	Timezone                 string          `json:"timezone"`
	SettledProductRevenueKes int             `json:"settled_product_revenue_kes"`
	TransportFeesMerchantKes int             `json:"transport_fees_merchant_kes"`
	TransportFeesCourierKes  int             `json:"transport_fees_courier_kes"`
	ExchangeTopupRevenueKes  int             `json:"exchange_topup_revenue_kes"`
	CompletedExchangeCount   int             `json:"completed_exchange_count"`
	PaidOrderCount           int             `json:"paid_order_count"`
	PendingOrderCount        int             `json:"pending_order_count"`
	AverageOrderValueKes     int             `json:"average_order_value_kes"`
	PaymentCorrections       []CorrectionRow `json:"payment_corrections"`
	Daily                    []DailySalesRow `json:"daily"`
}

func (s *Store) SalesReport(ctx context.Context, actor *auth.User, f Filter) (*SalesSummary, error) {
	if actor == nil || actor.Role != auth.RoleOwner {
		return nil, auth.ErrPermissionDenied
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	out := &SalesSummary{
		From:               f.From,
		To:                 f.To,
		Timezone:           f.Timezone,
		PaymentCorrections: []CorrectionRow{},
		Daily:              []DailySalesRow{},
	}

	if err := tx.QueryRow(ctx, `
		SELECT
			coalesce(sum("totalKes") FILTER (WHERE "paymentStatus" = 'PAID'), 0),
			count(*) FILTER (WHERE "paymentStatus" = 'PAID'),
			count(*) FILTER (WHERE "paymentStatus" = 'PENDING')
		FROM public."Order"
		WHERE "updatedAt" >= $1 AND "updatedAt" < $2`,
		f.StartUTC, f.EndUTC).Scan(&out.SettledProductRevenueKes, &out.PaidOrderCount, &out.PendingOrderCount); err != nil {
		return nil, err
	}
	if out.PaidOrderCount > 0 {
		out.AverageOrderValueKes = out.SettledProductRevenueKes / out.PaidOrderCount
	}

	if err := tx.QueryRow(ctx, `
		SELECT
			coalesce(sum(dq.amount_kes) FILTER (WHERE dq.fee_recipient = 'merchant'), 0),
			coalesce(sum(dq.amount_kes) FILTER (WHERE dq.fee_recipient = 'courier'), 0)
		FROM public.delivery_quote dq
		JOIN public."Order" o ON o.id = dq.order_id
		WHERE o."paymentStatus" = 'PAID'
		  AND dq.status = 'accepted'
		  AND o."updatedAt" >= $1 AND o."updatedAt" < $2`,
		f.StartUTC, f.EndUTC).Scan(&out.TransportFeesMerchantKes, &out.TransportFeesCourierKes); err != nil {
		return nil, err
	}

	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(price_difference_kes), 0), count(*)
		FROM public.exchange_request
		WHERE status = 'completed'
		  AND completed_at >= $1 AND completed_at < $2`,
		f.StartUTC, f.EndUTC).Scan(&out.ExchangeTopupRevenueKes, &out.CompletedExchangeCount); err != nil {
		return nil, err
	}

	corrRows, err := tx.Query(ctx, `
		SELECT action, count(*), coalesce(sum(amount_kes), 0)
		FROM public.payment_audit_ledger
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY action
		ORDER BY action`, f.StartUTC, f.EndUTC)
	if err != nil {
		return nil, err
	}
	defer corrRows.Close()
	for corrRows.Next() {
		var c CorrectionRow
		if err := corrRows.Scan(&c.Action, &c.Count, &c.TotalKes); err != nil {
			return nil, err
		}
		out.PaymentCorrections = append(out.PaymentCorrections, c)
	}
	if err := corrRows.Err(); err != nil {
		return nil, err
	}

	dailyRows, err := tx.Query(ctx, `
		WITH paid_orders AS (
			SELECT
				o.id,
				(o."updatedAt" AT TIME ZONE 'UTC' AT TIME ZONE $3)::date AS day,
				o."totalKes" AS product_rev,
				coalesce((SELECT sum(dq.amount_kes) FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted'), 0) AS transport_fee
			FROM public."Order" o
			WHERE o."paymentStatus" = 'PAID'
			  AND o."updatedAt" >= $1 AND o."updatedAt" < $2
		),
		daily_exchanges AS (
			SELECT
				(completed_at AT TIME ZONE $3)::date AS day,
				coalesce(sum(price_difference_kes), 0) AS topup_kes
			FROM public.exchange_request
			WHERE status = 'completed'
			  AND completed_at >= $1 AND completed_at < $2
			GROUP BY 1
		),
		daily_orders AS (
			SELECT day, count(*) AS paid_orders, coalesce(sum(product_rev), 0) AS product_rev, coalesce(sum(transport_fee), 0) AS transport_fee
			FROM paid_orders
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
		ORDER BY 1 DESC`,
		f.StartUTC, f.EndUTC, f.Timezone)
	if err != nil {
		return nil, err
	}
	defer dailyRows.Close()
	for dailyRows.Next() {
		var r DailySalesRow
		if err := dailyRows.Scan(&r.Date, &r.PaidOrders, &r.ProductRevenueKes, &r.TransportFeesKes, &r.ExchangeTopupTotalKes); err != nil {
			return nil, err
		}
		out.Daily = append(out.Daily, r)
	}
	return out, dailyRows.Err()
}

type StockMovementRow struct {
	BatchID     string    `json:"batch_id"`
	Kind        string    `json:"kind"`
	ProductID   string    `json:"product_id"`
	ProductName string    `json:"product_name"`
	Delta       int       `json:"delta"`
	Balance     int       `json:"balance"`
	Actor       string    `json:"actor"`
	Evidence    string    `json:"evidence"`
	CreatedAt   time.Time `json:"created_at"`
}

type StockMovementPage struct {
	Movements []StockMovementRow `json:"movements"`
	Total     int                `json:"total"`
}

func (s *Store) StockMovements(ctx context.Context, actor *auth.User, f Filter) (*StockMovementPage, error) {
	if actor == nil || actor.Role != auth.RoleOwner {
		return nil, auth.ErrPermissionDenied
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	page := &StockMovementPage{Movements: []StockMovementRow{}}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM public.stock_entry se
		JOIN public.stock_batch sb ON sb.id = se.batch_id
		WHERE sb.created_at >= $1 AND sb.created_at < $2`, f.StartUTC, f.EndUTC).Scan(&page.Total); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT sb.id, sb.kind, se.product_id, p.name, se.delta, se.balance, sb.actor, sb.evidence, sb.created_at
		FROM public.stock_entry se
		JOIN public.stock_batch sb ON sb.id = se.batch_id
		JOIN public.catalog_product p ON p.id = se.product_id
		WHERE sb.created_at >= $1 AND sb.created_at < $2
		ORDER BY sb.created_at DESC, sb.id, se.product_id
		LIMIT $3 OFFSET $4`, f.StartUTC, f.EndUTC, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r StockMovementRow
		if err := rows.Scan(&r.BatchID, &r.Kind, &r.ProductID, &r.ProductName, &r.Delta, &r.Balance, &r.Actor, &r.Evidence, &r.CreatedAt); err != nil {
			return nil, err
		}
		page.Movements = append(page.Movements, r)
	}
	return page, rows.Err()
}

type FunnelEventInput struct {
	EventID   string `json:"event_id"`
	SessionID string `json:"session_id"`
	EventType string `json:"event_type"`
	ProductID string `json:"product_id,omitempty"`
}

func (s *Store) RecordFunnelEvent(ctx context.Context, in FunnelEventInput) error {
	in.EventID = strings.TrimSpace(in.EventID)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.EventType = strings.TrimSpace(in.EventType)
	in.ProductID = strings.TrimSpace(in.ProductID)
	if in.EventID == "" || len(in.EventID) > 128 || in.SessionID == "" || len(in.SessionID) > 128 {
		return ErrInvalidEvent
	}
	switch in.EventType {
	case "catalog_view", "cart_add", "checkout_start", "payment_initiated":
	default:
		return ErrInvalidEvent
	}
	var prodPtr *string
	if in.ProductID != "" {
		prodPtr = &in.ProductID
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO public.analytics_funnel_event (event_id, session_id, event_type, product_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING`,
		in.EventID, in.SessionID, in.EventType, prodPtr)
	return err
}

type FunnelReport struct {
	From             string `json:"from"`
	To               string `json:"to"`
	Timezone         string `json:"timezone"`
	CatalogViews     int    `json:"catalog_views"`
	CartAdds         int    `json:"cart_adds"`
	CheckoutStarts   int    `json:"checkout_starts"`
	PaymentInitiated int    `json:"payment_initiated"`
	SettledOrders    int    `json:"settled_orders"`
}

func (s *Store) FunnelSummary(ctx context.Context, actor *auth.User, f Filter) (*FunnelReport, error) {
	if actor == nil || actor.Role != auth.RoleOwner {
		return nil, auth.ErrPermissionDenied
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	out := &FunnelReport{From: f.From, To: f.To, Timezone: f.Timezone}
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE event_type = 'catalog_view'),
			count(*) FILTER (WHERE event_type = 'cart_add'),
			count(*) FILTER (WHERE event_type = 'checkout_start'),
			count(*) FILTER (WHERE event_type = 'payment_initiated')
		FROM public.analytics_funnel_event
		WHERE occurred_at >= $1 AND occurred_at < $2`,
		f.StartUTC, f.EndUTC).Scan(&out.CatalogViews, &out.CartAdds, &out.CheckoutStarts, &out.PaymentInitiated); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM public."Order"
		WHERE "paymentStatus" = 'PAID'
		  AND "updatedAt" >= $1 AND "updatedAt" < $2`,
		f.StartUTC, f.EndUTC).Scan(&out.SettledOrders); err != nil {
		return nil, err
	}
	return out, nil
}

// ExportCSV streams PII-free reconciled order rows for the given filter.
func (s *Store) ExportCSV(ctx context.Context, actor *auth.User, f Filter, w io.Writer) error {
	if actor == nil || actor.Role != auth.RoleOwner {
		return auth.ErrPermissionDenied
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollback(tx)

	rows, err := tx.Query(ctx, `
		SELECT
			o.id,
			to_char(o."updatedAt" AT TIME ZONE 'UTC' AT TIME ZONE $3, 'YYYY-MM-DD HH24:MI:SS'),
			o."paymentStatus",
			o."totalKes",
			coalesce((SELECT sum(dq.amount_kes) FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted'), 0),
			coalesce((SELECT dq.fee_recipient FROM public.delivery_quote dq WHERE dq.order_id = o.id AND dq.status = 'accepted' ORDER BY dq.version DESC LIMIT 1), 'none'),
			coalesce((SELECT sum(er.price_difference_kes) FROM public.exchange_request er WHERE er.order_id = o.id AND er.status = 'completed'), 0)
		FROM public."Order" o
		WHERE o."paymentStatus" = 'PAID'
		  AND o."updatedAt" >= $1 AND o."updatedAt" < $2
		ORDER BY o."updatedAt" DESC, o.id DESC`,
		f.StartUTC, f.EndUTC, f.Timezone)
	if err != nil {
		return err
	}
	defer rows.Close()

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"order_id",
		"paid_at_local",
		"payment_status",
		"product_revenue_kes",
		"transport_fee_kes",
		"fee_recipient",
		"exchange_topup_kes",
	}); err != nil {
		return err
	}
	for rows.Next() {
		var orderID, paidLocal, status, recipient string
		var prodRev, transportFee, exchTopup int
		if err := rows.Scan(&orderID, &paidLocal, &status, &prodRev, &transportFee, &recipient, &exchTopup); err != nil {
			return err
		}
		if err := cw.Write([]string{
			csvCell(orderID),
			csvCell(paidLocal),
			csvCell(status),
			strconv.Itoa(prodRev),
			strconv.Itoa(transportFee),
			csvCell(recipient),
			strconv.Itoa(exchTopup),
		}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func csvCell(v string) string {
	if v != "" {
		switch v[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + v
		}
	}
	return v
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

var _ = fmt.Sprintf
