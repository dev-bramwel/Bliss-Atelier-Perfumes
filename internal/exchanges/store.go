// Package exchanges implements the unused-perfume exchange workflow,
// quarantined return tracking, 48-hour post-payment eligibility checks,
// and idempotent stock/audit updates.
package exchanges

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/outbox"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ExchangeWindow = 48 * time.Hour

var (
	ErrNotFound               = errors.New("exchange request not found")
	ErrOrderNotFound          = errors.New("order not found")
	ErrOrderNotPaid           = errors.New("only paid orders qualify for an exchange")
	ErrWindowExpired          = errors.New("exchange window has closed (must be within 48 hours of payment)")
	ErrInvalidQuantity        = errors.New("quantities must be positive integers")
	ErrInvalidProduct         = errors.New("returned and replacement products are required")
	ErrItemNotInOrder         = errors.New("returned product was not purchased in this order")
	ErrQuantityExceeded       = errors.New("returned quantity exceeds remaining eligible quantity on this order")
	ErrLowerValueExcluded     = errors.New("lower-value replacements are excluded; replacement must be equal or higher value")
	ErrReasonRequired         = errors.New("reason is required")
	ErrInspectionNotesReq     = errors.New("inspection notes are required")
	ErrInvalidTransition      = errors.New("exchange status does not permit this transition")
	ErrInspectionFailed       = errors.New("exchange cannot be approved unless inspection passed")
	ErrTopupRequired          = errors.New("verified top-up receipt and evidence are required for higher-value replacements")
	ErrAuthorityRequired      = errors.New("stock authority is unconfirmed; replacement approval required")
	ErrUnopenedProduct        = errors.New("opening balance required before modifying stock")
	ErrInsufficientStock      = errors.New("insufficient replacement stock")
	ErrInsufficientUnreserved = errors.New("insufficient unreserved replacement stock")
	ErrIdempotencyConflict    = errors.New("idempotency key already used with different request payload")
)

type Invalidator interface {
	Invalidate(ctx context.Context) error
}

type Store struct {
	Pool        *pgxpool.Pool
	Invalidator Invalidator
}

type ExchangeItem struct {
	ID                      string `json:"id"`
	ReturnedProductID       string `json:"returned_product_id"`
	ReturnedProductName     string `json:"returned_product_name"`
	ReturnedQuantity        int    `json:"returned_quantity"`
	ReturnedUnitPriceKes    int    `json:"returned_unit_price_kes"`
	ReplacementProductID    string `json:"replacement_product_id"`
	ReplacementProductName  string `json:"replacement_product_name"`
	ReplacementQuantity     int    `json:"replacement_quantity"`
	ReplacementUnitPriceKes int    `json:"replacement_unit_price_kes"`
	QuarantineStatus        string `json:"quarantine_status"`
}

type ExchangeRequest struct {
	ID                 string         `json:"id"`
	OrderID            string         `json:"order_id"`
	Status             string         `json:"status"`
	Reason             string         `json:"reason"`
	RequestedBy        string         `json:"requested_by"`
	RequestedByRole    string         `json:"requested_by_role"`
	InspectedBy        *string        `json:"inspected_by,omitempty"`
	InspectedByRole    *string        `json:"inspected_by_role,omitempty"`
	InspectionPassed   *bool          `json:"inspection_passed,omitempty"`
	InspectionNotes    *string        `json:"inspection_notes,omitempty"`
	InspectedAt        *time.Time     `json:"inspected_at,omitempty"`
	DecidedBy          *string        `json:"decided_by,omitempty"`
	DecidedByRole      *string        `json:"decided_by_role,omitempty"`
	DecisionReason     *string        `json:"decision_reason,omitempty"`
	DecidedAt          *time.Time     `json:"decided_at,omitempty"`
	CompletedBy        *string        `json:"completed_by,omitempty"`
	CompletedByRole    *string        `json:"completed_by_role,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	PriceDifferenceKes int            `json:"price_difference_kes"`
	TopupReceipt       *string        `json:"topup_receipt,omitempty"`
	TopupEvidence      *string        `json:"topup_evidence,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Items              []ExchangeItem `json:"items"`
}

type ExchangePage struct {
	Exchanges []ExchangeRequest `json:"exchanges"`
	Total     int               `json:"total"`
}

type CreateRequestInput struct {
	OrderID              string `json:"order_id"`
	ReturnedProductID    string `json:"returned_product_id"`
	ReturnedQuantity     int    `json:"returned_quantity"`
	ReplacementProductID string `json:"replacement_product_id"`
	ReplacementQuantity  int    `json:"replacement_quantity"`
	Reason               string `json:"reason"`
}

func (s *Store) List(ctx context.Context, statusFilter string, limit, offset int) (*ExchangePage, error) {
	page := &ExchangePage{Exchanges: []ExchangeRequest{}}
	statusFilter = strings.TrimSpace(statusFilter)
	if statusFilter == "all" {
		statusFilter = ""
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.exchange_request WHERE ($1='' OR status=$1)`, statusFilter).Scan(&page.Total); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id::text, order_id, status, reason, requested_by, requested_by_role,
			inspected_by, inspected_by_role, inspection_passed, inspection_notes, inspected_at,
			decided_by, decided_by_role, decision_reason, decided_at,
			completed_by, completed_by_role, completed_at,
			price_difference_kes, topup_receipt, topup_evidence, created_at, updated_at
		FROM public.exchange_request
		WHERE ($1='' OR status=$1)
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`, statusFilter, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	byID := make(map[string]int)
	for rows.Next() {
		var r ExchangeRequest
		if err := rows.Scan(
			&r.ID, &r.OrderID, &r.Status, &r.Reason, &r.RequestedBy, &r.RequestedByRole,
			&r.InspectedBy, &r.InspectedByRole, &r.InspectionPassed, &r.InspectionNotes, &r.InspectedAt,
			&r.DecidedBy, &r.DecidedByRole, &r.DecisionReason, &r.DecidedAt,
			&r.CompletedBy, &r.CompletedByRole, &r.CompletedAt,
			&r.PriceDifferenceKes, &r.TopupReceipt, &r.TopupEvidence, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		r.Items = []ExchangeItem{}
		byID[r.ID] = len(page.Exchanges)
		ids = append(ids, r.ID)
		page.Exchanges = append(page.Exchanges, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		itemRows, err := tx.Query(ctx, `
			SELECT ei.id::text, ei.exchange_id::text, ei.returned_product_id, rp.name, ei.returned_quantity, ei.returned_unit_price_kes,
				ei.replacement_product_id, np.name, ei.replacement_quantity, ei.replacement_unit_price_kes, ei.quarantine_status
			FROM public.exchange_item ei
			JOIN public.catalog_product rp ON rp.id = ei.returned_product_id
			JOIN public.catalog_product np ON np.id = ei.replacement_product_id
			WHERE ei.exchange_id::text = ANY($1)
			ORDER BY ei.id`, ids)
		if err != nil {
			return nil, err
		}
		defer itemRows.Close()
		for itemRows.Next() {
			var exID string
			var item ExchangeItem
			if err := itemRows.Scan(
				&item.ID, &exID, &item.ReturnedProductID, &item.ReturnedProductName, &item.ReturnedQuantity, &item.ReturnedUnitPriceKes,
				&item.ReplacementProductID, &item.ReplacementProductName, &item.ReplacementQuantity, &item.ReplacementUnitPriceKes, &item.QuarantineStatus,
			); err != nil {
				return nil, err
			}
			if idx, ok := byID[exID]; ok {
				page.Exchanges[idx].Items = append(page.Exchanges[idx].Items, item)
			}
		}
		if err := itemRows.Err(); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (s *Store) CreateRequest(ctx context.Context, actor *auth.User, in CreateRequestInput, idempotencyKey, ip string) (*ExchangeRequest, error) {
	if actor == nil || !auth.IsStaff(actor.Role) {
		return nil, auth.ErrPermissionDenied
	}
	in.OrderID = strings.TrimSpace(in.OrderID)
	in.ReturnedProductID = strings.TrimSpace(in.ReturnedProductID)
	in.ReplacementProductID = strings.TrimSpace(in.ReplacementProductID)
	in.Reason = strings.TrimSpace(in.Reason)
	idempotencyKey = strings.TrimSpace(idempotencyKey)

	if in.OrderID == "" {
		return nil, ErrOrderNotFound
	}
	if in.ReturnedProductID == "" || in.ReplacementProductID == "" {
		return nil, ErrInvalidProduct
	}
	if in.ReturnedQuantity <= 0 || in.ReplacementQuantity <= 0 {
		return nil, ErrInvalidQuantity
	}
	if in.Reason == "" || len(in.Reason) > 512 {
		return nil, ErrReasonRequired
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	var reqHash string
	if idempotencyKey != "" {
		raw, _ := json.Marshal(in)
		sum := sha256.Sum256(raw)
		reqHash = hex.EncodeToString(sum[:])

		// Reserve the idempotency row up front so concurrent retries with the same key
		// block until the first transaction commits or rolls back.
		tag, err := tx.Exec(ctx, `
			INSERT INTO public.idempotency_key (
				key, caller_scope, operation, request_hash, status, expires_at
			) VALUES ($1, $2, 'create_exchange', $3, 'in_progress', now() + interval '24 hours')
			ON CONFLICT (key, caller_scope, operation) DO NOTHING`,
			idempotencyKey, "staff:"+actor.ID, reqHash)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			var existingHash, existingStatus, existingBody string
			if err := tx.QueryRow(ctx, `
				SELECT request_hash, status, response_body
				FROM public.idempotency_key
				WHERE key=$1 AND caller_scope=$2 AND operation='create_exchange'
				FOR UPDATE`, idempotencyKey, "staff:"+actor.ID).Scan(&existingHash, &existingStatus, &existingBody); err != nil {
				return nil, err
			}
			if existingHash != reqHash {
				return nil, ErrIdempotencyConflict
			}
			if existingStatus == "completed" && existingBody != "" {
				var cached ExchangeRequest
				if err := json.Unmarshal([]byte(existingBody), &cached); err == nil {
					return &cached, nil
				}
			}
		}
	}

	var paymentStatus string
	var paidAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT o."paymentStatus",
			COALESCE(
				(SELECT max(pa.updated_at) FROM public.payment_attempt pa WHERE pa.order_id = o.id AND pa.status = 'succeeded'),
				(SELECT max(pal.created_at) FROM public.payment_audit_ledger pal WHERE pal.order_id = o.id),
				(SELECT m."updatedAt" FROM public."MpesaTransaction" m WHERE m."orderId" = o.id AND m.status IN ('PAID', 'COMPLETED', 'SUCCESS')),
				o."updatedAt"
			)
		FROM public."Order" o
		WHERE o.id = $1
		FOR UPDATE`, in.OrderID).Scan(&paymentStatus, &paidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if paymentStatus != orders.PaymentPaid {
		return nil, ErrOrderNotPaid
	}
	if time.Now().UTC().After(paidAt.UTC().Add(ExchangeWindow)) {
		return nil, ErrWindowExpired
	}

	var purchasedQty, returnedUnitPrice int
	err = tx.QueryRow(ctx, `
		SELECT coalesce(sum(quantity), 0), coalesce(max(unit_price_kes), 0)
		FROM public.order_item
		WHERE order_id = $1 AND product_id = $2`, in.OrderID, in.ReturnedProductID).Scan(&purchasedQty, &returnedUnitPrice)
	if err != nil {
		return nil, err
	}
	if purchasedQty <= 0 {
		return nil, ErrItemNotInOrder
	}

	var alreadyClaimed int
	err = tx.QueryRow(ctx, `
		SELECT coalesce(sum(ei.returned_quantity), 0)
		FROM public.exchange_item ei
		JOIN public.exchange_request er ON er.id = ei.exchange_id
		WHERE er.order_id = $1 AND ei.returned_product_id = $2 AND er.status <> 'rejected'`,
		in.OrderID, in.ReturnedProductID).Scan(&alreadyClaimed)
	if err != nil {
		return nil, err
	}
	if in.ReturnedQuantity > purchasedQty-alreadyClaimed {
		return nil, ErrQuantityExceeded
	}

	var replacementUnitPrice int
	err = tx.QueryRow(ctx, `
		SELECT coalesce(p.price_override_kes, c.common_price_kes)
		FROM public.catalog_product p
		CROSS JOIN public.catalog_settings c
		WHERE p.id = $1`, in.ReplacementProductID).Scan(&replacementUnitPrice)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidProduct
	}
	if err != nil {
		return nil, err
	}

	returnedTotal := returnedUnitPrice * in.ReturnedQuantity
	replacementTotal := replacementUnitPrice * in.ReplacementQuantity
	if replacementTotal < returnedTotal {
		return nil, ErrLowerValueExcluded
	}
	priceDiff := replacementTotal - returnedTotal

	var exchangeID string
	err = tx.QueryRow(ctx, `
		INSERT INTO public.exchange_request (
			order_id, status, reason, requested_by, requested_by_role, price_difference_kes
		) VALUES ($1, 'requested', $2, $3, $4, $5)
		RETURNING id::text`,
		in.OrderID, in.Reason, actor.ID, actor.Role, priceDiff).Scan(&exchangeID)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.exchange_item (
			exchange_id, returned_product_id, returned_quantity, returned_unit_price_kes,
			replacement_product_id, replacement_quantity, replacement_unit_price_kes, quarantine_status
		) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'quarantined')`,
		exchangeID, in.ReturnedProductID, in.ReturnedQuantity, returnedUnitPrice,
		in.ReplacementProductID, in.ReplacementQuantity, replacementUnitPrice); err != nil {
		return nil, err
	}

	// Quarantine returned bottles immediately; never add uninspected returns to saleable stock.
	if _, err := tx.Exec(ctx, `
		UPDATE public.catalog_product
		SET quarantined_stock = quarantined_stock + $2
		WHERE id = $1`, in.ReturnedProductID, in.ReturnedQuantity); err != nil {
		return nil, err
	}

	if err := audit(ctx, tx, actor, "request_exchange", exchangeID, ip, map[string]any{
		"order_id":               in.OrderID,
		"returned_product_id":    in.ReturnedProductID,
		"returned_quantity":      in.ReturnedQuantity,
		"replacement_product_id": in.ReplacementProductID,
		"replacement_quantity":   in.ReplacementQuantity,
		"price_difference_kes":   priceDiff,
		"reason":                 in.Reason,
	}); err != nil {
		return nil, err
	}

	res, err := loadByID(ctx, tx, exchangeID)
	if err != nil {
		return nil, err
	}

	if idempotencyKey != "" {
		rawResp, _ := json.Marshal(res)
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.idempotency_key (
				key, caller_scope, operation, request_hash, status, status_code, response_body, resource_id, expires_at
			) VALUES ($1, $2, 'create_exchange', $3, 'completed', 201, $4, $5, now() + interval '24 hours')
			ON CONFLICT (key, caller_scope, operation) DO UPDATE
			SET status='completed', status_code=201, response_body=EXCLUDED.response_body, resource_id=EXCLUDED.resource_id`,
			idempotencyKey, "staff:"+actor.ID, reqHash, string(rawResp), exchangeID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Store) Inspect(ctx context.Context, actor *auth.User, exchangeID string, passed bool, notes, ip string) (*ExchangeRequest, error) {
	if actor == nil || !auth.IsStaff(actor.Role) {
		return nil, auth.ErrPermissionDenied
	}
	notes = strings.TrimSpace(notes)
	if notes == "" || len(notes) > 1000 {
		return nil, ErrInspectionNotesReq
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	var status string
	var existingPassed *bool
	var existingNotes *string
	err = tx.QueryRow(ctx, `
		SELECT status, inspection_passed, inspection_notes
		FROM public.exchange_request
		WHERE id = $1::uuid
		FOR UPDATE`, exchangeID).Scan(&status, &existingPassed, &existingNotes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if status == "inspected" && existingPassed != nil && *existingPassed == passed && existingNotes != nil && *existingNotes == notes {
		return loadByID(ctx, tx, exchangeID)
	}
	if status != "requested" {
		return nil, ErrInvalidTransition
	}

	if _, err := tx.Exec(ctx, `
		UPDATE public.exchange_request
		SET status = 'inspected',
			inspected_by = $2,
			inspected_by_role = $3,
			inspection_passed = $4,
			inspection_notes = $5,
			inspected_at = now(),
			updated_at = now()
		WHERE id = $1::uuid`, exchangeID, actor.ID, actor.Role, passed, notes); err != nil {
		return nil, err
	}

	if err := audit(ctx, tx, actor, "inspect_exchange", exchangeID, ip, map[string]any{
		"passed": passed,
		"notes":  notes,
	}); err != nil {
		return nil, err
	}

	res, err := loadByID(ctx, tx, exchangeID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Store) Decide(ctx context.Context, actor *auth.User, exchangeID string, approve bool, reason, ip string) (*ExchangeRequest, error) {
	if actor == nil || !auth.IsStaff(actor.Role) {
		return nil, auth.ErrPermissionDenied
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 512 {
		return nil, ErrReasonRequired
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	var status string
	var inspectionPassed *bool
	err = tx.QueryRow(ctx, `
		SELECT status, inspection_passed
		FROM public.exchange_request
		WHERE id = $1::uuid
		FOR UPDATE`, exchangeID).Scan(&status, &inspectionPassed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	targetStatus := "rejected"
	auditAction := "reject_exchange"
	if approve {
		targetStatus = "approved"
		auditAction = "approve_exchange"
	}

	if status == targetStatus {
		return loadByID(ctx, tx, exchangeID)
	}
	if approve {
		if status != "inspected" {
			return nil, ErrInvalidTransition
		}
		if inspectionPassed == nil || !*inspectionPassed {
			return nil, ErrInspectionFailed
		}
	} else {
		if status != "requested" && status != "inspected" {
			return nil, ErrInvalidTransition
		}
		if _, err := tx.Exec(ctx, `
			UPDATE public.exchange_item
			SET quarantine_status = 'rejected'
			WHERE exchange_id = $1::uuid`, exchangeID); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE public.exchange_request
		SET status = $2,
			decided_by = $3,
			decided_by_role = $4,
			decision_reason = $5,
			decided_at = now(),
			updated_at = now()
		WHERE id = $1::uuid`, exchangeID, targetStatus, actor.ID, actor.Role, reason); err != nil {
		return nil, err
	}

	if err := audit(ctx, tx, actor, auditAction, exchangeID, ip, map[string]any{
		"status": targetStatus,
		"reason": reason,
	}); err != nil {
		return nil, err
	}

	res, err := loadByID(ctx, tx, exchangeID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Store) Complete(ctx context.Context, actor *auth.User, exchangeID, topupReceipt, topupEvidence, ip string) (*ExchangeRequest, error) {
	if actor == nil || !auth.IsStaff(actor.Role) {
		return nil, auth.ErrPermissionDenied
	}
	topupReceipt = strings.TrimSpace(topupReceipt)
	topupEvidence = strings.TrimSpace(topupEvidence)

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	var status, orderID string
	var inspectionPassed *bool
	var priceDiff int
	err = tx.QueryRow(ctx, `
		SELECT status, order_id, inspection_passed, price_difference_kes
		FROM public.exchange_request
		WHERE id = $1::uuid
		FOR UPDATE`, exchangeID).Scan(&status, &orderID, &inspectionPassed, &priceDiff)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if status == "completed" {
		return loadByID(ctx, tx, exchangeID)
	}
	if status != "approved" {
		return nil, ErrInvalidTransition
	}
	if inspectionPassed == nil || !*inspectionPassed {
		return nil, ErrInspectionFailed
	}
	if priceDiff > 0 && (topupReceipt == "" || topupEvidence == "") {
		return nil, ErrTopupRequired
	}

	var authority string
	if err := tx.QueryRow(ctx, `SELECT authority FROM public.stock_control WHERE singleton FOR UPDATE`).Scan(&authority); err != nil {
		return nil, err
	}
	if authority != "replacement" {
		return nil, ErrAuthorityRequired
	}

	var retProdID, repProdID string
	var retQty, repQty int
	err = tx.QueryRow(ctx, `
		SELECT returned_product_id, returned_quantity, replacement_product_id, replacement_quantity
		FROM public.exchange_item
		WHERE exchange_id = $1::uuid
		LIMIT 1`, exchangeID).Scan(&retProdID, &retQty, &repProdID, &repQty)
	if err != nil {
		return nil, err
	}

	// Lock products in deterministic order to avoid deadlocks.
	lockIDs := []string{retProdID}
	if repProdID != retProdID {
		lockIDs = append(lockIDs, repProdID)
	}
	sort.Strings(lockIDs)

	type prodState struct {
		stock       int
		quarantined int
		opened      bool
	}
	products := make(map[string]prodState, len(lockIDs))
	for _, pid := range lockIDs {
		var st prodState
		if err := tx.QueryRow(ctx, `SELECT stock, quarantined_stock, opened FROM public.catalog_product WHERE id = $1 FOR UPDATE`, pid).Scan(&st.stock, &st.quarantined, &st.opened); err != nil {
			return nil, err
		}
		products[pid] = st
	}

	repState := products[repProdID]
	if !repState.opened {
		return nil, ErrUnopenedProduct
	}
	if repState.stock < repQty {
		return nil, ErrInsufficientStock
	}

	var activeReserved int
	if err := tx.QueryRow(ctx, `SELECT coalesce((
		SELECT sum(ri.quantity)
		FROM public.stock_reservation_item ri
		JOIN public.stock_reservation r ON r.id = ri.reservation_id
		WHERE ri.product_id = $1 AND r.status = 'active' AND r.expires_at > now()
	), 0)`, repProdID).Scan(&activeReserved); err != nil {
		return nil, err
	}
	if repState.stock-repQty < activeReserved {
		return nil, ErrInsufficientUnreserved
	}

	// Apply stock transitions atomically.
	newRepStock := repState.stock - repQty
	if _, err := tx.Exec(ctx, `UPDATE public.catalog_product SET stock = $2 WHERE id = $1`, repProdID, newRepStock); err != nil {
		return nil, err
	}

	retState := products[retProdID]
	baseRetStock := retState.stock
	if retProdID == repProdID {
		baseRetStock = newRepStock
	}
	newRetStock := baseRetStock + retQty
	newQuarantined := retState.quarantined - retQty
	if newQuarantined < 0 {
		newQuarantined = 0
	}
	if _, err := tx.Exec(ctx, `UPDATE public.catalog_product SET stock = $2, quarantined_stock = $3 WHERE id = $1`, retProdID, newRetStock, newQuarantined); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `UPDATE public.exchange_item SET quarantine_status = 'released_to_stock' WHERE exchange_id = $1::uuid`, exchangeID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `UPDATE public.stock_control SET reconciled_until = NULL WHERE singleton`); err != nil {
		return nil, err
	}

	issueBatchID := "exch-issue-" + exchangeID
	issueDigest := digestFor(issueBatchID, repProdID, -repQty, orderID)
	if _, err := tx.Exec(ctx, `INSERT INTO public.stock_batch(id,digest,kind,actor,evidence) VALUES ($1,$2,'exchange_issue',$3,$4)`, issueBatchID, issueDigest, actor.ID, "exchange:"+exchangeID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.stock_entry(batch_id,product_id,delta,balance) VALUES ($1,$2,$3,$4)`, issueBatchID, repProdID, -repQty, newRepStock); err != nil {
		return nil, err
	}

	returnBatchID := "exch-return-" + exchangeID
	returnDigest := digestFor(returnBatchID, retProdID, retQty, orderID)
	if _, err := tx.Exec(ctx, `INSERT INTO public.stock_batch(id,digest,kind,actor,evidence) VALUES ($1,$2,'exchange_return',$3,$4)`, returnBatchID, returnDigest, actor.ID, "exchange:"+exchangeID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.stock_entry(batch_id,product_id,delta,balance) VALUES ($1,$2,$3,$4)`, returnBatchID, retProdID, retQty, newRetStock); err != nil {
		return nil, err
	}

	var receiptPtr, evidencePtr *string
	if topupReceipt != "" {
		receiptPtr = &topupReceipt
	}
	if topupEvidence != "" {
		evidencePtr = &topupEvidence
	}

	if _, err := tx.Exec(ctx, `
		UPDATE public.exchange_request
		SET status = 'completed',
			topup_receipt = $2,
			topup_evidence = $3,
			completed_by = $4,
			completed_by_role = $5,
			completed_at = now(),
			updated_at = now()
		WHERE id = $1::uuid`, exchangeID, receiptPtr, evidencePtr, actor.ID, actor.Role); err != nil {
		return nil, err
	}

	if err := audit(ctx, tx, actor, "complete_exchange", exchangeID, ip, map[string]any{
		"order_id":               orderID,
		"returned_product_id":    retProdID,
		"returned_quantity":      retQty,
		"replacement_product_id": repProdID,
		"replacement_quantity":   repQty,
		"price_difference_kes":   priceDiff,
		"topup_receipt":          topupReceipt,
		"topup_evidence":         topupEvidence,
		"not_full_price_sale":    true,
	}); err != nil {
		return nil, err
	}

	res, err := loadByID(ctx, tx, exchangeID)
	if err != nil {
		return nil, err
	}
	completedAt := time.Now().UTC()
	if res.CompletedAt != nil {
		completedAt = res.CompletedAt.UTC()
	}
	analyticsExchEvt, err := events.NewEvent(events.TypeAnalyticsExchangeCompleted, orderID, 1, telemetry.FromContext(ctx), events.AnalyticsExchangeCompletedPayload{
		ExchangeID:         exchangeID,
		OrderID:            orderID,
		Currency:           events.CurrencyKES,
		PriceDifferenceKes: priceDiff,
		CompletedAt:        completedAt,
	})
	if err != nil {
		return nil, err
	}
	if err := outbox.NewStore(s.Pool).Enqueue(ctx, tx, analyticsExchEvt, outbox.DestinationKafka); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	if s.Invalidator != nil {
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.Invalidator.Invalidate(cctx)
		}()
	}
	return res, nil
}

func loadByID(ctx context.Context, tx pgx.Tx, exchangeID string) (*ExchangeRequest, error) {
	var r ExchangeRequest
	err := tx.QueryRow(ctx, `
		SELECT id::text, order_id, status, reason, requested_by, requested_by_role,
			inspected_by, inspected_by_role, inspection_passed, inspection_notes, inspected_at,
			decided_by, decided_by_role, decision_reason, decided_at,
			completed_by, completed_by_role, completed_at,
			price_difference_kes, topup_receipt, topup_evidence, created_at, updated_at
		FROM public.exchange_request
		WHERE id = $1::uuid`, exchangeID).Scan(
		&r.ID, &r.OrderID, &r.Status, &r.Reason, &r.RequestedBy, &r.RequestedByRole,
		&r.InspectedBy, &r.InspectedByRole, &r.InspectionPassed, &r.InspectionNotes, &r.InspectedAt,
		&r.DecidedBy, &r.DecidedByRole, &r.DecisionReason, &r.DecidedAt,
		&r.CompletedBy, &r.CompletedByRole, &r.CompletedAt,
		&r.PriceDifferenceKes, &r.TopupReceipt, &r.TopupEvidence, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Items = []ExchangeItem{}
	rows, err := tx.Query(ctx, `
		SELECT ei.id::text, ei.returned_product_id, rp.name, ei.returned_quantity, ei.returned_unit_price_kes,
			ei.replacement_product_id, np.name, ei.replacement_quantity, ei.replacement_unit_price_kes, ei.quarantine_status
		FROM public.exchange_item ei
		JOIN public.catalog_product rp ON rp.id = ei.returned_product_id
		JOIN public.catalog_product np ON np.id = ei.replacement_product_id
		WHERE ei.exchange_id = $1::uuid
		ORDER BY ei.id`, exchangeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ExchangeItem
		if err := rows.Scan(
			&item.ID, &item.ReturnedProductID, &item.ReturnedProductName, &item.ReturnedQuantity, &item.ReturnedUnitPriceKes,
			&item.ReplacementProductID, &item.ReplacementProductName, &item.ReplacementQuantity, &item.ReplacementUnitPriceKes, &item.QuarantineStatus,
		); err != nil {
			return nil, err
		}
		r.Items = append(r.Items, item)
	}
	return &r, rows.Err()
}

func audit(ctx context.Context, tx pgx.Tx, u *auth.User, action, id, ip string, metadata map[string]any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.admin_audit_log(id,user_id,actor_role,action,resource,resource_id,ip_address,metadata) VALUES(gen_random_uuid()::text,$1,$2,$3,'exchange_request',$4,$5,$6)`, u.ID, u.Role, action, id, ip, raw)
	return err
}

func digestFor(batchID, productID string, delta int, orderID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%s", batchID, productID, delta, orderID)))
	return hex.EncodeToString(sum[:])
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
