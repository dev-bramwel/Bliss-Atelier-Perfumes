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
	platformURL    string
	financeURL     string
	receivedAlerts []AlertNotification
	testSinkAlerts []AlertNotification
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
	platformURL := os.Getenv("GO_PLATFORM_ESCALATION_URL")
	financeURL := os.Getenv("GO_FINANCE_ESCALATION_URL")

	// Initialize default metric series so initial Prometheus scrapes establish 0 baselines
	PaymentExceptionsTotal.Set(0, "AMOUNT_MISMATCH")
	PaymentExceptionsTotal.Set(0, "UNMAPPED_TRANSACTION")
	PaymentExceptionsTotal.Set(0, "DUPLICATE_RECEIPT")
	RabbitMQDLQMessagesGauge.Set(0, "notifications")

	return &DrillResponder{
		authToken:      token,
		forwardURL:     forwardURL,
		platformURL:    platformURL,
		financeURL:     financeURL,
		receivedAlerts: make([]AlertNotification, 0, MaxRetainedAlerts),
	}
}

// SetForwardURL sets the upstream operational escalation destination URL (e.g., PagerDuty, Opsgenie, Slack).
func (r *DrillResponder) SetForwardURL(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forwardURL = url
}

// SetTeamForwardURL sets a team-specific upstream operational escalation destination URL.
func (r *DrillResponder) SetTeamForwardURL(team, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch team {
	case "platform-sre":
		r.platformURL = url
	case "finance-ops":
		r.financeURL = url
	default:
		r.forwardURL = url
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

// TestSinkAlerts returns a thread-safe snapshot of all alerts forwarded to the /test-sink mock upstream.
func (r *DrillResponder) TestSinkAlerts() []AlertNotification {
	r.mu.RLock()
	defer r.mu.RUnlock()
	copied := make([]AlertNotification, len(r.testSinkAlerts))
	copy(copied, r.testSinkAlerts)
	return copied
}

// Reset clears retained alerts (useful in multi-stage drill suites).
func (r *DrillResponder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receivedAlerts = r.receivedAlerts[:0]
	r.testSinkAlerts = r.testSinkAlerts[:0]
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
			body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))

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

			// Determine external webhook forward destination
			var targetURL string
			r.mu.RLock()
			switch escalationTarget {
			case "platform-sre":
				if r.platformURL != "" {
					targetURL = r.platformURL
				} else {
					targetURL = r.forwardURL
				}
			case "finance-ops":
				if r.financeURL != "" {
					targetURL = r.financeURL
				} else {
					targetURL = r.forwardURL
				}
			default:
				targetURL = r.forwardURL
			}
			r.mu.RUnlock()

			if targetURL != "" {
				client := &http.Client{Timeout: 5 * time.Second}
				postReq, pErr := http.NewRequestWithContext(req.Context(), http.MethodPost, targetURL, bytes.NewReader(body))
				if pErr != nil {
					slog.Error("operational_escalation_forward_request_error", "channel", escalationTarget, "url", targetURL, "error", pErr.Error())
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadGateway)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":   "failed to create upstream escalation request",
						"channel": escalationTarget,
					})
					return
				}
				postReq.Header.Set("Content-Type", "application/json")
				resp, dErr := client.Do(postReq)
				if dErr != nil {
					slog.Error("operational_escalation_forward_network_error", "channel", escalationTarget, "url", targetURL, "error", dErr.Error())
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadGateway)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":   "upstream escalation delivery failed",
						"channel": escalationTarget,
					})
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					slog.Error("operational_escalation_upstream_rejected", "channel", escalationTarget, "url", targetURL, "status", resp.StatusCode, "response", string(respBody))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadGateway)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":           "upstream escalation rejected notification",
						"channel":         escalationTarget,
						"upstream_status": resp.StatusCode,
					})
					return
				}
				slog.Info("operational_escalation_forward_succeeded", "channel", escalationTarget, "url", targetURL, "status", resp.StatusCode)
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

	// Expose standard Prometheus exposition on /metrics so Prometheus can scrape responder in drills
	mux.Handle("/metrics", Handler())

	// Authenticated endpoint to induce application metrics during automated synthetic drills
	mux.HandleFunc("/drill/induce-metric", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !r.isAuthorized(req) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, MaxAlertBodyBytes))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
		var payload struct {
			Metric string  `json:"metric"`
			Label  string  `json:"label"`
			Value  float64 `json:"value"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}
		switch payload.Metric {
		case "payment_exceptions_total":
			typeLabel := payload.Label
			if typeLabel == "" {
				typeLabel = "AMOUNT_MISMATCH"
			}
			PaymentExceptionsTotal.Inc(typeLabel)
		case "rabbitmq_dlq_messages_gauge":
			queueLabel := payload.Label
			if queueLabel == "" {
				queueLabel = "notifications"
			}
			RabbitMQDLQMessagesGauge.Set(payload.Value, queueLabel)
		case "unresolved_payments_gauge":
			UnresolvedPaymentsGauge.Set(payload.Value)
		default:
			http.Error(w, "unknown metric name", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"induced"}`))
	})

	// Authenticated endpoint to clear/reset induced application metrics during recovery drills
	mux.HandleFunc("/drill/clear-metric", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !r.isAuthorized(req) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, MaxAlertBodyBytes))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
		var payload struct {
			Metric string `json:"metric"`
			Label  string `json:"label"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}
		switch payload.Metric {
		case "rabbitmq_dlq_messages_gauge":
			queueLabel := payload.Label
			if queueLabel == "" {
				queueLabel = "notifications"
			}
			RabbitMQDLQMessagesGauge.Set(0, queueLabel)
		case "unresolved_payments_gauge":
			UnresolvedPaymentsGauge.Set(0)
		default:
			http.Error(w, "unknown metric name", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"cleared"}`))
	})

	// Isolated mock upstream paging endpoint (/test-sink) to verify escalation forwarding without paging on-call
	mux.HandleFunc("/test-sink", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(r.TestSinkAlerts())
			return
		}
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if req.URL.Query().Get("fail") == "500" {
			http.Error(w, "simulated upstream paging provider internal failure", http.StatusInternalServerError)
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, MaxAlertBodyBytes))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
		var notif AlertNotification
		if err := json.Unmarshal(body, &notif); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		if len(r.testSinkAlerts) >= MaxRetainedAlerts {
			r.testSinkAlerts = r.testSinkAlerts[1:]
		}
		r.testSinkAlerts = append(r.testSinkAlerts, notif)
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"captured_by_test_sink"}`))
	})

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
