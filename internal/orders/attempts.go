package orders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/outbox"
)

// CreatePaymentAttempt registers a new payment attempt for an order under strict safety constraints.
// It enforces that:
// 1. The order is not already PAID.
// 2. The stock reservation is active and unexpired.
// 3. No unresolved or in-flight payment attempt is active within the provider timeout window (2 minutes).
func (s *Store) CreatePaymentAttempt(ctx context.Context, orderID, phone string, amountKes int, provider string) (*PaymentAttempt, error) {
	if orderID == "" {
		return nil, ErrOrderNotFound
	}
	if phone == "" {
		return nil, ErrInvalidCustomer
	}
	if amountKes <= 0 {
		return nil, errors.New("payment attempt amount must be greater than zero")
	}
	if provider == "" {
		provider = "mpesa_stk"
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	// 1. Verify order state with row lock to serialize concurrent attempts and prevent race conditions
	var paymentStatus string
	var totalKes int
	err = tx.QueryRow(ctx, `SELECT "paymentStatus", "totalKes" FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&paymentStatus, &totalKes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to query order: %w", err)
	}
	if paymentStatus == PaymentPaid {
		return nil, ErrOrderAlreadyPaid
	}

	// 2. Verify stock reservation is active and unexpired
	var resStatus string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at FROM public.stock_reservation WHERE order_id = $1`, orderID).Scan(&resStatus, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReservationNotFound
		}
		return nil, fmt.Errorf("failed to query reservation: %w", err)
	}
	if resStatus != ReservationActive || expiresAt.Before(time.Now()) {
		return nil, ErrOrderReservationExpired
	}

	// 3. Query existing attempts with row locking
	rows, err := tx.Query(ctx, `
		SELECT id, attempt_number, status, created_at
		FROM public.payment_attempt
		WHERE order_id = $1
		ORDER BY attempt_number DESC
		LIMIT 1
		FOR UPDATE
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query previous attempts: %w", err)
	}
	defer rows.Close()

	nextAttemptNum := 1
	if rows.Next() {
		var prevID string
		var prevNum int
		var prevStatus string
		var prevCreatedAt time.Time
		if err := rows.Scan(&prevID, &prevNum, &prevStatus, &prevCreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan previous attempt: %w", err)
		}
		rows.Close()

		// Unresolved attempt safety check:
		// Never blindly reinitiate an active, in-flight, or ambiguous charge within the provider resolution window (2 minutes)
		if (prevStatus == AttemptCreated || prevStatus == AttemptInitiating || prevStatus == AttemptAccepted || prevStatus == AttemptUnknown) && time.Since(prevCreatedAt) < 2*time.Minute {
			return nil, ErrUnresolvedPaymentAttempt
		}

		nextAttemptNum = prevNum + 1
	} else {
		rows.Close()
	}

	var attempt PaymentAttempt
	var pTxID, pRefID, errMsg *string

	err = tx.QueryRow(ctx, `
		INSERT INTO public.payment_attempt (
			order_id, attempt_number, amount_kes, phone, status, provider, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		RETURNING id, order_id, attempt_number, amount_kes, phone, status, provider, provider_transaction_id, provider_reference_id, error_message, created_at, updated_at
	`, orderID, nextAttemptNum, amountKes, phone, AttemptCreated, provider).Scan(
		&attempt.ID,
		&attempt.OrderID,
		&attempt.AttemptNumber,
		&attempt.AmountKes,
		&attempt.Phone,
		&attempt.Status,
		&attempt.Provider,
		&pTxID,
		&pRefID,
		&errMsg,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert payment attempt: %w", err)
	}

	if pTxID != nil {
		attempt.ProviderTransactionID = *pTxID
	}
	if pRefID != nil {
		attempt.ProviderReferenceID = *pRefID
	}
	if errMsg != nil {
		attempt.ErrorMessage = *errMsg
	}

	// Transactional outbox event enqueue
	evt, err := events.NewEvent(events.TypePaymentAttempt, orderID, nextAttemptNum, "", events.PaymentAttemptPayload{
		AttemptID:     attempt.ID,
		OrderID:       orderID,
		AttemptNumber: nextAttemptNum,
		AmountKes:     amountKes,
		Phone:         phone,
		Provider:      provider,
		Status:        AttemptCreated,
		Timestamp:     attempt.CreatedAt,
	})
	if err != nil {
		rollback(tx)
		return nil, fmt.Errorf("failed to construct payment attempt event: %w", err)
	}
	if err := outbox.NewStore(s.Pool).Enqueue(ctx, tx, evt, "rabbitmq"); err != nil {
		rollback(tx)
		return nil, fmt.Errorf("failed to enqueue payment attempt outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit payment attempt transaction: %w", err)
	}

	return &attempt, nil
}

// GetLatestPaymentAttempt retrieves the most recent payment attempt for an order.
func (s *Store) GetLatestPaymentAttempt(ctx context.Context, orderID string) (*PaymentAttempt, error) {
	var attempt PaymentAttempt
	var pTxID, pRefID, errMsg *string

	err := s.Pool.QueryRow(ctx, `
		SELECT id, order_id, attempt_number, amount_kes, phone, status, provider, provider_transaction_id, provider_reference_id, error_message, created_at, updated_at
		FROM public.payment_attempt
		WHERE order_id = $1
		ORDER BY attempt_number DESC
		LIMIT 1
	`, orderID).Scan(
		&attempt.ID,
		&attempt.OrderID,
		&attempt.AttemptNumber,
		&attempt.AmountKes,
		&attempt.Phone,
		&attempt.Status,
		&attempt.Provider,
		&pTxID,
		&pRefID,
		&errMsg,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttemptNotFound
		}
		return nil, fmt.Errorf("failed to query latest payment attempt: %w", err)
	}

	if pTxID != nil {
		attempt.ProviderTransactionID = *pTxID
	}
	if pRefID != nil {
		attempt.ProviderReferenceID = *pRefID
	}
	if errMsg != nil {
		attempt.ErrorMessage = *errMsg
	}

	return &attempt, nil
}

// GetPaymentAttempts returns all payment attempts recorded for an order.
func (s *Store) GetPaymentAttempts(ctx context.Context, orderID string) ([]PaymentAttempt, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, order_id, attempt_number, amount_kes, phone, status, provider, provider_transaction_id, provider_reference_id, error_message, created_at, updated_at
		FROM public.payment_attempt
		WHERE order_id = $1
		ORDER BY attempt_number ASC
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query payment attempts: %w", err)
	}
	defer rows.Close()

	var attempts []PaymentAttempt
	for rows.Next() {
		var a PaymentAttempt
		var pTxID, pRefID, errMsg *string
		if err := rows.Scan(
			&a.ID,
			&a.OrderID,
			&a.AttemptNumber,
			&a.AmountKes,
			&a.Phone,
			&a.Status,
			&a.Provider,
			&pTxID,
			&pRefID,
			&errMsg,
			&a.CreatedAt,
			&a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment attempt: %w", err)
		}
		if pTxID != nil {
			a.ProviderTransactionID = *pTxID
		}
		if pRefID != nil {
			a.ProviderReferenceID = *pRefID
		}
		if errMsg != nil {
			a.ErrorMessage = *errMsg
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}

// UpdatePaymentAttempt updates the status, provider identifiers, and optional error of a payment attempt.
func (s *Store) UpdatePaymentAttempt(ctx context.Context, attemptID, status, providerTxID, providerRefID, errorMsg string) error {
	var pTxID, pRefID, errMsg *string
	if providerTxID != "" {
		pTxID = &providerTxID
	}
	if providerRefID != "" {
		pRefID = &providerRefID
	}
	if errorMsg != "" {
		errMsg = &errorMsg
	}

	tag, err := s.Pool.Exec(ctx, `
		UPDATE public.payment_attempt
		SET status = $1, provider_transaction_id = coalesce($2, provider_transaction_id), provider_reference_id = coalesce($3, provider_reference_id), error_message = coalesce($4, error_message), updated_at = now()
		WHERE id = $5
	`, status, pTxID, pRefID, errMsg, attemptID)
	if err != nil {
		return fmt.Errorf("failed to update payment attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAttemptNotFound
	}
	return nil
}

// FindPaymentAttemptByProviderTx finds a payment attempt by its provider transaction ID (e.g. NCBA TransactionID or Daraja CheckoutRequestID).
func (s *Store) FindPaymentAttemptByProviderTx(ctx context.Context, providerTxID string) (*PaymentAttempt, error) {
	if providerTxID == "" {
		return nil, ErrAttemptNotFound
	}

	var attempt PaymentAttempt
	var pTxID, pRefID, errMsg *string

	err := s.Pool.QueryRow(ctx, `
		SELECT id, order_id, attempt_number, amount_kes, phone, status, provider, provider_transaction_id, provider_reference_id, error_message, created_at, updated_at
		FROM public.payment_attempt
		WHERE provider_transaction_id = $1 OR provider_reference_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, providerTxID).Scan(
		&attempt.ID,
		&attempt.OrderID,
		&attempt.AttemptNumber,
		&attempt.AmountKes,
		&attempt.Phone,
		&attempt.Status,
		&attempt.Provider,
		&pTxID,
		&pRefID,
		&errMsg,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttemptNotFound
		}
		return nil, fmt.Errorf("failed to query payment attempt by provider tx id: %w", err)
	}

	if pTxID != nil {
		attempt.ProviderTransactionID = *pTxID
	}
	if pRefID != nil {
		attempt.ProviderReferenceID = *pRefID
	}
	if errMsg != nil {
		attempt.ErrorMessage = *errMsg
	}

	return &attempt, nil
}

// FindInFlightPaymentAttempt finds the most recent active or uncompleted attempt matching phone and amount.
func (s *Store) FindInFlightPaymentAttempt(ctx context.Context, phone string, amountKes int, within time.Duration) (*PaymentAttempt, error) {
	if phone == "" || amountKes <= 0 {
		return nil, ErrAttemptNotFound
	}
	cutoff := time.Now().Add(-within)

	var attempt PaymentAttempt
	var pTxID, pRefID, errMsg *string

	err := s.Pool.QueryRow(ctx, `
		SELECT id, order_id, attempt_number, amount_kes, phone, status, provider, provider_transaction_id, provider_reference_id, error_message, created_at, updated_at
		FROM public.payment_attempt
		WHERE phone = $1 AND amount_kes = $2 AND status IN ('created', 'initiating', 'accepted', 'unknown') AND created_at >= $3
		ORDER BY created_at DESC
		LIMIT 1
	`, phone, amountKes, cutoff).Scan(
		&attempt.ID,
		&attempt.OrderID,
		&attempt.AttemptNumber,
		&attempt.AmountKes,
		&attempt.Phone,
		&attempt.Status,
		&attempt.Provider,
		&pTxID,
		&pRefID,
		&errMsg,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttemptNotFound
		}
		return nil, fmt.Errorf("failed to query in-flight payment attempt: %w", err)
	}

	if pTxID != nil {
		attempt.ProviderTransactionID = *pTxID
	}
	if pRefID != nil {
		attempt.ProviderReferenceID = *pRefID
	}
	if errMsg != nil {
		attempt.ErrorMessage = *errMsg
	}

	return &attempt, nil
}

// FindReceiptOrder checks if an M-Pesa receipt number has already been recorded for any order.
func (s *Store) FindReceiptOrder(ctx context.Context, receiptNumber string) (string, error) {
	if receiptNumber == "" {
		return "", nil
	}

	var orderID string
	err := s.Pool.QueryRow(ctx, `
		SELECT "orderId"
		FROM public."MpesaTransaction"
		WHERE "mpesaReceiptNumber" = $1 AND status = 'SUCCESS'
		LIMIT 1
	`, receiptNumber).Scan(&orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("failed to query receipt in mpesa transactions: %w", err)
	}

	return orderID, nil
}
