package orders

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
)

type Store struct {
	Pool *pgxpool.Pool
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func generateID(prefix string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto random read failed: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}

func generateUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto random read failed: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// GetAvailableStock calculates the unreserved quantity available for purchase
// by subtracting active unexpired reservations from on-hand catalog stock.
func (s *Store) GetAvailableStock(ctx context.Context, productID string) (int, error) {
	var available int
	err := s.Pool.QueryRow(ctx, `SELECT (p.stock - coalesce((
		SELECT sum(sri.quantity)
		FROM public.stock_reservation_item sri
		JOIN public.stock_reservation sr ON sr.id = sri.reservation_id
		WHERE sri.product_id = p.id AND sr.status = 'active' AND sr.expires_at > now()
	), 0))
	FROM public.catalog_product p
	WHERE p.id = $1`, productID).Scan(&available)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrProductNotFound
		}
		return 0, fmt.Errorf("failed to query available stock for product %s: %w", productID, err)
	}
	if available < 0 {
		available = 0
	}
	return available, nil
}

// CreateOrder reserves stock atomically and creates the order with snapshotted items.
// Reservation availability is tracked separately from physical on-hand stock, maintaining
// ledger consistency until payment is confirmed.
func (s *Store) CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
	if err := ValidateCustomer(&req.Customer); err != nil {
		return nil, err
	}
	if err := ValidateItems(req.Items); err != nil {
		return nil, err
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	// Check catalog stock control and authority
	var authority string
	var reconciledUntil *time.Time
	err = tx.QueryRow(ctx, "SELECT authority, reconciled_until FROM public.stock_control WHERE singleton = true").Scan(&authority, &reconciledUntil)
	if err != nil {
		return nil, ErrCatalogUnavailable
	}
	if authority != "replacement" || reconciledUntil == nil || reconciledUntil.Before(time.Now()) {
		return nil, ErrCatalogUnavailable
	}

	// Read common catalog price
	var commonPriceKes int
	err = tx.QueryRow(ctx, "SELECT common_price_kes FROM public.catalog_settings WHERE singleton = true").Scan(&commonPriceKes)
	if err != nil {
		return nil, ErrCatalogUnavailable
	}

	// Sort requested item IDs to acquire row locks in deterministic order and prevent deadlocks
	itemMap := make(map[string]int, len(req.Items))
	productIDs := make([]string, 0, len(req.Items))
	for _, it := range req.Items {
		itemMap[it.ID] = it.Qty
		productIDs = append(productIDs, it.ID)
	}
	sort.Strings(productIDs)

	// Lock catalog product rows in deterministic alphabetical order
	rows, err := tx.Query(ctx, "SELECT id, name, size, price_override_kes, stock, opened FROM public.catalog_product WHERE id = ANY($1) ORDER BY id FOR UPDATE", productIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to lock catalog products: %w", err)
	}
	defer rows.Close()

	type productRow struct {
		id               string
		name             string
		size             string
		priceOverrideKes *int
		stock            int
		opened           bool
	}

	foundProducts := make(map[string]productRow, len(productIDs))
	for rows.Next() {
		var p productRow
		if err := rows.Scan(&p.id, &p.name, &p.size, &p.priceOverrideKes, &p.stock, &p.opened); err != nil {
			return nil, fmt.Errorf("failed to scan catalog product row: %w", err)
		}
		foundProducts[p.id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog product cursor error: %w", err)
	}

	now := time.Now().UTC()

	// Query active unexpired reservations for these locked products
	resRows, err := tx.Query(ctx, `SELECT sri.product_id, coalesce(sum(sri.quantity), 0)
		FROM public.stock_reservation_item sri
		JOIN public.stock_reservation sr ON sr.id = sri.reservation_id
		WHERE sri.product_id = ANY($1) AND sr.status = 'active' AND sr.expires_at > $2
		GROUP BY sri.product_id`, productIDs, now)
	if err != nil {
		return nil, fmt.Errorf("failed to query active reservations: %w", err)
	}
	defer resRows.Close()

	reservedMap := make(map[string]int, len(productIDs))
	for resRows.Next() {
		var pid string
		var qty int
		if err := resRows.Scan(&pid, &qty); err != nil {
			return nil, fmt.Errorf("failed to scan active reservation count: %w", err)
		}
		reservedMap[pid] = qty
	}
	if err := resRows.Err(); err != nil {
		return nil, fmt.Errorf("active reservation cursor error: %w", err)
	}

	// Verify all requested products exist, are opened, and have sufficient available stock
	for _, id := range productIDs {
		p, exists := foundProducts[id]
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrProductNotFound, id)
		}
		if !p.opened {
			return nil, fmt.Errorf("%w: %s", ErrProductUnopened, id)
		}
		qty := itemMap[id]
		available := p.stock - reservedMap[id]
		if available < qty {
			return nil, fmt.Errorf("%w: product %s requested %d, available %d", ErrInsufficientStock, p.name, qty, available)
		}
	}

	orderID, err := generateID("ord")
	if err != nil {
		return nil, err
	}
	totalKes := 0
	orderItems := make([]OrderItem, 0, len(req.Items))

	// Calculate prices and prepare snapshotted items
	for _, it := range req.Items {
		p := foundProducts[it.ID]
		unitPrice := commonPriceKes
		if p.priceOverrideKes != nil && *p.priceOverrideKes > 0 {
			unitPrice = *p.priceOverrideKes
		}
		subtotal := unitPrice * it.Qty
		totalKes += subtotal

		itemID, err := generateUUID()
		if err != nil {
			return nil, err
		}

		orderItems = append(orderItems, OrderItem{
			ID:           itemID,
			OrderID:      orderID,
			ProductID:    p.id,
			ProductName:  p.name,
			Name:         p.name,
			ProductSize:  p.size,
			UnitPriceKes: unitPrice,
			Quantity:     it.Qty,
			SubtotalKes:  subtotal,
			CreatedAt:    now,
		})
	}

	// Create legacy JSON items for backward compatibility
	legacyItems := make([]map[string]any, len(orderItems))
	for i, item := range orderItems {
		legacyItems[i] = map[string]any{
			"id":       item.ProductID,
			"name":     item.ProductName,
			"size":     item.ProductSize,
			"priceKes": item.UnitPriceKes,
			"qty":      item.Quantity,
		}
	}
	legacyJSON, err := json.Marshal(legacyItems)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal legacy order items: %w", err)
	}

	// Insert into Order table
	_, err = tx.Exec(ctx, `INSERT INTO public."Order" (id, "createdAt", "updatedAt", "customerName", "customerPhone", "deliveryLocation", "deliveryOption", notes, "paymentMethod", "paymentStatus", "totalKes", items)
		VALUES ($1, $2, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		orderID, now, req.Customer.Name, req.Customer.Phone, req.Customer.Location, req.Customer.Delivery, req.Customer.Notes, req.Customer.Payment, PaymentPending, totalKes, legacyJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to insert order: %w", err)
	}

	// Insert snapshotted order_item rows
	for _, item := range orderItems {
		_, err = tx.Exec(ctx, `INSERT INTO public.order_item (id, order_id, product_id, product_name, product_size, unit_price_kes, quantity, subtotal_kes, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			item.ID, item.OrderID, item.ProductID, item.ProductName, item.ProductSize, item.UnitPriceKes, item.Quantity, item.SubtotalKes, item.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to insert order item: %w", err)
		}
	}

	// Insert order_fulfillment row
	_, err = tx.Exec(ctx, `INSERT INTO public.order_fulfillment (order_id, status, updated_at) VALUES ($1, $2, $3)`,
		orderID, FulfillmentUnfulfilled, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert order fulfillment: %w", err)
	}

	// Insert stock_reservation row (15-minute hold)
	resID, err := generateUUID()
	if err != nil {
		return nil, err
	}
	expiresAt := now.Add(DefaultReservationDuration)
	_, err = tx.Exec(ctx, `INSERT INTO public.stock_reservation (id, order_id, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		resID, orderID, ReservationActive, expiresAt, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert stock reservation: %w", err)
	}

	// Insert stock_reservation_item rows
	for _, item := range orderItems {
		_, err = tx.Exec(ctx, `INSERT INTO public.stock_reservation_item (reservation_id, product_id, quantity)
			VALUES ($1, $2, $3)`,
			resID, item.ProductID, item.Quantity)
		if err != nil {
			return nil, fmt.Errorf("failed to insert stock reservation item: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit create order transaction: %w", err)
	}

	return &Order{
		ID:                orderID,
		CreatedAt:         now,
		UpdatedAt:         now,
		CustomerName:      req.Customer.Name,
		CustomerPhone:     req.Customer.Phone,
		DeliveryLocation:  req.Customer.Location,
		DeliveryOption:    req.Customer.Delivery,
		Notes:             req.Customer.Notes,
		PaymentMethod:     req.Customer.Payment,
		PaymentStatus:     PaymentPending,
		FulfillmentStatus: FulfillmentUnfulfilled,
		TotalKes:          totalKes,
		Items:             orderItems,
		Reservation: &StockReservation{
			ID:        resID,
			OrderID:   orderID,
			Status:    ReservationActive,
			ExpiresAt: expiresAt,
			CreatedAt: now,
		},
	}, nil
}

// ReleaseExpired finds active reservations expired by `now` and marks them released.
// Guarantees exact-once release (idempotent; repeated runs cannot double-restore stock).
func (s *Store) ReleaseExpired(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	// Select active expired reservations with row locks in deterministic order
	rows, err := tx.Query(ctx, `SELECT id, order_id FROM public.stock_reservation WHERE status = $1 AND expires_at <= $2 ORDER BY id FOR UPDATE SKIP LOCKED`,
		ReservationActive, now)
	if err != nil {
		return 0, fmt.Errorf("failed to query expired reservations: %w", err)
	}
	defer rows.Close()

	type expiredRes struct {
		id      string
		orderID string
	}
	var expiredList []expiredRes
	for rows.Next() {
		var r expiredRes
		if err := rows.Scan(&r.id, &r.orderID); err != nil {
			return 0, fmt.Errorf("failed to scan expired reservation: %w", err)
		}
		expiredList = append(expiredList, r)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("expired reservation cursor error: %w", err)
	}

	releasedCount := 0
	for _, r := range expiredList {
		// Atomically transition status to released
		var updatedID string
		err := tx.QueryRow(ctx, `UPDATE public.stock_reservation SET status = $1, released_at = $2 WHERE id = $3 AND status = $4 RETURNING id`,
			ReservationReleased, now, r.id, ReservationActive).Scan(&updatedID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // Already released or committed concurrently
			}
			return 0, fmt.Errorf("failed to release reservation %s: %w", r.id, err)
		}

		// Update Order paymentStatus to EXPIRED
		_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3 AND "paymentStatus" = $4`,
			PaymentExpired, now, r.orderID, PaymentPending)
		if err != nil {
			return 0, fmt.Errorf("failed to update order %s payment status to EXPIRED: %w", r.orderID, err)
		}

		// Update order_fulfillment to cancelled
		_, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status = $1, updated_at = $2 WHERE order_id = $3 AND status = $4`,
			FulfillmentCancelled, now, r.orderID, FulfillmentUnfulfilled)
		if err != nil {
			return 0, fmt.Errorf("failed to cancel fulfillment for order %s: %w", r.orderID, err)
		}

		releasedCount++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit expired releases: %w", err)
	}
	return releasedCount, nil
}

// CancelOrder manually cancels a pending order and releases its active reservation.
func (s *Store) CancelOrder(ctx context.Context, orderID string) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	now := time.Now().UTC()

	// Acquire locks in consistent order: stock_reservation first, then Order
	var resID string
	var resStatus string
	err = tx.QueryRow(ctx, `SELECT id, status FROM public.stock_reservation WHERE order_id = $1 FOR UPDATE`, orderID).Scan(&resID, &resStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to query reservation for order %s: %w", orderID, err)
	}

	var currentPaymentStatus string
	err = tx.QueryRow(ctx, `SELECT "paymentStatus" FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&currentPaymentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		return fmt.Errorf("failed to query order %s: %w", orderID, err)
	}
	if currentPaymentStatus != PaymentPending {
		return ErrOrderNotPending
	}

	if resID != "" && resStatus == ReservationActive {
		var updatedID string
		_ = tx.QueryRow(ctx, `UPDATE public.stock_reservation SET status = $1, released_at = $2 WHERE id = $3 AND status = $4 RETURNING id`,
			ReservationReleased, now, resID, ReservationActive).Scan(&updatedID)
	}

	// Update Order and Fulfillment
	_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`,
		PaymentFailed, now, orderID)
	if err != nil {
		return fmt.Errorf("failed to update order status to FAILED: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status = $1, updated_at = $2 WHERE order_id = $3`,
		FulfillmentCancelled, now, orderID)
	if err != nil {
		return fmt.Errorf("failed to cancel order fulfillment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit cancel order: %w", err)
	}
	return nil
}

// ConfirmPayment processes a payment confirmation.
// If the reservation is active: commits the reservation, records the sale in the immutable stock ledger
// with a stock_batch and stock_entries, decrements on-hand stock, and marks order PAID.
// If the reservation has expired: marks order RESOLUTION_REQUIRED (never silently fulfills without stock).
func (s *Store) ConfirmPayment(ctx context.Context, orderID string, receiptNumber string) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollback(tx)

	now := time.Now().UTC()

	// Acquire locks in consistent order: stock_reservation first, then Order
	var resID, resStatus string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT id, status, expires_at FROM public.stock_reservation WHERE order_id = $1 FOR UPDATE`, orderID).Scan(&resID, &resStatus, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var dummy string
			if tx.QueryRow(ctx, `SELECT id FROM public."Order" WHERE id = $1`, orderID).Scan(&dummy) != nil {
				return ErrOrderNotFound
			}
			return ErrReservationNotFound
		}
		return fmt.Errorf("failed to query reservation for order %s: %w", orderID, err)
	}

	var currentPaymentStatus string
	var deliveryOption string
	err = tx.QueryRow(ctx, `SELECT "paymentStatus", COALESCE("deliveryOption", 'delivery') FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&currentPaymentStatus, &deliveryOption)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		return fmt.Errorf("failed to query order %s: %w", orderID, err)
	}
	if currentPaymentStatus == PaymentPaid {
		return nil // Idempotent duplicate success
	}

	opt := strings.ToLower(strings.TrimSpace(deliveryOption))
	isPickup := opt == "pickup" || opt == "showroom_pickup"
	if !isPickup {
		var quoteStatus string
		var quoteExpiresAt time.Time
		err = tx.QueryRow(ctx, `
			SELECT status, expires_at
			FROM public.delivery_quote
			WHERE order_id = $1
			ORDER BY version DESC
			LIMIT 1
		`, orderID).Scan(&quoteStatus, &quoteExpiresAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDeliveryQuoteUnaccepted
			}
			return fmt.Errorf("failed to query delivery quote for order %s: %w", orderID, err)
		}
		if quoteStatus != "accepted" {
			return ErrDeliveryQuoteUnaccepted
		}
		if now.After(quoteExpiresAt) {
			return ErrQuoteExpired
		}
	}

	// Verify both active status AND unexpired timestamp before committing hold
	if resStatus == ReservationActive && expiresAt.After(now) {
		// Commit reservation
		_, err = tx.Exec(ctx, `UPDATE public.stock_reservation SET status = $1 WHERE id = $2`, ReservationCommitted, resID)
		if err != nil {
			return fmt.Errorf("failed to commit stock reservation %s: %w", resID, err)
		}

		// Mark order as PAID
		_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`, PaymentPaid, now, orderID)
		if err != nil {
			return fmt.Errorf("failed to mark order %s as PAID: %w", orderID, err)
		}

		// Record M-Pesa transaction if receipt is supplied
		if receiptNumber != "" {
			mpesaID, err := generateID("mpesa")
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO public."MpesaTransaction" (id, "createdAt", "updatedAt", "orderId", "mpesaReceiptNumber", status)
				VALUES ($1, $2, $2, $3, $4, 'SUCCESS')
				ON CONFLICT ("orderId") DO UPDATE SET "mpesaReceiptNumber" = $4, status = 'SUCCESS', "updatedAt" = $2`,
				mpesaID, now, orderID, receiptNumber)
			if err != nil {
				return fmt.Errorf("failed to insert mpesa transaction: %w", err)
			}
		}

		// Query reserved items to record the sale batch and update on-hand stock in the ledger
		resItemRows, err := tx.Query(ctx, `SELECT product_id, quantity FROM public.stock_reservation_item WHERE reservation_id = $1 ORDER BY product_id`, resID)
		if err != nil {
			return fmt.Errorf("failed to query reservation items for ledger entry: %w", err)
		}
		defer resItemRows.Close()

		var batchLines []catalog.Line
		for resItemRows.Next() {
			var pid string
			var qty int
			if err := resItemRows.Scan(&pid, &qty); err != nil {
				return fmt.Errorf("failed to scan reservation item: %w", err)
			}
			batchLines = append(batchLines, catalog.Line{
				ProductID: pid,
				Quantity:  qty,
			})
		}
		if err := resItemRows.Err(); err != nil {
			return fmt.Errorf("reservation items cursor error: %w", err)
		}

		if len(batchLines) > 0 {
			sort.Slice(batchLines, func(i, j int) bool {
				return batchLines[i].ProductID < batchLines[j].ProductID
			})

			batchActor := "order:" + orderID
			batchEvidence := strings.TrimSpace(receiptNumber)
			if batchEvidence == "" {
				batchEvidence = "order:" + orderID
			}
			if len(batchEvidence) > 512 {
				batchEvidence = batchEvidence[:512]
			}

			batchID, err := generateID("batch")
			if err != nil {
				return err
			}

			batch := catalog.Batch{
				ID:       batchID,
				Kind:     "local_sale",
				Actor:    batchActor,
				Evidence: batchEvidence,
				Lines:    batchLines,
			}
			if err := batch.Validate(); err != nil {
				return fmt.Errorf("invalid stock batch: %w", err)
			}

			rawBatch, err := json.Marshal(batch)
			if err != nil {
				return fmt.Errorf("failed to marshal stock batch: %w", err)
			}
			hash := sha256.Sum256(rawBatch)
			digest := hex.EncodeToString(hash[:])

			_, err = tx.Exec(ctx, `INSERT INTO public.stock_batch (id, digest, kind, actor, evidence, created_at)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				batch.ID, digest, batch.Kind, batch.Actor, batch.Evidence, now)
			if err != nil {
				return fmt.Errorf("failed to insert stock batch: %w", err)
			}

			for _, l := range batch.Lines {
				var currentStock int
				if err := tx.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id = $1 FOR UPDATE`, l.ProductID).Scan(&currentStock); err != nil {
					return fmt.Errorf("failed to query product stock for ledger balance: %w", err)
				}
				newBalance := currentStock - l.Quantity
				if newBalance < 0 {
					return fmt.Errorf("insufficient stock for product %s on payment deduction", l.ProductID)
				}
				if _, err := tx.Exec(ctx, `UPDATE public.catalog_product SET stock = $1 WHERE id = $2`, newBalance, l.ProductID); err != nil {
					return fmt.Errorf("failed to update product stock: %w", err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO public.stock_entry (batch_id, product_id, delta, balance) VALUES ($1, $2, $3, $4)`,
					batch.ID, l.ProductID, -l.Quantity, newBalance); err != nil {
					return fmt.Errorf("failed to insert stock entry: %w", err)
				}
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit confirm payment transaction: %w", err)
		}
		return nil
	}

	// Reservation is not active or hold window expired — LATE PAYMENT!
	// Transition order to RESOLUTION_REQUIRED and do NOT fulfill silently
	if resStatus == ReservationActive {
		_, _ = tx.Exec(ctx, `UPDATE public.stock_reservation SET status = $1, released_at = $2 WHERE id = $3`,
			ReservationReleased, now, resID)
	}
	_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`,
		PaymentResolutionRequired, now, orderID)
	if err != nil {
		return fmt.Errorf("failed to update order status to RESOLUTION_REQUIRED: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status = $1, updated_at = $2 WHERE order_id = $3`,
		FulfillmentCancelled, now, orderID)
	if err != nil {
		return fmt.Errorf("failed to cancel order fulfillment: %w", err)
	}

	if receiptNumber != "" {
		mpesaID, err := generateID("mpesa")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO public."MpesaTransaction" (id, "createdAt", "updatedAt", "orderId", "mpesaReceiptNumber", status)
			VALUES ($1, $2, $2, $3, $4, 'LATE_PAID')
			ON CONFLICT ("orderId") DO UPDATE SET "mpesaReceiptNumber" = $4, status = 'LATE_PAID', "updatedAt" = $2`,
			mpesaID, now, orderID, receiptNumber)
		if err != nil {
			return fmt.Errorf("failed to record late mpesa transaction: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit late payment resolution: %w", err)
	}
	return ErrLatePaymentResolution
}

// GetOrder returns an order with its snapshotted items, fulfillment, and reservation details.
func (s *Store) GetOrder(ctx context.Context, orderID string) (*Order, error) {
	var o Order
	var fulfillmentStatus string
	var notes *string
	err := s.Pool.QueryRow(ctx, `SELECT o.id, o."createdAt", o."updatedAt", o."customerName", o."customerPhone", o."deliveryLocation",
		o."deliveryOption", o.notes, o."paymentMethod", o."paymentStatus", f.status, o."totalKes"
		FROM public."Order" o
		JOIN public.order_fulfillment f ON f.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(
		&o.ID, &o.CreatedAt, &o.UpdatedAt, &o.CustomerName, &o.CustomerPhone, &o.DeliveryLocation,
		&o.DeliveryOption, &notes, &o.PaymentMethod, &o.PaymentStatus, &fulfillmentStatus, &o.TotalKes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to query order %s: %w", orderID, err)
	}
	if notes != nil {
		o.Notes = *notes
	}
	o.FulfillmentStatus = fulfillmentStatus

	// Load order_item records
	rows, err := s.Pool.Query(ctx, `SELECT id, order_id, product_id, product_name, product_size, unit_price_kes, quantity, subtotal_kes, created_at
		FROM public.order_item WHERE order_id = $1 ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query order items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var it OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.ProductName, &it.ProductSize, &it.UnitPriceKes, &it.Quantity, &it.SubtotalKes, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan order item: %w", err)
		}
		it.Name = it.ProductName
		o.Items = append(o.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("order item cursor error: %w", err)
	}

	// Load reservation if exists
	var res StockReservation
	var releasedAt *time.Time
	err = s.Pool.QueryRow(ctx, `SELECT id, order_id, status, expires_at, created_at, released_at
		FROM public.stock_reservation WHERE order_id = $1`, orderID).Scan(
		&res.ID, &res.OrderID, &res.Status, &res.ExpiresAt, &res.CreatedAt, &releasedAt)
	if err == nil {
		res.ReleasedAt = releasedAt
		o.Reservation = &res
	}

	return &o, nil
}

// GetPaymentStatus returns public polling status for frontend checkout.
func (s *Store) GetPaymentStatus(ctx context.Context, orderID string) (*PaymentStatusResponse, error) {
	var paymentStatus, fulfillmentStatus string
	var expiresAt *time.Time
	var resStatus *string
	var receiptNumber *string

	err := s.Pool.QueryRow(ctx, `SELECT o."paymentStatus", f.status, r.expires_at, r.status, m."mpesaReceiptNumber"
		FROM public."Order" o
		JOIN public.order_fulfillment f ON f.order_id = o.id
		LEFT JOIN public.stock_reservation r ON r.order_id = o.id
		LEFT JOIN public."MpesaTransaction" m ON m."orderId" = o.id
		WHERE o.id = $1`, orderID).Scan(&paymentStatus, &fulfillmentStatus, &expiresAt, &resStatus, &receiptNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to query payment status: %w", err)
	}

	resp := &PaymentStatusResponse{
		OrderID:           orderID,
		PaymentStatus:     paymentStatus,
		FulfillmentStatus: fulfillmentStatus,
		ExpiresAt:         expiresAt,
	}
	if receiptNumber != nil {
		resp.ReceiptNumber = *receiptNumber
	}
	if expiresAt != nil && expiresAt.Before(time.Now().UTC()) && paymentStatus != PaymentPaid {
		resp.IsExpired = true
	}
	return resp, nil
}

// ListRecent returns recent orders up to limit for operational and dashboard synchronization.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT o.id, o."createdAt", o."updatedAt", o."customerName", o."customerPhone",
		o."deliveryLocation", o."deliveryOption", o.notes, o."paymentMethod", o."paymentStatus", f.status, o."totalKes"
		FROM public."Order" o
		JOIN public.order_fulfillment f ON f.order_id = o.id
		ORDER BY o."createdAt" DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent orders: %w", err)
	}
	defer rows.Close()

	var orderList []Order
	for rows.Next() {
		var o Order
		var notes *string
		var fulfillmentStatus string
		if err := rows.Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt, &o.CustomerName, &o.CustomerPhone,
			&o.DeliveryLocation, &o.DeliveryOption, &notes, &o.PaymentMethod, &o.PaymentStatus,
			&fulfillmentStatus, &o.TotalKes); err != nil {
			return nil, fmt.Errorf("failed to scan recent order: %w", err)
		}
		if notes != nil {
			o.Notes = *notes
		}
		o.FulfillmentStatus = fulfillmentStatus
		orderList = append(orderList, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent orders cursor error: %w", err)
	}

	if len(orderList) == 0 {
		return []Order{}, nil
	}

	// Fetch items for these orders
	orderIDs := make([]string, len(orderList))
	for i, o := range orderList {
		orderIDs[i] = o.ID
	}

	itemRows, err := s.Pool.Query(ctx, `SELECT id, order_id, product_id, product_name, product_size, unit_price_kes, quantity, subtotal_kes, created_at
		FROM public.order_item
		WHERE order_id = ANY($1)
		ORDER BY created_at ASC`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to query items for recent orders: %w", err)
	}
	defer itemRows.Close()

	itemsByOrderID := make(map[string][]OrderItem, len(orderList))
	for itemRows.Next() {
		var it OrderItem
		if err := itemRows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.ProductName, &it.ProductSize, &it.UnitPriceKes, &it.Quantity, &it.SubtotalKes, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan item for recent order: %w", err)
		}
		it.Name = it.ProductName
		itemsByOrderID[it.OrderID] = append(itemsByOrderID[it.OrderID], it)
	}
	if err := itemRows.Err(); err != nil {
		return nil, fmt.Errorf("recent order items cursor error: %w", err)
	}

	for i := range orderList {
		orderList[i].Items = itemsByOrderID[orderList[i].ID]
		if orderList[i].Items == nil {
			orderList[i].Items = []OrderItem{}
		}
	}

	return orderList, nil
}
