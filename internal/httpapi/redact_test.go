package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactString(t *testing.T) {
	rawToken := "gt_" + strings.Repeat("0123456789abcdef", 4)
	text := "Error handling token " + rawToken + " for customer phone +254712345678"

	redacted := RedactString(text)
	if strings.Contains(redacted, rawToken) {
		t.Fatalf("token was not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "gt_[REDACTED]") {
		t.Fatalf("expected gt_[REDACTED], got: %s", redacted)
	}
	if strings.Contains(redacted, "0712345678") || strings.Contains(redacted, "254712345678") {
		t.Fatalf("phone was not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "+254***REDACTED***") {
		t.Fatalf("expected +254***REDACTED***, got: %s", redacted)
	}
}

func TestRedactingReplaceAttr(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: RedactingReplaceAttr,
	})
	logger := slog.New(handler)

	rawToken := "gt_" + strings.Repeat("0123456789abcdef", 4)
	logger.Info("order_debug",
		"guest_token", rawToken,
		"authorization", "Bearer my-secret-token",
		"customer_phone", "0712345678",
		"safe_message", "token gt_1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff inside text",
		"order_id", "ord_123",
	)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse log entry: %v", err)
	}

	if entry["guest_token"] != "[REDACTED]" {
		t.Errorf("expected guest_token to be [REDACTED], got %v", entry["guest_token"])
	}
	if entry["authorization"] != "[REDACTED]" {
		t.Errorf("expected authorization to be [REDACTED], got %v", entry["authorization"])
	}
	if entry["customer_phone"] != "+254***REDACTED***" {
		t.Errorf("expected customer_phone to be +254***REDACTED***, got %v", entry["customer_phone"])
	}
	if strings.Contains(entry["safe_message"].(string), "11112222") {
		t.Errorf("expected token in message to be redacted, got %v", entry["safe_message"])
	}
	if entry["order_id"] != "ord_123" {
		t.Errorf("expected order_id to be preserved, got %v", entry["order_id"])
	}
}
