package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/outbox"
)

// RecordPaymentException records an actionable payment exception into the durable payment_exception ledger.
func (s *Store) RecordPaymentException(ctx context.Context, exc PaymentException) (*PaymentException, error) {
	if exc.ExceptionType == "" {
		return nil, errors.New("exception type is required")
	}
	if exc.Provider == "" {
		exc.Provider = "ncba"
	}
	if exc.Status == "" {
		exc.Status = ExceptionStatusOpen
	}

	metaBytes, err := json.Marshal(exc.Metadata)
	if err != nil {
		metaBytes = []byte("{}")
	}

	var res PaymentException
	var ordID, attID, pTxID, rcpt, phone, resAction, resNotes, resBy, ev *string
	var recAmt, expAmt *int
	var resAt *time.Time
	var rawMeta []byte

	err = s.Pool.QueryRow(ctx, `
		INSERT INTO public.payment_exception (
			order_id, attempt_id, exception_type, provider, provider_transaction_id,
			receipt_number, received_amount_kes, expected_amount_kes, customer_phone,
			status, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
		RETURNING id, order_id, attempt_id, exception_type, provider, provider_transaction_id,
			receipt_number, received_amount_kes, expected_amount_kes, customer_phone,
			status, resolution_action, resolution_notes, resolved_by, evidence,
			metadata, created_at, updated_at, resolved_at
	`, exc.OrderID, exc.AttemptID, exc.ExceptionType, exc.Provider, exc.ProviderTransactionID,
		exc.ReceiptNumber, exc.ReceivedAmountKes, exc.ExpectedAmountKes, exc.CustomerPhone,
		exc.Status, metaBytes).Scan(
		&res.ID,
		&ordID,
		&attID,
		&res.ExceptionType,
		&res.Provider,
		&pTxID,
		&rcpt,
		&recAmt,
		&expAmt,
		&phone,
		&res.Status,
		&resAction,
		&resNotes,
		&resBy,
		&ev,
		&rawMeta,
		&res.CreatedAt,
		&res.UpdatedAt,
		&resAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert payment exception: %w", err)
	}

	res.OrderID = ordID
	res.AttemptID = attID
	res.ProviderTransactionID = pTxID
	res.ReceiptNumber = rcpt
	res.ReceivedAmountKes = recAmt
	res.ExpectedAmountKes = expAmt
	res.CustomerPhone = phone
	res.ResolutionAction = resAction
	res.ResolutionNotes = resNotes
	res.ResolvedBy = resBy
	res.Evidence = ev
	res.ResolvedAt = resAt
	if len(rawMeta) > 0 {
		_ = json.Unmarshal(rawMeta, &res.Metadata)
	}

	return &res, nil
}

// GetPaymentExceptions lists payment exceptions with optional filtering by status.
func (s *Store) GetPaymentExceptions(ctx context.Context, filter ExceptionFilter) ([]PaymentException, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, order_id, attempt_id, exception_type, provider, provider_transaction_id,
			receipt_number, received_amount_kes, expected_amount_kes, customer_phone,
			status, resolution_action, resolution_notes, resolved_by, evidence,
			metadata, created_at, updated_at, resolved_at
		FROM public.payment_exception
	`
	var args []any
	if filter.Status != "" && filter.Status != "all" {
		query += " WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3"
		args = append(args, filter.Status, limit, offset)
	} else {
		query += " ORDER BY created_at DESC LIMIT $1 OFFSET $2"
		args = append(args, limit, offset)
	}

	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query payment exceptions: %w", err)
	}
	defer rows.Close()

	var exceptions []PaymentException
	for rows.Next() {
		var e PaymentException
		var ordID, attID, pTxID, rcpt, phone, resAction, resNotes, resBy, ev *string
		var recAmt, expAmt *int
		var resAt *time.Time
		var rawMeta []byte

		if err := rows.Scan(
			&e.ID,
			&ordID,
			&attID,
			&e.ExceptionType,
			&e.Provider,
			&pTxID,
			&rcpt,
			&recAmt,
			&expAmt,
			&phone,
			&e.Status,
			&resAction,
			&resNotes,
			&resBy,
			&ev,
			&rawMeta,
			&e.CreatedAt,
			&e.UpdatedAt,
			&resAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment exception: %w", err)
		}

		e.OrderID = ordID
		e.AttemptID = attID
		e.ProviderTransactionID = pTxID
		e.ReceiptNumber = rcpt
		e.ReceivedAmountKes = recAmt
		e.ExpectedAmountKes = expAmt
		e.CustomerPhone = phone
		e.ResolutionAction = resAction
		e.ResolutionNotes = resNotes
		e.ResolvedBy = resBy
		e.Evidence = ev
		e.ResolvedAt = resAt
		if len(rawMeta) > 0 {
			_ = json.Unmarshal(rawMeta, &e.Metadata)
		}
		exceptions = append(exceptions, e)
	}

	return exceptions, nil
}

// GetPaymentExceptionByID fetches a single payment exception by ID.
func (s *Store) GetPaymentExceptionByID(ctx context.Context, id string) (*PaymentException, error) {
	var e PaymentException
	var ordID, attID, pTxID, rcpt, phone, resAction, resNotes, resBy, ev *string
	var recAmt, expAmt *int
	var resAt *time.Time
	var rawMeta []byte

	err := s.Pool.QueryRow(ctx, `
		SELECT id, order_id, attempt_id, exception_type, provider, provider_transaction_id,
			receipt_number, received_amount_kes, expected_amount_kes, customer_phone,
			status, resolution_action, resolution_notes, resolved_by, evidence,
			metadata, created_at, updated_at, resolved_at
		FROM public.payment_exception
		WHERE id = $1
	`, id).Scan(
		&e.ID,
		&ordID,
		&attID,
		&e.ExceptionType,
		&e.Provider,
		&pTxID,
		&rcpt,
		&recAmt,
		&expAmt,
		&phone,
		&e.Status,
		&resAction,
		&resNotes,
		&resBy,
		&ev,
		&rawMeta,
		&e.CreatedAt,
		&e.UpdatedAt,
		&resAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrExceptionNotFound
		}
		return nil, fmt.Errorf("failed to query payment exception %s: %w", id, err)
	}

	e.OrderID = ordID
	e.AttemptID = attID
	e.ProviderTransactionID = pTxID
	e.ReceiptNumber = rcpt
	e.ReceivedAmountKes = recAmt
	e.ExpectedAmountKes = expAmt
	e.CustomerPhone = phone
	e.ResolutionAction = resAction
	e.ResolutionNotes = resNotes
	e.ResolvedBy = resBy
	e.Evidence = ev
	e.ResolvedAt = resAt
	if len(rawMeta) > 0 {
		_ = json.Unmarshal(rawMeta, &e.Metadata)
	}

	return &e, nil
}

// ResolvePaymentException updates an exception to resolved or dismissed with operator notes and evidence.
func (s *Store) ResolvePaymentException(ctx context.Context, id string, req ResolveExceptionRequest) error {
	if strings.TrimSpace(req.Action) == "" {
		return errors.New("resolution action is required")
	}
	if strings.TrimSpace(req.AuthorizedBy) == "" {
		return errors.New("authorizedBy is required")
	}
	if strings.TrimSpace(req.Notes) == "" {
		return errors.New("resolution notes are required")
	}

	status := ExceptionStatusResolved
	if req.Action == "voided" || req.Action == "dismissed_duplicate" {
		status = ExceptionStatusDismissed
	}

	var ev *string
	if req.Evidence != "" {
		ev = &req.Evidence
	}
	var ordID *string
	if req.OrderID != "" {
		ordID = &req.OrderID
	}

	tag, err := s.Pool.Exec(ctx, `
		UPDATE public.payment_exception
		SET status = $1, resolution_action = $2, resolution_notes = $3, resolved_by = $4,
			evidence = coalesce($5, evidence), order_id = coalesce($6, order_id),
			updated_at = now(), resolved_at = now()
		WHERE id = $7 AND status = 'open'
	`, status, req.Action, req.Notes, req.AuthorizedBy, ev, ordID, id)
	if err != nil {
		return fmt.Errorf("failed to resolve payment exception %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		// Check whether it was not found or already resolved
		var currentStatus string
		if err := s.Pool.QueryRow(ctx, `SELECT status FROM public.payment_exception WHERE id = $1`, id).Scan(&currentStatus); err == nil {
			if currentStatus != ExceptionStatusOpen {
				return ErrExceptionAlreadyResolved
			}
		}
		return ErrExceptionNotFound
	}

	return nil
}

// ListUnresolvedPaymentAttempts returns payment attempts in 'accepted' or 'unknown' status
// that are due for query reconciliation.
func (s *Store) ListUnresolvedPaymentAttempts(ctx context.Context, staleThreshold time.Duration, limit int) ([]PaymentAttempt, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	cutoff := time.Now().Add(-staleThreshold)

	rows, err := s.Pool.Query(ctx, `
		SELECT id, order_id, attempt_number, amount_kes, phone, status, provider,
			provider_transaction_id, provider_reference_id, error_message, query_count,
			last_queried_at, next_query_at, created_at, updated_at
		FROM public.payment_attempt
		WHERE status IN ('accepted', 'unknown')
			AND created_at <= $1
			AND query_count < 5
			AND (next_query_at IS NULL OR next_query_at <= now())
		ORDER BY created_at ASC
		LIMIT $2
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query unresolved payment attempts: %w", err)
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
			&a.QueryCount,
			&a.LastQueriedAt,
			&a.NextQueryAt,
			&a.CreatedAt,
			&a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan unresolved payment attempt: %w", err)
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

// UpdatePaymentAttemptQuerySchedule updates an attempt's query count and schedules the next query retry.
func (s *Store) UpdatePaymentAttemptQuerySchedule(ctx context.Context, attemptID string, nextQueryAt *time.Time, lastError string) error {
	var errStr *string
	if lastError != "" {
		errStr = &lastError
	}

	tag, err := s.Pool.Exec(ctx, `
		UPDATE public.payment_attempt
		SET query_count = query_count + 1,
			last_queried_at = now(),
			next_query_at = $1,
			error_message = coalesce($2, error_message),
			updated_at = now()
		WHERE id = $3
	`, nextQueryAt, errStr, attemptID)
	if err != nil {
		return fmt.Errorf("failed to update payment attempt query schedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAttemptNotFound
	}
	return nil
}

// RecordManualSettlement records a verified pay-later settlement.
// Invariant: Never arbitrary paid-status edits. Requires valid stock reservation,
// mandatory provider evidence, operator authorization, and receipt uniqueness.
func (s *Store) RecordManualSettlement(ctx context.Context, req ManualSettlementRequest) (*Order, *PaymentAuditRecord, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return nil, nil, errors.New("order ID is required")
	}
	if strings.TrimSpace(req.ReceiptNumber) == "" {
		return nil, nil, errors.New("receipt number is required as provider evidence")
	}
	if req.AmountKes <= 0 {
		return nil, nil, errors.New("settlement amount must be positive")
	}
	if strings.TrimSpace(req.Evidence) == "" {
		return nil, nil, errors.New("evidence is required (bank statement, transaction reference)")
	}
	if strings.TrimSpace(req.AuthorizedBy) == "" {
		return nil, nil, errors.New("authorizedBy is required")
	}
	if strings.TrimSpace(req.Notes) == "" {
		return nil, nil, errors.New("settlement justification notes are required")
	}
	if req.Provider == "" {
		req.Provider = "ncba"
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	now := time.Now().UTC()

	// 1. Serialize concurrent settlements on the same receipt using transaction-scoped advisory lock
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, req.ReceiptNumber); err != nil {
		return nil, nil, fmt.Errorf("failed to acquire receipt lock: %w", err)
	}

	// Receipt uniqueness check inside the transaction
	var existingOrderID string
	err = tx.QueryRow(ctx, `
		SELECT "orderId"
		FROM public."MpesaTransaction"
		WHERE "mpesaReceiptNumber" = $1 AND status = 'SUCCESS'
		LIMIT 1
	`, req.ReceiptNumber).Scan(&existingOrderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("receipt uniqueness check failed: %w", err)
	}
	if existingOrderID != "" && existingOrderID != req.OrderID {
		return nil, nil, fmt.Errorf("%w: receipt %s already paid order %s", ErrDuplicateReceipt, req.ReceiptNumber, existingOrderID)
	}

	// 2. Lock stock_reservation first, then Order (consistent locking order)
	var resID, resStatus string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT id, status, expires_at FROM public.stock_reservation WHERE order_id = $1 FOR UPDATE`, req.OrderID).Scan(&resID, &resStatus, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrReservationNotFound
		}
		return nil, nil, fmt.Errorf("failed to query reservation: %w", err)
	}

	var currentPaymentStatus, deliveryOption string
	var totalKes int
	err = tx.QueryRow(ctx, `SELECT "paymentStatus", COALESCE("deliveryOption", 'delivery'), "totalKes" FROM public."Order" WHERE id = $1 FOR UPDATE`, req.OrderID).Scan(&currentPaymentStatus, &deliveryOption, &totalKes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrOrderNotFound
		}
		return nil, nil, fmt.Errorf("failed to query order: %w", err)
	}
	if currentPaymentStatus == PaymentPaid {
		return nil, nil, ErrOrderAlreadyPaid
	}

	// 3. Expected payable verification & quote validity check
	opt := strings.ToLower(strings.TrimSpace(deliveryOption))
	isPickup := opt == "pickup" || opt == "showroom_pickup"
	expectedPayable := totalKes
	if !isPickup {
		var quoteStatus string
		var quoteExpiresAt time.Time
		var quoteAmount int
		var feeRecipient string
		err = tx.QueryRow(ctx, `
			SELECT status, expires_at, amount_kes, fee_recipient
			FROM public.delivery_quote
			WHERE order_id = $1
			ORDER BY version DESC
			LIMIT 1
		`, req.OrderID).Scan(&quoteStatus, &quoteExpiresAt, &quoteAmount, &feeRecipient)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, ErrDeliveryQuoteUnaccepted
			}
			return nil, nil, fmt.Errorf("failed to query delivery quote for order %s: %w", req.OrderID, err)
		}
		if quoteStatus != "accepted" {
			return nil, nil, ErrDeliveryQuoteUnaccepted
		}
		if now.After(quoteExpiresAt) {
			return nil, nil, ErrQuoteExpired
		}
		if feeRecipient == "merchant" {
			expectedPayable = totalKes + quoteAmount
		}
	}

	if req.AmountKes != expectedPayable {
		return nil, nil, fmt.Errorf("%w: settlement amount %d KES does not match payable total %d KES", ErrPayableMismatch, req.AmountKes, expectedPayable)
	}

	// 4. Invariant: Stock Reservation must be active & unexpired before marking order PAID!
	if resStatus != ReservationActive || expiresAt.Before(now) {
		return nil, nil, fmt.Errorf("%w: cannot mark order paid without active stock hold; please reconcile stock", ErrOrderReservationExpired)
	}

	// 5. Commit Reservation
	_, err = tx.Exec(ctx, `UPDATE public.stock_reservation SET status = $1 WHERE id = $2`, ReservationCommitted, resID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to commit reservation: %w", err)
	}

	// 6. Record sale in immutable stock ledger (stock_batch & stock_entry)
	resItemRows, err := tx.Query(ctx, `SELECT product_id, quantity FROM public.stock_reservation_item WHERE reservation_id = $1 ORDER BY product_id`, resID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query reservation items: %w", err)
	}
	defer resItemRows.Close()

	var batchLines []catalog.Line
	for resItemRows.Next() {
		var pID string
		var qty int
		if err := resItemRows.Scan(&pID, &qty); err != nil {
			return nil, nil, err
		}
		batchLines = append(batchLines, catalog.Line{
			ProductID: pID,
			Quantity:  qty,
		})
	}
	resItemRows.Close()

	if len(batchLines) > 0 {
		batchID, err := generateID("batch")
		if err != nil {
			return nil, nil, err
		}
		batch := catalog.Batch{
			ID:       batchID,
			Kind:     "local_sale",
			Actor:    req.AuthorizedBy,
			Evidence: req.Evidence,
			Lines:    batchLines,
		}
		rawBatch, err := json.Marshal(batch)
		if err != nil {
			return nil, nil, err
		}
		hash := sha256.Sum256(rawBatch)
		digest := hex.EncodeToString(hash[:])

		_, err = tx.Exec(ctx, `INSERT INTO public.stock_batch (id, digest, kind, actor, evidence, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			batch.ID, digest, batch.Kind, batch.Actor, batch.Evidence, now)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to insert stock batch: %w", err)
		}

		for _, l := range batch.Lines {
			var currentStock int
			if err := tx.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id = $1 FOR UPDATE`, l.ProductID).Scan(&currentStock); err != nil {
				return nil, nil, fmt.Errorf("failed to query product stock: %w", err)
			}
			newBalance := currentStock - l.Quantity
			if newBalance < 0 {
				return nil, nil, fmt.Errorf("insufficient stock for product %s on manual settlement", l.ProductID)
			}
			if _, err := tx.Exec(ctx, `UPDATE public.catalog_product SET stock = $1 WHERE id = $2`, newBalance, l.ProductID); err != nil {
				return nil, nil, fmt.Errorf("failed to update product stock: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO public.stock_entry (batch_id, product_id, delta, balance) VALUES ($1, $2, $3, $4)`,
				batch.ID, l.ProductID, -l.Quantity, newBalance); err != nil {
				return nil, nil, fmt.Errorf("failed to insert stock entry: %w", err)
			}
		}
	}

	// 7. Mark Order as PAID
	_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`, PaymentPaid, now, req.OrderID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to mark order as PAID: %w", err)
	}

	// 8. Record MpesaTransaction
	mpesaID, err := generateID("mpesa")
	if err != nil {
		return nil, nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public."MpesaTransaction" (id, "createdAt", "updatedAt", "orderId", "mpesaReceiptNumber", status)
		VALUES ($1, $2, $2, $3, $4, 'SUCCESS')
		ON CONFLICT ("orderId") DO UPDATE SET "mpesaReceiptNumber" = $4, status = 'SUCCESS', "updatedAt" = $2`,
		mpesaID, now, req.OrderID, req.ReceiptNumber)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to insert mpesa transaction: %w", err)
	}

	// 9. Conclude latest attempt or insert settlement attempt
	_, _ = tx.Exec(ctx, `
		UPDATE public.payment_attempt
		SET status = $1, provider_transaction_id = coalesce($2, provider_transaction_id), updated_at = $3
		WHERE id = (
			SELECT id FROM public.payment_attempt
			WHERE order_id = $4
			ORDER BY attempt_number DESC
			LIMIT 1
		)
	`, AttemptSucceeded, req.ReceiptNumber, now, req.OrderID)

	// 10. Record in public.payment_audit_ledger
	var audit PaymentAuditRecord
	err = tx.QueryRow(ctx, `
		INSERT INTO public.payment_audit_ledger (
			order_id, action, amount_kes, receipt_number, provider, evidence, notes, authorized_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		RETURNING id, order_id, action, amount_kes, receipt_number, provider, evidence, notes, authorized_by, created_at
	`, req.OrderID, AuditActionManualSettlement, req.AmountKes, req.ReceiptNumber, req.Provider, req.Evidence, req.Notes, req.AuthorizedBy).Scan(
		&audit.ID,
		&audit.OrderID,
		&audit.Action,
		&audit.AmountKes,
		&audit.ReceiptNumber,
		&audit.Provider,
		&audit.Evidence,
		&audit.Notes,
		&audit.AuthorizedBy,
		&audit.CreatedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to record payment audit entry: %w", err)
	}

	// 11. Automatically resolve any open exception for this order/receipt
	_, _ = tx.Exec(ctx, `
		UPDATE public.payment_exception
		SET status = 'resolved', resolution_action = 'applied_to_order', resolution_notes = $1,
			resolved_by = $2, evidence = coalesce($3, evidence), updated_at = now(), resolved_at = now()
		WHERE (order_id = $4 OR receipt_number = $5) AND status = 'open'
	`, "Resolved via authorized manual settlement", req.AuthorizedBy, req.Evidence, req.OrderID, req.ReceiptNumber)

	// 12. Enqueue transactional outbox event
	evt, err := events.NewEvent(events.TypeOrderPaid, req.OrderID, 2, "", events.OrderPaidPayload{
		OrderID:       req.OrderID,
		ReceiptNumber: req.ReceiptNumber,
		AmountKes:     req.AmountKes,
		PaidAt:        now,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to construct order paid event: %w", err)
	}
	if err := outbox.NewStore(s.Pool).Enqueue(ctx, tx, evt, "rabbitmq"); err != nil {
		return nil, nil, fmt.Errorf("failed to enqueue order paid outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to commit manual settlement: %w", err)
	}

	invalidateCatalogDetached(s.Invalidator)

	order, err := s.GetOrder(ctx, req.OrderID)
	if err != nil {
		return nil, nil, err
	}

	return order, &audit, nil
}

// RecordPaymentCorrection records an auditable duplicate or incorrect-charge correction.
func (s *Store) RecordPaymentCorrection(ctx context.Context, req PaymentCorrectionRequest) (*PaymentAuditRecord, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return nil, errors.New("order ID is required")
	}
	if strings.TrimSpace(req.Action) == "" {
		return nil, errors.New("action is required (e.g. duplicate_correction, amount_correction, refund_reconciliation)")
	}
	if strings.TrimSpace(req.ReceiptNumber) == "" {
		return nil, errors.New("receipt number is required as provider evidence")
	}
	if req.AmountKes <= 0 {
		return nil, errors.New("correction amount must be positive")
	}
	if strings.TrimSpace(req.Evidence) == "" {
		return nil, errors.New("evidence is required")
	}
	if strings.TrimSpace(req.AuthorizedBy) == "" {
		return nil, errors.New("authorizedBy is required")
	}
	if strings.TrimSpace(req.Notes) == "" {
		return nil, errors.New("justification notes are required")
	}
	if req.Provider == "" {
		req.Provider = "ncba"
	}

	// Verify order exists
	var dummy string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM public."Order" WHERE id = $1`, req.OrderID).Scan(&dummy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to check order: %w", err)
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	var audit PaymentAuditRecord
	err = tx.QueryRow(ctx, `
		INSERT INTO public.payment_audit_ledger (
			order_id, action, amount_kes, receipt_number, provider, evidence, notes, authorized_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		RETURNING id, order_id, action, amount_kes, receipt_number, provider, evidence, notes, authorized_by, created_at
	`, req.OrderID, req.Action, req.AmountKes, req.ReceiptNumber, req.Provider, req.Evidence, req.Notes, req.AuthorizedBy).Scan(
		&audit.ID,
		&audit.OrderID,
		&audit.Action,
		&audit.AmountKes,
		&audit.ReceiptNumber,
		&audit.Provider,
		&audit.Evidence,
		&audit.Notes,
		&audit.AuthorizedBy,
		&audit.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert audit ledger entry: %w", err)
	}

	// If linked exception ID is specified, resolve it
	if req.ExceptionID != nil && *req.ExceptionID != "" {
		_, err = tx.Exec(ctx, `
			UPDATE public.payment_exception
			SET status = 'resolved', resolution_action = $1, resolution_notes = $2,
				resolved_by = $3, evidence = $4, updated_at = now(), resolved_at = now()
			WHERE id = $5 AND status = 'open'
		`, req.Action, req.Notes, req.AuthorizedBy, req.Evidence, *req.ExceptionID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve linked exception: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit correction transaction: %w", err)
	}

	return &audit, nil
}

// ListPaymentAuditRecords returns audit ledger entries for an order or all recent entries.
func (s *Store) ListPaymentAuditRecords(ctx context.Context, orderID string) ([]PaymentAuditRecord, error) {
	query := `
		SELECT id, order_id, action, amount_kes, receipt_number, provider, evidence, notes, authorized_by, created_at
		FROM public.payment_audit_ledger
	`
	var args []any
	if orderID != "" {
		query += ` WHERE order_id = $1 ORDER BY created_at DESC LIMIT 50`
		args = append(args, orderID)
	} else {
		query += ` ORDER BY created_at DESC LIMIT 50`
	}

	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query payment audit ledger: %w", err)
	}
	defer rows.Close()

	var records []PaymentAuditRecord
	for rows.Next() {
		var r PaymentAuditRecord
		if err := rows.Scan(
			&r.ID,
			&r.OrderID,
			&r.Action,
			&r.AmountKes,
			&r.ReceiptNumber,
			&r.Provider,
			&r.Evidence,
			&r.Notes,
			&r.AuthorizedBy,
			&r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment audit record: %w", err)
		}
		records = append(records, r)
	}

	return records, nil
}
