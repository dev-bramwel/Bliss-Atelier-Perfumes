package reconciliation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
)

type mockStore struct {
	mu           sync.Mutex
	attempts     []orders.PaymentAttempt
	orders       map[string]*orders.Order
	exceptions   []orders.PaymentException
	updatedAtts  map[string]orders.PaymentAttempt
	schedQueries map[string]time.Time
	confirmed    map[string]string // orderID -> receiptNumber
}

func newMockStore() *mockStore {
	return &mockStore{
		orders:       make(map[string]*orders.Order),
		updatedAtts:  make(map[string]orders.PaymentAttempt),
		schedQueries: make(map[string]time.Time),
		confirmed:    make(map[string]string),
	}
}

func (m *mockStore) ListUnresolvedPaymentAttempts(ctx context.Context, staleThreshold time.Duration, limit int) ([]orders.PaymentAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts, nil
}

func (m *mockStore) UpdatePaymentAttemptQuerySchedule(ctx context.Context, attemptID string, nextQueryAt *time.Time, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if nextQueryAt != nil {
		m.schedQueries[attemptID] = *nextQueryAt
	}
	return nil
}

func (m *mockStore) UpdatePaymentAttempt(ctx context.Context, attemptID, status, providerTxID, providerRefID, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedAtts[attemptID] = orders.PaymentAttempt{
		ID:                    attemptID,
		Status:                status,
		ProviderTransactionID: providerTxID,
		ProviderReferenceID:   providerRefID,
		ErrorMessage:          errorMsg,
	}
	return nil
}

func (m *mockStore) ConfirmPayment(ctx context.Context, orderID string, receiptNumber string, paidAmountKes ...int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirmed[orderID] = receiptNumber
	if ord, ok := m.orders[orderID]; ok {
		ord.PaymentStatus = orders.PaymentPaid
	}
	return nil
}

func (m *mockStore) RecordPaymentException(ctx context.Context, exc orders.PaymentException) (*orders.PaymentException, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exc.ID = "exc-mock-1"
	m.exceptions = append(m.exceptions, exc)
	return &exc, nil
}

func (m *mockStore) GetOrder(ctx context.Context, orderID string) (*orders.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		return ord, nil
	}
	return nil, orders.ErrOrderNotFound
}

type mockProvider struct {
	querySTKFn func(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error)
}

func (p *mockProvider) InitiateSTK(ctx context.Context, req provider.STKInitiateRequest) (*provider.STKInitiateResponse, error) {
	return nil, errors.New("not implemented")
}

func (p *mockProvider) QuerySTK(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error) {
	if p.querySTKFn != nil {
		return p.querySTKFn(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (p *mockProvider) QueryReceipt(ctx context.Context, req provider.ReceiptQueryRequest) (*provider.ReceiptQueryResponse, error) {
	return nil, errors.New("not implemented")
}

func (p *mockProvider) Close() {}

func TestReconciler_SuccessConvergence(t *testing.T) {
	store := newMockStore()
	store.orders["ord-100"] = &orders.Order{
		ID:            "ord-100",
		PaymentStatus: orders.PaymentPending,
	}
	store.attempts = []orders.PaymentAttempt{
		{
			ID:                    "att-1",
			OrderID:               "ord-100",
			AttemptNumber:         1,
			AmountKes:             5000,
			Phone:                 "254711111111",
			Status:                orders.AttemptAccepted,
			Provider:              "ncba",
			ProviderTransactionID: "NCBA-TX-1001",
			CreatedAt:             time.Now().Add(-2 * time.Minute),
		},
	}

	prov := &mockProvider{
		querySTKFn: func(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error) {
			if req.TransactionID == "NCBA-TX-1001" {
				return &provider.STKQueryResponse{
					Status:      "SUCCESS",
					Description: "The service request is processed successfully.",
				}, nil
			}
			return nil, provider.ErrProviderRejected
		},
	}

	rec := New(store, prov, DefaultConfig(), nil)
	summary, err := rec.ReconcileStaleAttempts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Processed != 1 || summary.Succeeded != 1 || summary.Failed != 0 {
		t.Errorf("unexpected summary: %+v", summary)
	}

	if store.confirmed["ord-100"] != "NCBA-TX-1001" {
		t.Errorf("expected order ord-100 confirmed with receipt NCBA-TX-1001, got %q", store.confirmed["ord-100"])
	}

	if store.updatedAtts["att-1"].Status != orders.AttemptSucceeded {
		t.Errorf("expected attempt att-1 status succeeded, got %q", store.updatedAtts["att-1"].Status)
	}
}

func TestReconciler_FailedConvergence(t *testing.T) {
	store := newMockStore()
	store.attempts = []orders.PaymentAttempt{
		{
			ID:                    "att-2",
			OrderID:               "ord-101",
			AttemptNumber:         1,
			AmountKes:             3500,
			Phone:                 "254722222222",
			Status:                orders.AttemptAccepted,
			Provider:              "ncba",
			ProviderTransactionID: "NCBA-TX-2002",
			CreatedAt:             time.Now().Add(-2 * time.Minute),
		},
	}

	prov := &mockProvider{
		querySTKFn: func(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error) {
			return &provider.STKQueryResponse{
				Status:      "FAILED",
				Description: "Request cancelled by user",
			}, nil
		},
	}

	rec := New(store, prov, DefaultConfig(), nil)
	summary, err := rec.ReconcileStaleAttempts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Processed != 1 || summary.Failed != 1 || summary.Succeeded != 0 {
		t.Errorf("unexpected summary: %+v", summary)
	}

	if store.updatedAtts["att-2"].Status != orders.AttemptFailed {
		t.Errorf("expected attempt att-2 status failed, got %q", store.updatedAtts["att-2"].Status)
	}
	if store.updatedAtts["att-2"].ErrorMessage != "Request cancelled by user" {
		t.Errorf("expected error message preserved, got %q", store.updatedAtts["att-2"].ErrorMessage)
	}
}

func TestReconciler_PendingBackoff(t *testing.T) {
	store := newMockStore()
	store.attempts = []orders.PaymentAttempt{
		{
			ID:                    "att-3",
			OrderID:               "ord-102",
			AttemptNumber:         1,
			AmountKes:             3500,
			Phone:                 "254722222222",
			Status:                orders.AttemptAccepted,
			Provider:              "ncba",
			ProviderTransactionID: "NCBA-TX-3003",
			QueryCount:            1,
			CreatedAt:             time.Now().Add(-2 * time.Minute),
		},
	}

	prov := &mockProvider{
		querySTKFn: func(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error) {
			return &provider.STKQueryResponse{
				Status:      "PENDING",
				Description: "Transaction in progress",
			}, nil
		},
	}

	cfg := DefaultConfig()
	cfg.BaseBackoff = 10 * time.Second
	rec := New(store, prov, cfg, nil)
	summary, err := rec.ReconcileStaleAttempts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Processed != 1 || summary.Pending != 1 {
		t.Errorf("unexpected summary: %+v", summary)
	}

	schedTime, exists := store.schedQueries["att-3"]
	if !exists {
		t.Fatal("expected query schedule recorded for att-3")
	}

	// For queryCount = 1: multiplier = 1<<1 = 2 -> 20s
	expectedMin := time.Now().Add(18 * time.Second)
	if schedTime.Before(expectedMin) {
		t.Errorf("expected scheduled time >= %v, got %v", expectedMin, schedTime)
	}
}

func TestReconciler_MaxQueriesExceeded_RaisesException(t *testing.T) {
	store := newMockStore()
	store.attempts = []orders.PaymentAttempt{
		{
			ID:                    "att-4",
			OrderID:               "ord-103",
			AttemptNumber:         1,
			AmountKes:             4000,
			Phone:                 "254733333333",
			Status:                orders.AttemptAccepted,
			Provider:              "ncba",
			ProviderTransactionID: "NCBA-TX-4004",
			QueryCount:            4, // next will be 5 == MaxQueryAttempts
			CreatedAt:             time.Now().Add(-10 * time.Minute),
		},
	}

	prov := &mockProvider{
		querySTKFn: func(ctx context.Context, req provider.STKQueryRequest) (*provider.STKQueryResponse, error) {
			return nil, provider.ErrProviderTimeout
		},
	}

	cfg := DefaultConfig()
	cfg.MaxQueryAttempts = 5
	rec := New(store, prov, cfg, nil)
	summary, err := rec.ReconcileStaleAttempts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.ExceptionsRaised != 1 {
		t.Errorf("expected 1 exception raised, got %d", summary.ExceptionsRaised)
	}

	if store.updatedAtts["att-4"].Status != orders.AttemptUnknown {
		t.Errorf("expected attempt att-4 status unknown, got %q", store.updatedAtts["att-4"].Status)
	}

	if len(store.exceptions) != 1 {
		t.Fatalf("expected 1 exception in store, got %d", len(store.exceptions))
	}

	exc := store.exceptions[0]
	if exc.ExceptionType != orders.ExceptionStaleAttemptUnresolved {
		t.Errorf("expected exception type %s, got %s", orders.ExceptionStaleAttemptUnresolved, exc.ExceptionType)
	}
	if exc.OrderID == nil || *exc.OrderID != "ord-103" {
		t.Errorf("expected order ID ord-103 on exception")
	}
}

func TestReconciler_AmbiguousInitiationTimeout(t *testing.T) {
	store := newMockStore()
	store.attempts = []orders.PaymentAttempt{
		{
			ID:                    "att-5",
			OrderID:               "ord-104",
			AttemptNumber:         1,
			AmountKes:             2500,
			Phone:                 "254744444444",
			Status:                orders.AttemptUnknown,
			Provider:              "ncba",
			ProviderTransactionID: "", // No provider transaction ID
			CreatedAt:             time.Now().Add(-6 * time.Minute),
		},
	}

	rec := New(store, nil, DefaultConfig(), nil)
	summary, err := rec.ReconcileStaleAttempts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Processed != 1 || summary.Failed != 1 {
		t.Errorf("unexpected summary: %+v", summary)
	}

	if store.updatedAtts["att-5"].Status != orders.AttemptFailed {
		t.Errorf("expected attempt att-5 status failed, got %q", store.updatedAtts["att-5"].Status)
	}
}
