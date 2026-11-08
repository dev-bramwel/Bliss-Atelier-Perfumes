package httpapi

import (
	"log/slog"
	"regexp"
	"strings"
)

var (
	guestTokenRegex = regexp.MustCompile(`\bgt_[0-9a-fA-F]{64}\b`)
	phoneRegex      = regexp.MustCompile(`(?:\+?254|0)[17]\d{8}`)
)

// RedactString sanitizes tokens and sensitive personal phone numbers within free text.
func RedactString(s string) string {
	if s == "" {
		return ""
	}
	s = guestTokenRegex.ReplaceAllString(s, "gt_[REDACTED]")
	s = phoneRegex.ReplaceAllString(s, "+254***REDACTED***")
	return s
}

// RedactingReplaceAttr is an slog ReplaceAttr callback that masks credentials,
// tokens, and customer PII before writing structured logs.
func RedactingReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	keyLower := strings.ToLower(a.Key)

	switch keyLower {
	case "token", "guest_token", "raw_token", "authorization", "x-guest-token", "x-manager-id", "x-manager-secret", "x-manager-key", "manager_secret", "secret", "password", "credentials", "auth":
		return slog.String(a.Key, "[REDACTED]")
	case "phone", "customer_phone", "recipient_phone", "mobile":
		return slog.String(a.Key, "+254***REDACTED***")
	}

	if a.Value.Kind() == slog.KindString {
		strVal := a.Value.String()
		redacted := RedactString(strVal)
		if redacted != strVal {
			return slog.String(a.Key, redacted)
		}
	}

	return a
}
