package orders

import (
	"context"
	"errors"
	"testing"
)

func TestStore_PaymentExceptionsCRUD(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	phone := "254712345678"
	recAmt := 6000
	expAmt := 5000
	txID := "NCBA-TX-ERR-1"

	exc, err := store.RecordPaymentException(ctx, PaymentException{
		ExceptionType:         ExceptionAmountMismatch,
		Provider:              "ncba",
		ProviderTransactionID: &txID,
		ReceiptNumber:         &txID,
		ReceivedAmountKes:     &recAmt,
		ExpectedAmountKes:     &expAmt,
		CustomerPhone:         &phone,
		Status:                ExceptionStatusOpen,
		Metadata: map[string]any{
			"notes": "overpayment of 1000 KES",
		},
	})
	if err != nil {
		t.Fatalf("failed to record payment exception: %v", err)
	}
	if exc.ID == "" || exc.Status != ExceptionStatusOpen {
		t.Fatalf("unexpected exception recorded: %+v", exc)
	}

	// 1. Get by ID
	fetched, err := store.GetPaymentExceptionByID(ctx, exc.ID)
	if err != nil {
		t.Fatalf("failed to get exception by ID: %v", err)
	}
	if fetched.ID != exc.ID || fetched.ExceptionType != ExceptionAmountMismatch {
		t.Errorf("mismatched fetched exception: %+v", fetched)
	}

	// 2. List open exceptions
	list, err := store.GetPaymentExceptions(ctx, ExceptionFilter{Status: "open", Limit: 10})
	if err != nil {
		t.Fatalf("failed to list exceptions: %v", err)
	}
	if len(list) != 1 || list[0].ID != exc.ID {
		t.Fatalf("expected 1 exception in list, got %d", len(list))
	}

	// 3. Resolve exception
	err = store.ResolvePaymentException(ctx, exc.ID, ResolveExceptionRequest{
		Action:       "refund_recorded",
		Notes:        "1000 KES difference refunded via NCBA reversal",
		AuthorizedBy: "test-admin",
		Evidence:     "Reversal ticket #REF-8899",
	})
	if err != nil {
		t.Fatalf("failed to resolve exception: %v", err)
	}

	resolved, err := store.GetPaymentExceptionByID(ctx, exc.ID)
	if err != nil {
		t.Fatalf("failed to get resolved exception: %v", err)
	}
	if resolved.Status != ExceptionStatusResolved || resolved.ResolvedBy == nil || *resolved.ResolvedBy != "test-admin" {
		t.Errorf("unexpected resolved state: %+v", resolved)
	}

	// 4. Resolving again fails
	err = store.ResolvePaymentException(ctx, exc.ID, ResolveExceptionRequest{
		Action:       "voided",
		Notes:        "duplicate resolve",
		AuthorizedBy: "test-admin",
	})
	if !errors.Is(err, ErrExceptionAlreadyResolved) {
		t.Errorf("expected ErrExceptionAlreadyResolved, got %v", err)
	}
}

func TestStore_RecordManualSettlement_Success(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	setupCatalogFixture(t, pool, "p1", 10, true, nil)

	// Create Order
	order, err := store.CreateOrder(ctx, CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Grace Hopper",
			Phone:    "254700000001",
			Location: "Nairobi",
			Delivery: "pickup",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: "p1", Qty: 2},
		},
	})
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// Record an open exception to verify auto-resolution
	txID := "NCBA-BANK-STMT-001"
	amt := order.TotalKes
	_, err = store.RecordPaymentException(ctx, PaymentException{
		OrderID:           &order.ID,
		ExceptionType:     ExceptionManualResolutionReq,
		Provider:          "ncba",
		ReceiptNumber:     &txID,
		ReceivedAmountKes: &amt,
		Status:            ExceptionStatusOpen,
	})
	if err != nil {
		t.Fatalf("failed to record initial exception: %v", err)
	}

	// Manual Settlement
	settledOrder, audit, err := store.RecordManualSettlement(ctx, ManualSettlementRequest{
		OrderID:       order.ID,
		ReceiptNumber: txID,
		AmountKes:     order.TotalKes,
		Evidence:      "NCBA Till statement 2026-11-09 line 15",
		AuthorizedBy:  "manager_alice",
		Notes:         "Verified customer pay-later direct till payment",
	})
	if err != nil {
		t.Fatalf("failed to record manual settlement: %v", err)
	}

	if settledOrder.PaymentStatus != PaymentPaid {
		t.Errorf("expected order status PAID, got %s", settledOrder.PaymentStatus)
	}
	if audit.Action != AuditActionManualSettlement || audit.ReceiptNumber != txID {
		t.Errorf("unexpected audit record: %+v", audit)
	}

	// Check catalog stock decremented (10 - 2 = 8)
	stock, err := store.GetAvailableStock(ctx, "p1")
	if err != nil {
		t.Fatalf("failed to get available stock: %v", err)
	}
	if stock != 8 {
		t.Errorf("expected remaining stock 8, got %d", stock)
	}

	// Verify open exception for this order was auto-resolved
	exceptions, err := store.GetPaymentExceptions(ctx, ExceptionFilter{Status: "resolved"})
	if err != nil {
		t.Fatalf("failed to query resolved exceptions: %v", err)
	}
	if len(exceptions) != 1 || exceptions[0].Status != ExceptionStatusResolved {
		t.Errorf("expected exception to be auto-resolved, got %+v", exceptions)
	}
}

func TestStore_RecordManualSettlement_Invariants(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	setupCatalogFixture(t, pool, "p2", 10, true, nil)

	order, err := store.CreateOrder(ctx, CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Ada Lovelace",
			Phone:    "254700000002",
			Location: "Nairobi",
			Delivery: "pickup",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: "p2", Qty: 1},
		},
	})
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// 1. Missing evidence fails
	_, _, err = store.RecordManualSettlement(ctx, ManualSettlementRequest{
		OrderID:       order.ID,
		ReceiptNumber: "RCPT-1",
		AmountKes:     order.TotalKes,
		Evidence:      "",
		AuthorizedBy:  "manager",
		Notes:         "Notes",
	})
	if err == nil {
		t.Error("expected error when evidence is empty")
	}

	// 2. Payable amount mismatch fails
	_, _, err = store.RecordManualSettlement(ctx, ManualSettlementRequest{
		OrderID:       order.ID,
		ReceiptNumber: "RCPT-1",
		AmountKes:     order.TotalKes + 500,
		Evidence:      "Bank slip",
		AuthorizedBy:  "manager",
		Notes:         "Notes",
	})
	if !errors.Is(err, ErrPayableMismatch) {
		t.Errorf("expected ErrPayableMismatch, got %v", err)
	}

	// 3. Expired stock reservation cannot silently mark PAID
	_, err = pool.Exec(ctx, `UPDATE public.stock_reservation SET expires_at = now() - interval '1 hour' WHERE order_id = $1`, order.ID)
	if err != nil {
		t.Fatalf("failed to expire reservation: %v", err)
	}

	_, _, err = store.RecordManualSettlement(ctx, ManualSettlementRequest{
		OrderID:       order.ID,
		ReceiptNumber: "RCPT-1",
		AmountKes:     order.TotalKes,
		Evidence:      "Bank slip",
		AuthorizedBy:  "manager",
		Notes:         "Notes",
	})
	if !errors.Is(err, ErrOrderReservationExpired) {
		t.Errorf("expected ErrOrderReservationExpired on expired hold, got %v", err)
	}
}

func TestStore_RecordPaymentCorrection(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	setupCatalogFixture(t, pool, "p3", 10, true, nil)

	order, err := store.CreateOrder(ctx, CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Alan Turing",
			Phone:    "254700000003",
			Location: "Nairobi",
			Delivery: "pickup",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: "p3", Qty: 1},
		},
	})
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// Record a duplicate receipt exception
	txID := "NCBA-DUP-99"
	amt := order.TotalKes
	exc, err := store.RecordPaymentException(ctx, PaymentException{
		OrderID:           &order.ID,
		ExceptionType:     ExceptionDuplicateReceipt,
		Provider:          "ncba",
		ReceiptNumber:     &txID,
		ReceivedAmountKes: &amt,
		Status:            ExceptionStatusOpen,
	})
	if err != nil {
		t.Fatalf("failed to record exception: %v", err)
	}

	// Record correction linking the exception
	audit, err := store.RecordPaymentCorrection(ctx, PaymentCorrectionRequest{
		OrderID:       order.ID,
		Action:        AuditActionDuplicateCorrection,
		ReceiptNumber: txID,
		AmountKes:     amt,
		Evidence:      "NCBA Reversal Ack #REV1234",
		AuthorizedBy:  "manager_bob",
		Notes:         "Processed bank reversal for accidental second prompt",
		ExceptionID:   &exc.ID,
	})
	if err != nil {
		t.Fatalf("failed to record correction: %v", err)
	}

	if audit.Action != AuditActionDuplicateCorrection || audit.ReceiptNumber != txID {
		t.Errorf("unexpected audit: %+v", audit)
	}

	// Verify exception is now resolved
	updatedExc, err := store.GetPaymentExceptionByID(ctx, exc.ID)
	if err != nil {
		t.Fatalf("failed to get exception: %v", err)
	}
	if updatedExc.Status != ExceptionStatusResolved {
		t.Errorf("expected exception resolved, got %s", updatedExc.Status)
	}

	// Verify audit ledger query
	records, err := store.ListPaymentAuditRecords(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to list audit records: %v", err)
	}
	if len(records) != 1 || records[0].Action != AuditActionDuplicateCorrection {
		t.Errorf("unexpected audit list: %+v", records)
	}
}

func TestStore_UnresolvedAttemptPromptGuard(t *testing.T) {
	pool := testPool(t)
	store := &Store{Pool: pool}
	ctx := context.Background()

	setupCatalogFixture(t, pool, "p4", 10, true, nil)

	order, err := store.CreateOrder(ctx, CreateOrderRequest{
		Customer: CustomerInput{
			Name:     "Margaret Hamilton",
			Phone:    "254700000004",
			Location: "Nairobi",
			Delivery: "pickup",
			Payment:  "pay_now",
		},
		Items: []ItemInput{
			{ID: "p4", Qty: 1},
		},
	})
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// Attempt 1 was created with CreateOrder
	if order.LatestAttempt == nil {
		t.Fatal("expected order to have initial payment attempt")
	}
	att1 := order.LatestAttempt
	if att1.Status != AttemptCreated {
		t.Fatalf("expected status created, got %s", att1.Status)
	}

	// Update to accepted (bank accepted push request)
	err = store.UpdatePaymentAttempt(ctx, att1.ID, AttemptAccepted, "NCBA-TX-GUARD", "REF-GUARD", "")
	if err != nil {
		t.Fatalf("failed to update attempt: %v", err)
	}

	// Invariant: "no uncertain outcome causes another prompt"
	// Immediate retry must fail with ErrUnresolvedPaymentAttempt
	_, err = store.CreatePaymentAttempt(ctx, order.ID, "254700000004", order.TotalKes, "ncba")
	if !errors.Is(err, ErrUnresolvedPaymentAttempt) {
		t.Errorf("expected ErrUnresolvedPaymentAttempt, got %v", err)
	}

	// If reconciler marks attempt failed (e.g. user cancelled prompt)
	err = store.UpdatePaymentAttempt(ctx, att1.ID, AttemptFailed, "NCBA-TX-GUARD", "REF-GUARD", "Customer cancelled")
	if err != nil {
		t.Fatalf("failed to mark attempt failed: %v", err)
	}

	// Now retry is unblocked!
	att2, err := store.CreatePaymentAttempt(ctx, order.ID, "254700000004", order.TotalKes, "ncba")
	if err != nil {
		t.Fatalf("failed to create attempt 2 after failure: %v", err)
	}
	if att2.AttemptNumber != 2 {
		t.Errorf("expected attemptNumber 2, got %d", att2.AttemptNumber)
	}
}
