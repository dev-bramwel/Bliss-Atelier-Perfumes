// Package outbox implements the transactional outbox pattern, persisting events
// atomically with business mutations in PostgreSQL and dispatching them with publisher confirms.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
)

// Status constants for outbox events.
const (
	StatusPending   = "pending"
	StatusPublished = "published"
	StatusFailed    = "failed"

	DefaultDestination = "rabbitmq"
	DefaultMaxRetries  = 5
)

// EventRecord represents a stored outbox event row in public.outbox_event.
type EventRecord struct {
	ID               string
	EventType        string
	AggregateID      string
	AggregateVersion int
	CorrelationID    string
	SchemaVersion    int
	Payload          []byte
	Destination      string
	Status           string
	RetryCount       int
	ErrorMessage     string
	PublishedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Store handles transactional outbox persistence and state transitions.
type Store struct {
	Pool *pgxpool.Pool
}

// NewStore creates a new outbox Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{Pool: pool}
}

// Enqueue persists an event envelope inside the caller's active database transaction (tx).
// This guarantees atomic dual-write safety: if the business mutation commits, the outbox event commits.
func (s *Store) Enqueue(ctx context.Context, tx pgx.Tx, env events.Envelope, destination string) error {
	if tx == nil {
		return errors.New("outbox: active transaction required for transactional enqueue")
	}
	if err := env.Validate(); err != nil {
		return fmt.Errorf("outbox: invalid event envelope: %w", err)
	}
	if destination == "" {
		destination = DefaultDestination
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO public.outbox_event (
			id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version,
			payload, destination, status, retry_count, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, 0, $10, now()
		)
	`, env.EventID, env.EventType, env.AggregateID, env.AggregateVersion, env.CorrelationID, env.SchemaVersion,
		env.Payload, destination, StatusPending, env.OccurredAt)
	if err != nil {
		return fmt.Errorf("outbox: failed to insert outbox event: %w", err)
	}

	return nil
}

// FetchPending retrieves pending outbox events using FOR UPDATE SKIP LOCKED to allow concurrent workers.
func (s *Store) FetchPending(ctx context.Context, batchSize int, destination string) ([]EventRecord, error) {
	if s.Pool == nil {
		return nil, errors.New("outbox: database pool not initialized")
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	if destination == "" {
		destination = DefaultDestination
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version,
		       payload, destination, status, retry_count, error_message, published_at, created_at, updated_at
		FROM public.outbox_event
		WHERE destination = $1 AND status = 'pending'
		ORDER BY created_at ASC
		LIMIT $2
	`, destination, batchSize)
	if err != nil {
		return nil, fmt.Errorf("outbox: failed to query pending events: %w", err)
	}
	defer rows.Close()

	var records []EventRecord
	for rows.Next() {
		var rec EventRecord
		var errMsg *string
		if err := rows.Scan(
			&rec.ID,
			&rec.EventType,
			&rec.AggregateID,
			&rec.AggregateVersion,
			&rec.CorrelationID,
			&rec.SchemaVersion,
			&rec.Payload,
			&rec.Destination,
			&rec.Status,
			&rec.RetryCount,
			&errMsg,
			&rec.PublishedAt,
			&rec.CreatedAt,
			&rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox: failed to scan event record: %w", err)
		}
		if errMsg != nil {
			rec.ErrorMessage = *errMsg
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: row iteration error: %w", err)
	}

	return records, nil
}

// MarkPublished marks an outbox event as successfully confirmed and published.
func (s *Store) MarkPublished(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if s.Pool == nil {
		return errors.New("outbox: database pool not initialized")
	}

	_, err := s.Pool.Exec(ctx, `
		UPDATE public.outbox_event
		SET status = $1, published_at = now(), updated_at = now()
		WHERE id = ANY($2)
	`, StatusPublished, ids)
	if err != nil {
		return fmt.Errorf("outbox: failed to mark events published: %w", err)
	}

	return nil
}

// MarkFailed increments the retry count and records the failure reason.
// If retry_count reaches maxRetries, the event is marked failed.
func (s *Store) MarkFailed(ctx context.Context, id string, reason string, maxRetries int) error {
	if s.Pool == nil {
		return errors.New("outbox: database pool not initialized")
	}
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}

	_, err := s.Pool.Exec(ctx, `
		UPDATE public.outbox_event
		SET retry_count = retry_count + 1,
		    error_message = $1,
		    status = CASE WHEN retry_count + 1 >= $2 THEN 'failed' ELSE 'pending' END,
		    updated_at = now()
		WHERE id = $3
	`, reason, maxRetries, id)
	if err != nil {
		return fmt.Errorf("outbox: failed to mark event failure: %w", err)
	}

	return nil
}
