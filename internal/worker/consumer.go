// Package worker implements durable background message consumption, inbox deduplication,
// poison message quarantine to DLQ, and notification side effects.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/messaging"
)

// ConsumerName identifying this background worker in public.inbox_message.
const ConsumerName = "bliss-notification-worker"

// Status constants for inbox messages.
const (
	InboxProcessing   = "processing"
	InboxProcessed    = "processed"
	InboxDeadLettered = "dead_lettered"
)

// ErrPoisonPayload indicates a deterministic unrecoverable payload error that should route directly to DLQ.
var ErrPoisonPayload = errors.New("poison payload")

// HandlerFunc processes a validated event envelope.
type HandlerFunc func(ctx context.Context, env events.Envelope) error

// NotificationConsumer consumes messages from the broker, deduplicates using PostgreSQL,
// and routes poison messages to the dead letter queue.
type NotificationConsumer struct {
	pool     *pgxpool.Pool
	consumer messaging.Consumer
	handlers map[string]HandlerFunc
	logger   *slog.Logger
	mu       sync.RWMutex
}

// NewNotificationConsumer initializes the consumer with default event handlers.
func NewNotificationConsumer(pool *pgxpool.Pool, consumer messaging.Consumer, logger *slog.Logger) *NotificationConsumer {
	if logger == nil {
		logger = slog.Default()
	}

	nc := &NotificationConsumer{
		pool:     pool,
		consumer: consumer,
		handlers: make(map[string]HandlerFunc),
		logger:   logger,
	}

	// Register default notification handlers
	nc.RegisterHandler(events.TypeOrderCreated, nc.handleOrderCreated)
	nc.RegisterHandler(events.TypeOrderPaid, nc.handleOrderPaid)
	nc.RegisterHandler(events.TypePaymentAttempt, nc.handlePaymentAttempt)

	return nc
}

// RegisterHandler binds an event type to a custom processing function.
func (c *NotificationConsumer) RegisterHandler(eventType string, handler HandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[eventType] = handler
}

// Start begins consuming deliveries from the queue until context cancellation.
func (c *NotificationConsumer) Start(ctx context.Context) error {
	c.logger.Info("notification_consumer_started", "consumer_name", ConsumerName)
	return c.consumer.Consume(ctx, c.ProcessDelivery)
}

// ProcessDelivery handles a single delivered message with deduplication, atomic pre-claiming, and DLQ routing.
func (c *NotificationConsumer) ProcessDelivery(ctx context.Context, d messaging.Delivery) error {
	msgID := d.MessageID()
	if msgID == "" {
		// Deliveries without message IDs are treated as poison
		c.logger.Error("consumer_missing_message_id", "routing_key", d.RoutingKey())
		_ = d.RejectToDLQ()
		return errors.New("delivery missing message ID")
	}

	// 1. Parse and Validate Envelope (Poison Message Detection)
	var env events.Envelope
	if err := json.Unmarshal(d.Body(), &env); err != nil {
		c.logger.Error("consumer_poison_message_unmarshal_failed", "msg_id", msgID, "error", err.Error())
		c.recordDeadLetter(ctx, msgID, d.Body(), "unmarshal failed: "+err.Error())
		_ = d.RejectToDLQ()
		return nil
	}

	if err := env.Validate(); err != nil {
		c.logger.Error("consumer_poison_message_validation_failed", "msg_id", msgID, "error", err.Error())
		c.recordDeadLetter(ctx, msgID, d.Body(), "validation failed: "+err.Error())
		_ = d.RejectToDLQ()
		return nil
	}

	// 2. Atomic Pre-Claim and Durable Deduplication
	claimed, isDuplicate, err := c.claimMessage(ctx, msgID, d.Body())
	if err != nil {
		c.logger.Error("consumer_inbox_claim_failed", "msg_id", msgID, "error", err.Error())
		_ = d.Nack(true)
		return err
	}
	if !claimed {
		if isDuplicate {
			c.logger.Info("consumer_duplicate_delivery_ignored", "msg_id", msgID, "redelivered", d.Redelivered())
			return d.Ack()
		}
		// Another worker is actively processing this message; requeue
		_ = d.Nack(true)
		return nil
	}

	// 3. Dispatch to Event Handler
	c.mu.RLock()
	handler, exists := c.handlers[env.EventType]
	c.mu.RUnlock()

	if exists && handler != nil {
		if err := handler(ctx, env); err != nil {
			if errors.Is(err, ErrPoisonPayload) {
				c.logger.Error("consumer_poison_payload_quarantined", "msg_id", msgID, "event_id", env.EventID, "error", err.Error())
				c.recordDeadLetter(ctx, msgID, d.Body(), "payload rejected: "+err.Error())
				_ = d.RejectToDLQ()
				return nil
			}

			c.logger.Warn("consumer_handler_failed", "event_id", env.EventID, "type", env.EventType, "error", err.Error())
			// Transient handler failure: release claim and requeue for retry
			c.releaseProcessingClaim(ctx, msgID)
			_ = d.Nack(true)
			return err
		}
	} else {
		c.logger.Debug("consumer_unhandled_event_type", "type", env.EventType, "event_id", env.EventID)
	}

	// 4. Record Successful Processing in Inbox
	if err := c.recordProcessed(ctx, msgID, d.Body()); err != nil {
		c.logger.Error("consumer_record_inbox_failed", "event_id", env.EventID, "error", err.Error())
		_ = d.Nack(true)
		return err
	}

	// 5. Positive Acknowledgment
	return d.Ack()
}

// claimMessage atomically inserts a processing row into public.inbox_message.
func (c *NotificationConsumer) claimMessage(ctx context.Context, msgID string, payload []byte) (claimed bool, isDuplicate bool, err error) {
	if c.pool == nil {
		return true, false, nil
	}

	tag, insertErr := c.pool.Exec(ctx, `
		INSERT INTO public.inbox_message (id, consumer_name, status, payload, created_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (id, consumer_name) DO NOTHING
	`, msgID, ConsumerName, InboxProcessing, payload)
	if insertErr != nil {
		return false, false, insertErr
	}

	if tag.RowsAffected() == 1 {
		return true, false, nil
	}

	// Row already exists: check its status
	var status string
	var createdAt time.Time
	queryErr := c.pool.QueryRow(ctx, `
		SELECT status, created_at FROM public.inbox_message
		WHERE id = $1 AND consumer_name = $2
	`, msgID, ConsumerName).Scan(&status, &createdAt)
	if queryErr != nil {
		return false, false, queryErr
	}

	if status == InboxProcessed || status == InboxDeadLettered {
		return false, true, nil
	}

	// If processing status was set over 1 minute ago, reclaim abandoned lock
	if status == InboxProcessing && time.Since(createdAt) > 1*time.Minute {
		updateTag, updateErr := c.pool.Exec(ctx, `
			UPDATE public.inbox_message
			SET created_at = now()
			WHERE id = $1 AND consumer_name = $2 AND status = $3
		`, msgID, ConsumerName, InboxProcessing)
		if updateErr == nil && updateTag.RowsAffected() == 1 {
			return true, false, nil
		}
	}

	return false, false, nil
}

// releaseProcessingClaim removes the processing claim on transient handler failure so retries can proceed.
func (c *NotificationConsumer) releaseProcessingClaim(ctx context.Context, msgID string) {
	if c.pool == nil {
		return
	}
	_, _ = c.pool.Exec(ctx, `
		DELETE FROM public.inbox_message
		WHERE id = $1 AND consumer_name = $2 AND status = $3
	`, msgID, ConsumerName, InboxProcessing)
}

// recordProcessed persists the message in public.inbox_message as processed.
func (c *NotificationConsumer) recordProcessed(ctx context.Context, msgID string, payload []byte) error {
	if c.pool == nil {
		return nil
	}

	_, err := c.pool.Exec(ctx, `
		INSERT INTO public.inbox_message (id, consumer_name, status, payload, created_at, processed_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (id, consumer_name) DO UPDATE
		SET status = $3, processed_at = now()
	`, msgID, ConsumerName, InboxProcessed, payload)
	return err
}

// recordDeadLetter records a poison message in public.inbox_message as dead_lettered.
func (c *NotificationConsumer) recordDeadLetter(ctx context.Context, msgID string, payload []byte, reason string) {
	if c.pool == nil {
		return
	}

	_, _ = c.pool.Exec(ctx, `
		INSERT INTO public.inbox_message (id, consumer_name, status, payload, error_message, created_at, processed_at)
		VALUES ($1, $2, $3, $4, $5, now(), now())
		ON CONFLICT (id, consumer_name) DO UPDATE
		SET status = $3, error_message = $5, processed_at = now()
	`, msgID, ConsumerName, InboxDeadLettered, payload, reason)
}

// Default Handlers

func (c *NotificationConsumer) handleOrderCreated(ctx context.Context, env events.Envelope) error {
	var payload events.OrderCreatedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrPoisonPayload, err)
	}
	c.logger.Info("notification_order_created",
		"order_id", payload.OrderID,
		"total_kes", payload.TotalKes,
		"item_count", len(payload.Items),
	)
	return nil
}

func (c *NotificationConsumer) handleOrderPaid(ctx context.Context, env events.Envelope) error {
	var payload events.OrderPaidPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrPoisonPayload, err)
	}
	c.logger.Info("notification_order_paid",
		"order_id", payload.OrderID,
		"receipt", payload.ReceiptNumber,
		"amount_kes", payload.AmountKes,
	)
	return nil
}

func (c *NotificationConsumer) handlePaymentAttempt(ctx context.Context, env events.Envelope) error {
	var payload events.PaymentAttemptPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrPoisonPayload, err)
	}
	c.logger.Info("notification_payment_attempt",
		"order_id", payload.OrderID,
		"attempt_number", payload.AttemptNumber,
		"status", payload.Status,
		"amount_kes", payload.AmountKes,
	)
	return nil
}
