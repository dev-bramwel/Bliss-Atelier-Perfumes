package telemetry

import (
	"net/http"
	"strconv"
	"time"
)

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusCapturingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// HTTPMiddleware returns an http.Handler middleware that instruments incoming requests:
// 1. Injects/propagates X-Correlation-ID header and context.
// 2. Tracks in-flight request gauge.
// 3. Normalizes route paths to prevent label cardinality explosions.
// 4. Records request counts and latency histograms with bounded method, route, and status labels.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corrID, req := EnsureCorrelationID(r)
		w.Header().Set("X-Correlation-ID", corrID)

		HTTPRequestsInFlight.Inc()
		defer HTTPRequestsInFlight.Dec()

		sw := &statusCapturingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // default status if WriteHeader is not explicitly called
		}

		start := time.Now()
		next.ServeHTTP(sw, req)
		duration := time.Since(start).Seconds()

		route := NormalizeRoute(r.URL.Path)
		statusStr := strconv.Itoa(sw.statusCode)

		HTTPRequestsTotal.Inc(r.Method, route, statusStr)
		HTTPRequestDuration.Observe(duration, r.Method, route)
	})
}
