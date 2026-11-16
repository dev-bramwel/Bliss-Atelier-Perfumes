package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

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

// DrillResponder is a lightweight receiver that captures incoming Alertmanager notifications
// and exposes readiness/liveness endpoints for synthetic incident drills.
type DrillResponder struct {
	mu             sync.RWMutex
	receivedAlerts []AlertNotification
}

// NewDrillResponder initializes an in-memory alert recipient store.
func NewDrillResponder() *DrillResponder {
	return &DrillResponder{
		receivedAlerts: make([]AlertNotification, 0),
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
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		var notif AlertNotification
		if err := json.Unmarshal(body, &notif); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.receivedAlerts = append(r.receivedAlerts, notif)
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"received"}`))
	})
	return mux
}

// RunResponder serves the drill responder over HTTP until ctx is cancelled.
func RunResponder(ctx context.Context, addr string) error {
	if addr == "" {
		addr = ":9099"
	}
	responder := NewDrillResponder()
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
