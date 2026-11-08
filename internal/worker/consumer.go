// Package worker implements durable background message consumption, inbox deduplication,
// poison message quarantine to DLQ, and notification side effects.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/messaging"
)

// ConsumerName identifying this background worker in public.inbox_message.
const ConsumerName = "bliss-notification-worker"

// Status constants for inbox messages.
const (
	InboxProcessed    = "processed"
	InboxDeadLettered = "dead_lettered"
)

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

// ProcessDelivery handles a single delivered message with deduplication and DLQ routing.
func (c *NotificationConsumer) ProcessDelivery(ctx context.Context, d messaging.Delivery) error {
	msgID := d.MessageID()
	if msgID == "" {
		// Deliveries without message IDs are treated as poison
		c.logger.Error("consumer_missing_message_id", "routing_key", d.RoutingKey())
		_ = d.RejectToDLQ()
		return errors.New("delivery missing message ID")
	}

	// 1. Durable Consumer Deduplication Check
	// If this message was already processed, immediately ACK and avoid duplicate side effects.
	alreadyProcessed, err := c.isMessageProcessed(ctx, msgID)
	if err != nil {
		c.logger.Error("consumer_inbox_check_failed", "msg_id", msgID, "error", err.Error())
		// Transient DB error: requeue message
		_ = d.Nack(true)
		return err
	}
	if alreadyProcessed {
		c.logger.Info("consumer_duplicate_delivery_ignored", "msg_id", msgID, "redelivered", d.Redelivered())
		return d.Ack()
	}

	// 2. Parse and Validate Envelope (Poison Message Detection)
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

	// 3. Dispatch to Event Handler
	c.mu.RLock()
	handler, exists := c.handlers[env.EventType]
	c.mu.RUnlock()

	if exists && handler != nil {
		if err := handler(ctx, env); err != nil {
			c.logger.Warn("consumer_handler_failed", "event_id", env.EventID, "type", env.EventType, "error", err.Error())
			// Transient handler failure: requeue for retry
			_ = d.Nack(true)
			return err
		}
	} else {
		c.logger.Debug("consumer_unhandled_event_type", "type", env.EventType, "event_id", env.EventID)
	}

	// 4. Record Successful Processing in Inbox
	if err := c.recordProcessed(ctx, env.EventID, d.Body()); err != nil {
		c.logger.Error("consumer_record_inbox_failed", "event_id", env.EventID, "error", err.Error())
		// If inbox write fails, requeue
		_ = d.Nack(true)
		return err
	}

	// 5. Positive Acknowledgment
	return d.Ack()
}

// isMessageProcessed checks if public.inbox_message already has a record for (msgID, ConsumerName).
func (c *NotificationConsumer) isMessageProcessed(ctx context.Context, msgID string) (bool, error) {
	if c.pool == nil {
		return false, nil
	}

	var status string
	err := c.pool.QueryRow(ctx, `
		SELECT status FROM public.inbox_message
		WHERE id = $1 AND consumer_name = $2
	`, msgID, ConsumerName).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	return status == InboxProcessed, nil
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
		return err
	}
	c.logger.Info("notification_order_created",
		"order_id", payload.OrderID,
		"customer", payload.CustomerName,
		"phone", payload.CustomerPhone,
		"total_kes", payload.TotalKes,
	)
	return nil
}

func (c *NotificationConsumer) handleOrderPaid(ctx context.Context, env events.Envelope) error {
	var payload events.OrderPaidPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
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
		return err
	}
	c.logger.Info("notification_payment_attempt",
		"order_id", payload.OrderID,
		"attempt_number", payload.AttemptNumber,
		"status", payload.Status,
		"amount_kes", payload.AmountKes,
	)
	return nil
}
