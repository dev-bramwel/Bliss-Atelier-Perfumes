package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultDrillResponderToken is the default pre-shared authentication token for local & CI drills.
const DefaultDrillResponderToken = "bliss-drill-responder-secret"

// MaxAlertBodyBytes enforces a strict 64 KiB payload bound on Alertmanager webhooks.
const MaxAlertBodyBytes = 64 * 1024

// MaxRetainedAlerts bounds in-memory alert history to prevent heap exhaustion.
const MaxRetainedAlerts = 50

// ResponderAlert represents a single alert within an Alertmanager notification payload.
type ResponderAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt,omitempty"`
	EndsAt      string            `json:"endsAt,omitempty"`
}

// AlertNotification represents the payload sent by Alertmanager to the designated responder webhook.
type AlertNotification struct {
	Receiver string           `json:"receiver"`
	Status   string           `json:"status"`
	Alerts   []ResponderAlert `json:"alerts"`
}

// DrillResponder is a bounded, authenticated receiver that captures incoming Alertmanager
// notifications and exposes liveness endpoints for synthetic incident drills.
type DrillResponder struct {
	mu             sync.RWMutex
	authToken      string
	receivedAlerts []AlertNotification
}

// NewDrillResponder initializes an in-memory alert recipient store with token validation.
func NewDrillResponder(authToken ...string) *DrillResponder {
	token := DefaultDrillResponderToken
	if len(authToken) > 0 && authToken[0] != "" {
		token = authToken[0]
	} else if env := os.Getenv("GO_DRILL_RESPONDER_TOKEN"); env != "" {
		token = env
	}

	return &DrillResponder{
		authToken:      token,
		receivedAlerts: make([]AlertNotification, 0, MaxRetainedAlerts),
	}
}

// Alerts returns a thread-safe snapshot of all captured alert notifications.
func (r *DrillResponder) Alerts() []AlertNotification {
	r.mu.RLock()
	defer r.mu.RUnlock()
	copied := make([]AlertNotification, len(r.receivedAlerts))
	copy(copied, r.receivedAlerts)
	return copied
}

// Reset clears retained alerts (useful in multi-stage drill suites).
func (r *DrillResponder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receivedAlerts = r.receivedAlerts[:0]
}

// Handler returns the HTTP router serving /alerts and /health/live.
func (r *DrillResponder) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/alerts", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Enforce authentication via Bearer token or X-Drill-Token header
		if r.authToken != "" {
			authHeader := strings.TrimSpace(req.Header.Get("Authorization"))
			expectedBearer := "Bearer " + r.authToken
			customToken := strings.TrimSpace(req.Header.Get("X-Drill-Token"))

			if authHeader != expectedBearer && customToken != r.authToken {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
		}

		// Enforce strict 64 KiB request body limit
		req.Body = http.MaxBytesReader(w, req.Body, MaxAlertBodyBytes)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		var notif AlertNotification
		if err := json.Unmarshal(body, &notif); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}

		// Retain alerts with bounded ring buffer semantics
		r.mu.Lock()
		if len(r.receivedAlerts) >= MaxRetainedAlerts {
			r.receivedAlerts = r.receivedAlerts[1:]
		}
		r.receivedAlerts = append(r.receivedAlerts, notif)
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"received"}`))
	})
	return mux
}

// RunResponder serves the drill responder over HTTP until ctx is cancelled.
func RunResponder(ctx context.Context, addr string, authToken ...string) error {
	if addr == "" {
		addr = ":9099"
	}
	responder := NewDrillResponder(authToken...)
	server := &http.Server{
		Addr:         addr,
		Handler:      responder.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("drill responder listener unavailable on %s: %w", addr, err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()

	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(drainCtx)
		<-done
		return nil
	}
}
