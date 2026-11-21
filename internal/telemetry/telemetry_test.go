package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestMetricsRegistryFormatting(t *testing.T) {
	reg := NewRegistry()

	cnt := reg.NewCounter("test_orders_total", "Test orders total count.", []string{"status"})
	cnt.Inc("success")
	cnt.Inc("success")
	cnt.Inc("failed")

	g := reg.NewGauge("test_in_flight", "Test in flight gauge.", nil)
	g.Set(42)

	h := reg.NewHistogram("test_duration_seconds", "Test duration histogram.", []string{"route"}, []float64{0.01, 0.05, 0.1})
	h.Observe(0.02, "/api/orders")
	h.Observe(0.08, "/api/orders")

	out := reg.Gather()

	// Verify Header comments
	if !strings.Contains(out, "# HELP test_orders_total Test orders total count.\n# TYPE test_orders_total counter") {
		t.Errorf("expected counter header, got:\n%s", out)
	}
	if !strings.Contains(out, `test_orders_total{status="success"} 2`) {
		t.Errorf("expected 2 success orders, got:\n%s", out)
	}
	if !strings.Contains(out, `test_orders_total{status="failed"} 1`) {
		t.Errorf("expected 1 failed order, got:\n%s", out)
	}
	if !strings.Contains(out, "test_in_flight 42") {
		t.Errorf("expected gauge 42, got:\n%s", out)
	}

	// Verify Histogram buckets
	if !strings.Contains(out, `test_duration_seconds_bucket{route="/api/orders",le="0.01"} 0`) {
		t.Errorf("expected 0 bucket count at 0.01, got:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{route="/api/orders",le="0.05"} 1`) {
		t.Errorf("expected 1 bucket count at 0.05, got:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{route="/api/orders",le="+Inf"} 2`) {
		t.Errorf("expected +Inf count 2, got:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_count{route="/api/orders"} 2`) {
		t.Errorf("expected count 2, got:\n%s", out)
	}
}

func TestRouteNormalizationBoundedCardinality(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"/", "/"},
		{"/health/live", "/health/live"},
		{"/health/ready", "/health/ready"},
		{"/api/health/live", "/api/health/live"},
		{"/api/catalog", "/api/catalog"},
		{"/api/catalog/invalidate", "/api/catalog/invalidate"},
		{"/api/orders", "/api/orders"},
		{"/api/orders/ord_123abc456", "/api/orders/{id}"},
		{"/api/orders/ord_9999/payment-status", "/api/orders/{id}/payment-status"},
		{"/api/orders/ord_xyz/retry-payment", "/api/orders/{id}/retry-payment"},
		{"/api/orders/ord_xyz/delivery-quote/accept", "/api/orders/{id}/delivery-quote/accept"},
		{"/api/auth/google", "/api/auth/google"},
		{"/api/auth/google/callback", "/api/auth/google/callback"},
		{"/api/admin/users", "/api/admin/users"},
		{"/api/admin/users/usr_01/role", "/api/admin/users/{id}/role"},
		{"/api/admin/payments/unresolved", "/api/admin/payments/unresolved"},
		{"/api/admin/payments/exceptions/exc_999/resolve", "/api/admin/payments/exceptions/{id}/resolve"},
		{"/static/app.js", "/static/{file}"},
		{"/img/eros.jpeg", "/img/{file}"},
		{"/attacker/probing/path/with/uuid/550e8400-e29b-41d4-a716-446655440000", "/unmapped"},
		{"/some/random/attack/vector", "/unmapped"},
	}

	for _, tc := range testCases {
		actual := NormalizeRoute(tc.input)
		if actual != tc.expected {
			t.Errorf("NormalizeRoute(%q) = %q; want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestCorrelationIDContextAndPropagation(t *testing.T) {
	// 1. Generate new ID if none provided
	req1 := httptest.NewRequest("GET", "/api/catalog", nil)
	id1, req1WithCtx := EnsureCorrelationID(req1)
	if !strings.HasPrefix(id1, "c_") {
		t.Errorf("expected correlation ID to start with c_, got %s", id1)
	}
	if FromContext(req1WithCtx.Context()) != id1 {
		t.Errorf("expected context to hold %s, got %s", id1, FromContext(req1WithCtx.Context()))
	}

	// 2. Preserve incoming valid correlation ID
	req2 := httptest.NewRequest("GET", "/api/catalog", nil)
	req2.Header.Set("X-Correlation-ID", "custom-trace-uuid-12345")
	id2, _ := EnsureCorrelationID(req2)
	if id2 != "custom-trace-uuid-12345" {
		t.Errorf("expected custom-trace-uuid-12345, got %s", id2)
	}

	// 3. Attr output
	ctx := WithCorrelationID(context.Background(), "trace-abc-123")
	attr := Attr(ctx)
	if attr.Key != "correlation_id" || attr.Value.String() != "trace-abc-123" {
		t.Errorf("unexpected slog attr: %v", attr)
	}

	// 4. CorrelationHandler integration with structured logger
	var logBuf bytes.Buffer
	jsonHandler := slog.NewJSONHandler(&logBuf, nil)
	corrLogger := slog.New(NewCorrelationHandler(jsonHandler))

	// Log with context containing correlation ID
	corrLogger.InfoContext(ctx, "payment_processed", "amount", 2500)
	logged := logBuf.String()
	if !strings.Contains(logged, `"correlation_id":"trace-abc-123"`) {
		t.Errorf("expected structured log to contain correlation_id attribute, got: %s", logged)
	}

	// Log without correlation ID
	logBuf.Reset()
	corrLogger.InfoContext(context.Background(), "system_ready")
	loggedWithout := logBuf.String()
	if strings.Contains(loggedWithout, `"correlation_id"`) {
		t.Errorf("expected no correlation_id when context lacks one, got: %s", loggedWithout)
	}
}

func TestCorrelationID_RejectsPIIAndPhoneNumbers(t *testing.T) {
	piiInputs := []string{
		"254712345678",       // Kenyan phone with country code
		"0712345678",         // Local Kenyan phone
		"0112345678",         // Local 01... phone
		"123456789012",       // Pure numeric sequence
		"phone_254712345678", // String embedding phone number
		"customer@test.com",  // Email
	}

	for _, input := range piiInputs {
		req := httptest.NewRequest("GET", "/api/catalog", nil)
		req.Header.Set("X-Correlation-ID", input)
		id, _ := EnsureCorrelationID(req)
		if id == input {
			t.Errorf("expected caller PII %q to be rejected, but it was accepted", input)
		}
		if !strings.HasPrefix(id, "c_") {
			t.Errorf("expected safe generated ID starting with c_, got %s", id)
		}
	}
}

func TestHTTPMiddlewareMetricsCapture(t *testing.T) {
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := HTTPMiddleware(testHandler)

	// Call 1: Success
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/orders/ord_12345", nil)
	wrapped.ServeHTTP(rec1, req1)

	if rec1.Header().Get("X-Correlation-ID") == "" {
		t.Errorf("expected X-Correlation-ID in response header")
	}

	// Call 2: Failure
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/fail", nil)
	wrapped.ServeHTTP(rec2, req2)

	metricsOutput := DefaultRegistry.Gather()

	// Check metrics
	if !strings.Contains(metricsOutput, `http_requests_total{method="GET",route="/api/orders/{id}",status="200"}`) {
		t.Errorf("expected GET /api/orders/{id} 200 metric in:\n%s", metricsOutput)
	}
	if !strings.Contains(metricsOutput, `http_requests_total{method="POST",route="/unmapped",status="500"}`) {
		t.Errorf("expected POST /unmapped 500 metric in:\n%s", metricsOutput)
	}
}

func TestConcurrentTelemetryAccess(t *testing.T) {
	cnt := DefaultRegistry.NewCounter("concurrent_test_total", "Test concurrent counter.", []string{"worker"})
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cnt.Inc("worker-1")
			}
		}(i)
	}

	wg.Wait()
	out := DefaultRegistry.Gather()
	if !strings.Contains(out, `concurrent_test_total{worker="worker-1"} 5000`) {
		t.Errorf("expected count 5000, got:\n%s", out)
	}
}

func TestTelemetryRedactionAndZeroPIIInMetrics(t *testing.T) {
	// Scrape metrics and verify no customer phone numbers or secrets are present
	metrics := DefaultRegistry.Gather()

	phonePattern := regexp.MustCompile(`(?:\+?254|0)[17]\d{8}`)
	if phonePattern.MatchString(metrics) {
		t.Fatalf("CRITICAL SECURITY VIOLATION: Customer phone number leaked in metrics output:\n%s", metrics)
	}

	tokenPattern := regexp.MustCompile(`\bgt_[0-9a-fA-F]{64}\b`)
	if tokenPattern.MatchString(metrics) {
		t.Fatalf("CRITICAL SECURITY VIOLATION: Guest token leaked in metrics output:\n%s", metrics)
	}
}

func TestNormalizeMethod(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"GET", "GET"},
		{"get", "GET"},
		{"POST", "POST"},
		{"post", "POST"},
		{"PUT", "PUT"},
		{"put", "PUT"},
		{"DELETE", "DELETE"},
		{"delete", "DELETE"},
		{"PATCH", "PATCH"},
		{"patch", "PATCH"},
		{"HEAD", "HEAD"},
		{"OPTIONS", "OPTIONS"},
		{"options", "OPTIONS"},
		{"PROPFIND", "OTHER"},
		{"TRACK", "OTHER"},
		{"CONNECT", "OTHER"},
		{"BREW", "OTHER"},
		{"", "OTHER"},
		{"  post  ", "POST"},
		{"attack-verb-with-arbitrary-string", "OTHER"},
	}

	for _, tc := range cases {
		got := NormalizeMethod(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeMethod(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestHTTPMiddlewareMethodNormalization(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Send an arbitrary HTTP method
	req := httptest.NewRequest("ARBITRARY_METHOD", "/api/catalog", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := DefaultRegistry.Gather()
	if !strings.Contains(metrics, `http_requests_total{method="OTHER",route="/api/catalog",status="200"}`) {
		t.Fatalf("expected request metric with normalized method OTHER, got:\n%s", metrics)
	}
	if strings.Contains(metrics, `method="ARBITRARY_METHOD"`) {
		t.Fatalf("un-normalized method leaked into metrics labels:\n%s", metrics)
	}
}

func TestStatusCapturingResponseWriter_FirstStatusHonoredAndInterfacesForwarded(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusCapturingResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	// 1. First WriteHeader sets code
	sw.WriteHeader(http.StatusCreated)
	// 2. Second WriteHeader should be ignored
	sw.WriteHeader(http.StatusInternalServerError)

	if sw.statusCode != http.StatusCreated {
		t.Errorf("expected first status 201 to be preserved, got %d", sw.statusCode)
	}

	// 3. Unwrap returns original ResponseWriter
	if sw.Unwrap() != rec {
		t.Errorf("expected Unwrap() to return inner recorder")
	}

	// 4. Flush does not panic
	sw.Flush()

	// 5. ReadFrom works
	data := "test-body-stream"
	n, err := sw.ReadFrom(strings.NewReader(data))
	if err != nil || n != int64(len(data)) {
		t.Errorf("expected ReadFrom to stream %d bytes, got %d (err: %v)", len(data), n, err)
	}
}
