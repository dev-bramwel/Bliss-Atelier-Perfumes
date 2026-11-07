package orders

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func generateID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// CreateOrder reserves stock atomically and creates the order with snapshotted items.
func (s *Store) CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
	if err := ValidateCustomer(&req.Customer); err != nil {
		return nil, err
	}
	if err := ValidateItems(req.Items); err != nil {
		return nil, err
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
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

	// Lock catalog product rows
	rows, err := tx.Query(ctx, "SELECT id, name, size, price_override_kes, stock, opened FROM public.catalog_product WHERE id = ANY($1) ORDER BY id FOR UPDATE", productIDs)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		foundProducts[p.id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Verify all requested products exist, are opened, and have sufficient stock
	for _, id := range productIDs {
		p, exists := foundProducts[id]
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrProductNotFound, id)
		}
		if !p.opened {
			return nil, fmt.Errorf("%w: %s", ErrProductUnopened, id)
		}
		qty := itemMap[id]
		if p.stock < qty {
			return nil, fmt.Errorf("%w: product %s requested %d, available %d", ErrInsufficientStock, p.name, qty, p.stock)
		}
	}

	now := time.Now().UTC()
	orderID := generateID("ord")
	totalKes := 0
	orderItems := make([]OrderItem, 0, len(req.Items))

	// Deduct stock and build snapshotted items
	for _, it := range req.Items {
		p := foundProducts[it.ID]
		unitPrice := commonPriceKes
		if p.priceOverrideKes != nil && *p.priceOverrideKes > 0 {
			unitPrice = *p.priceOverrideKes
		}
		subtotal := unitPrice * it.Qty
		totalKes += subtotal

		// Atomically decrement stock
		if _, err := tx.Exec(ctx, "UPDATE public.catalog_product SET stock = stock - $1 WHERE id = $2", it.Qty, p.id); err != nil {
			return nil, err
		}

		orderItems = append(orderItems, OrderItem{
			ID:           generateUUID(),
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
		return nil, err
	}

	// Insert into Order table
	_, err = tx.Exec(ctx, `INSERT INTO public."Order" (id, "createdAt", "updatedAt", "customerName", "customerPhone", "deliveryLocation", "deliveryOption", notes, "paymentMethod", "paymentStatus", "totalKes", items)
		VALUES ($1, $2, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		orderID, now, req.Customer.Name, req.Customer.Phone, req.Customer.Location, req.Customer.Delivery, req.Customer.Notes, req.Customer.Payment, PaymentPending, totalKes, legacyJSON)
	if err != nil {
		return nil, err
	}

	// Insert snapshotted order_item rows
	for _, item := range orderItems {
		_, err = tx.Exec(ctx, `INSERT INTO public.order_item (id, order_id, product_id, product_name, product_size, unit_price_kes, quantity, subtotal_kes, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			item.ID, item.OrderID, item.ProductID, item.ProductName, item.ProductSize, item.UnitPriceKes, item.Quantity, item.SubtotalKes, item.CreatedAt)
		if err != nil {
			return nil, err
		}
	}

	// Insert order_fulfillment row
	_, err = tx.Exec(ctx, `INSERT INTO public.order_fulfillment (order_id, status, updated_at) VALUES ($1, $2, $3)`,
		orderID, FulfillmentUnfulfilled, now)
	if err != nil {
		return nil, err
	}

	// Insert stock_reservation row (15-minute hold)
	resID := generateUUID()
	expiresAt := now.Add(DefaultReservationDuration)
	_, err = tx.Exec(ctx, `INSERT INTO public.stock_reservation (id, order_id, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		resID, orderID, ReservationActive, expiresAt, now)
	if err != nil {
		return nil, err
	}

	// Insert stock_reservation_item rows
	for _, item := range orderItems {
		_, err = tx.Exec(ctx, `INSERT INTO public.stock_reservation_item (reservation_id, product_id, quantity)
			VALUES ($1, $2, $3)`,
			resID, item.ProductID, item.Quantity)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
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

// ReleaseExpired finds active reservations expired by `now`, marks them released, and restores inventory safely.
// Guarantees exact-once release (idempotent; repeated runs cannot double-restore stock).
func (s *Store) ReleaseExpired(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer rollback(tx)

	// Select active expired reservations with row locks
	rows, err := tx.Query(ctx, `SELECT id, order_id FROM public.stock_reservation WHERE status = $1 AND expires_at <= $2 FOR UPDATE SKIP LOCKED`,
		ReservationActive, now)
	if err != nil {
		return 0, err
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
			return 0, err
		}
		expiredList = append(expiredList, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
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
			return 0, err
		}

		// Restore product stock
		_, err = tx.Exec(ctx, `UPDATE public.catalog_product p
			SET stock = p.stock + sri.quantity
			FROM public.stock_reservation_item sri
			WHERE sri.reservation_id = $1 AND p.id = sri.product_id`, r.id)
		if err != nil {
			return 0, err
		}

		// Update Order paymentStatus to EXPIRED
		_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3 AND "paymentStatus" = $4`,
			PaymentExpired, now, r.orderID, PaymentPending)
		if err != nil {
			return 0, err
		}

		// Update order_fulfillment to cancelled
		_, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status = $1, updated_at = $2 WHERE order_id = $3 AND status = $4`,
			FulfillmentCancelled, now, r.orderID, FulfillmentUnfulfilled)
		if err != nil {
			return 0, err
		}

		releasedCount++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return releasedCount, nil
}

// CancelOrder manually cancels a pending order, releases its active reservation, and restores stock.
func (s *Store) CancelOrder(ctx context.Context, orderID string) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer rollback(tx)

	var currentPaymentStatus string
	err = tx.QueryRow(ctx, `SELECT "paymentStatus" FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&currentPaymentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		return err
	}
	if currentPaymentStatus != PaymentPending {
		return ErrOrderNotPending
	}

	now := time.Now().UTC()
	var resID string
	var resStatus string
	err = tx.QueryRow(ctx, `SELECT id, status FROM public.stock_reservation WHERE order_id = $1 FOR UPDATE`, orderID).Scan(&resID, &resStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	if resID != "" && resStatus == ReservationActive {
		var updatedID string
		err := tx.QueryRow(ctx, `UPDATE public.stock_reservation SET status = $1, released_at = $2 WHERE id = $3 AND status = $4 RETURNING id`,
			ReservationReleased, now, resID, ReservationActive).Scan(&updatedID)
		if err == nil {
			// Restore stock
			_, err = tx.Exec(ctx, `UPDATE public.catalog_product p
				SET stock = p.stock + sri.quantity
				FROM public.stock_reservation_item sri
				WHERE sri.reservation_id = $1 AND p.id = sri.product_id`, resID)
			if err != nil {
				return err
			}
		}
	}

	// Update Order and Fulfillment
	_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`,
		PaymentFailed, now, orderID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status = $1, updated_at = $2 WHERE order_id = $3`,
		FulfillmentCancelled, now, orderID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ConfirmPayment processes a payment confirmation.
// If the reservation is active: commits the reservation and marks order PAID.
// If the reservation has expired: marks order RESOLUTION_REQUIRED (never silently fulfills without stock).
func (s *Store) ConfirmPayment(ctx context.Context, orderID string, receiptNumber string) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer rollback(tx)

	var currentPaymentStatus string
	err = tx.QueryRow(ctx, `SELECT "paymentStatus" FROM public."Order" WHERE id = $1 FOR UPDATE`, orderID).Scan(&currentPaymentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		return err
	}
	if currentPaymentStatus == PaymentPaid {
		return nil // Idempotent duplicate success
	}

	now := time.Now().UTC()
	var resID, resStatus string
	err = tx.QueryRow(ctx, `SELECT id, status FROM public.stock_reservation WHERE order_id = $1 FOR UPDATE`, orderID).Scan(&resID, &resStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReservationNotFound
		}
		return err
	}

	if resStatus == ReservationActive {
		// Commit reservation
		_, err = tx.Exec(ctx, `UPDATE public.stock_reservation SET status = $1 WHERE id = $2`, ReservationCommitted, resID)
		if err != nil {
			return err
		}

		// Mark order as PAID
		_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`, PaymentPaid, now, orderID)
		if err != nil {
			return err
		}

		// Record M-Pesa transaction if receipt is supplied
		if receiptNumber != "" {
			_, err = tx.Exec(ctx, `INSERT INTO public."MpesaTransaction" (id, "createdAt", "updatedAt", "orderId", "mpesaReceiptNumber", status)
				VALUES ($1, $2, $2, $3, $4, 'SUCCESS')
				ON CONFLICT ("orderId") DO UPDATE SET "mpesaReceiptNumber" = $4, status = 'SUCCESS', "updatedAt" = $2`,
				generateID("mpesa"), now, orderID, receiptNumber)
			if err != nil {
				return err
			}
		}

		return tx.Commit(ctx)
	}

	// Reservation is not active (released or expired) — LATE PAYMENT!
	// Transition order to RESOLUTION_REQUIRED and do NOT fulfill silently
	_, err = tx.Exec(ctx, `UPDATE public."Order" SET "paymentStatus" = $1, "updatedAt" = $2 WHERE id = $3`,
		PaymentResolutionRequired, now, orderID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status = $1, updated_at = $2 WHERE order_id = $3`,
		FulfillmentCancelled, now, orderID)
	if err != nil {
		return err
	}

	if receiptNumber != "" {
		_, err = tx.Exec(ctx, `INSERT INTO public."MpesaTransaction" (id, "createdAt", "updatedAt", "orderId", "mpesaReceiptNumber", status)
			VALUES ($1, $2, $2, $3, $4, 'LATE_PAID')
			ON CONFLICT ("orderId") DO UPDATE SET "mpesaReceiptNumber" = $4, status = 'LATE_PAID', "updatedAt" = $2`,
			generateID("mpesa"), now, orderID, receiptNumber)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
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
		return nil, err
	}
	if notes != nil {
		o.Notes = *notes
	}
	o.FulfillmentStatus = fulfillmentStatus

	// Load order_item records
	rows, err := s.Pool.Query(ctx, `SELECT id, order_id, product_id, product_name, product_size, unit_price_kes, quantity, subtotal_kes, created_at
		FROM public.order_item WHERE order_id = $1 ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var it OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.ProductName, &it.ProductSize, &it.UnitPriceKes, &it.Quantity, &it.SubtotalKes, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.Name = it.ProductName
		o.Items = append(o.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
		return nil, err
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
		return nil, err
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
			return nil, err
		}
		if notes != nil {
			o.Notes = *notes
		}
		o.FulfillmentStatus = fulfillmentStatus
		orderList = append(orderList, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
		return nil, err
	}
	defer itemRows.Close()

	itemsByOrderID := make(map[string][]OrderItem, len(orderList))
	for itemRows.Next() {
		var it OrderItem
		if err := itemRows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.ProductName, &it.ProductSize, &it.UnitPriceKes, &it.Quantity, &it.SubtotalKes, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.Name = it.ProductName
		itemsByOrderID[it.OrderID] = append(itemsByOrderID[it.OrderID], it)
	}
	if err := itemRows.Err(); err != nil {
		return nil, err
	}

	for i := range orderList {
		orderList[i].Items = itemsByOrderID[orderList[i].ID]
		if orderList[i].Items == nil {
			orderList[i].Items = []OrderItem{}
		}
	}

	return orderList, nil
}
