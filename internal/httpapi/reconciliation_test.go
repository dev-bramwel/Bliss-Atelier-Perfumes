package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
)

const testSecret = "test-manager-secret"

func TestAdmin_AuthRequired(t *testing.T) {
	mock := &mockOrderService{}
	secCfg := SecurityConfig{
		ManagerSecret: testSecret,
	}
	server := New(":0", 5*time.Second, nil, nil, mock, nil, secCfg)

	// 1. Missing Authorization header
	req := httptest.NewRequest("GET", "/api/admin/payments/exceptions", nil)
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Invalid Secret
	req = httptest.NewRequest("GET", "/api/admin/payments/exceptions", nil)
	req.Header.Set("Authorization", "Bearer invalid-secret")
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 3. Valid Secret
	req = httptest.NewRequest("GET", "/api/admin/payments/exceptions", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
}

func TestAdmin_PaymentExceptionsLifecycle(t *testing.T) {
	ordID := "ord-1"
	rcpt := "RCPT-101"
	amt := 5000
	mockExceptions := []orders.PaymentException{
		{
			ID:                "exc-1",
			OrderID:           &ordID,
			ExceptionType:     orders.ExceptionAmountMismatch,
			Provider:          "ncba",
			ReceiptNumber:     &rcpt,
			ReceivedAmountKes: &amt,
			Status:            orders.ExceptionStatusOpen,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	}

	resolvedID := ""
	mock := &mockOrderService{
		getExceptionsFn: func(ctx context.Context, filter orders.ExceptionFilter) ([]orders.PaymentException, error) {
			return mockExceptions, nil
		},
		getExcByIDFn: func(ctx context.Context, id string) (*orders.PaymentException, error) {
			if id == "exc-1" {
				return &mockExceptions[0], nil
			}
			return nil, orders.ErrExceptionNotFound
		},
		resolveExcFn: func(ctx context.Context, id string, req orders.ResolveExceptionRequest) error {
			resolvedID = id
			return nil
		},
	}

	server := New(":0", 5*time.Second, nil, nil, mock, nil, SecurityConfig{ManagerSecret: testSecret})

	// List Exceptions
	req := httptest.NewRequest("GET", "/api/admin/payments/exceptions?status=open", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var list []orders.PaymentException
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(list) != 1 || list[0].ID != "exc-1" {
		t.Errorf("unexpected list result: %+v", list)
	}

	// Get Single Exception
	req = httptest.NewRequest("GET", "/api/admin/payments/exceptions/exc-1", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Resolve Exception
	resolveBody, _ := json.Marshal(orders.ResolveExceptionRequest{
		Action: "refund_recorded",
		Notes:  "Reversal verified with bank statement",
	})
	req = httptest.NewRequest("POST", "/api/admin/payments/exceptions/exc-1/resolve", bytes.NewReader(resolveBody))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if resolvedID != "exc-1" {
		t.Errorf("expected exc-1 resolved, got %s", resolvedID)
	}
}

func TestAdmin_ManualSettlement(t *testing.T) {
	mock := &mockOrderService{
		manualSettleFn: func(ctx context.Context, req orders.ManualSettlementRequest) (*orders.Order, *orders.PaymentAuditRecord, error) {
			if req.ReceiptNumber == "DUP-RECEIPT" {
				return nil, nil, orders.ErrDuplicateReceipt
			}
			if req.OrderID == "ord-expired" {
				return nil, nil, orders.ErrOrderReservationExpired
			}
			return &orders.Order{
				ID:            req.OrderID,
				PaymentStatus: orders.PaymentPaid,
				TotalKes:      req.AmountKes,
			}, &orders.PaymentAuditRecord{
				ID:            "audit-settle-1",
				OrderID:       req.OrderID,
				Action:        orders.AuditActionManualSettlement,
				AmountKes:     req.AmountKes,
				ReceiptNumber: req.ReceiptNumber,
				AuthorizedBy:  req.AuthorizedBy,
			}, nil
		},
	}

	server := New(":0", 5*time.Second, nil, nil, mock, nil, SecurityConfig{ManagerSecret: testSecret})

	// Success manual settlement
	body, _ := json.Marshal(orders.ManualSettlementRequest{
		OrderID:       "ord-valid",
		ReceiptNumber: "NCBA-OFFLINE-789",
		AmountKes:     7500,
		Evidence:      "Bank branch deposit slip #987654",
		Notes:         "Verified direct cash deposit with bank manager",
	})
	req := httptest.NewRequest("POST", "/api/admin/payments/manual-settlement", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Order orders.Order              `json:"order"`
		Audit orders.PaymentAuditRecord `json:"audit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Order.PaymentStatus != orders.PaymentPaid || resp.Audit.ReceiptNumber != "NCBA-OFFLINE-789" {
		t.Errorf("unexpected response: %+v", resp)
	}

	// Duplicate receipt error
	bodyDup, _ := json.Marshal(orders.ManualSettlementRequest{
		OrderID:       "ord-valid",
		ReceiptNumber: "DUP-RECEIPT",
		AmountKes:     7500,
		Evidence:      "Slip",
		Notes:         "Notes",
	})
	req = httptest.NewRequest("POST", "/api/admin/payments/manual-settlement", bytes.NewReader(bodyDup))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict on duplicate receipt, got %d", rec.Code)
	}

	// Expired reservation error
	bodyExp, _ := json.Marshal(orders.ManualSettlementRequest{
		OrderID:       "ord-expired",
		ReceiptNumber: "VALID-RCPT",
		AmountKes:     7500,
		Evidence:      "Slip",
		Notes:         "Notes",
	})
	req = httptest.NewRequest("POST", "/api/admin/payments/manual-settlement", bytes.NewReader(bodyExp))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict on expired reservation, got %d", rec.Code)
	}
}

func TestAdmin_PaymentCorrection(t *testing.T) {
	mock := &mockOrderService{
		correctionFn: func(ctx context.Context, req orders.PaymentCorrectionRequest) (*orders.PaymentAuditRecord, error) {
			return &orders.PaymentAuditRecord{
				ID:            "audit-corr-1",
				OrderID:       req.OrderID,
				Action:        req.Action,
				AmountKes:     req.AmountKes,
				ReceiptNumber: req.ReceiptNumber,
				AuthorizedBy:  req.AuthorizedBy,
				Evidence:      req.Evidence,
			}, nil
		},
	}

	server := New(":0", 5*time.Second, nil, nil, mock, nil, SecurityConfig{ManagerSecret: testSecret})

	body, _ := json.Marshal(orders.PaymentCorrectionRequest{
		OrderID:       "ord-corr",
		Action:        orders.AuditActionDuplicateCorrection,
		ReceiptNumber: "TX-DUP-1",
		AmountKes:     5000,
		Evidence:      "Reversal transaction REF-9990",
		Notes:         "Customer charged twice due to handset retry; reversal processed",
	})
	req := httptest.NewRequest("POST", "/api/admin/payments/corrections", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var audit orders.PaymentAuditRecord
	if err := json.NewDecoder(rec.Body).Decode(&audit); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if audit.Action != orders.AuditActionDuplicateCorrection || audit.ReceiptNumber != "TX-DUP-1" {
		t.Errorf("unexpected audit record: %+v", audit)
	}
}

func TestAdmin_ReconcileAndAuditLedger(t *testing.T) {
	mock := &mockOrderService{
		listAuditFn: func(ctx context.Context, orderID string) ([]orders.PaymentAuditRecord, error) {
			return []orders.PaymentAuditRecord{
				{
					ID:            "audit-1",
					OrderID:       "ord-1",
					Action:        orders.AuditActionManualSettlement,
					AmountKes:     5000,
					ReceiptNumber: "R1",
				},
			}, nil
		},
		listUnresolvedFn: func(ctx context.Context, staleThreshold time.Duration, limit int) ([]orders.PaymentAttempt, error) {
			return []orders.PaymentAttempt{
				{ID: "att-1", OrderID: "ord-1", Status: orders.AttemptAccepted},
			}, nil
		},
	}

	server := New(":0", 5*time.Second, nil, nil, mock, nil, SecurityConfig{ManagerSecret: testSecret})

	// List Audit
	req := httptest.NewRequest("GET", "/api/admin/payments/audit-ledger?orderId=ord-1", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Trigger Reconcile
	req = httptest.NewRequest("POST", "/api/admin/payments/reconcile", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	rec = httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIPN_PaymentExceptionTriggeredOnUnmapped(t *testing.T) {
	var recordedExc *orders.PaymentException
	mock := &mockOrderService{
		recordExcFn: func(ctx context.Context, exc orders.PaymentException) (*orders.PaymentException, error) {
			recordedExc = &exc
			return &exc, nil
		},
	}

	ncbaCfg := provider.NCBAConfig{
		IPNUsername:  "ncba_user",
		IPNPassword:  "ncba_pass",
		IPNSecretKey: "ncba_secret",
	}

	payload := provider.NCBAIPNPayload{
		TransType:         "TILLNUMBER",
		TransID:           "UNMAPPED-TX-101",
		TransTime:         "20261109120000",
		TransAmount:       "5000.00",
		BusinessShortCode: "880100",
		BillRefNumber:     "UNKNOWN-REF",
		Mobile:            "254700000000",
		Name:              "JANE DOE",
		Username:          "ncba_user",
		Password:          "ncba_pass",
	}
	payload.Hash = provider.ComputeNCBAIPNHash("ncba_secret", payload)

	server := New(":0", 5*time.Second, nil, nil, mock, nil, SecurityConfig{}, ncbaCfg)

	pBytes, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(pBytes))
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for unmapped IPN, got %d", rec.Code)
	}

	if recordedExc == nil {
		t.Fatal("expected RecordPaymentException to be called for unmapped IPN")
	}
	if recordedExc.ExceptionType != orders.ExceptionUnmappedTransaction {
		t.Errorf("expected exception type %s, got %s", orders.ExceptionUnmappedTransaction, recordedExc.ExceptionType)
	}
	if recordedExc.ReceiptNumber == nil || *recordedExc.ReceiptNumber != "UNMAPPED-TX-101" {
		t.Errorf("expected receipt UNMAPPED-TX-101 on exception")
	}
}
