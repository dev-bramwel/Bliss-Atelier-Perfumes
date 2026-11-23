// Package operations provides session-scoped operational reads and audited fulfillment.
package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/delivery"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrConflict = errors.New("operation conflicts with the current order state")

type Invalidator interface {
	Invalidate(ctx context.Context) error
}

type Store struct {
	Pool        *pgxpool.Pool
	Invalidator Invalidator
}
type InventoryItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PriceKes  int    `json:"priceKes"`
	Stock     int    `json:"stock"`
	Held      int    `json:"held"`
	Available int    `json:"available"`
	Opened    bool   `json:"opened"`
}
type InventoryPage struct {
	Products       []InventoryItem `json:"products"`
	CommonPriceKes int             `json:"common_price_kes"`
	Total          int             `json:"total"`
	ApprovalUntil  *time.Time      `json:"approval_until"`
}
type OrderRow struct {
	orders.Order
	AssignedTo string                `json:"assigned_to"`
	Quote      *delivery.PublicQuote `json:"delivery_quote,omitempty"`
}
type OrderPage struct {
	Orders          []OrderRow `json:"orders"`
	Total           int        `json:"total"`
	PendingDispatch int        `json:"pending_dispatch"`
}

func (s *Store) CanAccess(ctx context.Context, id, userID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.order_assignment WHERE order_id=$1 AND user_id=$2)`, id, userID).Scan(&allowed)
	return allowed, err
}

func (s *Store) Inventory(ctx context.Context, limit, offset int) (*InventoryPage, error) {
	p := &InventoryPage{Products: []InventoryItem{}}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	err = tx.QueryRow(ctx, `SELECT c.common_price_kes, i.reconciled_until, (SELECT count(*) FROM public.catalog_product) FROM public.catalog_settings c CROSS JOIN public.stock_control i`).Scan(&p.CommonPriceKes, &p.ApprovalUntil, &p.Total)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.name,coalesce(p.price_override_kes,c.common_price_kes),p.stock,p.opened,
 coalesce((SELECT sum(ri.quantity) FROM public.stock_reservation_item ri JOIN public.stock_reservation r ON r.id=ri.reservation_id WHERE ri.product_id=p.id AND r.status='active' AND r.expires_at>now()),0)
 FROM public.catalog_product p CROSS JOIN public.catalog_settings c ORDER BY length(p.id),p.id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i InventoryItem
		if err := rows.Scan(&i.ID, &i.Name, &i.PriceKes, &i.Stock, &i.Opened, &i.Held); err != nil {
			return nil, err
		}
		i.Available = max(0, i.Stock-i.Held)
		p.Products = append(p.Products, i)
	}
	return p, rows.Err()
}

func (s *Store) Orders(ctx context.Context, employeeID string, limit, offset int) (*OrderPage, error) {
	p := &OrderPage{Orders: []OrderRow{}}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	err = tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE o."paymentStatus"='PAID' AND f.status='unfulfilled') FROM public."Order" o JOIN public.order_fulfillment f ON f.order_id=o.id LEFT JOIN public.order_assignment a ON a.order_id=o.id WHERE ($1='' OR a.user_id=$1)`, employeeID).Scan(&p.Total, &p.PendingDispatch)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id,o."createdAt",o."updatedAt",o."customerName",o."customerPhone",o."deliveryLocation",o."deliveryOption",coalesce(o.notes,''),o."paymentMethod",o."paymentStatus",f.status,o."totalKes",coalesce(a.user_id,''),
 (SELECT row_to_json(q) FROM (SELECT order_id,version,amount_kes,fee_recipient,arrangement,CASE WHEN status='pending' AND expires_at<=now() THEN 'expired' ELSE status END AS status,expires_at,created_at,accepted_at,customer_notes FROM public.delivery_quote WHERE order_id=o.id ORDER BY version DESC LIMIT 1) q)
 FROM public."Order" o JOIN public.order_fulfillment f ON f.order_id=o.id LEFT JOIN public.order_assignment a ON a.order_id=o.id WHERE ($1='' OR a.user_id=$1) ORDER BY o."createdAt" DESC,o.id DESC LIMIT $2 OFFSET $3`, employeeID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r OrderRow
		var raw []byte
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.CustomerName, &r.CustomerPhone, &r.DeliveryLocation, &r.DeliveryOption, &r.Notes, &r.PaymentMethod, &r.PaymentStatus, &r.FulfillmentStatus, &r.TotalKes, &r.AssignedTo, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &r.Quote); err != nil {
				return nil, err
			}
		}
		p.Orders = append(p.Orders, r)
	}
	return p, rows.Err()
}

// Assign and Fulfill serialize on the order row; their audit record commits atomically.
func (s *Store) Assign(ctx context.Context, id, target string, actor *auth.User, ip string) error {
	if actor.Role != auth.RoleOwner {
		return auth.ErrPermissionDenied
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var existing string
	if err = tx.QueryRow(ctx, `SELECT id FROM public."Order" WHERE id=$1 FOR UPDATE`, id).Scan(&existing); errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrOrderNotFound
	} else if err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.app_user WHERE id=$1 AND role='employee' AND is_active)`, target).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.order_assignment(order_id,user_id,assigned_by) VALUES($1,$2,$3) ON CONFLICT(order_id) DO UPDATE SET user_id=$2,assigned_by=$3,updated_at=now()`, id, target, actor.ID); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "assign_order", id, ip, map[string]any{"assigned_to": target}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Fulfill(ctx context.Context, id string, actor *auth.User, ip string) error {
	if !auth.IsStaff(actor.Role) {
		return auth.ErrPermissionDenied
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var payment, status string
	err = tx.QueryRow(ctx, `SELECT o."paymentStatus",f.status FROM public."Order" o JOIN public.order_fulfillment f ON f.order_id=o.id WHERE o.id=$1 FOR UPDATE OF o,f`, id).Scan(&payment, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrOrderNotFound
	}
	if err != nil {
		return err
	}
	if actor.Role == auth.RoleEmployee {
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.order_assignment WHERE order_id=$1 AND user_id=$2)`, id, actor.ID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return auth.ErrPermissionDenied
		}
	}
	if payment != orders.PaymentPaid || status != orders.FulfillmentUnfulfilled {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE public.order_fulfillment SET status='fulfilled',updated_at=now() WHERE order_id=$1`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "fulfill_order", id, ip, map[string]any{"status": "fulfilled"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Restock atomically increments the ledger stock for a product, records a stock ledger entry and admin audit entry,
// and enforces CanManageInventory authorization.
func (s *Store) Restock(ctx context.Context, actor *auth.User, productID string, quantity int, reference string, ip string) error {
	if actor == nil || !auth.CanManageInventory(*actor) {
		return auth.ErrPermissionDenied
	}
	if quantity <= 0 {
		return errors.New("quantity must be greater than zero")
	}
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return errors.New("reference note is required")
	}
	if len(reference) > 512 {
		return errors.New("reference note exceeds 512 characters")
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)

	var currentStock int
	err = tx.QueryRow(ctx, `SELECT stock FROM public.catalog_product WHERE id=$1 FOR UPDATE`, productID).Scan(&currentStock)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("product not found")
	}
	if err != nil {
		return err
	}

	newStock := currentStock + quantity
	if newStock > 1000000 {
		return errors.New("stock limit exceeded")
	}

	if _, err = tx.Exec(ctx, `UPDATE public.catalog_product SET stock=$2, opened=true WHERE id=$1`, productID, newStock); err != nil {
		return fmt.Errorf("failed to update stock: %w", err)
	}

	batchID := fmt.Sprintf("restock_%s_%d", productID, time.Now().UnixNano())
	rawDigest := sha256.Sum256([]byte(batchID + productID + reference))
	digest := hex.EncodeToString(rawDigest[:])

	if _, err = tx.Exec(ctx, `INSERT INTO public.stock_batch(id,digest,kind,actor,evidence) VALUES ($1,$2,'receipt',$3,$4)`, batchID, digest, actor.ID, reference); err != nil {
		return fmt.Errorf("failed to record stock batch: %w", err)
	}

	if _, err = tx.Exec(ctx, `INSERT INTO public.stock_entry(batch_id,product_id,delta,balance) VALUES ($1,$2,$3,$4)`, batchID, productID, quantity, newStock); err != nil {
		return fmt.Errorf("failed to record stock entry: %w", err)
	}

	if err = auditResource(ctx, tx, actor, "restock_inventory", "catalog_product", productID, ip, map[string]any{
		"product_id": productID,
		"quantity":   quantity,
		"new_stock":  newStock,
		"reference":  reference,
		"batch_id":   batchID,
	}); err != nil {
		return fmt.Errorf("failed to record audit: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return err
	}

	if s.Invalidator != nil {
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.Invalidator.Invalidate(cctx)
		}()
	}

	return nil
}

func audit(ctx context.Context, tx pgx.Tx, u *auth.User, action, id, ip string, metadata map[string]any) error {
	return auditResource(ctx, tx, u, action, "order", id, ip, metadata)
}

func auditResource(ctx context.Context, tx pgx.Tx, u *auth.User, action, resource, id, ip string, metadata map[string]any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.admin_audit_log(id,user_id,actor_role,action,resource,resource_id,ip_address,metadata) VALUES(gen_random_uuid()::text,$1,$2,$3,$4,$5,$6,$7)`, u.ID, u.Role, action, resource, id, ip, raw)
	return err
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
