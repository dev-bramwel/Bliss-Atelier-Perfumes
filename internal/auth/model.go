package auth

import (
	"time"
)

// Supported Bliss application roles.
const (
	RoleOwner    = "owner"
	RoleEmployee = "employee"
	RoleCustomer = "customer"
)

// Session lifetime limits per ADR 006 / ADR 005.
const (
	StaffInactivityLimit    = 30 * time.Minute
	StaffMaxLifetime        = 12 * time.Hour
	CustomerInactivityLimit = 7 * 24 * time.Hour
	CustomerMaxLifetime     = 30 * 24 * time.Hour
	LoginTransactionExpiry  = 10 * time.Minute
)

// User represents an authenticated application user identified primarily by Google subject ID (`google_sub`).
type User struct {
	ID         string    `json:"id"`
	GoogleSub  string    `json:"google_sub"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	PictureURL string    `json:"picture_url"`
	Role       string    `json:"role"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UserSession represents an authoritative server-side session.
type UserSession struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	TokenHash    string    `json:"-"`
	CSRFSecret   string    `json:"-"`
	IPAddress    string    `json:"ip_address"`
	UserAgent    string    `json:"user_agent"`
	IsRevoked    bool      `json:"is_revoked"`
	ExpiresAt    time.Time `json:"expires_at"`
	MaxExpiresAt time.Time `json:"max_expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// LoginTransaction tracks short-lived, browser-bound PKCE and state parameters during OIDC flow.
type LoginTransaction struct {
	ID                 string     `json:"id"`
	StateHash          string     `json:"state_hash"`
	Nonce              string     `json:"nonce"`
	PKCEVerifier       string     `json:"pkce_verifier"`
	BrowserBindingHash string     `json:"browser_binding_hash"`
	ReturnURL          string     `json:"return_url"`
	ExpiresAt          time.Time  `json:"expires_at"`
	UsedAt             *time.Time `json:"used_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// AuditLogEntry represents an immutable record in the administrative audit log.
type AuditLogEntry struct {
	ID         string         `json:"id"`
	UserID     *string        `json:"user_id,omitempty"`
	ActorRole  string         `json:"actor_role"`
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	ResourceID string         `json:"resource_id"`
	IPAddress  string         `json:"ip_address"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
}

// GoogleProfile holds verified claims extracted from a valid Google ID token.
type GoogleProfile struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}
