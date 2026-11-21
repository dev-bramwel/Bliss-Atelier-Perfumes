package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

type contextKey struct{}

var correlationContextKey = contextKey{}

var (
	validCorrelationRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{8,64}$`)
	digitOnlyRegex        = regexp.MustCompile(`^\d+$`)
	longDigitSeqRegex     = regexp.MustCompile(`\d{9,}`)
	phonePatternRegex     = regexp.MustCompile(`(?i)(?:^|[^0-9])(?:254|\+254|0)[17]\d{8}(?:[^0-9]|$)`)
)

// isSafeCorrelationID verifies that an incoming correlation ID matches structural requirements
// and does NOT contain caller-supplied PII such as phone numbers, pure numeric sequences, or emails.
func isSafeCorrelationID(id string) bool {
	if !validCorrelationRegex.MatchString(id) {
		return false
	}
	if digitOnlyRegex.MatchString(id) {
		return false
	}
	if longDigitSeqRegex.MatchString(id) {
		return false
	}
	if phonePatternRegex.MatchString(id) {
		return false
	}
	if strings.Contains(id, "@") {
		return false
	}
	return true
}

// NewCorrelationID generates a cryptographically random correlation identifier.
func NewCorrelationID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "corr_fallback_" + hex.EncodeToString(b)
	}
	return "c_" + hex.EncodeToString(b)
}

// FromContext extracts the correlation ID from the context if present, or returns an empty string.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(correlationContextKey).(string); ok {
		return v
	}
	return ""
}

// WithCorrelationID attaches a correlation ID to a context.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationContextKey, id)
}

// EnsureCorrelationID inspects request headers (X-Correlation-ID, X-Request-ID),
// sanitizes or generates a valid identifier, and returns the ID along with a request
// whose context contains the correlation ID.
func EnsureCorrelationID(r *http.Request) (string, *http.Request) {
	id := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
	if id == "" {
		id = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	}

	if id == "" || !isSafeCorrelationID(id) {
		id = NewCorrelationID()
	}

	ctx := WithCorrelationID(r.Context(), id)
	return id, r.WithContext(ctx)
}

// Attr returns an slog.Attr containing the correlation_id from context for structured logging.
func Attr(ctx context.Context) slog.Attr {
	id := FromContext(ctx)
	if id == "" {
		return slog.String("correlation_id", "none")
	}
	return slog.String("correlation_id", id)
}

// CorrelationHandler is an slog.Handler middleware that automatically extracts
// correlation_id from context.Context and attaches it as a structured log attribute.
type CorrelationHandler struct {
	inner slog.Handler
}

// NewCorrelationHandler wraps an inner slog.Handler with automatic context correlation_id injection.
func NewCorrelationHandler(inner slog.Handler) *CorrelationHandler {
	return &CorrelationHandler{inner: inner}
}

func (h *CorrelationHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *CorrelationHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	return h.inner.Handle(ctx, r)
}

func (h *CorrelationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CorrelationHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *CorrelationHandler) WithGroup(name string) slog.Handler {
	return &CorrelationHandler{inner: h.inner.WithGroup(name)}
}
