package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CanonicalRequestHash calculates the SHA-256 hexadecimal digest of the raw request payload.
func CanonicalRequestHash(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// BeginIdempotent coordinates caller/operation scoped idempotency.
// Returns:
//   - (*IdempotencyRecord, isReplay=false, nil) when a new operation can proceed
//   - (*IdempotencyRecord, isReplay=true, nil) when an identical previous request completed and should be replayed
//   - (nil, false, ErrIdempotencyConflict) when the same key was used with a different request payload
//   - (nil, false, ErrIdempotencyInProgress) when a concurrent same-key request is currently being processed
func (s *Store) BeginIdempotent(ctx context.Context, key, callerScope, operation, requestHash string, ttl time.Duration) (*IdempotencyRecord, bool, error) {
	if key == "" || callerScope == "" || operation == "" {
		return nil, false, errors.New("idempotency key, caller scope, and operation are required")
	}
	if ttl <= 0 {
		ttl = DefaultIdempotencyTTL
	}

	for {
		var rec IdempotencyRecord
		var headerBytes []byte
		var resourceID *string

		err := s.Pool.QueryRow(ctx, `
			SELECT key, caller_scope, operation, request_hash, status, status_code, response_headers, response_body, resource_id, created_at, expires_at
			FROM public.idempotency_key
			WHERE key = $1 AND caller_scope = $2 AND operation = $3
		`, key, callerScope, operation).Scan(
			&rec.Key,
			&rec.CallerScope,
			&rec.Operation,
			&rec.RequestHash,
			&rec.Status,
			&rec.StatusCode,
			&headerBytes,
			&rec.ResponseBody,
			&resourceID,
			&rec.CreatedAt,
			&rec.ExpiresAt,
		)

		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("failed to query idempotency key: %w", err)
		}

		if errors.Is(err, pgx.ErrNoRows) {
			// Key not found: attempt to insert in_progress record
			expiresAt := time.Now().Add(ttl)
			_, insertErr := s.Pool.Exec(ctx, `
				INSERT INTO public.idempotency_key (
					key, caller_scope, operation, request_hash, status, status_code, response_headers, response_body, resource_id, created_at, expires_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, now(), $10)
			`, key, callerScope, operation, requestHash, IdempotencyInProgress, 0, "{}", "", nil, expiresAt)

			if insertErr != nil {
				var pgErr *pgconn.PgError
				if errors.As(insertErr, &pgErr) && pgErr.Code == "23505" {
					// Concurrent insert race: loop and read existing row
					continue
				}
				return nil, false, fmt.Errorf("failed to insert idempotency key: %w", insertErr)
			}

			rec = IdempotencyRecord{
				Key:             key,
				CallerScope:     callerScope,
				Operation:       operation,
				RequestHash:     requestHash,
				Status:          IdempotencyInProgress,
				StatusCode:      0,
				ResponseHeaders: map[string]string{},
				ResponseBody:    "",
				CreatedAt:       time.Now(),
				ExpiresAt:       expiresAt,
			}
			return &rec, false, nil
		}

		// Row exists
		if resourceID != nil {
			rec.ResourceID = *resourceID
		}
		if len(headerBytes) > 0 {
			_ = json.Unmarshal(headerBytes, &rec.ResponseHeaders)
		}

		now := time.Now()
		// If expired, recycle key
		if rec.ExpiresAt.Before(now) {
			expiresAt := now.Add(ttl)
			tag, updateErr := s.Pool.Exec(ctx, `
				UPDATE public.idempotency_key
				SET request_hash = $1, status = $2, status_code = 0, response_headers = '{}'::jsonb, response_body = '', resource_id = NULL, created_at = now(), expires_at = $3
				WHERE key = $4 AND caller_scope = $5 AND operation = $6 AND expires_at < now()
			`, requestHash, IdempotencyInProgress, expiresAt, key, callerScope, operation)
			if updateErr != nil {
				return nil, false, fmt.Errorf("failed to recycle expired idempotency key: %w", updateErr)
			}
			if tag.RowsAffected() == 1 {
				rec.RequestHash = requestHash
				rec.Status = IdempotencyInProgress
				rec.StatusCode = 0
				rec.ResponseHeaders = map[string]string{}
				rec.ResponseBody = ""
				rec.ResourceID = ""
				rec.CreatedAt = now
				rec.ExpiresAt = expiresAt
				return &rec, false, nil
			}
			// Another worker updated it, retry loop
			continue
		}

		switch rec.Status {
		case IdempotencyInProgress:
			return nil, false, ErrIdempotencyInProgress
		case IdempotencyCompleted:
			if rec.RequestHash != requestHash {
				return nil, false, ErrIdempotencyConflict
			}
			return &rec, true, nil
		case IdempotencyFailed:
			// Failed attempt can be retried
			expiresAt := now.Add(ttl)
			tag, updateErr := s.Pool.Exec(ctx, `
				UPDATE public.idempotency_key
				SET request_hash = $1, status = $2, status_code = 0, response_headers = '{}'::jsonb, response_body = '', resource_id = NULL, created_at = now(), expires_at = $3
				WHERE key = $4 AND caller_scope = $5 AND operation = $6 AND status = $7
			`, requestHash, IdempotencyInProgress, expiresAt, key, callerScope, operation, IdempotencyFailed)
			if updateErr != nil {
				return nil, false, fmt.Errorf("failed to retry failed idempotency key: %w", updateErr)
			}
			if tag.RowsAffected() == 1 {
				rec.RequestHash = requestHash
				rec.Status = IdempotencyInProgress
				rec.StatusCode = 0
				rec.ResponseHeaders = map[string]string{}
				rec.ResponseBody = ""
				rec.ResourceID = ""
				rec.CreatedAt = now
				rec.ExpiresAt = expiresAt
				return &rec, false, nil
			}
			continue
		default:
			return nil, false, fmt.Errorf("unknown idempotency status: %s", rec.Status)
		}
	}
}

// CompleteIdempotent saves the outcome of the idempotent operation.
func (s *Store) CompleteIdempotent(ctx context.Context, key, callerScope, operation string, statusCode int, headers map[string]string, responseBody, resourceID string) error {
	headerBytes, err := json.Marshal(headers)
	if err != nil {
		headerBytes = []byte("{}")
	}

	var resID *string
	if resourceID != "" {
		resID = &resourceID
	}

	_, err = s.Pool.Exec(ctx, `
		UPDATE public.idempotency_key
		SET status = $1, status_code = $2, response_headers = $3::jsonb, response_body = $4, resource_id = $5
		WHERE key = $6 AND caller_scope = $7 AND operation = $8
	`, IdempotencyCompleted, statusCode, string(headerBytes), responseBody, resID, key, callerScope, operation)
	if err != nil {
		return fmt.Errorf("failed to complete idempotency key: %w", err)
	}
	return nil
}

// FailIdempotent marks an operation as failed or removes the in_progress lock so the client can fix or retry.
// It strictly guards that only in_progress records can be marked failed, preventing any accidental
// downgrade of an already-completed idempotency record.
func (s *Store) FailIdempotent(ctx context.Context, key, callerScope, operation string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE public.idempotency_key
		SET status = $1
		WHERE key = $2 AND caller_scope = $3 AND operation = $4 AND status = $5
	`, IdempotencyFailed, key, callerScope, operation, IdempotencyInProgress)
	if err != nil {
		return fmt.Errorf("failed to mark idempotency key failed: %w", err)
	}
	return nil
}
