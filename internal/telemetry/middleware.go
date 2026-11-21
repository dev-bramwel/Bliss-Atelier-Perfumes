package telemetry

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (w *statusCapturingResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.statusCode = code
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusCapturingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap allows net/http.ResponseController to access the underlying ResponseWriter.
func (w *statusCapturingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush forwards http.Flusher interface if implemented by the underlying ResponseWriter.
func (w *statusCapturingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards http.Hijacker interface if implemented by the underlying ResponseWriter.
func (w *statusCapturingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Push forwards http.Pusher interface if implemented by the underlying ResponseWriter.
func (w *statusCapturingResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// ReadFrom forwards io.ReaderFrom interface if implemented by the underlying ResponseWriter.
func (w *statusCapturingResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(w.ResponseWriter, src)
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
		method := NormalizeMethod(r.Method)
		statusStr := strconv.Itoa(sw.statusCode)

		HTTPRequestsTotal.Inc(method, route, statusStr)
		HTTPRequestDuration.Observe(duration, method, route)
	})
}
