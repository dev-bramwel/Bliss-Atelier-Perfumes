package outbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/messaging"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry"
)

// PublisherConfig configures the outbox dispatcher.
type PublisherConfig struct {
	PollInterval time.Duration
	BatchSize    int
	Destination  string
	MaxRetries   int
}

// DefaultPublisherConfig returns standard defaults.
func DefaultPublisherConfig() PublisherConfig {
	return PublisherConfig{
		PollInterval: 500 * time.Millisecond,
		BatchSize:    50,
		Destination:  DefaultDestination,
		MaxRetries:   DefaultMaxRetries,
	}
}

// EventStore defines persistence operations required by the outbox dispatcher.
type EventStore interface {
	FetchPending(ctx context.Context, batchSize int, destination string) ([]EventRecord, error)
	CountPending(ctx context.Context, destination string) (int, error)
	MarkPublished(ctx context.Context, ids []string) error
	MarkFailed(ctx context.Context, id string, reason string, maxRetries int) error
}

// BackgroundPublisher polls the outbox store for pending events and publishes them with publisher confirms.
type BackgroundPublisher struct {
	store    EventStore
	producer messaging.Publisher
	cfg      PublisherConfig
	logger   *slog.Logger
}

// NewBackgroundPublisher creates an outbox background publisher.
func NewBackgroundPublisher(store EventStore, producer messaging.Publisher, cfg PublisherConfig, logger *slog.Logger) *BackgroundPublisher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.Destination == "" {
		cfg.Destination = DefaultDestination
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = DefaultMaxRetries
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &BackgroundPublisher{
		store:    store,
		producer: producer,
		cfg:      cfg,
		logger:   logger,
	}
}

// Start runs the polling loop until the context is canceled.
func (p *BackgroundPublisher) Start(ctx context.Context) error {
	p.logger.InfoContext(ctx, "outbox_publisher_started", "destination", p.cfg.Destination, "poll_interval", p.cfg.PollInterval)
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	// Initial sweep immediately on startup
	_ = p.ProcessBatch(ctx)

	for {
		select {
		case <-ctx.Done():
			p.logger.InfoContext(ctx, "outbox_publisher_stopped")
			return ctx.Err()
		case <-ticker.C:
			if err := p.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
				p.logger.ErrorContext(ctx, "outbox_process_batch_failed", "error", err.Error())
			}
		}
	}
}

// updatePendingGauge queries current outbox depth, updating the gauge or marking -1 and counting on error.
func (p *BackgroundPublisher) updatePendingGauge(ctx context.Context) {
	if count, countErr := p.store.CountPending(ctx, p.cfg.Destination); countErr == nil {
		telemetry.OutboxPendingEventsGauge.Set(float64(count))
	} else {
		p.logger.ErrorContext(ctx, "outbox_count_pending_failed", "destination", p.cfg.Destination, "error", countErr.Error())
		telemetry.OutboxCountErrorsTotal.Inc(p.cfg.Destination)
		telemetry.OutboxPendingEventsGauge.Set(-1)
	}
}

// ProcessBatch fetches a single batch of pending outbox events and publishes them with confirms.
func (p *BackgroundPublisher) ProcessBatch(ctx context.Context) error {
	p.updatePendingGauge(ctx)

	records, err := p.store.FetchPending(ctx, p.cfg.BatchSize, p.cfg.Destination)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	var publishedIDs []string
	for _, rec := range records {
		if ctx.Err() != nil {
			break
		}

		msgCtx := ctx
		if rec.CorrelationID != "" {
			msgCtx = telemetry.WithCorrelationID(ctx, rec.CorrelationID)
		}

		// Build canonical envelope to publish
		env := events.Envelope{
			EventID:          rec.ID,
			EventType:        rec.EventType,
			SchemaVersion:    rec.SchemaVersion,
			OccurredAt:       rec.CreatedAt,
			AggregateID:      rec.AggregateID,
			AggregateVersion: rec.AggregateVersion,
			CorrelationID:    rec.CorrelationID,
			Payload:          rec.Payload,
		}

		body, err := json.Marshal(env)
		if err != nil {
			p.logger.ErrorContext(msgCtx, "outbox_marshal_envelope_failed", "event_id", rec.ID, "error", err.Error())
			_ = p.store.MarkFailed(ctx, rec.ID, err.Error(), p.cfg.MaxRetries)
			continue
		}

		// Routing key e.g. "order.created" from "order.created.v1"
		routingKey := toRoutingKey(rec.EventType)

		if err := p.producer.Publish(ctx, routingKey, rec.ID, body); err != nil {
			p.logger.WarnContext(msgCtx, "outbox_publish_unconfirmed", "event_id", rec.ID, "routing_key", routingKey, "error", err.Error())
			_ = p.store.MarkFailed(ctx, rec.ID, err.Error(), p.cfg.MaxRetries)
			continue
		}

		publishedIDs = append(publishedIDs, rec.ID)
	}

	if len(publishedIDs) > 0 {
		if err := p.store.MarkPublished(ctx, publishedIDs); err != nil {
			p.logger.ErrorContext(ctx, "outbox_mark_published_failed", "count", len(publishedIDs), "error", err.Error())
			return err
		}
		telemetry.OutboxEventsPublishedTotal.Add(float64(len(publishedIDs)), p.cfg.Destination)
		p.logger.DebugContext(ctx, "outbox_events_published", "count", len(publishedIDs))
		p.updatePendingGauge(ctx)
	}

	return nil
}

// toRoutingKey converts event types like "order.created.v1" to AMQP routing keys like "order.created".
func toRoutingKey(eventType string) string {
	parts := strings.Split(eventType, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return eventType
}
