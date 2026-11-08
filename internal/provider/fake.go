package provider

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeMode defines the simulation behavior of FakeAdapter.
type FakeMode string

const (
	ModeAccept    FakeMode = "accept"
	ModeReject    FakeMode = "reject"
	ModeTimeout   FakeMode = "timeout"
	ModeMalformed FakeMode = "malformed"
)

// FakeAdapter implements PaymentProvider for tests, local devstack, and CI drills.
type FakeAdapter struct {
	mu           sync.RWMutex
	mode         FakeMode
	initiations  []STKInitiateRequest
	queries      []STKQueryRequest
	txCounter    int
	receiptStore map[string]ReceiptTransaction
}

// NewFakeAdapter creates a new FakeAdapter with the specified simulation mode.
func NewFakeAdapter(mode FakeMode) *FakeAdapter {
	if mode == "" {
		mode = ModeAccept
	}
	return &FakeAdapter{
		mode:         mode,
		receiptStore: make(map[string]ReceiptTransaction),
	}
}

func (f *FakeAdapter) SetMode(mode FakeMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *FakeAdapter) Close() {}

func (f *FakeAdapter) InitiateSTK(ctx context.Context, req STKInitiateRequest) (*STKInitiateResponse, error) {
	if req.AmountKes <= 0 {
		return nil, ErrInvalidAmount
	}
	_, err := FormatKenyanPhone(req.Phone)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.initiations = append(f.initiations, req)
	f.txCounter++
	txID := fmt.Sprintf("NCBA_TX_%06d", f.txCounter)
	refID := fmt.Sprintf("NCBA_REF_%06d", f.txCounter)
	currentMode := f.mode
	f.mu.Unlock()

	switch currentMode {
	case ModeTimeout:
		return nil, ErrProviderTimeout
	case ModeReject:
		return &STKInitiateResponse{
			TransactionID:     txID,
			ReferenceID:       refID,
			StatusCode:        "1",
			StatusDescription: "Rule failed: invalid till configuration or limits exceeded",
		}, ErrProviderRejected
	case ModeMalformed:
		return nil, ErrProviderMalformed
	default: // ModeAccept
		return &STKInitiateResponse{
			TransactionID:     txID,
			ReferenceID:       refID,
			StatusCode:        "0",
			StatusDescription: "Success",
			CustomerMessage:   "Please enter your M-Pesa PIN on your phone to complete payment.",
		}, nil
	}
}

func (f *FakeAdapter) QuerySTK(ctx context.Context, req STKQueryRequest) (*STKQueryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, req)

	switch f.mode {
	case ModeTimeout:
		return nil, ErrProviderTimeout
	case ModeReject:
		return &STKQueryResponse{
			Status:      "FAILED",
			Description: "Customer cancelled payment prompt",
		}, nil
	default:
		return &STKQueryResponse{
			Status:      "SUCCESS",
			Description: "Success",
		}, nil
	}
}

func (f *FakeAdapter) QueryReceipt(ctx context.Context, req ReceiptQueryRequest) (*ReceiptQueryResponse, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	tx, exists := f.receiptStore[req.MpesaReference]
	if !exists {
		return &ReceiptQueryResponse{
			Status:       0,
			Transactions: nil,
		}, nil
	}

	return &ReceiptQueryResponse{
		Status:       0,
		Transactions: []ReceiptTransaction{tx},
	}, nil
}

func (f *FakeAdapter) RecordReceipt(tx ReceiptTransaction) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receiptStore[tx.TransID] = tx
}

func (f *FakeAdapter) GetInitiations() []STKInitiateRequest {
	f.mu.RLock()
	defer f.mu.RUnlock()
	copied := make([]STKInitiateRequest, len(f.initiations))
	copy(copied, f.initiations)
	return copied
}

// GenerateMockIPN generates a valid NCBAIPNPayload corresponding to an order and secret key.
func (f *FakeAdapter) GenerateMockIPN(secretKey, username, password, orderID, phone string, amountKes int, mpesaReceipt, ftRef string) NCBAIPNPayload {
	if mpesaReceipt == "" {
		mpesaReceipt = fmt.Sprintf("NLJ%dSV", time.Now().UnixNano()%1000000)
	}
	if ftRef == "" {
		ftRef = fmt.Sprintf("FTC%d", time.Now().UnixNano()%1000000)
	}
	p := NCBAIPNPayload{
		TransType:         "Pay Bill",
		TransID:           mpesaReceipt,
		FTRef:             ftRef,
		TransTime:         time.Now().Format("20060102150405"),
		TransAmount:       fmt.Sprintf("%d.0", amountKes),
		BusinessShortCode: "880100",
		BillRefNumber:     orderID,
		Narrative:         "Bliss Atelier order",
		Mobile:            phone,
		Name:              "CUSTOMER NAME",
		Username:          username,
		Password:          password,
	}
	p.Hash = ComputeNCBAIPNHash(secretKey, p)
	return p
}
