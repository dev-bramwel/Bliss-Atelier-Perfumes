// Package httpapi provides the foundation HTTP surface, not order/payment routes.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

type Ping func(context.Context) error

type API struct {
	server   *http.Server
	draining atomic.Bool
}

func New(address string, timeout time.Duration, ping Ping) *API {
	a := &API{}
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, code int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]string{"status": "Bliss Atelier API running"})
	})
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if a.draining.Load() {
			respond(w, 503, map[string]string{"status": "draining"})
			return
		}
		if ping != nil {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			if ping(ctx) != nil {
				respond(w, 503, map[string]string{"status": "unavailable"})
				return
			}
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	a.server = &http.Server{Addr: address, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: timeout + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	return a
}

// Serve drains in-flight requests and closes remaining connections at the deadline.
func (a *API) Serve(ctx context.Context, listener net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	done := make(chan error, 1)
	go func() { done <- a.server.Serve(listener) }()
	logger.Info("http_started")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("HTTP server failed")
	case <-ctx.Done():
		a.draining.Store(true)
		drainCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		err := a.server.Shutdown(drainCtx)
		if err != nil {
			_ = a.server.Close()
			<-done
			return errors.New("HTTP shutdown deadline exceeded")
		}
		<-done
		logger.Info("http_stopped")
		return nil
	}
}
