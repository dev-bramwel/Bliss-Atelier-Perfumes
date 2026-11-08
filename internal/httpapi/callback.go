package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
)

// CreateCallbackSignature generates an HMAC-SHA256 hexadecimal signature for an order ID.
func CreateCallbackSignature(orderID, secret string) string {
	if secret == "" || orderID == "" {
		return ""
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(orderID))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyCallbackSignature verifies that a callback signature matches HMAC-SHA256(orderID, secret) in constant time.
func VerifyCallbackSignature(orderID, signature, secret string) bool {
	if secret == "" || orderID == "" || len(signature) != 64 {
		return false
	}
	expected := CreateCallbackSignature(orderID, secret)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(expected)) == 1
}

// GenerateCallbackURL builds a signed capability callback URL for an order.
func GenerateCallbackURL(baseURL, orderID, secret string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	q := u.Query()
	q.Set("orderId", orderID)
	q.Set("signature", CreateCallbackSignature(orderID, secret))
	u.RawQuery = q.Encode()
	return u.String()
}

// DarajaCallbackPayload represents the Safaricom Daraja STK callback JSON payload.
type DarajaCallbackPayload struct {
	Body struct {
		StkCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  *struct {
				Item []struct {
					Name  string `json:"Name"`
					Value any    `json:"Value"`
				} `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// ExtractMetadata extracts amount, receipt number, phone, and transaction date from Daraja CallbackMetadata items.
func (p *DarajaCallbackPayload) ExtractMetadata() (amount int, receipt string, phone string, date string) {
	cb := p.Body.StkCallback
	if cb.CallbackMetadata == nil {
		return 0, "", "", ""
	}
	for _, item := range cb.CallbackMetadata.Item {
		switch item.Name {
		case "Amount":
			switch v := item.Value.(type) {
			case float64:
				amount = int(v + 0.5)
			case int:
				amount = v
			case string:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					amount = int(f + 0.5)
				}
			}
		case "MpesaReceiptNumber":
			if s, ok := item.Value.(string); ok {
				receipt = strings.TrimSpace(s)
			}
		case "PhoneNumber":
			switch v := item.Value.(type) {
			case float64:
				phone = fmt.Sprintf("%.0f", v)
			case string:
				phone = strings.TrimSpace(v)
			}
		case "TransactionDate":
			switch v := item.Value.(type) {
			case float64:
				date = fmt.Sprintf("%.0f", v)
			case string:
				date = strings.TrimSpace(v)
			}
		}
	}
	return amount, receipt, phone, date
}

// newPaymentCallbackHandlers creates HTTP handlers for NCBA IPN push notifications and M-Pesa callbacks.
func newPaymentCallbackHandlers(
	orderStore OrderService,
	ncbaCfg provider.NCBAConfig,
	callbackSecret string,
	timeout time.Duration,
) (ncbaHandler http.HandlerFunc, mpesaHandler http.HandlerFunc) {

	// handleNCBAIPN processes incoming NCBA Paybill / Till push notifications.
	ncbaHandler = func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		w.Header().Set("Content-Type", "application/json")

		// 1. Read bounded body (max 64KB)
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Failed to read request body or body too large",
			})
			return
		}

		var payload provider.NCBAIPNPayload
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Invalid JSON payload",
			})
			return
		}

		// 2. Authenticate credentials and verify hash signature
		if !provider.VerifyNCBAIPN(ncbaCfg, payload) {
			slog.Warn("ncba_ipn_authentication_failed",
				"username", payload.Username,
				"trans_id", payload.TransID,
			)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Unauthorized notification; invalid credentials or signature hash",
			})
			return
		}

		paidAmount, err := payload.ParseAmount()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Invalid transaction amount: " + err.Error(),
			})
			return
		}

		// 3. Correlate with Order and Payment Attempt
		var order *orders.Order
		var attempt *orders.PaymentAttempt

		// Strategy A: BillRefNumber matching Order ID
		ref := strings.TrimSpace(payload.BillRefNumber)
		if ref != "" && ref != "N/A" && ref != ncbaCfg.AccountNo {
			ord, err := orderStore.GetOrder(ctx, ref)
			if err == nil && ord != nil {
				order = ord
				att, _ := orderStore.GetLatestPaymentAttempt(ctx, ord.ID)
				attempt = att
			}
		}

		// Strategy B: Match in-flight attempt by phone & amount within 15 minutes
		if order == nil {
			mobile := payload.GetMobile()
			att, err := orderStore.FindInFlightPaymentAttempt(ctx, mobile, paidAmount, 15*time.Minute)
			if err == nil && att != nil {
				ord, err := orderStore.GetOrder(ctx, att.OrderID)
				if err == nil && ord != nil {
					order = ord
					attempt = att
				}
			}
		}

		// Strategy C: Match by TransID (if already linked)
		if order == nil && payload.TransID != "" {
			att, err := orderStore.FindPaymentAttemptByProviderTx(ctx, payload.TransID)
			if err == nil && att != nil {
				ord, err := orderStore.GetOrder(ctx, att.OrderID)
				if err == nil && ord != nil {
					order = ord
					attempt = att
				}
			}
		}

		// If completely unmapped, return HTTP 503 so NCBA will redeliver
		if order == nil || attempt == nil {
			slog.Warn("ncba_ipn_unmapped_transaction",
				"trans_id", payload.TransID,
				"amount", paidAmount,
				"mobile", payload.GetMobile(),
				"bill_ref", payload.BillRefNumber,
			)
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Transaction mapping is not available yet",
			})
			return
		}

		// 4. Settled success never regresses
		if order.PaymentStatus == orders.PaymentPaid {
			slog.Info("ncba_ipn_duplicate_settled_order",
				"order_id", order.ID,
				"trans_id", payload.TransID,
			)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "0",
				"ResultDesc": "Accepted",
			})
			return
		}

		// 5. Amount & Currency Verification
		if paidAmount != attempt.AmountKes {
			slog.Error("ncba_ipn_amount_mismatch",
				"order_id", order.ID,
				"attempt_id", attempt.ID,
				"received_amount", paidAmount,
				"expected_amount", attempt.AmountKes,
			)
			_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptFailed, payload.TransID, payload.FTRef,
				fmt.Sprintf("AMOUNT_MISMATCH: received %d KES, expected %d KES", paidAmount, attempt.AmountKes))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Amount mismatch against expected payable order total",
			})
			return
		}

		// 6. Receipt Uniqueness (Deduplication)
		if payload.TransID != "" {
			existingOrderID, err := orderStore.FindReceiptOrder(ctx, payload.TransID)
			if err == nil && existingOrderID != "" && existingOrderID != order.ID {
				slog.Error("ncba_ipn_duplicate_receipt_replayed",
					"order_id", order.ID,
					"trans_id", payload.TransID,
					"existing_order_id", existingOrderID,
				)
				_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptFailed, payload.TransID, payload.FTRef,
					"DUPLICATE_RECEIPT: receipt already used on order "+existingOrderID)
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"ResultCode": "1",
					"ResultDesc": "Transaction receipt already utilized on another order",
				})
				return
			}
		}

		// 7. Transactional State Transition
		if err := orderStore.ConfirmPayment(ctx, order.ID, payload.TransID, paidAmount); err != nil {
			slog.Error("ncba_ipn_confirm_payment_failed",
				"order_id", order.ID,
				"reason", err.Error(),
			)
			if errors.Is(err, orders.ErrOrderAlreadyPaid) {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"ResultCode": "0",
					"ResultDesc": "Accepted",
				})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ResultCode": "1",
				"ResultDesc": "Internal error confirming payment",
			})
			return
		}

		_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptSucceeded, payload.TransID, payload.FTRef, "")

		slog.Info("ncba_ipn_payment_confirmed",
			"order_id", order.ID,
			"attempt_id", attempt.ID,
			"trans_id", payload.TransID,
			"ft_ref", payload.FTRef,
			"amount_kes", paidAmount,
		)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"ResultCode": "0",
			"ResultDesc": "Accepted",
		})
	}

	// mpesaHandler processes Safaricom Daraja STK callbacks and routes NCBA IPN if forwarded to /api/mpesa/callback.
	mpesaHandler = func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			respond(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
			return
		}

		// Check if body is an NCBA IPN payload (e.g. contains "TransType" and "TransID")
		var peekMap map[string]any
		if err := json.Unmarshal(bodyBytes, &peekMap); err == nil {
			if _, hasTransType := peekMap["TransType"]; hasTransType {
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				ncbaHandler(w, r)
				return
			}
		}

		// URL Capability Verification
		orderID := strings.TrimSpace(r.URL.Query().Get("orderId"))
		sig := strings.TrimSpace(r.URL.Query().Get("signature"))
		token := strings.TrimSpace(r.URL.Query().Get("token"))

		hasSignedOrderID := orderID != "" && sig != "" && VerifyCallbackSignature(orderID, sig, callbackSecret)
		hasLegacyToken := token != "" && callbackSecret != "" && subtle.ConstantTimeCompare([]byte(token), []byte(callbackSecret)) == 1

		if callbackSecret == "" || (!hasSignedOrderID && !hasLegacyToken) {
			slog.Warn("mpesa_callback_unauthorized", "order_id", orderID)
			respond(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized callback."})
			return
		}

		// NOTE: Callback URL capability is NEVER treated as a provider body signature!
		// The payload content, metadata items, amounts, and receipts are independently validated.

		var cb DarajaCallbackPayload
		if err := json.Unmarshal(bodyBytes, &cb); err != nil {
			respond(w, http.StatusBadRequest, map[string]string{"error": "Invalid M-Pesa callback."})
			return
		}

		stk := cb.Body.StkCallback
		if stk.CheckoutRequestID == "" {
			respond(w, http.StatusBadRequest, map[string]string{"error": "Invalid M-Pesa callback."})
			return
		}

		// Correlate with Order / Attempt
		var order *orders.Order
		var attempt *orders.PaymentAttempt

		if stk.CheckoutRequestID != "" {
			att, err := orderStore.FindPaymentAttemptByProviderTx(ctx, stk.CheckoutRequestID)
			if err == nil && att != nil {
				attempt = att
				ord, err := orderStore.GetOrder(ctx, att.OrderID)
				if err == nil && ord != nil {
					order = ord
				}
			}
		}

		// Callback-before-mapping recovery using signed URL orderId
		if order == nil && hasSignedOrderID {
			ord, err := orderStore.GetOrder(ctx, orderID)
			if err == nil && ord != nil {
				order = ord
				att, _ := orderStore.GetLatestPaymentAttempt(ctx, ord.ID)
				attempt = att
			}
		}

		if order == nil {
			slog.Warn("mpesa_callback_unmapped_checkout_id", "checkout_request_id", stk.CheckoutRequestID)
			respond(w, http.StatusServiceUnavailable, map[string]string{"error": "Transaction is not available yet."})
			return
		}

		// Settled success never regresses
		if order.PaymentStatus == orders.PaymentPaid {
			slog.Info("mpesa_callback_settled_order_noop", "order_id", order.ID, "result_code", stk.ResultCode)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ResultCode": 0,
				"ResultDesc": "Accepted",
			})
			return
		}

		// Handle Customer Cancellation or Late Failure
		if stk.ResultCode != 0 {
			slog.Info("mpesa_callback_failure_reported",
				"order_id", order.ID,
				"result_code", stk.ResultCode,
				"result_desc", stk.ResultDesc,
			)
			if attempt != nil {
				_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptFailed, stk.CheckoutRequestID, stk.MerchantRequestID, stk.ResultDesc)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ResultCode": 0,
				"ResultDesc": "Accepted",
			})
			return
		}

		// Handle Success (ResultCode == 0)
		amount, receipt, _, _ := cb.ExtractMetadata()
		if receipt == "" || amount <= 0 {
			slog.Error("mpesa_callback_missing_metadata", "order_id", order.ID)
			respond(w, http.StatusBadRequest, map[string]string{"error": "Missing callback payment metadata."})
			return
		}

		// Amount & Currency Verification
		if attempt != nil && amount != attempt.AmountKes {
			slog.Error("mpesa_callback_amount_mismatch",
				"order_id", order.ID,
				"received_amount", amount,
				"expected_amount", attempt.AmountKes,
			)
			_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptFailed, stk.CheckoutRequestID, stk.MerchantRequestID,
				fmt.Sprintf("AMOUNT_MISMATCH: received %d KES, expected %d KES", amount, attempt.AmountKes))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ResultCode": 0,
				"ResultDesc": "Accepted",
			})
			return
		}

		// Receipt Deduplication
		existingOrderID, err := orderStore.FindReceiptOrder(ctx, receipt)
		if err == nil && existingOrderID != "" && existingOrderID != order.ID {
			slog.Error("mpesa_callback_duplicate_receipt",
				"order_id", order.ID,
				"receipt", receipt,
				"existing_order_id", existingOrderID,
			)
			if attempt != nil {
				_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptFailed, stk.CheckoutRequestID, stk.MerchantRequestID,
					"DUPLICATE_RECEIPT: receipt already used on order "+existingOrderID)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ResultCode": 0,
				"ResultDesc": "Accepted",
			})
			return
		}

		// Execute State Transition
		if err := orderStore.ConfirmPayment(ctx, order.ID, receipt, amount); err != nil {
			slog.Error("mpesa_callback_confirm_payment_failed", "order_id", order.ID, "reason", err.Error())
			if errors.Is(err, orders.ErrOrderAlreadyPaid) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ResultCode": 0,
					"ResultDesc": "Accepted",
				})
				return
			}
			respond(w, http.StatusInternalServerError, map[string]string{"error": "Callback processing failed."})
			return
		}

		if attempt != nil {
			_ = orderStore.UpdatePaymentAttempt(ctx, attempt.ID, orders.AttemptSucceeded, receipt, stk.MerchantRequestID, "")
		}

		slog.Info("mpesa_callback_payment_confirmed",
			"order_id", order.ID,
			"receipt", receipt,
			"amount_kes", amount,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ResultCode": 0,
			"ResultDesc": "Accepted",
		})
	}

	return ncbaHandler, mpesaHandler
}
