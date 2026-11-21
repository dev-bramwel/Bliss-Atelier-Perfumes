package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/rediscache"
)

// Service errors.
var (
	ErrSessionNotFound     = errors.New("session not found or revoked")
	ErrSessionExpired      = errors.New("session has expired")
	ErrAccountDeactivated  = errors.New("account is deactivated")
	ErrInvalidCSRF         = errors.New("invalid or expired csrf token")
	ErrPermissionDenied    = errors.New("permission denied: insufficient authority")
	ErrBootstrapNotAllowed = errors.New("initial owner bootstrap is already completed or target account not found")
)

// Service coordinates authentication, session state, anti-CSRF, and RBAC authorization.
type Service struct {
	store               Store
	verifier            OIDCVerifier
	redisClient         *rediscache.Client
	bootstrapOwnerEmail string
}

// NewService constructs an auth Service.
func NewService(store Store, verifier OIDCVerifier, redisClient *rediscache.Client) *Service {
	return &Service{
		store:       store,
		verifier:    verifier,
		redisClient: redisClient,
	}
}

// SetBootstrapOwnerEmail configures the email that is eligible for one-time initial owner bootstrap.
func (s *Service) SetBootstrapOwnerEmail(email string) {
	s.bootstrapOwnerEmail = strings.TrimSpace(email)
}

// IsSafeReturnURL ensures return URLs are relative paths preventing open redirect vulnerabilities.
func IsSafeReturnURL(target string) bool {
	if target == "" {
		return true
	}
	// Must start with '/' and not '//' (which browsers treat as protocol-relative external URLs)
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return u.Host == "" && u.Scheme == ""
}

// BeginLogin creates a browser-bound login transaction and returns the Google authorization URL.
func (s *Service) BeginLogin(ctx context.Context, returnURL string, ip string, userAgent string) (authURL string, bindingCookie string, err error) {
	if !IsSafeReturnURL(returnURL) {
		returnURL = "/"
	}

	state, err := RandomString(32)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate state: %w", err)
	}
	nonce, err := RandomString(32)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	pkceVerifier, err := GeneratePKCEVerifier()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate pkce verifier: %w", err)
	}
	bindingCookie, err = RandomString(32)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate binding cookie: %w", err)
	}

	stateHash := HashToken(state)
	browserBindingHash := BrowserBindingHash(bindingCookie, userAgent)
	codeChallenge := ComputePKCEChallenge(pkceVerifier)

	ltx := LoginTransaction{
		StateHash:          stateHash,
		Nonce:              nonce,
		PKCEVerifier:       pkceVerifier,
		BrowserBindingHash: browserBindingHash,
		ReturnURL:          returnURL,
		ExpiresAt:          time.Now().Add(LoginTransactionExpiry),
	}

	if err := s.store.CreateLoginTransaction(ctx, ltx); err != nil {
		return "", "", fmt.Errorf("failed to persist login transaction: %w", err)
	}

	authURL = s.verifier.AuthCodeURL(state, nonce, codeChallenge)
	return authURL, bindingCookie, nil
}

// CompleteLogin validates OIDC callback params, exchanges code with PKCE, verifies token,
// resolves/provisions user, and creates an authoritative session. Returns user, session, rawToken, csrfToken, returnURL, error.
func (s *Service) CompleteLogin(ctx context.Context, state string, code string, bindingCookie string, ip string, userAgent string) (*User, *UserSession, string, string, string, error) {
	if state == "" || code == "" || bindingCookie == "" {
		return nil, nil, "", "", "", errors.New("missing oauth state, code, or browser binding")
	}

	stateHash := HashToken(state)
	browserBindingHash := BrowserBindingHash(bindingCookie, userAgent)

	// 1. Consume login transaction (atomic, single-use, browser-bound)
	tx, err := s.store.ConsumeLoginTransaction(ctx, stateHash, browserBindingHash)
	if err != nil {
		return nil, nil, "", "", "", fmt.Errorf("invalid oauth transaction: %w", err)
	}

	// 2. Exchange authorization code with PKCE verifier
	rawIDToken, err := s.verifier.ExchangeCode(ctx, code, tx.PKCEVerifier)
	if err != nil {
		return nil, nil, "", "", "", fmt.Errorf("token exchange failed: %w", err)
	}

	// 3. Cryptographically verify ID token and nonce
	profile, err := s.verifier.VerifyIDToken(ctx, rawIDToken, tx.Nonce)
	if err != nil {
		return nil, nil, "", "", "", fmt.Errorf("id token verification failed: %w", err)
	}

	// 4. Resolve or create user account (google_sub is primary identity key)
	// Security: Only associate and trust email if Google has cryptographically verified it.
	// This prevents unverified email claims from hijacking accounts or satisfying bootstrap requirements.
	userEmail := ""
	if profile.EmailVerified {
		userEmail = strings.TrimSpace(profile.Email)
	}
	user, err := s.store.UpsertUserByGoogleSub(ctx, profile.Subject, userEmail, profile.Name, profile.Picture)
	if err != nil {
		return nil, nil, "", "", "", fmt.Errorf("failed to upsert user account: %w", err)
	}

	// If bootstrap owner email is configured, and user's verified email matches, bootstrap if no owner exists
	if s.bootstrapOwnerEmail != "" && user.Role != RoleOwner && strings.EqualFold(userEmail, s.bootstrapOwnerEmail) {
		if _, bErr := s.BootstrapInitialOwner(ctx, userEmail); bErr == nil {
			if updatedUser, uErr := s.store.GetUserByID(ctx, user.ID); uErr == nil && updatedUser != nil {
				user = updatedUser
			}
		}
	}

	if !user.IsActive {
		return nil, nil, "", "", "", ErrAccountDeactivated
	}

	// 5. Create authoritative server session
	rawToken, err := GenerateSessionToken()
	if err != nil {
		return nil, nil, "", "", "", fmt.Errorf("failed to generate session token: %w", err)
	}
	csrfSecret, err := RandomString(32)
	if err != nil {
		return nil, nil, "", "", "", fmt.Errorf("failed to generate csrf secret: %w", err)
	}

	now := time.Now()
	var inactivityLimit, maxLifetime time.Duration
	if IsStaff(user.Role) {
		inactivityLimit = StaffInactivityLimit
		maxLifetime = StaffMaxLifetime
	} else {
		inactivityLimit = CustomerInactivityLimit
		maxLifetime = CustomerMaxLifetime
	}

	session := UserSession{
		UserID:       user.ID,
		TokenHash:    HashToken(rawToken),
		CSRFSecret:   csrfSecret,
		IPAddress:    ip,
		UserAgent:    userAgent,
		IsRevoked:    false,
		ExpiresAt:    now.Add(inactivityLimit),
		MaxExpiresAt: now.Add(maxLifetime),
	}

	if err := s.store.CreateSession(ctx, session); err != nil {
		return nil, nil, "", "", "", fmt.Errorf("failed to create session: %w", err)
	}

	// Cache session in Redis if available
	s.cacheSession(ctx, session.TokenHash, user, &session)

	// Audit login for staff members
	if IsStaff(user.Role) {
		_ = s.store.RecordAuditLog(ctx, AuditLogEntry{
			UserID:     &user.ID,
			ActorRole:  user.Role,
			Action:     "staff_login",
			Resource:   "auth",
			ResourceID: user.ID,
			IPAddress:  ip,
			Metadata: map[string]any{
				"email": user.Email,
			},
		})
	}

	csrfToken := GenerateCSRFToken(csrfSecret)
	returnURL := tx.ReturnURL
	if !IsSafeReturnURL(returnURL) || returnURL == "" {
		returnURL = "/"
	}
	return user, &session, rawToken, csrfToken, returnURL, nil
}

// ValidateSession verifies token against PostgreSQL (with Redis acceleration) and slides inactivity.
// Fail-Closed: DB failure denies access; stale Redis cache never authorizes if DB validation fails.
func (s *Service) ValidateSession(ctx context.Context, rawToken string, ip string) (*UserSession, *User, error) {
	if rawToken == "" {
		return nil, nil, ErrSessionNotFound
	}
	tokenHash := HashToken(rawToken)

	// Fast path: check Redis cache
	if cachedSes, cachedUser := s.getCachedSession(ctx, tokenHash); cachedSes != nil && cachedUser != nil {
		// Verify cached validity
		now := time.Now()
		if cachedSes.IsRevoked || !cachedUser.IsActive {
			s.evictCachedSession(ctx, tokenHash)
			return nil, nil, ErrSessionNotFound
		}
		if now.After(cachedSes.ExpiresAt) || now.After(cachedSes.MaxExpiresAt) {
			s.evictCachedSession(ctx, tokenHash)
			return nil, nil, ErrSessionExpired
		}

		// Revalidate against authoritative DB to verify session revocation, user status, and authoritative expiry
		authoritativeSes, authoritativeUser, err := s.store.GetSessionByTokenHash(ctx, tokenHash)
		if err != nil || authoritativeSes == nil || authoritativeUser == nil || authoritativeSes.IsRevoked || !authoritativeUser.IsActive || authoritativeUser.Role != cachedUser.Role {
			s.evictCachedSession(ctx, tokenHash)
			return nil, nil, ErrSessionNotFound
		}
		if now.After(authoritativeSes.ExpiresAt) || now.After(authoritativeSes.MaxExpiresAt) {
			s.evictCachedSession(ctx, tokenHash)
			return nil, nil, ErrSessionExpired
		}

		// Redis hit is valid; update sliding expiration in DB and Redis
		s.slideExpiration(ctx, cachedSes, authoritativeUser, tokenHash, now)
		return cachedSes, authoritativeUser, nil
	}

	// Authoritative DB path (Fail-Closed: DB error aborts request)
	ses, user, err := s.store.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, nil, fmt.Errorf("session lookup failed: %w", err)
	}
	if ses == nil || user == nil {
		return nil, nil, ErrSessionNotFound
	}

	if ses.IsRevoked || !user.IsActive {
		return nil, nil, ErrSessionNotFound
	}

	now := time.Now()
	if now.After(ses.ExpiresAt) || now.After(ses.MaxExpiresAt) {
		return nil, nil, ErrSessionExpired
	}

	// Slide inactivity expiry
	s.slideExpiration(ctx, ses, user, tokenHash, now)
	return ses, user, nil
}

func (s *Service) slideExpiration(ctx context.Context, ses *UserSession, user *User, tokenHash string, now time.Time) {
	var inactivityLimit time.Duration
	if IsStaff(user.Role) {
		inactivityLimit = StaffInactivityLimit
	} else {
		inactivityLimit = CustomerInactivityLimit
	}

	newExpiresAt := now.Add(inactivityLimit)
	if newExpiresAt.After(ses.MaxExpiresAt) {
		newExpiresAt = ses.MaxExpiresAt
	}

	ses.ExpiresAt = newExpiresAt
	ses.LastSeenAt = now

	// Authoritative DB touch must succeed before refreshing Redis cache (Fail-Closed)
	if err := s.store.TouchSession(ctx, tokenHash, newExpiresAt); err == nil {
		s.cacheSession(ctx, tokenHash, user, ses)
	}
}

// RevokeSession explicitly logs out the current session.
func (s *Service) RevokeSession(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	tokenHash := HashToken(rawToken)
	s.evictCachedSession(ctx, tokenHash)
	return s.store.RevokeSessionByTokenHash(ctx, tokenHash)
}

// RevokeAllUserSessions invalidates all active sessions for a user across all devices.
func (s *Service) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	revokedHashes, err := s.store.RevokeSessionsByUserID(ctx, userID)
	if err != nil {
		return err
	}
	for _, h := range revokedHashes {
		s.evictCachedSession(ctx, h)
	}
	return nil
}

// UpdateUserRole enforces Downward Isolation & Upward Visibility when modifying user roles.
func (s *Service) UpdateUserRole(ctx context.Context, actor *User, targetUserID string, newRole string, ip string) error {
	if actor == nil || !IsStaff(actor.Role) {
		return ErrPermissionDenied
	}
	if !IsValidRole(newRole) {
		return errors.New("invalid role")
	}

	targetUser, err := s.store.GetUserByID(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to fetch target user: %w", err)
	}
	if targetUser == nil {
		return errors.New("user not found")
	}

	// Downward Isolation: check if actor can manage target's current role AND target's new role
	if !CanManageRole(actor.Role, targetUser.Role) || !CanManageRole(actor.Role, newRole) {
		return ErrPermissionDenied
	}

	// Update role in DB
	if err := s.store.UpdateUserRole(ctx, targetUserID, newRole); err != nil {
		return err
	}

	// Invariant: Privileged role change immediately revokes target's existing sessions
	revokedHashes, err := s.store.RevokeSessionsByUserID(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to revoke target user sessions: %w", err)
	}
	for _, h := range revokedHashes {
		s.evictCachedSession(ctx, h)
	}

	// Append-only audit record
	if err := s.store.RecordAuditLog(ctx, AuditLogEntry{
		UserID:     &actor.ID,
		ActorRole:  actor.Role,
		Action:     "update_user_role",
		Resource:   "app_user",
		ResourceID: targetUserID,
		IPAddress:  ip,
		Metadata: map[string]any{
			"previous_role": targetUser.Role,
			"new_role":      newRole,
			"target_email":  targetUser.Email,
		},
	}); err != nil {
		return fmt.Errorf("failed to record audit log: %w", err)
	}

	return nil
}

// UpdateUserStatus updates an account's active status and revokes sessions on deactivation.
func (s *Service) UpdateUserStatus(ctx context.Context, actor *User, targetUserID string, isActive bool, ip string) error {
	if actor == nil || !IsStaff(actor.Role) {
		return ErrPermissionDenied
	}

	targetUser, err := s.store.GetUserByID(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to fetch target user: %w", err)
	}
	if targetUser == nil {
		return errors.New("user not found")
	}

	// Downward Isolation: manager cannot alter status of owner or manager
	if !CanManageRole(actor.Role, targetUser.Role) {
		return ErrPermissionDenied
	}

	if err := s.store.UpdateUserStatus(ctx, targetUserID, isActive); err != nil {
		return err
	}

	if !isActive {
		revokedHashes, err := s.store.RevokeSessionsByUserID(ctx, targetUserID)
		if err != nil {
			return fmt.Errorf("failed to revoke sessions for deactivated user: %w", err)
		}
		for _, h := range revokedHashes {
			s.evictCachedSession(ctx, h)
		}
	}

	if err := s.store.RecordAuditLog(ctx, AuditLogEntry{
		UserID:     &actor.ID,
		ActorRole:  actor.Role,
		Action:     "update_user_status",
		Resource:   "app_user",
		ResourceID: targetUserID,
		IPAddress:  ip,
		Metadata: map[string]any{
			"is_active":    isActive,
			"target_email": targetUser.Email,
		},
	}); err != nil {
		return fmt.Errorf("failed to record audit log: %w", err)
	}

	return nil
}

// ListUsers retrieves users respecting Upward Visibility and Downward Isolation:
// - Owner: can list all users of all roles.
// - Manager: can only list employee and customer accounts.
// - Employee / Customer: denied access (Downward Isolation).
func (s *Service) ListUsers(ctx context.Context, actor *User, filterRole string) ([]User, error) {
	if actor == nil || (actor.Role != RoleOwner && actor.Role != RoleManager) {
		return nil, ErrPermissionDenied
	}

	if actor.Role == RoleManager {
		// Manager can only see employee and customer tiers
		if filterRole == RoleOwner || filterRole == RoleManager {
			return nil, ErrPermissionDenied
		}
	}

	users, err := s.store.ListUsers(ctx, filterRole)
	if err != nil {
		return nil, err
	}

	if actor.Role == RoleManager {
		var filtered []User
		for _, u := range users {
			if u.Role == RoleEmployee || u.Role == RoleCustomer {
				filtered = append(filtered, u)
			}
		}
		return filtered, nil
	}

	return users, nil
}

// InspectAuditLogs retrieves audit logs respecting Downward Isolation:
// - Owner: full visibility over all system audit logs.
// - Manager: operational visibility only (manager, employee, customer); owner logs hidden.
// - Employee / Customer: denied access (Downward Isolation).
func (s *Service) InspectAuditLogs(ctx context.Context, actor *User, limit int, offset int) ([]AuditLogEntry, error) {
	if actor == nil || (actor.Role != RoleOwner && actor.Role != RoleManager) {
		return nil, ErrPermissionDenied
	}
	if p, ok := s.store.(pagedStore); ok {
		return p.QueryAuditPage(ctx, actor.Role, limit, offset)
	}

	entries, err := s.store.QueryAuditLogs(ctx, limit, offset)
	if err != nil {
		return nil, err
	}

	var visible []AuditLogEntry
	for _, e := range entries {
		if CanInspectAuditLogs(actor.Role, e.ActorRole) {
			visible = append(visible, e)
		}
	}
	return visible, nil
}

// BootstrapInitialOwner implements the restricted one-time operator bootstrap per ADR 006 / ADR 005.
func (s *Service) BootstrapInitialOwner(ctx context.Context, targetEmail string) (bool, error) {
	targetEmail = strings.TrimSpace(targetEmail)
	if targetEmail == "" {
		return false, nil
	}

	// 1. Invariant: Disabled permanently if any owner already exists
	ownerCount, err := s.store.CountOwners(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check existing owners: %w", err)
	}
	if ownerCount > 0 {
		return false, nil
	}

	// 2. Target must already exist from Google sign-in with verified email
	targetUser, err := s.store.GetUserByEmail(ctx, targetEmail)
	if err != nil {
		return false, fmt.Errorf("failed to lookup bootstrap user: %w", err)
	}
	if targetUser == nil || targetUser.Email == "" {
		return false, ErrBootstrapNotAllowed
	}

	// 3. Promote target user to owner
	if err := s.store.UpdateUserRole(ctx, targetUser.ID, RoleOwner); err != nil {
		return false, fmt.Errorf("failed to promote initial owner: %w", err)
	}

	// Invalidate previous sessions; if revocation fails, rollback promotion to avoid elevating unrevoked sessions
	revokedHashes, err := s.store.RevokeSessionsByUserID(ctx, targetUser.ID)
	if err != nil {
		_ = s.store.UpdateUserRole(ctx, targetUser.ID, RoleCustomer)
		return false, fmt.Errorf("failed to revoke previous sessions for bootstrap target: %w", err)
	}
	for _, h := range revokedHashes {
		s.evictCachedSession(ctx, h)
	}

	// Record immutable audit entry
	_ = s.store.RecordAuditLog(ctx, AuditLogEntry{
		UserID:     &targetUser.ID,
		ActorRole:  "system_bootstrap",
		Action:     "initial_owner_bootstrapped",
		Resource:   "app_user",
		ResourceID: targetUser.ID,
		IPAddress:  "127.0.0.1",
		Metadata: map[string]any{
			"email": targetUser.Email,
			"sub":   targetUser.GoogleSub,
		},
	})

	return true, nil
}

// Redis session cache helpers
type cachedSessionData struct {
	Session    UserSession `json:"session"`
	CSRFSecret string      `json:"csrf_secret"`
	TokenHash  string      `json:"token_hash"`
	User       User        `json:"user"`
}

func (s *Service) cacheSession(ctx context.Context, tokenHash string, user *User, ses *UserSession) {
	if s.redisClient == nil || s.redisClient.Underlying() == nil {
		return
	}
	data := cachedSessionData{
		Session:    *ses,
		CSRFSecret: ses.CSRFSecret,
		TokenHash:  ses.TokenHash,
		User:       *user,
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	ttl := time.Until(ses.ExpiresAt)
	if ttl <= 0 {
		return
	}
	_ = s.redisClient.Underlying().Set(ctx, "session:"+tokenHash, bytes, ttl).Err()
}

func (s *Service) getCachedSession(ctx context.Context, tokenHash string) (*UserSession, *User) {
	if s.redisClient == nil || s.redisClient.Underlying() == nil {
		return nil, nil
	}
	val, err := s.redisClient.Underlying().Get(ctx, "session:"+tokenHash).Result()
	if err != nil {
		return nil, nil
	}
	var data cachedSessionData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return nil, nil
	}
	data.Session.CSRFSecret = data.CSRFSecret
	data.Session.TokenHash = data.TokenHash
	return &data.Session, &data.User
}

func (s *Service) evictCachedSession(ctx context.Context, tokenHash string) {
	if s.redisClient == nil || s.redisClient.Underlying() == nil {
		return
	}
	_ = s.redisClient.Underlying().Del(ctx, "session:"+tokenHash).Err()
}
