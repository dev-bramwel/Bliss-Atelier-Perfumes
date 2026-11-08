package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormatKenyanPhone(t *testing.T) {
	tests := []struct {
		input       string
		expected    string
		shouldError bool
	}{
		{"0712345678", "254712345678", false},
		{"+254712345678", "254712345678", false},
		{"254712345678", "254712345678", false},
		{"0712-345-678", "254712345678", false},
		{"0712 345 678", "254712345678", false},
		{"0112345678", "254112345678", false},
		{"12345", "", true},
		{"25471234567", "", true},   // 11 digits
		{"2547123456789", "", true}, // 13 digits
		{"invalid-phone", "", true},
	}

	for _, tt := range tests {
		got, err := FormatKenyanPhone(tt.input)
		if tt.shouldError {
			if err == nil {
				t.Errorf("FormatKenyanPhone(%q) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("FormatKenyanPhone(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("FormatKenyanPhone(%q) = %q, expected %q", tt.input, got, tt.expected)
			}
		}
	}
}

func TestNCBA_HashVerification(t *testing.T) {
	cfg := NCBAConfig{
		IPNUsername:  "bliss_ipn_user",
		IPNPassword:  "test-pass",
		IPNSecretKey: "test-secret",
	}

	payload := NCBAIPNPayload{
		TransType:         "Pay Bill",
		TransID:           "RK91U5J2AD",
		FTRef:             "FTC231009BMZG",
		TransTime:         "20231109112200",
		TransAmount:       "2500.0",
		BusinessShortCode: "880100",
		BillRefNumber:     "000001",
		Narrative:         "Bliss Order",
		Mobile:            "254712345678",
		Name:              "JANE DOE",
		Username:          "bliss_ipn_user",
		Password:          "test-pass",
	}

	hash := ComputeNCBAIPNHash(cfg.IPNSecretKey, payload)
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	payload.Hash = hash

	// 1. Valid payload must pass
	if !VerifyNCBAIPN(cfg, payload) {
		t.Error("expected valid NCBA IPN payload to pass verification")
	}

	// 2. Modified amount must fail
	tamperedPayload := payload
	tamperedPayload.TransAmount = "500.0"
	if VerifyNCBAIPN(cfg, tamperedPayload) {
		t.Error("expected tampered amount to fail hash verification")
	}

	// 3. Modified receipt ID must fail
	tamperedPayload2 := payload
	tamperedPayload2.TransID = "DIFFERENT_TX"
	if VerifyNCBAIPN(cfg, tamperedPayload2) {
		t.Error("expected tampered TransID to fail hash verification")
	}

	// 4. Invalid username must fail
	tamperedPayload3 := payload
	tamperedPayload3.Username = "wrong_user"
	if VerifyNCBAIPN(cfg, tamperedPayload3) {
		t.Error("expected invalid username to fail verification")
	}

	// 5. Invalid password must fail
	tamperedPayload4 := payload
	tamperedPayload4.Password = "wrong_pass"
	if VerifyNCBAIPN(cfg, tamperedPayload4) {
		t.Error("expected invalid password to fail verification")
	}
}

func TestNCBA_InitiateSTK_Success(t *testing.T) {
	var tokenCalls int32
	var initCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/payments/api/v1/auth/token":
			atomic.AddInt32(&tokenCalls, 1)
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Basic ") {
				http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "ncba-mock-jwt-token-xyz",
				"token_type":   "Bearer",
				"expires_in":   18000,
				"status":       200,
			})
		case "/payments/api/v1/stk-push/initiate":
			atomic.AddInt32(&initCalls, 1)
			auth := r.Header.Get("Authorization")
			if auth != "Bearer ncba-mock-jwt-token-xyz" {
				http.Error(w, `{"message":"invalid bearer token"}`, http.StatusUnauthorized)
				return
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"message":"malformed json"}`, http.StatusBadRequest)
				return
			}
			if body["PayBillNo"] != "880100" || body["AccountNo"] != "000001" || body["Network"] != "Safaricom" {
				http.Error(w, `{"message":"invalid till details"}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"TransactionID":     "NCBA_STK_TX_12345",
				"StatusCode":        "0",
				"StatusDescription": "Success",
				"ReferenceID":       "NCBA_REF_998877",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(time.Second)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	cfg := NCBAConfig{
		BaseURL:   server.URL,
		Username:  "ncba_user",
		Password:  "ncba_pass",
		PayBillNo: "880100",
		AccountNo: "000001",
	}

	adapter, err := NewNCBA(cfg, client)
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	resp, err := adapter.InitiateSTK(context.Background(), STKInitiateRequest{
		OrderID:   "ord-test-1",
		Phone:     "0712345678",
		AmountKes: 2500,
	})
	if err != nil {
		t.Fatalf("InitiateSTK failed: %v", err)
	}

	if resp.TransactionID != "NCBA_STK_TX_12345" {
		t.Errorf("expected TransactionID NCBA_STK_TX_12345, got %s", resp.TransactionID)
	}
	if resp.ReferenceID != "NCBA_REF_998877" {
		t.Errorf("expected ReferenceID NCBA_REF_998877, got %s", resp.ReferenceID)
	}
	if resp.StatusCode != "0" {
		t.Errorf("expected StatusCode '0', got %s", resp.StatusCode)
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Errorf("expected 1 token call, got %d", atomic.LoadInt32(&tokenCalls))
	}
	if atomic.LoadInt32(&initCalls) != 1 {
		t.Errorf("expected 1 init call, got %d", atomic.LoadInt32(&initCalls))
	}
}

func TestNCBA_TokenReuse_ThreadSafe(t *testing.T) {
	var tokenCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/payments/api/v1/auth/token":
			atomic.AddInt32(&tokenCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "ncba-shared-jwt-token",
				"token_type":   "Bearer",
				"expires_in":   18000,
				"status":       200,
			})
		case "/payments/api/v1/stk-push/initiate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"TransactionID":     "NCBA_CONCURRENT_TX",
				"StatusCode":        "0",
				"StatusDescription": "Success",
				"ReferenceID":       "REF_CONCURRENT",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, _ := New(2 * time.Second)
	defer client.Close()

	adapter, _ := NewNCBA(NCBAConfig{
		BaseURL:   server.URL,
		Username:  "u",
		Password:  "p",
		PayBillNo: "880100",
		AccountNo: "000001",
	}, client)
	defer adapter.Close()

	var wg sync.WaitGroup
	const concurrency = 30

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := adapter.InitiateSTK(context.Background(), STKInitiateRequest{
				OrderID:   "ord-concurrent",
				Phone:     "0712345678",
				AmountKes: 1000 + idx,
			})
			if err != nil {
				t.Errorf("concurrent initiate failed: %v", err)
			}
			if resp.TransactionID != "NCBA_CONCURRENT_TX" {
				t.Errorf("unexpected txID: %s", resp.TransactionID)
			}
		}(i)
	}

	wg.Wait()

	// Token should have been fetched once and reused across all concurrent goroutines
	if calls := atomic.LoadInt32(&tokenCalls); calls != 1 {
		t.Errorf("expected exactly 1 token acquisition, got %d", calls)
	}
}

func TestNCBA_InitiateSTK_Errors(t *testing.T) {
	// 1. Timeout simulation
	t.Run("Timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/payments/api/v1/auth/token" {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 18000})
				return
			}
			// Simulate hanging request that triggers timeout
			time.Sleep(150 * time.Millisecond)
		}))
		defer server.Close()

		client, _ := New(50 * time.Millisecond)
		defer client.Close()

		adapter, _ := NewNCBA(NCBAConfig{BaseURL: server.URL, Username: "u", Password: "p"}, client)
		defer adapter.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := adapter.InitiateSTK(ctx, STKInitiateRequest{OrderID: "o", Phone: "0711111111", AmountKes: 500})
		if !errors.Is(err, ErrProviderTimeout) {
			t.Fatalf("expected ErrProviderTimeout, got %v", err)
		}
	})

	// 2. Provider explicit rejection
	t.Run("Rejection", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/payments/api/v1/auth/token" {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 18000})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"TransactionID":     nil,
				"StatusCode":        "1",
				"StatusDescription": "Daily transaction limit exceeded",
				"ReferenceID":       nil,
			})
		}))
		defer server.Close()

		client, _ := New(time.Second)
		defer client.Close()

		adapter, _ := NewNCBA(NCBAConfig{BaseURL: server.URL, Username: "u", Password: "p"}, client)
		defer adapter.Close()

		_, err := adapter.InitiateSTK(context.Background(), STKInitiateRequest{OrderID: "o", Phone: "0711111111", AmountKes: 500})
		if !errors.Is(err, ErrProviderRejected) {
			t.Fatalf("expected ErrProviderRejected, got %v", err)
		}
	})
}

func TestFakeAdapter(t *testing.T) {
	fake := NewFakeAdapter(ModeAccept)
	resp, err := fake.InitiateSTK(context.Background(), STKInitiateRequest{
		OrderID:   "ord-fake",
		Phone:     "0722000000",
		AmountKes: 1500,
	})
	if err != nil || resp.StatusCode != "0" {
		t.Fatalf("expected success, got %v", err)
	}

	fake.SetMode(ModeTimeout)
	_, err = fake.InitiateSTK(context.Background(), STKInitiateRequest{
		OrderID:   "ord-fake",
		Phone:     "0722000000",
		AmountKes: 1500,
	})
	if !errors.Is(err, ErrProviderTimeout) {
		t.Errorf("expected ErrProviderTimeout, got %v", err)
	}

	fake.SetMode(ModeReject)
	_, err = fake.InitiateSTK(context.Background(), STKInitiateRequest{
		OrderID:   "ord-fake",
		Phone:     "0722000000",
		AmountKes: 1500,
	})
	if !errors.Is(err, ErrProviderRejected) {
		t.Errorf("expected ErrProviderRejected, got %v", err)
	}
}
