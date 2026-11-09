// Package reconciliation coordinates automated query reconciliation for stale payment attempts,
// bounded exponential backoff, and actionable exception escalation.
package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
)

// OrderStore defines the repository capabilities required by the reconciler.
type OrderStore interface {
	ListUnresolvedPaymentAttempts(ctx context.Context, staleThreshold time.Duration, limit int) ([]orders.PaymentAttempt, error)
	UpdatePaymentAttemptQuerySchedule(ctx context.Context, attemptID string, nextQueryAt *time.Time, lastError string) error
	UpdatePaymentAttempt(ctx context.Context, attemptID, status, providerTxID, providerRefID, errorMsg string) error
	ConfirmPayment(ctx context.Context, orderID string, receiptNumber string, paidAmountKes ...int) error
	RecordPaymentException(ctx context.Context, exc orders.PaymentException) (*orders.PaymentException, error)
	GetOrder(ctx context.Context, orderID string) (*orders.Order, error)
}

// Config configures timing thresholds and bounds for query reconciliation.
type Config struct {
	StaleThreshold   time.Duration // Time an attempt must be pending before querying provider (default 1m)
	MaxQueryAttempts int           // Max queries before raising an unresolved exception (default 5)
	BaseBackoff      time.Duration // Initial retry delay (default 15s)
	MaxBackoff       time.Duration // Maximum exponential backoff ceiling (default 4m)
}

// DefaultConfig returns safe production defaults for NCBA mobile payments.
func DefaultConfig() Config {
	return Config{
		StaleThreshold:   1 * time.Minute,
		MaxQueryAttempts: 5,
		BaseBackoff:      15 * time.Second,
		MaxBackoff:       4 * time.Minute,
	}
}

// Summary reports metrics from a reconciliation sweep.
type Summary struct {
	Processed        int `json:"processed"`
	Succeeded        int `json:"succeeded"`
	Failed           int `json:"failed"`
	Pending          int `json:"pending"`
	ExceptionsRaised int `json:"exceptionsRaised"`
}

// Reconciler executes automated reconciliation against the provider.
type Reconciler struct {
	store    OrderStore
	provider provider.PaymentProvider
	cfg      Config
	logger   *slog.Logger
}

// New creates an authorized reconciler.
func New(store OrderStore, prov provider.PaymentProvider, cfg Config, logger *slog.Logger) *Reconciler {
	if cfg.StaleThreshold <= 0 {
		cfg.StaleThreshold = 1 * time.Minute
	}
	if cfg.MaxQueryAttempts <= 0 {
		cfg.MaxQueryAttempts = 5
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 15 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 4 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Reconciler{
		store:    store,
		provider: prov,
		cfg:      cfg,
		logger:   logger,
	}
}

// ReconcileStaleAttempts finds unresolved payment attempts and converges them or schedules retries.
func (r *Reconciler) ReconcileStaleAttempts(ctx context.Context) (Summary, error) {
	var s Summary
	if r.store == nil {
		return s, errors.New("reconciler store is nil")
	}

	attempts, err := r.store.ListUnresolvedPaymentAttempts(ctx, r.cfg.StaleThreshold, 50)
	if err != nil {
		return s, fmt.Errorf("failed to list unresolved attempts: %w", err)
	}

	s.Processed = len(attempts)
	if len(attempts) == 0 {
		return s, nil
	}

	r.logger.Info("reconciler_sweep_starting", "unresolved_count", len(attempts))

	for _, att := range attempts {
		if ctx.Err() != nil {
			return s, ctx.Err()
		}

		// Case 1: Ambiguous initiation with no provider transaction ID
		if att.ProviderTransactionID == "" {
			if time.Since(att.CreatedAt) >= 5*time.Minute {
				r.logger.Warn("reconciler_timing_out_unmapped_attempt",
					"attempt_id", att.ID,
					"order_id", att.OrderID,
					"created_at", att.CreatedAt,
				)
				_ = r.store.UpdatePaymentAttempt(ctx, att.ID, orders.AttemptFailed, "", "",
					"Initiation timed out before provider transaction ID was established")
				s.Failed++
			}
			continue
		}

		// If no provider adapter is configured (e.g. unit test mode without provider), skip live querying
		if r.provider == nil {
			continue
		}

		// Case 2: Query Provider STK Status
		qResp, qErr := r.provider.QuerySTK(ctx, provider.STKQueryRequest{
			TransactionID: att.ProviderTransactionID,
		})

		if qErr != nil {
			r.logger.Warn("reconciler_provider_query_failed",
				"attempt_id", att.ID,
				"tx_id", att.ProviderTransactionID,
				"error", qErr.Error(),
			)
			r.handleRetryOrException(ctx, att, qErr.Error(), &s)
			continue
		}

		switch qResp.Status {
		case "SUCCESS":
			r.logger.Info("reconciler_converged_success",
				"attempt_id", att.ID,
				"order_id", att.OrderID,
				"tx_id", att.ProviderTransactionID,
			)
			// Confirm payment for the order
			cErr := r.store.ConfirmPayment(ctx, att.OrderID, att.ProviderTransactionID, att.AmountKes)
			if cErr != nil && !errors.Is(cErr, orders.ErrLatePaymentResolution) {
				r.logger.Error("reconciler_confirm_payment_error",
					"order_id", att.OrderID,
					"error", cErr.Error(),
				)
				// Retry or raise exception
				r.handleRetryOrException(ctx, att, "ConfirmPayment failed: "+cErr.Error(), &s)
				continue
			}

			_ = r.store.UpdatePaymentAttempt(ctx, att.ID, orders.AttemptSucceeded, att.ProviderTransactionID, att.ProviderReferenceID, "")
			s.Succeeded++

		case "FAILED":
			r.logger.Info("reconciler_converged_failure",
				"attempt_id", att.ID,
				"order_id", att.OrderID,
				"description", qResp.Description,
			)
			_ = r.store.UpdatePaymentAttempt(ctx, att.ID, orders.AttemptFailed, att.ProviderTransactionID, att.ProviderReferenceID, qResp.Description)
			s.Failed++

		default: // "PENDING" or unknown provider status
			r.logger.Info("reconciler_attempt_still_pending",
				"attempt_id", att.ID,
				"order_id", att.OrderID,
				"provider_status", qResp.Status,
			)
			r.handleRetryOrException(ctx, att, "provider status: "+qResp.Status, &s)
		}
	}

	r.logger.Info("reconciler_sweep_completed",
		"processed", s.Processed,
		"succeeded", s.Succeeded,
		"failed", s.Failed,
		"pending", s.Pending,
		"exceptions_raised", s.ExceptionsRaised,
	)

	return s, nil
}

func (r *Reconciler) handleRetryOrException(ctx context.Context, att orders.PaymentAttempt, reason string, s *Summary) {
	nextQueryCount := att.QueryCount + 1

	if nextQueryCount >= r.cfg.MaxQueryAttempts {
		r.logger.Error("reconciler_max_attempts_exceeded_raising_exception",
			"attempt_id", att.ID,
			"order_id", att.OrderID,
			"queries", nextQueryCount,
			"reason", reason,
		)
		// Mark attempt unknown
		_ = r.store.UpdatePaymentAttempt(ctx, att.ID, orders.AttemptUnknown, att.ProviderTransactionID, att.ProviderReferenceID,
			"Exceeded maximum query attempts without definitive status from provider: "+reason)

		// Raise actionable payment exception
		ordID := att.OrderID
		attID := att.ID
		pTx := att.ProviderTransactionID
		phone := att.Phone
		amt := att.AmountKes
		_, err := r.store.RecordPaymentException(ctx, orders.PaymentException{
			OrderID:               &ordID,
			AttemptID:             &attID,
			ExceptionType:         orders.ExceptionStaleAttemptUnresolved,
			Provider:              att.Provider,
			ProviderTransactionID: &pTx,
			ExpectedAmountKes:     &amt,
			CustomerPhone:         &phone,
			Status:                orders.ExceptionStatusOpen,
			Metadata: map[string]any{
				"queryCount": nextQueryCount,
				"lastError":  reason,
			},
		})
		if err != nil {
			r.logger.Error("reconciler_failed_to_record_exception", "error", err.Error())
		} else {
			s.ExceptionsRaised++
		}
		return
	}

	// Calculate bounded exponential backoff: base * 2^queryCount
	multiplier := 1 << att.QueryCount
	if multiplier > 16 {
		multiplier = 16
	}
	delay := r.cfg.BaseBackoff * time.Duration(multiplier)
	if delay > r.cfg.MaxBackoff {
		delay = r.cfg.MaxBackoff
	}

	nextTime := time.Now().Add(delay)
	_ = r.store.UpdatePaymentAttemptQuerySchedule(ctx, att.ID, &nextTime, reason)
	s.Pending++
}
