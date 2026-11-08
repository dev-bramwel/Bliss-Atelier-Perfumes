package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
)

func TestNCBA_IPN_Handlers(t *testing.T) {
	ncbaCfg := provider.NCBAConfig{
		IPNUsername:  "ncba_test_user",
		IPNPassword:  "ncba_test_pass",
		IPNSecretKey: "test-secret",
	}

	fake := provider.NewFakeAdapter(provider.ModeAccept)

	t.Run("ValidPaymentSuccess", func(t *testing.T) {
		var confirmedOrder string
		var confirmedReceipt string
		var confirmedAmount int
		var updatedAttemptID string
		var updatedAttemptStatus string

		mock := &mockOrderService{
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				if orderID == "ord-ncba-1" {
					return &orders.Order{
						ID:            "ord-ncba-1",
						TotalKes:      3500,
						PaymentStatus: orders.PaymentPending,
						CustomerPhone: "254712345678",
					}, nil
				}
				return nil, orders.ErrOrderNotFound
			},
			getLatestAttFn: func(ctx context.Context, orderID string) (*orders.PaymentAttempt, error) {
				return &orders.PaymentAttempt{
					ID:        "att-ncba-1",
					OrderID:   "ord-ncba-1",
					AmountKes: 3500,
					Status:    orders.AttemptAccepted,
					Phone:     "254712345678",
				}, nil
			},
			confirmPayFn: func(ctx context.Context, orderID, receipt string, paidAmount ...int) error {
				confirmedOrder = orderID
				confirmedReceipt = receipt
				if len(paidAmount) > 0 {
					confirmedAmount = paidAmount[0]
				}
				return nil
			},
			updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
				updatedAttemptID = attemptID
				updatedAttemptStatus = status
				return nil
			},
			findReceiptOrdFn: func(ctx context.Context, receiptNumber string) (string, error) {
				return "", nil // receipt not previously used
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			ncbaCfg.IPNUsername,
			ncbaCfg.IPNPassword,
			"ord-ncba-1",
			"254712345678",
			3500,
			"NLJ7RT61SV",
			"FTC231009BMZG",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
		}

		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["ResultCode"] != "0" || resp["ResultDesc"] != "Accepted" {
			t.Errorf("unexpected response: %+v", resp)
		}

		if confirmedOrder != "ord-ncba-1" || confirmedReceipt != "NLJ7RT61SV" || confirmedAmount != 3500 {
			t.Errorf("ConfirmPayment not properly invoked: order=%s, receipt=%s, amount=%d", confirmedOrder, confirmedReceipt, confirmedAmount)
		}
		if updatedAttemptID != "att-ncba-1" || updatedAttemptStatus != orders.AttemptSucceeded {
			t.Errorf("attempt not updated to SUCCEEDED: id=%s, status=%s", updatedAttemptID, updatedAttemptStatus)
		}
	})

	t.Run("Unauthorized_BadCredentials", func(t *testing.T) {
		mock := &mockOrderService{}
		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			"wrong_user",
			"wrong_pass",
			"ord-ncba-bad",
			"254712345678",
			3500,
			"REC123",
			"FT123",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
		}
	})

	t.Run("SettledSuccessNeverRegresses", func(t *testing.T) {
		var confirmCalled bool

		mock := &mockOrderService{
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				return &orders.Order{
					ID:            "ord-already-paid",
					TotalKes:      2000,
					PaymentStatus: orders.PaymentPaid, // ALREADY PAID
					CustomerPhone: "254712345678",
				}, nil
			},
			getLatestAttFn: func(ctx context.Context, orderID string) (*orders.PaymentAttempt, error) {
				return &orders.PaymentAttempt{
					ID:        "att-paid",
					OrderID:   "ord-already-paid",
					AmountKes: 2000,
					Status:    orders.AttemptSucceeded,
				}, nil
			},
			confirmPayFn: func(ctx context.Context, orderID, receipt string, paidAmount ...int) error {
				confirmCalled = true
				return nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		// Duplicate success notification
		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			ncbaCfg.IPNUsername,
			ncbaCfg.IPNPassword,
			"ord-already-paid",
			"254712345678",
			2000,
			"DUP_REC_1",
			"DUP_FT_1",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for settled duplicate, got %d", w.Code)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["ResultCode"] != "0" {
			t.Errorf("expected ResultCode '0', got %v", resp)
		}
		if confirmCalled {
			t.Errorf("ConfirmPayment should not be re-invoked on already settled order")
		}
	})

	t.Run("AmountMismatch_RejectsSettlement", func(t *testing.T) {
		var attemptFailedWith string

		mock := &mockOrderService{
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				return &orders.Order{
					ID:            "ord-mismatch",
					TotalKes:      5000,
					PaymentStatus: orders.PaymentPending,
					CustomerPhone: "254711223344",
				}, nil
			},
			getLatestAttFn: func(ctx context.Context, orderID string) (*orders.PaymentAttempt, error) {
				return &orders.PaymentAttempt{
					ID:        "att-mismatch",
					OrderID:   "ord-mismatch",
					AmountKes: 5000, // Expected 5000
					Status:    orders.AttemptAccepted,
					Phone:     "254711223344",
				}, nil
			},
			updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
				if status == orders.AttemptFailed {
					attemptFailedWith = errMsg
				}
				return nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		// Notification with underpayment: 2000 instead of 5000
		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			ncbaCfg.IPNUsername,
			ncbaCfg.IPNPassword,
			"ord-mismatch",
			"254711223344",
			2000, // Mismatch!
			"REC_MISMATCH",
			"FT_MISMATCH",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["ResultCode"] != "1" {
			t.Fatalf("expected ResultCode '1' on mismatch, got %v", resp)
		}
		if !strings.Contains(attemptFailedWith, "AMOUNT_MISMATCH") {
			t.Errorf("expected attempt to be marked with AMOUNT_MISMATCH, got %q", attemptFailedWith)
		}
	})

	t.Run("ReceiptDeduplication_RejectsDuplicateReceipt", func(t *testing.T) {
		var attemptFailedWith string

		mock := &mockOrderService{
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				return &orders.Order{
					ID:            "ord-new",
					TotalKes:      1500,
					PaymentStatus: orders.PaymentPending,
					CustomerPhone: "254799887766",
				}, nil
			},
			getLatestAttFn: func(ctx context.Context, orderID string) (*orders.PaymentAttempt, error) {
				return &orders.PaymentAttempt{
					ID:        "att-new",
					OrderID:   "ord-new",
					AmountKes: 1500,
					Status:    orders.AttemptAccepted,
					Phone:     "254799887766",
				}, nil
			},
			findReceiptOrdFn: func(ctx context.Context, receiptNumber string) (string, error) {
				// Simulates that this receipt was already recorded on "ord-previously-settled"
				return "ord-previously-settled", nil
			},
			updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
				if status == orders.AttemptFailed {
					attemptFailedWith = errMsg
				}
				return nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			ncbaCfg.IPNUsername,
			ncbaCfg.IPNPassword,
			"ord-new",
			"254799887766",
			1500,
			"REPLAYED_RECEIPT_1",
			"FT_REPLAYED",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["ResultCode"] != "1" {
			t.Fatalf("expected ResultCode '1' on replayed receipt, got %v", resp)
		}
		if !strings.Contains(attemptFailedWith, "DUPLICATE_RECEIPT") {
			t.Errorf("expected DUPLICATE_RECEIPT error message, got %q", attemptFailedWith)
		}
	})

	t.Run("CallbackBeforeMapping_PhoneAndAmountCorrelation", func(t *testing.T) {
		var confirmedOrder string

		mock := &mockOrderService{
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				if orderID == "ord-in-flight" {
					return &orders.Order{
						ID:            "ord-in-flight",
						TotalKes:      4200,
						PaymentStatus: orders.PaymentPending,
						CustomerPhone: "254700112233",
					}, nil
				}
				return nil, orders.ErrOrderNotFound
			},
			findInFlightFn: func(ctx context.Context, phone string, amountKes int, within time.Duration) (*orders.PaymentAttempt, error) {
				if phone == "254700112233" && amountKes == 4200 {
					return &orders.PaymentAttempt{
						ID:        "att-in-flight-99",
						OrderID:   "ord-in-flight",
						AmountKes: 4200,
						Status:    orders.AttemptAccepted,
						Phone:     "254700112233",
					}, nil
				}
				return nil, orders.ErrAttemptNotFound
			},
			confirmPayFn: func(ctx context.Context, orderID, receipt string, paidAmount ...int) error {
				confirmedOrder = orderID
				return nil
			},
			updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
				return nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		// Notification where BillRefNumber was not set to orderID (e.g. general till shortcode)
		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			ncbaCfg.IPNUsername,
			ncbaCfg.IPNPassword,
			"000001", // BillRefNumber is Till alias, not orderID!
			"254700112233",
			4200,
			"REC_CORRELATED_1",
			"FT_CORRELATED_1",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		if confirmedOrder != "ord-in-flight" {
			t.Errorf("expected order ord-in-flight to be correlated, got %s", confirmedOrder)
		}
	})

	t.Run("UnmappedTransaction_Returns503ForRetry", func(t *testing.T) {
		mock := &mockOrderService{
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				return nil, orders.ErrOrderNotFound
			},
			findInFlightFn: func(ctx context.Context, phone string, amountKes int, within time.Duration) (*orders.PaymentAttempt, error) {
				return nil, orders.ErrAttemptNotFound
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, ncbaCfg)

		ipn := fake.GenerateMockIPN(
			ncbaCfg.IPNSecretKey,
			ncbaCfg.IPNUsername,
			ncbaCfg.IPNPassword,
			"UNMAPPED_REF",
			"254799999999",
			9999,
			"REC_UNMAPPED",
			"FT_UNMAPPED",
		)
		bodyBytes, _ := json.Marshal(ipn)

		req := httptest.NewRequest("POST", "/api/payments/ncba/ipn", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 Service Unavailable, got %d", w.Code)
		}
	})
}

func TestDaraja_Callback_Handlers(t *testing.T) {
	testSecret := "test-secret"
	secCfg := SecurityConfig{
		MpesaCallbackSecret: testSecret,
	}

	t.Run("ValidSuccessCallback", func(t *testing.T) {
		var confirmedOrder string
		var confirmedReceipt string

		mock := &mockOrderService{
			findByProvTxFn: func(ctx context.Context, checkoutID string) (*orders.PaymentAttempt, error) {
				if checkoutID == "ws_CO_12345" {
					return &orders.PaymentAttempt{
						ID:        "att-daraja-1",
						OrderID:   "ord-daraja-1",
						AmountKes: 2500,
						Status:    orders.AttemptAccepted,
					}, nil
				}
				return nil, orders.ErrAttemptNotFound
			},
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				if orderID == "ord-daraja-1" {
					return &orders.Order{
						ID:            "ord-daraja-1",
						TotalKes:      2500,
						PaymentStatus: orders.PaymentPending,
					}, nil
				}
				return nil, orders.ErrOrderNotFound
			},
			confirmPayFn: func(ctx context.Context, orderID, receipt string, paidAmount ...int) error {
				confirmedOrder = orderID
				confirmedReceipt = receipt
				return nil
			},
			updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
				return nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, secCfg)

		payload := `{
			"Body": {
				"stkCallback": {
					"MerchantRequestID": "MR_123",
					"CheckoutRequestID": "ws_CO_12345",
					"ResultCode": 0,
					"ResultDesc": "The service request is processed successfully.",
					"CallbackMetadata": {
						"Item": [
							{"Name": "Amount", "Value": 2500},
							{"Name": "MpesaReceiptNumber", "Value": "NLJ7RT61SV"},
							{"Name": "TransactionDate", "Value": 20231219102115},
							{"Name": "PhoneNumber", "Value": 254712345678}
						]
					}
				}
			}
		}`

		url := GenerateCallbackURL("/api/mpesa/callback", "ord-daraja-1", testSecret)
		req := httptest.NewRequest("POST", url, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
		}

		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["ResultCode"] != float64(0) {
			t.Errorf("expected ResultCode 0, got %v", resp)
		}
		if confirmedOrder != "ord-daraja-1" || confirmedReceipt != "NLJ7RT61SV" {
			t.Errorf("expected confirmation of ord-daraja-1 with NLJ7RT61SV, got %s / %s", confirmedOrder, confirmedReceipt)
		}
	})

	t.Run("Unauthorized_MissingSignature", func(t *testing.T) {
		mock := &mockOrderService{}
		api := New("127.0.0.1:0", time.Second, nil, mock, secCfg)

		payload := `{"Body":{"stkCallback":{"CheckoutRequestID":"ws_1","ResultCode":0}}}`
		req := httptest.NewRequest("POST", "/api/mpesa/callback?orderId=ord-1&signature=invalid_signature_hex", strings.NewReader(payload))
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
		}
	})

	t.Run("CustomerCancelled_DoesNotDowngrade", func(t *testing.T) {
		var attemptFailedWith string

		mock := &mockOrderService{
			findByProvTxFn: func(ctx context.Context, checkoutID string) (*orders.PaymentAttempt, error) {
				return &orders.PaymentAttempt{
					ID:        "att-cancel",
					OrderID:   "ord-cancel",
					AmountKes: 1000,
					Status:    orders.AttemptAccepted,
				}, nil
			},
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				return &orders.Order{
					ID:            "ord-cancel",
					PaymentStatus: orders.PaymentPending,
				}, nil
			},
			updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
				if status == orders.AttemptFailed {
					attemptFailedWith = errMsg
				}
				return nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, secCfg)

		// ResultCode 1032 = Request cancelled by user
		payload := `{
			"Body": {
				"stkCallback": {
					"MerchantRequestID": "MR_CANCEL",
					"CheckoutRequestID": "ws_CO_CANCEL",
					"ResultCode": 1032,
					"ResultDesc": "Request cancelled by user"
				}
			}
		}`

		url := GenerateCallbackURL("/api/mpesa/callback", "ord-cancel", testSecret)
		req := httptest.NewRequest("POST", url, strings.NewReader(payload))
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		if attemptFailedWith != "Request cancelled by user" {
			t.Errorf("expected failure reason recorded on attempt, got %q", attemptFailedWith)
		}
	})

	t.Run("SettledSuccessNeverRegresses_Daraja", func(t *testing.T) {
		mock := &mockOrderService{
			findByProvTxFn: func(ctx context.Context, checkoutID string) (*orders.PaymentAttempt, error) {
				return &orders.PaymentAttempt{
					ID:        "att-paid",
					OrderID:   "ord-already-paid",
					AmountKes: 1000,
					Status:    orders.AttemptSucceeded,
				}, nil
			},
			getFn: func(ctx context.Context, orderID string) (*orders.Order, error) {
				return &orders.Order{
					ID:            "ord-already-paid",
					PaymentStatus: orders.PaymentPaid, // Already PAID
				}, nil
			},
		}

		api := New("127.0.0.1:0", time.Second, nil, mock, secCfg)

		// Late failure on an already paid order
		payload := `{"Body":{"stkCallback":{"CheckoutRequestID":"ws_CO_LATE","ResultCode":1037,"ResultDesc":"DS timeout"}}}`
		url := GenerateCallbackURL("/api/mpesa/callback", "ord-already-paid", testSecret)
		req := httptest.NewRequest("POST", url, strings.NewReader(payload))
		w := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on late failure for paid order, got %d", w.Code)
		}
	})
}

func TestOrderCreationWithProviderSTKPush(t *testing.T) {
	fake := provider.NewFakeAdapter(provider.ModeAccept)

	var createdAttempt *orders.PaymentAttempt
	var updatedStatus string
	var updatedTxID string

	mock := &mockOrderService{
		createFn: func(ctx context.Context, req orders.CreateOrderRequest) (*orders.Order, error) {
			att := &orders.PaymentAttempt{
				ID:            "att-initial-1",
				OrderID:       "ord-stk-1",
				AttemptNumber: 1,
				AmountKes:     2500,
				Phone:         req.Customer.Phone,
				Status:        orders.AttemptCreated,
				Provider:      "ncba_stk",
			}
			createdAttempt = att
			return &orders.Order{
				ID:            "ord-stk-1",
				TotalKes:      2500,
				PaymentMethod: "mpesa",
				CustomerPhone: req.Customer.Phone,
				GuestToken:    "gt_testtoken",
				LatestAttempt: att,
			}, nil
		},
		updateAttemptFn: func(ctx context.Context, attemptID, status, txID, refID, errMsg string) error {
			updatedStatus = status
			updatedTxID = txID
			return nil
		},
	}

	api := New("127.0.0.1:0", time.Second, nil, mock, fake)

	payload := `{"customer":{"name":"Alice","phone":"254712345678","location":"Nairobi","delivery":"delivery"},"paymentMethod":"mpesa","items":[{"id":"p1","qty":1}]}`
	req := httptest.NewRequest("POST", "/api/orders", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d (body: %s)", w.Code, w.Body.String())
	}
	if createdAttempt == nil {
		t.Fatal("expected attempt to be created")
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	stkPush, ok := resp["stkPush"].(map[string]any)
	if !ok || stkPush == nil {
		t.Fatalf("expected stkPush metadata in response, got %v", resp["stkPush"])
	}

	if stkPush["checkoutRequestId"] == "" {
		t.Errorf("expected checkoutRequestId to be non-empty, got %v", stkPush)
	}

	if updatedStatus != orders.AttemptAccepted {
		t.Errorf("expected attempt to be updated to accepted, got %s", updatedStatus)
	}
	if updatedTxID == "" {
		t.Errorf("expected provider transaction ID to be saved, got empty string")
	}
}
