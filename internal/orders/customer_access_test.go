package orders

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
)

func TestShopperPayloadAndUntrustedIdentity(t *testing.T) {
	var req CreateOrderRequest
	err := json.Unmarshal([]byte(`{"customerName":"Shopper","customerPhone":"254712345678","deliveryOption":"delivery","deliveryLocation":"Nyali","paymentMethod":"pay_now","notes":"Gift","user_id":"owner","UserID":"owner","items":[{"id":"p1","quantity":2}]}`), &req)
	if err != nil || req.Customer.Name != "Shopper" || req.Items[0].Qty != 2 || req.UserID != "" {
		t.Fatalf("shopper contract: %+v %v", req, err)
	}
	if err = json.Unmarshal([]byte(`{"items":[{"id":"p1","qty":1,"quantity":2}]}`), &req); !errors.Is(err, ErrInvalidQuantity) {
		t.Fatal("conflicting quantities accepted")
	}
}

func TestCustomerOrderOwnershipAndPickup(t *testing.T) {
	pool := testPool(t)
	setupCatalogFixture(t, pool, "p1", 10, true, nil)
	ctx := context.Background()
	store := &Store{Pool: pool}
	user, err := auth.NewPGStore(pool).UpsertUserByGoogleSub(ctx, "shopper", "shopper@example.test", "Shopper", "")
	if err != nil {
		t.Fatal(err)
	}
	order, err := store.CreateOrder(ctx, CreateOrderRequest{UserID: user.ID, Customer: CustomerInput{Name: "Shopper", Phone: "254712345678", Location: "Nyali", Delivery: "delivery", Payment: "pay_now"}, Items: []ItemInput{{ID: "p1", Qty: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if order.LatestAttempt != nil {
		t.Fatal("delivery order created a payment attempt before quote acceptance")
	}
	if owns, err := store.CustomerOwnsOrder(ctx, order.ID, user.ID); err != nil || !owns {
		t.Fatal("customer ownership missing", err)
	}
	if owns, err := store.CustomerOwnsOrder(ctx, order.ID, "someone-else"); err != nil || owns {
		t.Fatal("cross-customer ownership leaked", err)
	}
	if err = store.SwitchToPickup(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetOrder(ctx, order.ID)
	if err != nil || current.DeliveryOption != "pickup" {
		t.Fatal("pickup change failed", err)
	}
	if !current.Reservation.ExpiresAt.Equal(order.Reservation.ExpiresAt) {
		t.Fatal("pickup renewed reservation")
	}
	attempt, err := store.CreatePaymentAttempt(ctx, order.ID, "254712345678", 2500, "mpesa_stk")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SwitchToPickup(ctx, order.ID); !errors.Is(err, ErrUnresolvedPaymentAttempt) {
		t.Fatal("changed delivery with unresolved payment", err)
	}
	if err = store.UpdatePaymentAttempt(ctx, attempt.ID, AttemptFailed, "", "", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err = store.ConfirmPayment(ctx, order.ID, "SHOPPER-RECEIPT", 2500); err != nil {
		t.Fatal(err)
	}
	if err = store.SwitchToPickup(ctx, order.ID); !errors.Is(err, ErrOrderAlreadyPaid) {
		t.Fatal("paid order changed", err)
	}
}
