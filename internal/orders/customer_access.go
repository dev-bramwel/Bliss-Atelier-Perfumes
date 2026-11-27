package orders

import (
	"context"
	"errors"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/outbox"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

// CustomerOwnsOrder checks ownership independently of the browser's guest token.
func (s *Store) CustomerOwnsOrder(ctx context.Context, orderID, userID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.guest_order_access WHERE order_id=$1 AND user_id=$2)`, orderID, userID).Scan(&allowed)
	return allowed, err
}

// SwitchToPickup serializes against payment initiation and preserves the existing hold.
func (s *Store) SwitchToPickup(ctx context.Context, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT "paymentStatus" FROM public."Order" WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		return err
	}
	if status == PaymentPaid {
		return ErrOrderAlreadyPaid
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.payment_attempt WHERE order_id=$1 AND status IN ('created','initiating','accepted','unknown'))`, id).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrUnresolvedPaymentAttempt
	}
	var held bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.stock_reservation WHERE order_id=$1 AND status='active' AND expires_at>now())`, id).Scan(&held); err != nil {
		return err
	}
	if !held {
		return ErrOrderReservationExpired
	}
	var totalKes int
	var updatedAt time.Time
	if err = tx.QueryRow(ctx, `UPDATE public."Order" SET "deliveryOption"='pickup', "deliveryLocation"='Mombasa Atelier', "updatedAt"=now() WHERE id=$1 RETURNING "totalKes", "updatedAt"`, id).Scan(&totalKes, &updatedAt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE public.delivery_quote SET status='rejected', rejected_at=now() WHERE order_id=$1 AND status IN ('pending','accepted')`, id); err != nil {
		return err
	}
	analyticsUpdEvt, err := events.NewEvent(events.TypeAnalyticsOrderUpdated, id, 1, telemetry.FromContext(ctx), events.AnalyticsOrderUpdatedPayload{
		OrderID:       id,
		Currency:      events.CurrencyKES,
		TotalKes:      totalKes,
		PaymentStatus: status,
		UpdatedAt:     updatedAt.UTC(),
	})
	if err != nil {
		return err
	}
	if err := outbox.NewStore(s.Pool).Enqueue(ctx, tx, analyticsUpdEvt, outbox.DestinationKafka); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
