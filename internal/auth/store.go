package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store defines persistence operations for users, login transactions, sessions, and audit logs.
type Store interface {
	// User operations
	UpsertUserByGoogleSub(ctx context.Context, googleSub, email, name, pictureURL string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CountOwners(ctx context.Context) (int, error)
	UpdateUserRole(ctx context.Context, id string, newRole string) error
	UpdateUserStatus(ctx context.Context, id string, isActive bool) error
	ListUsers(ctx context.Context, filterRole string) ([]User, error)

	// Login transaction operations
	CreateLoginTransaction(ctx context.Context, tx LoginTransaction) error
	ConsumeLoginTransaction(ctx context.Context, stateHash string, browserBindingHash string) (*LoginTransaction, error)

	// Session operations
	CreateSession(ctx context.Context, session UserSession) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*UserSession, *User, error)
	TouchSession(ctx context.Context, tokenHash string, newExpiresAt time.Time) error
	RevokeSessionByTokenHash(ctx context.Context, tokenHash string) error
	RevokeSessionsByUserID(ctx context.Context, userID string) ([]string, error)

	// Audit operations
	RecordAuditLog(ctx context.Context, entry AuditLogEntry) error
	QueryAuditLogs(ctx context.Context, limit int, offset int) ([]AuditLogEntry, error)
}

// PGStore implements Store backed by PostgreSQL via pgxpool.Pool.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore constructs a PostgreSQL-backed authentication store.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// generateID returns a random 32-character hex ID.
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

// UpsertUserByGoogleSub inserts a new customer account or updates profile fields for an existing user.
func (s *PGStore) UpsertUserByGoogleSub(ctx context.Context, googleSub, email, name, pictureURL string) (*User, error) {
	if googleSub == "" {
		return nil, errors.New("google sub is required")
	}

	newID := generateID("usr")
	query := `
		INSERT INTO public.app_user (id, google_sub, email, name, picture_url, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'customer', TRUE, now(), now())
		ON CONFLICT (google_sub) DO UPDATE
		SET email = EXCLUDED.email,
		    name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE app_user.name END,
		    picture_url = CASE WHEN EXCLUDED.picture_url <> '' THEN EXCLUDED.picture_url ELSE app_user.picture_url END,
		    updated_at = now()
		RETURNING id, google_sub, email, name, picture_url, role, is_active, created_at, updated_at;
	`

	var u User
	err := s.pool.QueryRow(ctx, query, newID, googleSub, email, name, pictureURL).Scan(
		&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.PictureURL, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert user by google sub: %w", err)
	}
	return &u, nil
}

// GetUserByID fetches a user by their primary key ID.
func (s *PGStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	query := `SELECT id, google_sub, email, name, picture_url, role, is_active, created_at, updated_at FROM public.app_user WHERE id = $1`
	var u User
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.PictureURL, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user by id: %w", err)
	}
	return &u, nil
}

// GetUserByEmail finds a user by their registered email.
func (s *PGStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `SELECT id, google_sub, email, name, picture_url, role, is_active, created_at, updated_at FROM public.app_user WHERE lower(email) = lower($1) LIMIT 1`
	var u User
	err := s.pool.QueryRow(ctx, query, email).Scan(
		&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.PictureURL, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}
	return &u, nil
}

// CountOwners returns the total count of active users with role 'owner'.
func (s *PGStore) CountOwners(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM public.app_user WHERE role = 'owner' AND is_active = TRUE`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count owners: %w", err)
	}
	return count, nil
}

// UpdateUserRole updates a user's role.
func (s *PGStore) UpdateUserRole(ctx context.Context, id string, newRole string) error {
	if !IsValidRole(newRole) {
		return errors.New("invalid role")
	}
	cmd, err := s.pool.Exec(ctx, `UPDATE public.app_user SET role = $1, updated_at = now() WHERE id = $2`, newRole, id)
	if err != nil {
		return fmt.Errorf("failed to update user role: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return errors.New("user not found")
	}
	return nil
}

// UpdateUserStatus updates a user's active status.
func (s *PGStore) UpdateUserStatus(ctx context.Context, id string, isActive bool) error {
	cmd, err := s.pool.Exec(ctx, `UPDATE public.app_user SET is_active = $1, updated_at = now() WHERE id = $2`, isActive, id)
	if err != nil {
		return fmt.Errorf("failed to update user status: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return errors.New("user not found")
	}
	return nil
}

// ListUsers retrieves users, optionally filtered by role.
func (s *PGStore) ListUsers(ctx context.Context, filterRole string) ([]User, error) {
	var rows pgx.Rows
	var err error
	if filterRole != "" {
		rows, err = s.pool.Query(ctx, `SELECT id, google_sub, email, name, picture_url, role, is_active, created_at, updated_at FROM public.app_user WHERE role = $1 ORDER BY created_at DESC LIMIT 200`, filterRole)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT id, google_sub, email, name, picture_url, role, is_active, created_at, updated_at FROM public.app_user ORDER BY created_at DESC LIMIT 200`)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.PictureURL, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateLoginTransaction inserts a short-lived login transaction for PKCE/state verification.
func (s *PGStore) CreateLoginTransaction(ctx context.Context, tx LoginTransaction) error {
	id := tx.ID
	if id == "" {
		id = generateID("ltx")
	}
	query := `
		INSERT INTO public.login_transaction (id, state_hash, nonce, pkce_verifier, browser_binding_hash, return_url, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now());
	`
	_, err := s.pool.Exec(ctx, query, id, tx.StateHash, tx.Nonce, tx.PKCEVerifier, tx.BrowserBindingHash, tx.ReturnURL, tx.ExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to create login transaction: %w", err)
	}
	return nil
}

// ConsumeLoginTransaction atomically marks a login transaction as used if valid, browser-bound, and unexpired.
func (s *PGStore) ConsumeLoginTransaction(ctx context.Context, stateHash string, browserBindingHash string) (*LoginTransaction, error) {
	query := `
		UPDATE public.login_transaction
		SET used_at = now()
		WHERE state_hash = $1
		  AND browser_binding_hash = $2
		  AND used_at IS NULL
		  AND expires_at > now()
		RETURNING id, state_hash, nonce, pkce_verifier, browser_binding_hash, return_url, expires_at, used_at, created_at;
	`
	var tx LoginTransaction
	err := s.pool.QueryRow(ctx, query, stateHash, browserBindingHash).Scan(
		&tx.ID, &tx.StateHash, &tx.Nonce, &tx.PKCEVerifier, &tx.BrowserBindingHash, &tx.ReturnURL, &tx.ExpiresAt, &tx.UsedAt, &tx.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("invalid, expired, replayed, or browser-mismatched login transaction")
		}
		return nil, fmt.Errorf("failed to consume login transaction: %w", err)
	}
	return &tx, nil
}

// CreateSession inserts an authoritative server session into PostgreSQL.
func (s *PGStore) CreateSession(ctx context.Context, session UserSession) error {
	id := session.ID
	if id == "" {
		id = generateID("ses")
	}
	query := `
		INSERT INTO public.user_session (id, user_id, token_hash, csrf_secret, ip_address, user_agent, is_revoked, expires_at, max_expires_at, created_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now());
	`
	_, err := s.pool.Exec(ctx, query, id, session.UserID, session.TokenHash, session.CSRFSecret, session.IPAddress, session.UserAgent, session.IsRevoked, session.ExpiresAt, session.MaxExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSessionByTokenHash retrieves an active session along with its associated user.
func (s *PGStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*UserSession, *User, error) {
	query := `
		SELECT s.id, s.user_id, s.token_hash, s.csrf_secret, s.ip_address, s.user_agent,
		       s.is_revoked, s.expires_at, s.max_expires_at, s.created_at, s.last_seen_at,
		       u.id, u.google_sub, u.email, u.name, u.picture_url, u.role, u.is_active, u.created_at, u.updated_at
		FROM public.user_session s
		JOIN public.app_user u ON s.user_id = u.id
		WHERE s.token_hash = $1 AND s.is_revoked = FALSE;
	`
	var ses UserSession
	var u User
	err := s.pool.QueryRow(ctx, query, tokenHash).Scan(
		&ses.ID, &ses.UserID, &ses.TokenHash, &ses.CSRFSecret, &ses.IPAddress, &ses.UserAgent,
		&ses.IsRevoked, &ses.ExpiresAt, &ses.MaxExpiresAt, &ses.CreatedAt, &ses.LastSeenAt,
		&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.PictureURL, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to query session by token hash: %w", err)
	}
	return &ses, &u, nil
}

// TouchSession updates last_seen_at and slides expires_at.
func (s *PGStore) TouchSession(ctx context.Context, tokenHash string, newExpiresAt time.Time) error {
	query := `UPDATE public.user_session SET last_seen_at = now(), expires_at = $1 WHERE token_hash = $2 AND is_revoked = FALSE`
	_, err := s.pool.Exec(ctx, query, newExpiresAt, tokenHash)
	return err
}

// RevokeSessionByTokenHash marks a session as revoked.
func (s *PGStore) RevokeSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE public.user_session SET is_revoked = TRUE WHERE token_hash = $1`, tokenHash)
	return err
}

// RevokeSessionsByUserID revokes all active sessions for a user (e.g. on role change or user deactivation),
// returning the token hashes that were revoked so caches can be cleared.
func (s *PGStore) RevokeSessionsByUserID(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `UPDATE public.user_session SET is_revoked = TRUE WHERE user_id = $1 AND is_revoked = FALSE RETURNING token_hash`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err == nil {
			hashes = append(hashes, h)
		}
	}
	return hashes, rows.Err()
}

// RecordAuditLog writes an immutable record to admin_audit_log.
func (s *PGStore) RecordAuditLog(ctx context.Context, entry AuditLogEntry) error {
	id := entry.ID
	if id == "" {
		id = generateID("aud")
	}
	metaBytes, err := json.Marshal(entry.Metadata)
	if err != nil {
		metaBytes = []byte("{}")
	}
	query := `
		INSERT INTO public.admin_audit_log (id, user_id, actor_role, action, resource, resource_id, ip_address, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now());
	`
	_, err = s.pool.Exec(ctx, query, id, entry.UserID, entry.ActorRole, entry.Action, entry.Resource, entry.ResourceID, entry.IPAddress, metaBytes)
	if err != nil {
		return fmt.Errorf("failed to record audit log: %w", err)
	}
	return nil
}

// QueryAuditLogs retrieves audit logs ordered by creation time descending.
func (s *PGStore) QueryAuditLogs(ctx context.Context, limit int, offset int) ([]AuditLogEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := `
		SELECT id, user_id, actor_role, action, resource, resource_id, ip_address, metadata, created_at
		FROM public.admin_audit_log
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2;
	`
	rows, err := s.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()

	var entries []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		var metaRaw []byte
		if err := rows.Scan(&entry.ID, &entry.UserID, &entry.ActorRole, &entry.Action, &entry.Resource, &entry.ResourceID, &entry.IPAddress, &metaRaw, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &entry.Metadata)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
