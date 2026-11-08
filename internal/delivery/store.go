package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides persistence and validation operations for delivery quotes.
type Store struct {
	Pool *pgxpool.Pool
}

// NewStore initializes a Store with the given database connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{Pool: pool}
}

// CreateQuote creates a new delivery quote for an order.
// If a prior quote existed for this order, the version number is incremented,
// resetting customer acceptance to 'pending'.
func (s *Store) CreateQuote(ctx context.Context, orderID string, p CreateQuoteParams) (*DeliveryQuote, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("order ID is required")
	}
	if p.AmountKES < 0 {
		return nil, fmt.Errorf("%w: amount cannot be negative", ErrInvalidQuote)
	}
	if p.FeeRecipient != FeeRecipientMerchant && p.FeeRecipient != FeeRecipientCourier {
		return nil, fmt.Errorf("%w: invalid fee recipient", ErrInvalidQuote)
	}
	arrangement := strings.TrimSpace(p.Arrangement)
	if arrangement == "" {
		return nil, fmt.Errorf("%w: arrangement is required", ErrInvalidQuote)
	}
	createdBy := strings.TrimSpace(p.CreatedBy)
	if createdBy == "" {
		return nil, fmt.Errorf("%w: created_by is required", ErrInvalidQuote)
	}
	if p.ExpiresAt.IsZero() || !p.ExpiresAt.After(time.Now()) {
		return nil, fmt.Errorf("%w: expiration must be in the future", ErrInvalidQuote)
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Check order existence and status
	var paymentStatus string
	err = tx.QueryRow(ctx, `SELECT "paymentStatus" FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&paymentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	if paymentStatus != "PENDING" {
		return nil, ErrOrderNotPending
	}

	// Determine next version number for this order
	var currentVersion int
	err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM public.delivery_quote WHERE order_id = $1`, orderID).Scan(&currentVersion)
	if err != nil {
		return nil, err
	}
	nextVersion := currentVersion + 1

	var q DeliveryQuote
	var feeRec string
	var status string
	err = tx.QueryRow(ctx, `
		INSERT INTO public.delivery_quote (
			order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_by, customer_notes
		) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8)
		RETURNING id, order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_at, accepted_at, rejected_at, created_by, COALESCE(customer_notes, '')
	`, orderID, nextVersion, p.AmountKES, string(p.FeeRecipient), arrangement, p.ExpiresAt, createdBy, strings.TrimSpace(p.CustomerNotes)).Scan(
		&q.ID, &q.OrderID, &q.Version, &q.AmountKES, &feeRec, &q.Arrangement, &status,
		&q.ExpiresAt, &q.CreatedAt, &q.AcceptedAt, &q.RejectedAt, &q.CreatedBy, &q.CustomerNotes,
	)
	if err != nil {
		return nil, err
	}
	q.FeeRecipient = FeeRecipient(feeRec)
	q.Status = QuoteStatus(status)

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &q, nil
}

// GetActiveQuote retrieves the quote with the highest version for an order.
// If the quote was pending but has passed its expiration, its status in the
// returned struct will reflect 'expired'.
func (s *Store) GetActiveQuote(ctx context.Context, orderID string) (*DeliveryQuote, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("order ID is required")
	}

	var q DeliveryQuote
	var feeRec string
	var status string
	err := s.Pool.QueryRow(ctx, `
		SELECT id, order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_at, accepted_at, rejected_at, created_by, COALESCE(customer_notes, '')
		FROM public.delivery_quote
		WHERE order_id = $1
		ORDER BY version DESC
		LIMIT 1
	`, orderID).Scan(
		&q.ID, &q.OrderID, &q.Version, &q.AmountKES, &feeRec, &q.Arrangement, &status,
		&q.ExpiresAt, &q.CreatedAt, &q.AcceptedAt, &q.RejectedAt, &q.CreatedBy, &q.CustomerNotes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrQuoteNotFound
		}
		return nil, err
	}
	q.FeeRecipient = FeeRecipient(feeRec)
	q.Status = QuoteStatus(status)

	if q.Status == QuoteStatusPending && time.Now().After(q.ExpiresAt) {
		q.Status = QuoteStatusExpired
	}

	return &q, nil
}

// AcceptQuote transitions a pending quote to 'accepted' if not expired.
// Is idempotent: accepting an already-accepted quote at the specified version succeeds.
func (s *Store) AcceptQuote(ctx context.Context, orderID string, version int) (*DeliveryQuote, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("order ID is required")
	}
	if version <= 0 {
		return nil, fmt.Errorf("%w: version must be positive", ErrInvalidQuote)
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Check order existence and status
	var paymentStatus string
	err = tx.QueryRow(ctx, `SELECT "paymentStatus" FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&paymentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	if paymentStatus != "PENDING" {
		return nil, ErrOrderNotPending
	}

	var q DeliveryQuote
	var feeRec string
	var status string
	err = tx.QueryRow(ctx, `
		SELECT id, order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_at, accepted_at, rejected_at, created_by, COALESCE(customer_notes, '')
		FROM public.delivery_quote
		WHERE order_id = $1 AND version = $2
		FOR UPDATE
	`, orderID, version).Scan(
		&q.ID, &q.OrderID, &q.Version, &q.AmountKES, &feeRec, &q.Arrangement, &status,
		&q.ExpiresAt, &q.CreatedAt, &q.AcceptedAt, &q.RejectedAt, &q.CreatedBy, &q.CustomerNotes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrQuoteNotFound
		}
		return nil, err
	}
	q.FeeRecipient = FeeRecipient(feeRec)
	q.Status = QuoteStatus(status)

	if q.Status == QuoteStatusAccepted {
		// Idempotent: already accepted
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &q, nil
	}

	var latestVersion int
	err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM public.delivery_quote WHERE order_id = $1`, orderID).Scan(&latestVersion)
	if err != nil {
		return nil, err
	}
	if version < latestVersion {
		return nil, fmt.Errorf("%w: quote version %d is superseded by version %d", ErrQuoteConflict, version, latestVersion)
	}

	if q.Status != QuoteStatusPending {
		return nil, fmt.Errorf("%w: quote status is %s", ErrQuoteNotPending, q.Status)
	}

	now := time.Now()
	if now.After(q.ExpiresAt) {
		if _, err := tx.Exec(ctx, `UPDATE public.delivery_quote SET status = 'expired' WHERE id = $1`, q.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrQuoteExpired
	}

	err = tx.QueryRow(ctx, `
		UPDATE public.delivery_quote
		SET status = 'accepted', accepted_at = now()
		WHERE id = $1
		RETURNING id, order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_at, accepted_at, rejected_at, created_by, COALESCE(customer_notes, '')
	`, q.ID).Scan(
		&q.ID, &q.OrderID, &q.Version, &q.AmountKES, &feeRec, &q.Arrangement, &status,
		&q.ExpiresAt, &q.CreatedAt, &q.AcceptedAt, &q.RejectedAt, &q.CreatedBy, &q.CustomerNotes,
	)
	if err != nil {
		return nil, err
	}
	q.FeeRecipient = FeeRecipient(feeRec)
	q.Status = QuoteStatus(status)

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &q, nil
}

// RejectQuote transitions a pending quote to 'rejected' with customer-provided reason.
// Idempotent: rejecting an already-rejected quote at the specified version succeeds.
func (s *Store) RejectQuote(ctx context.Context, orderID string, version int, reason string) (*DeliveryQuote, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("order ID is required")
	}
	if version <= 0 {
		return nil, fmt.Errorf("%w: version must be positive", ErrInvalidQuote)
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var q DeliveryQuote
	var feeRec string
	var status string
	err = tx.QueryRow(ctx, `
		SELECT id, order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_at, accepted_at, rejected_at, created_by, COALESCE(customer_notes, '')
		FROM public.delivery_quote
		WHERE order_id = $1 AND version = $2
		FOR UPDATE
	`, orderID, version).Scan(
		&q.ID, &q.OrderID, &q.Version, &q.AmountKES, &feeRec, &q.Arrangement, &status,
		&q.ExpiresAt, &q.CreatedAt, &q.AcceptedAt, &q.RejectedAt, &q.CreatedBy, &q.CustomerNotes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrQuoteNotFound
		}
		return nil, err
	}
	q.FeeRecipient = FeeRecipient(feeRec)
	q.Status = QuoteStatus(status)

	if q.Status == QuoteStatusRejected {
		// Idempotent: already rejected
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &q, nil
	}

	var latestVersion int
	err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM public.delivery_quote WHERE order_id = $1`, orderID).Scan(&latestVersion)
	if err != nil {
		return nil, err
	}
	if version < latestVersion {
		return nil, fmt.Errorf("%w: quote version %d is superseded by version %d", ErrQuoteConflict, version, latestVersion)
	}

	if q.Status != QuoteStatusPending {
		return nil, fmt.Errorf("%w: quote status is %s", ErrQuoteNotPending, q.Status)
	}

	updatedNotes := strings.TrimSpace(reason)
	if q.CustomerNotes != "" && updatedNotes != "" {
		updatedNotes = q.CustomerNotes + " | Rejection: " + updatedNotes
	} else if q.CustomerNotes != "" {
		updatedNotes = q.CustomerNotes
	}

	err = tx.QueryRow(ctx, `
		UPDATE public.delivery_quote
		SET status = 'rejected', rejected_at = now(), customer_notes = $2
		WHERE id = $1
		RETURNING id, order_id, version, amount_kes, fee_recipient, arrangement, status, expires_at, created_at, accepted_at, rejected_at, created_by, COALESCE(customer_notes, '')
	`, q.ID, updatedNotes).Scan(
		&q.ID, &q.OrderID, &q.Version, &q.AmountKES, &feeRec, &q.Arrangement, &status,
		&q.ExpiresAt, &q.CreatedAt, &q.AcceptedAt, &q.RejectedAt, &q.CreatedBy, &q.CustomerNotes,
	)
	if err != nil {
		return nil, err
	}
	q.FeeRecipient = FeeRecipient(feeRec)
	q.Status = QuoteStatus(status)

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &q, nil
}

// ExpireQuotes marks all expired pending quotes as 'expired'.
func (s *Store) ExpireQuotes(ctx context.Context, asOf time.Time) (int64, error) {
	res, err := s.Pool.Exec(ctx, `
		UPDATE public.delivery_quote
		SET status = 'expired'
		WHERE status = 'pending' AND expires_at <= $1
	`, asOf)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

// CalculatePayable computes the reconciled item and transport amounts.
// Operating Rule 2:
// - Direct courier transport fees are paid by customer to courier upon arrival (merchant STK excludes courier fee).
// - Merchant transport fees are collected upfront alongside items.
// Operating Rule 3:
// - Customer quote acceptance is required before STK initiation or payment confirmation.
func CalculatePayable(itemsSubtotalKES int, quote *DeliveryQuote, isPickup bool) (*PayableBreakdown, error) {
	if itemsSubtotalKES < 0 {
		return nil, fmt.Errorf("%w: items subtotal cannot be negative", ErrInvalidQuote)
	}

	if isPickup {
		return &PayableBreakdown{
			ItemsSubtotalKES:   itemsSubtotalKES,
			TransportFeeKES:    0,
			FeeRecipient:       FeeRecipientMerchant,
			PayableMerchantKES: itemsSubtotalKES,
			CustomerDirectKES:  0,
			QuoteVersion:       0,
			QuoteStatus:        QuoteStatusAccepted,
		}, nil
	}

	if quote == nil {
		return nil, ErrDeliveryQuoteUnaccepted
	}

	if quote.Status != QuoteStatusAccepted {
		return nil, fmt.Errorf("%w: current quote status is %s", ErrDeliveryQuoteUnaccepted, quote.Status)
	}

	if time.Now().After(quote.ExpiresAt) {
		return nil, fmt.Errorf("%w: accepted quote has expired", ErrQuoteExpired)
	}

	switch quote.FeeRecipient {
	case FeeRecipientCourier:
		return &PayableBreakdown{
			ItemsSubtotalKES:   itemsSubtotalKES,
			TransportFeeKES:    quote.AmountKES,
			FeeRecipient:       FeeRecipientCourier,
			PayableMerchantKES: itemsSubtotalKES, // Courier transport fee is excluded from merchant collection!
			CustomerDirectKES:  quote.AmountKES,  // Paid directly to rider on arrival
			QuoteVersion:       quote.Version,
			QuoteStatus:        QuoteStatusAccepted,
		}, nil
	case FeeRecipientMerchant:
		return &PayableBreakdown{
			ItemsSubtotalKES:   itemsSubtotalKES,
			TransportFeeKES:    quote.AmountKES,
			FeeRecipient:       FeeRecipientMerchant,
			PayableMerchantKES: itemsSubtotalKES + quote.AmountKES, // Merchant collects transport fee
			CustomerDirectKES:  0,
			QuoteVersion:       quote.Version,
			QuoteStatus:        QuoteStatusAccepted,
		}, nil
	default:
		return nil, fmt.Errorf("%w: unknown fee recipient %s", ErrInvalidQuote, quote.FeeRecipient)
	}
}

// ToPublic converts an internal DeliveryQuote into a sanitized PublicQuote.
func (q *DeliveryQuote) ToPublic() PublicQuote {
	return PublicQuote{
		OrderID:       q.OrderID,
		Version:       q.Version,
		AmountKES:     q.AmountKES,
		FeeRecipient:  q.FeeRecipient,
		Arrangement:   q.Arrangement,
		Status:        q.Status,
		ExpiresAt:     q.ExpiresAt,
		CreatedAt:     q.CreatedAt,
		AcceptedAt:    q.AcceptedAt,
		CustomerNotes: q.CustomerNotes,
	}
}
