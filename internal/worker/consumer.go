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
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry"
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

// InboxStore abstracts persistent consumer inbox operations for deduplication,
// claim locking, dead-letter tracking, and queue depth sampling.
type InboxStore interface {
	ClaimMessage(ctx context.Context, msgID, consumerName string, payload []byte) (claimed bool, isDuplicate bool, err error)
	ReleaseProcessingClaim(ctx context.Context, msgID, consumerName string) error
	RecordProcessed(ctx context.Context, msgID, consumerName string, payload []byte) error
	RecordDeadLetter(ctx context.Context, msgID, consumerName string, payload []byte, reason string) error
	DeadLetterCount(ctx context.Context, consumerName string) (int64, error)
}

// HandlerFunc processes a validated event envelope.
type HandlerFunc func(ctx context.Context, env events.Envelope) error

// NotificationConsumer consumes messages from the broker, deduplicates using PostgreSQL,
// and routes poison messages to the dead letter queue.
type NotificationConsumer struct {
	pool     *pgxpool.Pool
	inbox    InboxStore
	consumer messaging.Consumer
	handlers map[string]HandlerFunc
	logger   *slog.Logger
	mu       sync.RWMutex
}

// NewNotificationConsumer initializes the consumer with default event handlers.
func NewNotificationConsumer(pool *pgxpool.Pool, consumer messaging.Consumer, logger *slog.Logger) *NotificationConsumer {
	var inbox InboxStore
	if pool != nil {
		inbox = NewPGInboxStore(pool)
	}
	nc := NewNotificationConsumerWithInbox(inbox, consumer, logger)
	nc.pool = pool
	return nc
}

// NewNotificationConsumerWithInbox initializes the consumer with a custom InboxStore.
func NewNotificationConsumerWithInbox(inbox InboxStore, consumer messaging.Consumer, logger *slog.Logger) *NotificationConsumer {
	if logger == nil {
		logger = slog.Default()
	}

	nc := &NotificationConsumer{
		inbox:    inbox,
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
		telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "dead_letter")
		if c.inbox != nil {
			_, _ = c.SyncDLQGauge(ctx)
		} else {
			telemetry.RabbitMQDLQMessagesGauge.Inc("dead_letter")
		}
		_ = d.RejectToDLQ()
		return errors.New("delivery missing message ID")
	}

	// 1. Parse and Validate Envelope (Poison Message Detection)
	var env events.Envelope
	if err := json.Unmarshal(d.Body(), &env); err != nil {
		c.logger.ErrorContext(ctx, "consumer_poison_message_unmarshal_failed", "msg_id", msgID, "error", err.Error())
		c.recordDeadLetter(ctx, msgID, d.Body(), "unmarshal failed: "+err.Error())
		telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "dead_letter")
		if c.inbox == nil {
			telemetry.RabbitMQDLQMessagesGauge.Inc("dead_letter")
		}
		_ = d.RejectToDLQ()
		return nil
	}

	if env.CorrelationID != "" {
		ctx = telemetry.WithCorrelationID(ctx, env.CorrelationID)
	}

	if err := env.Validate(); err != nil {
		c.logger.ErrorContext(ctx, "consumer_poison_message_validation_failed", "msg_id", msgID, "error", err.Error())
		c.recordDeadLetter(ctx, msgID, d.Body(), "validation failed: "+err.Error())
		telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "dead_letter")
		if c.inbox == nil {
			telemetry.RabbitMQDLQMessagesGauge.Inc("dead_letter")
		}
		_ = d.RejectToDLQ()
		return nil
	}

	// 2. Atomic Pre-Claim and Durable Deduplication
	claimed, isDuplicate, err := c.claimMessage(ctx, msgID, d.Body())
	if err != nil {
		c.logger.ErrorContext(ctx, "consumer_inbox_claim_failed", "msg_id", msgID, "error", err.Error())
		_ = d.Nack(true)
		return err
	}
	if !claimed {
		if isDuplicate {
			c.logger.InfoContext(ctx, "consumer_duplicate_delivery_ignored", "msg_id", msgID, "redelivered", d.Redelivered())
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
				c.logger.ErrorContext(ctx, "consumer_poison_payload_quarantined", "msg_id", msgID, "event_id", env.EventID, "error", err.Error())
				c.recordDeadLetter(ctx, msgID, d.Body(), "payload rejected: "+err.Error())
				telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "dead_letter")
				if c.inbox == nil {
					telemetry.RabbitMQDLQMessagesGauge.Inc("dead_letter")
				}
				_ = d.RejectToDLQ()
				return nil
			}

			c.logger.WarnContext(ctx, "consumer_handler_failed", "event_id", env.EventID, "type", env.EventType, "error", err.Error())
			// Transient handler failure: release claim and requeue for retry
			telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "nack")
			c.releaseProcessingClaim(ctx, msgID)
			_ = d.Nack(true)
			return err
		}
	} else {
		c.logger.DebugContext(ctx, "consumer_unhandled_event_type", "type", env.EventType, "event_id", env.EventID)
	}

	// 4. Record Successful Processing in Inbox
	if err := c.recordProcessed(ctx, msgID, d.Body()); err != nil {
		c.logger.ErrorContext(ctx, "consumer_record_inbox_failed", "event_id", env.EventID, "error", err.Error())
		telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "nack")
		_ = d.Nack(true)
		return err
	}

	// 5. Positive Acknowledgment
	telemetry.RabbitMQMessagesConsumedTotal.Inc("notifications", "ack")
	return d.Ack()
}

func (c *NotificationConsumer) claimMessage(ctx context.Context, msgID string, payload []byte) (claimed bool, isDuplicate bool, err error) {
	if c.inbox == nil {
		return true, false, nil
	}
	return c.inbox.ClaimMessage(ctx, msgID, ConsumerName, payload)
}

func (c *NotificationConsumer) releaseProcessingClaim(ctx context.Context, msgID string) {
	if c.inbox == nil {
		return
	}
	_ = c.inbox.ReleaseProcessingClaim(ctx, msgID, ConsumerName)
}

func (c *NotificationConsumer) recordProcessed(ctx context.Context, msgID string, payload []byte) error {
	if c.inbox == nil {
		return nil
	}
	err := c.inbox.RecordProcessed(ctx, msgID, ConsumerName, payload)
	if err == nil {
		_, _ = c.SyncDLQGauge(ctx)
	}
	return err
}

func (c *NotificationConsumer) recordDeadLetter(ctx context.Context, msgID string, payload []byte, reason string) {
	if c.inbox == nil {
		return
	}
	_ = c.inbox.RecordDeadLetter(ctx, msgID, ConsumerName, payload, reason)
	_, _ = c.SyncDLQGauge(ctx)
}

// SyncDLQGauge updates the dead_letter queue gauge based on persistent state in inbox_message.
func (c *NotificationConsumer) SyncDLQGauge(ctx context.Context) (int64, error) {
	if c.inbox == nil {
		return 0, nil
	}
	count, err := c.inbox.DeadLetterCount(ctx, ConsumerName)
	if err != nil {
		return 0, err
	}
	telemetry.RabbitMQDLQMessagesGauge.Set(float64(count), "dead_letter")
	return count, nil
}

// SyncDLQGauge queries public.inbox_message for active dead_lettered records
// and synchronizes rabbitmq_dlq_messages_gauge to the true queue depth.
func SyncDLQGauge(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	if pool == nil {
		return 0, nil
	}
	store := NewPGInboxStore(pool)
	count, err := store.DeadLetterCount(ctx, ConsumerName)
	if err != nil {
		return 0, err
	}
	telemetry.RabbitMQDLQMessagesGauge.Set(float64(count), "dead_letter")
	return count, nil
}

// PGInboxStore implements InboxStore backed by PostgreSQL public.inbox_message.
type PGInboxStore struct {
	pool *pgxpool.Pool
}

// NewPGInboxStore creates an InboxStore backed by pgxpool.Pool.
func NewPGInboxStore(pool *pgxpool.Pool) *PGInboxStore {
	return &PGInboxStore{pool: pool}
}

func (s *PGInboxStore) ClaimMessage(ctx context.Context, msgID, consumerName string, payload []byte) (claimed bool, isDuplicate bool, err error) {
	if s.pool == nil {
		return true, false, nil
	}

	tag, insertErr := s.pool.Exec(ctx, `
		INSERT INTO public.inbox_message (id, consumer_name, status, payload, created_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (id, consumer_name) DO NOTHING
	`, msgID, consumerName, InboxProcessing, payload)
	if insertErr != nil {
		return false, false, insertErr
	}

	if tag.RowsAffected() == 1 {
		return true, false, nil
	}

	// Row already exists: check its status
	var status string
	var createdAt time.Time
	queryErr := s.pool.QueryRow(ctx, `
		SELECT status, created_at FROM public.inbox_message
		WHERE id = $1 AND consumer_name = $2
	`, msgID, consumerName).Scan(&status, &createdAt)
	if queryErr != nil {
		return false, false, queryErr
	}

	if status == InboxProcessed {
		return false, true, nil
	}

	if status == InboxDeadLettered {
		// Quarantined dead-letter is being replayed: transition status to processing and update payload
		updateTag, updateErr := s.pool.Exec(ctx, `
			UPDATE public.inbox_message
			SET status = $1, payload = $2, error_message = NULL, created_at = now()
			WHERE id = $3 AND consumer_name = $4 AND status = $5
		`, InboxProcessing, payload, msgID, consumerName, InboxDeadLettered)
		if updateErr == nil && updateTag.RowsAffected() == 1 {
			return true, false, nil
		}
	}

	// If processing status was set over 1 minute ago, reclaim abandoned lock
	if status == InboxProcessing && time.Since(createdAt) > 1*time.Minute {
		updateTag, updateErr := s.pool.Exec(ctx, `
			UPDATE public.inbox_message
			SET created_at = now()
			WHERE id = $1 AND consumer_name = $2 AND status = $3
		`, msgID, consumerName, InboxProcessing)
		if updateErr == nil && updateTag.RowsAffected() == 1 {
			return true, false, nil
		}
	}

	return false, false, nil
}

func (s *PGInboxStore) ReleaseProcessingClaim(ctx context.Context, msgID, consumerName string) error {
	if s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM public.inbox_message
		WHERE id = $1 AND consumer_name = $2 AND status = $3
	`, msgID, consumerName, InboxProcessing)
	return err
}

func (s *PGInboxStore) RecordProcessed(ctx context.Context, msgID, consumerName string, payload []byte) error {
	if s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO public.inbox_message (id, consumer_name, status, payload, created_at, processed_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (id, consumer_name) DO UPDATE
		SET status = $3, error_message = NULL, processed_at = now()
	`, msgID, consumerName, InboxProcessed, payload)
	return err
}

func (s *PGInboxStore) RecordDeadLetter(ctx context.Context, msgID, consumerName string, payload []byte, reason string) error {
	if s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO public.inbox_message (id, consumer_name, status, payload, error_message, created_at, processed_at)
		VALUES ($1, $2, $3, $4, $5, now(), now())
		ON CONFLICT (id, consumer_name) DO UPDATE
		SET status = $3, error_message = $5, processed_at = now()
	`, msgID, consumerName, InboxDeadLettered, payload, reason)
	return err
}

func (s *PGInboxStore) DeadLetterCount(ctx context.Context, consumerName string) (int64, error) {
	if s.pool == nil {
		return 0, nil
	}
	var count int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM public.inbox_message
		WHERE status = $1 AND consumer_name = $2
	`, InboxDeadLettered, consumerName).Scan(&count)
	return count, err
}

// MemoryInboxRecord represents an in-memory inbox message record for testing.
type MemoryInboxRecord struct {
	ID           string
	ConsumerName string
	Status       string
	Payload      []byte
	ErrorMessage string
	CreatedAt    time.Time
	ProcessedAt  time.Time
}

// MemoryInboxStore implements InboxStore in-memory for testing, enforcing the identical
// state machine, deduplication, and replay semantics as PGInboxStore.
type MemoryInboxStore struct {
	mu      sync.Mutex
	records map[string]*MemoryInboxRecord
}

// NewMemoryInboxStore creates an in-memory InboxStore.
func NewMemoryInboxStore() *MemoryInboxStore {
	return &MemoryInboxStore{
		records: make(map[string]*MemoryInboxRecord),
	}
}

func (m *MemoryInboxStore) key(msgID, consumerName string) string {
	return msgID + ":" + consumerName
}

func (m *MemoryInboxStore) ClaimMessage(ctx context.Context, msgID, consumerName string, payload []byte) (claimed bool, isDuplicate bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(msgID, consumerName)
	existing, exists := m.records[k]
	if !exists {
		m.records[k] = &MemoryInboxRecord{
			ID:           msgID,
			ConsumerName: consumerName,
			Status:       InboxProcessing,
			Payload:      payload,
			CreatedAt:    time.Now(),
		}
		return true, false, nil
	}

	if existing.Status == InboxProcessed {
		return false, true, nil
	}

	if existing.Status == InboxDeadLettered {
		// Quarantined dead-letter is being replayed: transition status to processing and update payload
		existing.Status = InboxProcessing
		existing.Payload = payload
		existing.ErrorMessage = ""
		existing.CreatedAt = time.Now()
		return true, false, nil
	}

	if existing.Status == InboxProcessing && time.Since(existing.CreatedAt) > 1*time.Minute {
		existing.CreatedAt = time.Now()
		return true, false, nil
	}

	return false, false, nil
}

func (m *MemoryInboxStore) ReleaseProcessingClaim(ctx context.Context, msgID, consumerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(msgID, consumerName)
	if existing, exists := m.records[k]; exists && existing.Status == InboxProcessing {
		delete(m.records, k)
	}
	return nil
}

func (m *MemoryInboxStore) RecordProcessed(ctx context.Context, msgID, consumerName string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(msgID, consumerName)
	m.records[k] = &MemoryInboxRecord{
		ID:           msgID,
		ConsumerName: consumerName,
		Status:       InboxProcessed,
		Payload:      payload,
		ProcessedAt:  time.Now(),
	}
	return nil
}

func (m *MemoryInboxStore) RecordDeadLetter(ctx context.Context, msgID, consumerName string, payload []byte, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(msgID, consumerName)
	m.records[k] = &MemoryInboxRecord{
		ID:           msgID,
		ConsumerName: consumerName,
		Status:       InboxDeadLettered,
		Payload:      payload,
		ErrorMessage: reason,
		ProcessedAt:  time.Now(),
	}
	return nil
}

func (m *MemoryInboxStore) DeadLetterCount(ctx context.Context, consumerName string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for _, r := range m.records {
		if r.ConsumerName == consumerName && r.Status == InboxDeadLettered {
			count++
		}
	}
	return count, nil
}

func (m *MemoryInboxStore) Get(msgID, consumerName string) (*MemoryInboxRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, exists := m.records[m.key(msgID, consumerName)]
	if !exists {
		return nil, false
	}
	cp := *r
	return &cp, true
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
