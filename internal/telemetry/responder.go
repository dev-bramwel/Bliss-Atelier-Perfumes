package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

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
// notifications, dispatches operational escalations, and exposes liveness endpoints.
type DrillResponder struct {
	mu             sync.RWMutex
	authToken      string
	forwardURL     string
	receivedAlerts []AlertNotification
}

// NewDrillResponder initializes an in-memory alert recipient store with token validation
// and optional operational escalation webhook forwarding.
func NewDrillResponder(authToken ...string) *DrillResponder {
	var token string
	if len(authToken) > 0 && authToken[0] != "" {
		token = authToken[0]
	} else if env := os.Getenv("GO_DRILL_RESPONDER_TOKEN"); env != "" {
		token = env
	} else if file := os.Getenv("GO_DRILL_RESPONDER_TOKEN_FILE"); file != "" {
		if b, err := os.ReadFile(file); err == nil {
			token = strings.TrimSpace(string(b))
		}
	}

	forwardURL := os.Getenv("GO_ESCALATION_FORWARD_URL")

	return &DrillResponder{
		authToken:      token,
		forwardURL:     forwardURL,
		receivedAlerts: make([]AlertNotification, 0, MaxRetainedAlerts),
	}
}

// SetForwardURL sets the upstream operational escalation destination URL (e.g., PagerDuty, Opsgenie, Slack).
func (r *DrillResponder) SetForwardURL(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forwardURL = url
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

func (r *DrillResponder) isAuthorized(req *http.Request) bool {
	if r.authToken == "" {
		return false
	}
	authHeader := strings.TrimSpace(req.Header.Get("Authorization"))
	expectedBearer := "Bearer " + r.authToken
	customToken := strings.TrimSpace(req.Header.Get("X-Drill-Token"))
	return authHeader == expectedBearer || customToken == r.authToken
}

// Handler returns the HTTP router serving /alerts, /health/live, and operational escalation endpoints.
func (r *DrillResponder) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	handleAlertEndpoint := func(escalationTarget string) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet {
				if !r.isAuthorized(req) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(r.Alerts())
				return
			}

			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}

			// Enforce authentication via Bearer token or X-Drill-Token header
			if !r.isAuthorized(req) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
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

			// Operational escalation dispatch & structured audit
			if escalationTarget != "" {
				slog.Warn("operational_alert_escalated",
					"channel", escalationTarget,
					"receiver", notif.Receiver,
					"status", notif.Status,
					"alert_count", len(notif.Alerts),
				)
				for _, a := range notif.Alerts {
					slog.Warn("operational_escalation_incident",
						"channel", escalationTarget,
						"alertname", a.Labels["alertname"],
						"severity", a.Labels["severity"],
						"team", a.Labels["team"],
						"owner", a.Annotations["owner"],
						"escalation", a.Annotations["escalation"],
						"runbook_url", a.Annotations["runbook_url"],
					)
				}
			}

			// Optional external webhook forwarder (PagerDuty, Opsgenie, Slack)
			r.mu.RLock()
			fwd := r.forwardURL
			r.mu.RUnlock()
			if fwd != "" {
				go func(target string, payload []byte) {
					client := &http.Client{Timeout: 5 * time.Second}
					postReq, pErr := http.NewRequestWithContext(context.Background(), http.MethodPost, target, bytes.NewReader(payload))
					if pErr == nil {
						postReq.Header.Set("Content-Type", "application/json")
						if resp, dErr := client.Do(postReq); dErr == nil {
							_ = resp.Body.Close()
						}
					}
				}(fwd, body)
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
		}
	}

	mux.HandleFunc("/alerts", handleAlertEndpoint(""))
	mux.HandleFunc("/escalations/platform-sre", handleAlertEndpoint("platform-sre"))
	mux.HandleFunc("/escalations/finance-ops", handleAlertEndpoint("finance-ops"))
	mux.HandleFunc("/escalations/default", handleAlertEndpoint("default"))
	return mux
}

// RunResponder serves the drill responder over HTTP until ctx is cancelled.
func RunResponder(ctx context.Context, addr string, authToken ...string) error {
	if addr == "" {
		addr = ":9099"
	}
	responder := NewDrillResponder(authToken...)
	if responder.authToken == "" {
		return errors.New("drill responder token required: configure authToken, GO_DRILL_RESPONDER_TOKEN, or GO_DRILL_RESPONDER_TOKEN_FILE")
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           responder.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
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
