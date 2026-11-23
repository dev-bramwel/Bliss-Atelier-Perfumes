package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoleHierarchyAndIsolation(t *testing.T) {
	// 1. Role Rank checks
	if RoleRank(RoleOwner) <= RoleRank(RoleEmployee) {
		t.Fatal("owner should outrank employee")
	}
	if RoleRank(RoleEmployee) <= RoleRank(RoleCustomer) {
		t.Fatal("employee should outrank customer")
	}

	// 2. Upward Visibility & Downward Isolation in Role Management
	// Only Owner (Apex Admin) can manage roles (create, promote, demote)
	if !CanManageRole(RoleOwner, RoleEmployee) || !CanManageRole(RoleOwner, RoleCustomer) || !CanManageRole(RoleOwner, RoleOwner) {
		t.Fatal("owner should be able to manage all roles")
	}
	// Subordinate roles (Employee, Customer) must have zero role management capability
	if CanManageRole(RoleEmployee, RoleEmployee) || CanManageRole(RoleEmployee, RoleCustomer) || CanManageRole(RoleEmployee, RoleOwner) {
		t.Fatal("employee must not have role management authority; only owner can manage roles")
	}
	if CanManageRole(RoleCustomer, RoleCustomer) || CanManageRole(RoleCustomer, RoleEmployee) || CanManageRole(RoleCustomer, RoleOwner) {
		t.Fatal("customer must not have role management authority")
	}

	// User status management isolation: Owner manages all; Employee manages Customer only; Customer has zero status privileges
	if !CanManageUserStatus(RoleOwner, RoleEmployee) || !CanManageUserStatus(RoleOwner, RoleCustomer) {
		t.Fatal("owner should be able to manage user statuses")
	}
	if !CanManageUserStatus(RoleEmployee, RoleCustomer) {
		t.Fatal("employee should be able to manage customer user status")
	}
	if CanManageUserStatus(RoleEmployee, RoleEmployee) || CanManageUserStatus(RoleEmployee, RoleOwner) {
		t.Fatal("employee must not manage employee or owner user status")
	}
	if CanManageUserStatus(RoleCustomer, RoleCustomer) || CanManageUserStatus(RoleCustomer, RoleOwner) || CanManageUserStatus(RoleCustomer, RoleEmployee) {
		t.Fatal("customer must not manage user status")
	}

	// 3. Audit Log Visibility
	// Owner can inspect all
	if !CanInspectAuditLogs(RoleOwner, RoleOwner) || !CanInspectAuditLogs(RoleOwner, RoleEmployee) || !CanInspectAuditLogs(RoleOwner, RoleCustomer) {
		t.Fatal("owner must have upward visibility over all audit logs")
	}
	// Employee and customer cannot inspect audit logs
	if CanInspectAuditLogs(RoleEmployee, RoleEmployee) || CanInspectAuditLogs(RoleEmployee, RoleCustomer) || CanInspectAuditLogs(RoleEmployee, RoleOwner) {
		t.Fatal("employee must not inspect audit logs")
	}
	if CanInspectAuditLogs(RoleCustomer, RoleCustomer) || CanInspectAuditLogs(RoleCustomer, RoleOwner) {
		t.Fatal("customer must not inspect audit logs")
	}

	// 4. Critical Business Invariants
	if !CanMutateCatalogPricing(RoleOwner) || CanMutateCatalogPricing(RoleEmployee) || CanMutateCatalogPricing(RoleCustomer) {
		t.Fatal("only owner can alter base catalog pricing")
	}
	if !CanApproveOpeningStock(RoleOwner) || CanApproveOpeningStock(RoleEmployee) || CanApproveOpeningStock(RoleCustomer) {
		t.Fatal("only owner can approve inventory opening stock counts")
	}
	if !CanReviewPaymentExceptions(RoleOwner) || CanReviewPaymentExceptions(RoleEmployee) || CanReviewPaymentExceptions(RoleCustomer) {
		t.Fatal("only owner can review payment exceptions")
	}
}

func TestCryptoUtilities(t *testing.T) {
	// Session token entropy
	tok, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok) < 64 {
		t.Fatalf("expected at least 64 chars session token, got %d", len(tok))
	}

	// PKCE RFC 7636
	verifier, err := GeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("invalid pkce verifier length: %d", len(verifier))
	}
	challenge := ComputePKCEChallenge(verifier)
	if challenge == "" || challenge == verifier {
		t.Fatal("invalid computed pkce challenge")
	}

	// Token hashing
	h1 := HashToken(tok)
	h2 := HashToken(tok)
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("hash mismatch or invalid length: %s", h1)
	}

	// CSRF token generation & validation
	csrfSecret, _ := RandomString(32)
	csrfToken := GenerateCSRFToken(csrfSecret)
	if !ValidateCSRFToken(csrfSecret, csrfToken, 1*time.Hour) {
		t.Fatal("csrf token should be valid")
	}
	if ValidateCSRFToken("wrong-secret", csrfToken, 1*time.Hour) {
		t.Fatal("csrf validation with wrong secret should fail")
	}
	if ValidateCSRFToken(csrfSecret, "tampered.token", 1*time.Hour) {
		t.Fatal("tampered csrf token should fail")
	}
}

func TestReturnURLValidation(t *testing.T) {
	safeURLs := []string{"/", "/admin", "/orders/123", "/checkout/delivery", ""}
	for _, u := range safeURLs {
		if !IsSafeReturnURL(u) {
			t.Errorf("expected URL to be safe: %s", u)
		}
	}

	unsafeURLs := []string{
		"//evil.com",
		"//evil.com/path",
		"https://evil.com",
		"http://attacker.com/steal",
		"javascript:alert(1)",
		"data:text/html,bad",
	}
	for _, u := range unsafeURLs {
		if IsSafeReturnURL(u) {
			t.Errorf("expected URL to be rejected as unsafe: %s", u)
		}
	}
}

// In-memory test store implementing Store interface
type memStore struct {
	mu          sync.Mutex
	users       map[string]*User
	usersBySub  map[string]*User
	usersByMail map[string]*User
	txs         map[string]*LoginTransaction
	sessions    map[string]*UserSession
	auditLogs   []AuditLogEntry
}

func newMemStore() *memStore {
	return &memStore{
		users:       make(map[string]*User),
		usersBySub:  make(map[string]*User),
		usersByMail: make(map[string]*User),
		txs:         make(map[string]*LoginTransaction),
		sessions:    make(map[string]*UserSession),
	}
}

func (m *memStore) UpsertUserByGoogleSub(ctx context.Context, googleSub, email, name, pictureURL string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.usersBySub[googleSub]; ok {
		u.Email = email
		u.Name = name
		u.PictureURL = pictureURL
		u.UpdatedAt = time.Now()
		m.usersByMail[strings.ToLower(email)] = u
		return u, nil
	}
	id := generateID("usr")
	u := &User{
		ID:         id,
		GoogleSub:  googleSub,
		Email:      email,
		Name:       name,
		PictureURL: pictureURL,
		Role:       RoleCustomer, // Default public registration is strictly customer!
		IsActive:   true,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	m.users[id] = u
	m.usersBySub[googleSub] = u
	m.usersByMail[strings.ToLower(email)] = u
	return u, nil
}

func (m *memStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.users[id], nil
}

func (m *memStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usersByMail[strings.ToLower(email)], nil
}

func (m *memStore) CountOwners(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, u := range m.users {
		if u.Role == RoleOwner {
			count++
		}
	}
	return count, nil
}

func (m *memStore) CountActiveOwners(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, u := range m.users {
		if u.Role == RoleOwner && u.IsActive {
			count++
		}
	}
	return count, nil
}

func (m *memStore) DeactivateUserAndRevokeSessions(ctx context.Context, userID string, actorRole string, isSelf bool, auditEntry *AuditLogEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}

	if !isSelf && actorRole != "" {
		if !CanManageUserStatus(actorRole, u.Role) {
			return nil, ErrPermissionDenied
		}
	}

	if u.Role == RoleOwner && u.IsActive {
		activeOwnerCount := 0
		for _, o := range m.users {
			if o.Role == RoleOwner && o.IsActive {
				activeOwnerCount++
			}
		}
		if activeOwnerCount <= 1 {
			return nil, ErrCannotDeactivateSoleOwner
		}
	}

	u.IsActive = false
	m.users[userID] = u

	var revoked []string
	for tokenHash, s := range m.sessions {
		if s.UserID == userID && !s.IsRevoked {
			s.IsRevoked = true
			m.sessions[tokenHash] = s
			revoked = append(revoked, tokenHash)
		}
	}

	if auditEntry != nil {
		m.auditLogs = append([]AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return revoked, nil
}

func (m *memStore) ReactivateUser(ctx context.Context, userID string, actorRole string, auditEntry *AuditLogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return errors.New("user not found")
	}

	if actorRole != "" {
		if !CanManageUserStatus(actorRole, u.Role) {
			return ErrPermissionDenied
		}
	}

	u.IsActive = true
	m.users[userID] = u

	if auditEntry != nil {
		m.auditLogs = append([]AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return nil
}

func (m *memStore) UpdateUserRole(ctx context.Context, id string, newRole string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Role = newRole
	m.users[id] = u
	return nil
}

func (m *memStore) UpdateUserRoleAndRevokeSessions(ctx context.Context, userID string, newRole string, auditEntry *AuditLogEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}

	if u.Role == RoleOwner && u.IsActive && newRole != RoleOwner {
		activeOwnerCount := 0
		for _, o := range m.users {
			if o.Role == RoleOwner && o.IsActive {
				activeOwnerCount++
			}
		}
		if activeOwnerCount <= 1 {
			return nil, ErrCannotDemoteSoleOwner
		}
	}

	u.Role = newRole
	m.users[userID] = u

	var revoked []string
	for tokenHash, s := range m.sessions {
		if s.UserID == userID && !s.IsRevoked {
			s.IsRevoked = true
			m.sessions[tokenHash] = s
			revoked = append(revoked, tokenHash)
		}
	}

	if auditEntry != nil {
		m.auditLogs = append([]AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return revoked, nil
}

func (m *memStore) UpdateUserPermissions(ctx context.Context, actorID string, userID string, canManageInventory bool, auditEntry *AuditLogEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if actorID != "" {
		actor, ok := m.users[actorID]
		if !ok || !actor.IsActive || actor.Role != RoleOwner {
			return nil, ErrPermissionDenied
		}
	}

	u, ok := m.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}

	if u.Role != RoleEmployee {
		return nil, fmt.Errorf("%w: permissions can only be modified for employees", ErrInvalidRole)
	}

	u.CanManageInventory = canManageInventory
	m.users[userID] = u

	var sessionHashes []string
	now := time.Now()
	for tokenHash, s := range m.sessions {
		if s.UserID == userID && !s.IsRevoked && s.ExpiresAt.After(now) {
			sessionHashes = append(sessionHashes, tokenHash)
		}
	}

	if auditEntry != nil {
		m.auditLogs = append([]AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return sessionHashes, nil
}

func (m *memStore) UpdateUserStatus(ctx context.Context, id string, isActive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.IsActive = isActive
	m.users[id] = u
	return nil
}

func (m *memStore) ListUsers(ctx context.Context, filterRole string) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []User
	for _, u := range m.users {
		if filterRole == "" || u.Role == filterRole {
			res = append(res, *u)
		}
	}
	return res, nil
}

func (m *memStore) CreateLoginTransaction(ctx context.Context, tx LoginTransaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs[tx.StateHash] = &tx
	return nil
}

func (m *memStore) ConsumeLoginTransaction(ctx context.Context, stateHash string, browserBindingHash string) (*LoginTransaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, ok := m.txs[stateHash]
	if !ok || tx.UsedAt != nil || tx.BrowserBindingHash != browserBindingHash || time.Now().After(tx.ExpiresAt) {
		return nil, errors.New("invalid or expired login transaction")
	}
	now := time.Now()
	tx.UsedAt = &now
	return tx, nil
}

func (m *memStore) CreateSession(ctx context.Context, session UserSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[session.UserID]
	if !ok {
		return errors.New("user not found")
	}
	if !u.IsActive {
		return ErrAccountDeactivated
	}
	m.sessions[session.TokenHash] = &session
	return nil
}

func (m *memStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*UserSession, *User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ses, ok := m.sessions[tokenHash]
	if !ok || ses.IsRevoked {
		return nil, nil, nil
	}
	u, ok := m.users[ses.UserID]
	if !ok {
		return nil, nil, nil
	}
	return ses, u, nil
}

func (m *memStore) TouchSession(ctx context.Context, tokenHash string, newExpiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ses, ok := m.sessions[tokenHash]; ok {
		ses.ExpiresAt = newExpiresAt
		ses.LastSeenAt = time.Now()
	}
	return nil
}

func (m *memStore) RevokeSessionByTokenHash(ctx context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ses, ok := m.sessions[tokenHash]; ok {
		ses.IsRevoked = true
	}
	return nil
}

func (m *memStore) RevokeSessionsByUserID(ctx context.Context, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var revoked []string
	for _, ses := range m.sessions {
		if ses.UserID == userID && !ses.IsRevoked {
			ses.IsRevoked = true
			revoked = append(revoked, ses.TokenHash)
		}
	}
	return revoked, nil
}

func (m *memStore) RecordAuditLog(ctx context.Context, entry AuditLogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditLogs = append(m.auditLogs, entry)
	return nil
}

func (m *memStore) QueryAuditLogs(ctx context.Context, limit int, offset int) ([]AuditLogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auditLogs, nil
}

func TestAuthServiceFullFlow(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	verifier := NewMockOIDCVerifier()
	service := NewService(store, verifier, nil)

	// 1. Begin Login
	authURL, bindingCookie, err := service.BeginLogin(ctx, "/admin", "127.0.0.1", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("BeginLogin failed: %v", err)
	}
	if !strings.Contains(authURL, "state=") || !strings.Contains(authURL, "nonce=") {
		t.Fatalf("invalid auth URL: %s", authURL)
	}

	// Extract state from authURL
	parts := strings.Split(authURL, "&")
	var state, nonce string
	for _, p := range parts {
		if strings.HasPrefix(p, "state=") {
			state = strings.TrimPrefix(p, "state=")
		}
		if strings.HasPrefix(p, "nonce=") {
			nonce = strings.TrimPrefix(p, "nonce=")
		}
	}

	// Register mock token in verifier
	testCode := "valid-auth-code"
	testRawIDToken := "jwt-id-token"
	googleProfile := &GoogleProfile{
		Subject:       "google-sub-12345",
		Email:         "owner.candidate@blissatelier.com",
		EmailVerified: true,
		Name:          "Owner Candidate",
	}
	verifier.RegisterToken(testCode, testRawIDToken, googleProfile, nonce)

	// 2. Complete Login
	user, ses, rawToken, csrfToken, returnURL, err := service.CompleteLogin(ctx, state, testCode, bindingCookie, "127.0.0.1", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("CompleteLogin failed: %v", err)
	}
	if returnURL != "/admin" {
		t.Fatalf("expected returnURL to be /admin, got %s", returnURL)
	}

	if user.GoogleSub != "google-sub-12345" || user.Role != RoleCustomer {
		t.Fatalf("expected initial user role to be customer, got %s", user.Role)
	}
	if ses == nil || rawToken == "" || csrfToken == "" {
		t.Fatal("expected valid session, token, and csrf token")
	}

	// 3. Single-use replay protection
	_, _, _, _, _, err = service.CompleteLogin(ctx, state, testCode, bindingCookie, "127.0.0.1", "Mozilla/5.0")
	if err == nil {
		t.Fatal("replaying login transaction must fail")
	}

	// 4. Validate Session
	validSes, validUser, err := service.ValidateSession(ctx, rawToken, "127.0.0.1")
	if err != nil {
		t.Fatalf("ValidateSession failed: %v", err)
	}
	if validUser.ID != user.ID || validSes.ID != ses.ID {
		t.Fatal("session validation returned incorrect user or session")
	}

	// 4b. Security: Unverified email claim is omitted and cannot satisfy bootstrap
	authURL2, bind2, _ := service.BeginLogin(ctx, "/", "127.0.0.1", "test")
	parts2 := strings.Split(authURL2, "&")
	var state2, nonce2 string
	for _, p := range parts2 {
		if strings.HasPrefix(p, "state=") {
			state2 = strings.TrimPrefix(p, "state=")
		}
		if strings.HasPrefix(p, "nonce=") {
			nonce2 = strings.TrimPrefix(p, "nonce=")
		}
	}
	unverifiedCode := "unverified-code"
	verifier.RegisterToken(unverifiedCode, "id-tok-unverified", &GoogleProfile{
		Subject:       "sub-unverified",
		Email:         "unverified@blissatelier.com",
		EmailVerified: false,
		Name:          "Unverified User",
	}, nonce2)
	uUser, _, _, _, _, err := service.CompleteLogin(ctx, state2, unverifiedCode, bind2, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("CompleteLogin failed: %v", err)
	}
	if uUser.Email != "" {
		t.Fatalf("expected unverified email to be omitted, got %q", uUser.Email)
	}
	unverifiedBootstrap, err := service.BootstrapInitialOwner(ctx, "unverified@blissatelier.com")
	if !errors.Is(err, ErrBootstrapNotAllowed) || unverifiedBootstrap {
		t.Fatalf("expected ErrBootstrapNotAllowed for unverified email, got %v", err)
	}

	// 5. Bootstrap Initial Owner
	// Bootstrap succeeds for pre-existing account
	bootstrapped, err := service.BootstrapInitialOwner(ctx, "owner.candidate@blissatelier.com")
	if err != nil || !bootstrapped {
		t.Fatalf("BootstrapInitialOwner failed: %v", err)
	}

	// Verify user is now owner
	updatedUser, _ := store.GetUserByID(ctx, user.ID)
	if updatedUser.Role != RoleOwner {
		t.Fatalf("expected role owner, got %s", updatedUser.Role)
	}

	// Invariant: Session was revoked upon role change!
	_, _, err = service.ValidateSession(ctx, rawToken, "127.0.0.1")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected old session to be revoked on role change, got %v", err)
	}

	// Invariant: Second bootstrap must be permanently disabled
	bootstrappedAgain, err := service.BootstrapInitialOwner(ctx, "someone.else@blissatelier.com")
	if err != nil || bootstrappedAgain {
		t.Fatal("subsequent bootstrap attempts must be permanently disabled once an owner exists")
	}

	// Invariant: Even if owner account is deactivated, bootstrap remains permanently disabled
	_ = store.UpdateUserStatus(ctx, updatedUser.ID, false)
	bootstrappedAfterDeactivate, err := service.BootstrapInitialOwner(ctx, "someone.else@blissatelier.com")
	if err != nil || bootstrappedAfterDeactivate {
		t.Fatal("bootstrap must remain permanently disabled even if owner is deactivated")
	}
	_ = store.UpdateUserStatus(ctx, updatedUser.ID, true)

	// 6. Test User Role Updates & Downward Isolation
	// Create an employee candidate
	empProfile := &GoogleProfile{
		Subject: "google-sub-emp",
		Email:   "employee@blissatelier.com",
		Name:    "Counter Employee",
	}
	empUser, _ := store.UpsertUserByGoogleSub(ctx, empProfile.Subject, empProfile.Email, empProfile.Name, "")

	// Owner promotes employee candidate to RoleEmployee
	err = service.UpdateUserRole(ctx, updatedUser, empUser.ID, RoleEmployee, "127.0.0.1")
	if err != nil {
		t.Fatalf("owner failed to promote employee: %v", err)
	}

	empUpdated, _ := store.GetUserByID(ctx, empUser.ID)
	if empUpdated.Role != RoleEmployee {
		t.Fatalf("expected role employee, got %s", empUpdated.Role)
	}

	// Downward Isolation: Employee cannot promote anyone to owner
	err = service.UpdateUserRole(ctx, empUpdated, empUser.ID, RoleOwner, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when employee attempts to promote to owner, got %v", err)
	}

	// Downward Isolation: Employee cannot demote anyone (only owner has role management)
	err = service.UpdateUserRole(ctx, empUpdated, empUser.ID, RoleCustomer, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when employee attempts role change, got %v", err)
	}

	// Downward Isolation: Employee cannot modify owner's role
	err = service.UpdateUserRole(ctx, empUpdated, updatedUser.ID, RoleCustomer, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when employee attempts to modify owner, got %v", err)
	}

	// Downward Isolation: Employee cannot inspect audit logs
	_, err = service.InspectAuditLogs(ctx, empUpdated, 10, 0)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when employee attempts to inspect audit logs, got %v", err)
	}

	// Downward Isolation: Employee cannot list users
	_, err = service.ListUsers(ctx, empUpdated, "")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when employee attempts to list users, got %v", err)
	}

	// Owner can inspect audit logs
	ownerLogs, err := service.InspectAuditLogs(ctx, updatedUser, 10, 0)
	if err != nil {
		t.Fatalf("owner failed to inspect audit logs: %v", err)
	}
	if len(ownerLogs) == 0 {
		t.Fatal("expected audit logs for owner inspection")
	}

	// Owner listing sees all tiers
	ownerVisibleUsers, err := service.ListUsers(ctx, updatedUser, "")
	if err != nil {
		t.Fatalf("owner failed to list users: %v", err)
	}
	if len(ownerVisibleUsers) < 2 {
		t.Fatalf("owner should see at least 2 users, got %d", len(ownerVisibleUsers))
	}

	// Exclusive Owner Authority: Owner CAN demote employee to customer
	err = service.UpdateUserRole(ctx, updatedUser, empUser.ID, RoleCustomer, "127.0.0.1")
	if err != nil {
		t.Fatalf("owner failed to demote employee to customer: %v", err)
	}
	empDemoted, _ := store.GetUserByID(ctx, empUser.ID)
	if empDemoted.Role != RoleCustomer {
		t.Fatalf("expected role customer after demotion, got %s", empDemoted.Role)
	}
}

func TestDeactivateSelfAndStaffReactivation(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	verifier := NewMockOIDCVerifier()
	service := NewService(store, verifier, nil)

	// Create owner, employee, and customer
	owner, _ := store.UpsertUserByGoogleSub(ctx, "sub-owner", "owner@blissatelier.com", "Owner User", "")
	_ = store.UpdateUserRole(ctx, owner.ID, RoleOwner)
	owner, _ = store.GetUserByID(ctx, owner.ID)

	emp, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp", "emp@blissatelier.com", "Employee User", "")
	_ = store.UpdateUserRole(ctx, emp.ID, RoleEmployee)
	emp, _ = store.GetUserByID(ctx, emp.ID)

	cust, _ := store.UpsertUserByGoogleSub(ctx, "sub-cust", "cust@example.com", "Customer User", "")

	// 1. Sole owner cannot deactivate self
	err := service.DeactivateSelf(ctx, owner, "127.0.0.1")
	if !errors.Is(err, ErrCannotDeactivateSoleOwner) {
		t.Fatalf("expected ErrCannotDeactivateSoleOwner for sole owner, got %v", err)
	}

	// 1b. Having an inactive second owner must still reject deactivation of the sole active owner
	inactiveOwner, _ := store.UpsertUserByGoogleSub(ctx, "sub-owner2", "owner2@blissatelier.com", "Inactive Owner", "")
	_ = store.UpdateUserRole(ctx, inactiveOwner.ID, RoleOwner)
	_ = store.UpdateUserStatus(ctx, inactiveOwner.ID, false)
	err = service.DeactivateSelf(ctx, owner, "127.0.0.1")
	if !errors.Is(err, ErrCannotDeactivateSoleOwner) {
		t.Fatalf("expected ErrCannotDeactivateSoleOwner when other owners are inactive, got %v", err)
	}

	// 1c. With 2 active owners, first can deactivate, but second is immediately blocked from leaving 0 active owners
	_ = store.UpdateUserStatus(ctx, inactiveOwner.ID, true)
	activeOwner2, _ := store.GetUserByID(ctx, inactiveOwner.ID)
	err = service.DeactivateSelf(ctx, activeOwner2, "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to deactivate one of two active owners: %v", err)
	}
	// Now only owner 1 remains active; owner 1 cannot deactivate
	err = service.DeactivateSelf(ctx, owner, "127.0.0.1")
	if !errors.Is(err, ErrCannotDeactivateSoleOwner) {
		t.Fatalf("expected ErrCannotDeactivateSoleOwner for remaining active owner, got %v", err)
	}

	// 1d. Sole active owner cannot be demoted to employee
	err = service.UpdateUserRole(ctx, owner, owner.ID, RoleEmployee, "127.0.0.1")
	if !errors.Is(err, ErrCannotDemoteSoleOwner) {
		t.Fatalf("expected ErrCannotDemoteSoleOwner when demoting sole active owner, got %v", err)
	}

	// Reactivate 2nd owner; now demoting one owner succeeds, but demoting the remaining one fails
	_ = store.UpdateUserStatus(ctx, inactiveOwner.ID, true)
	err = service.UpdateUserRole(ctx, owner, inactiveOwner.ID, RoleEmployee, "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to demote owner when another active owner exists: %v", err)
	}
	err = service.UpdateUserRole(ctx, owner, owner.ID, RoleEmployee, "127.0.0.1")
	if !errors.Is(err, ErrCannotDemoteSoleOwner) {
		t.Fatalf("expected ErrCannotDemoteSoleOwner for remaining owner after other was demoted, got %v", err)
	}

	// 2. Customer can self-deactivate
	err = service.DeactivateSelf(ctx, cust, "127.0.0.1")
	if err != nil {
		t.Fatalf("customer self-deactivation failed: %v", err)
	}

	// Verify customer status is inactive
	updatedCust, _ := store.GetUserByID(ctx, cust.ID)
	if updatedCust.IsActive {
		t.Fatal("expected customer to be deactivated")
	}

	// Verify audit log has user.self_deactivate
	logs, _ := store.QueryAuditLogs(ctx, 10, 0)
	foundAudit := false
	for _, l := range logs {
		if l.Action == "user.self_deactivate" && l.ResourceID == cust.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatal("expected audit log entry with action user.self_deactivate")
	}

	// 3. Employee can reactivate customer
	err = service.UpdateUserStatus(ctx, emp, cust.ID, true, "127.0.0.1")
	if err != nil {
		t.Fatalf("employee should be able to reactivate customer, got %v", err)
	}
	reactivatedCust, _ := store.GetUserByID(ctx, cust.ID)
	if !reactivatedCust.IsActive {
		t.Fatal("expected customer to be reactivated by employee")
	}

	// 4. Employee CANNOT deactivate/reactivate another employee
	otherEmp, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp2", "emp2@blissatelier.com", "Employee 2", "")
	_ = store.UpdateUserRole(ctx, otherEmp.ID, RoleEmployee)
	err = service.UpdateUserStatus(ctx, emp, otherEmp.ID, false, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when employee attempts to deactivate another employee, got %v", err)
	}

	// 5. Employee CANNOT deactivate owner
	err = service.UpdateUserStatus(ctx, emp, owner.ID, false, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when employee attempts to deactivate owner, got %v", err)
	}

	// 6. Inactive owner can be demoted or deactivated while a different sole active owner exists
	inactiveOwnerRecord, _ := store.UpsertUserByGoogleSub(ctx, "sub-inactive-owner", "inactive.owner@blissatelier.com", "Inactive Owner", "")
	_ = store.UpdateUserRole(ctx, inactiveOwnerRecord.ID, RoleOwner)
	_ = store.UpdateUserStatus(ctx, inactiveOwnerRecord.ID, false)

	// Sole active owner exists (owner), yet demoting inactiveOwnerRecord does not reduce active owner count
	err = service.UpdateUserRole(ctx, owner, inactiveOwnerRecord.ID, RoleEmployee, "127.0.0.1")
	if err != nil {
		t.Fatalf("expected demoting inactive owner to succeed when sole active owner exists, got %v", err)
	}

	// Re-assign role to owner (while inactive) and verify deactivating an inactive owner also succeeds
	_ = store.UpdateUserRole(ctx, inactiveOwnerRecord.ID, RoleOwner)
	err = service.UpdateUserStatus(ctx, owner, inactiveOwnerRecord.ID, false, "127.0.0.1")
	if err != nil {
		t.Fatalf("expected deactivating inactive owner to succeed when sole active owner exists, got %v", err)
	}

	// 7. Session creation rejects deactivated user
	err = store.CreateSession(ctx, UserSession{
		ID:        "ses_test_inactive",
		UserID:    inactiveOwnerRecord.ID,
		TokenHash: "hash_test_inactive",
	})
	if !errors.Is(err, ErrAccountDeactivated) {
		t.Fatalf("expected ErrAccountDeactivated when creating session for deactivated user, got %v", err)
	}
}

func TestCanManageInventory(t *testing.T) {
	// 1. Owner always can manage inventory
	owner := User{ID: "usr-owner", Role: RoleOwner, CanManageInventory: false}
	if !CanManageInventory(owner) {
		t.Fatal("expected owner to be permitted to manage inventory")
	}

	// 2. Employee with permission can manage inventory
	empAllowed := User{ID: "usr-emp1", Role: RoleEmployee, CanManageInventory: true}
	if !CanManageInventory(empAllowed) {
		t.Fatal("expected employee with CanManageInventory=true to be permitted")
	}

	// 3. Employee without permission cannot manage inventory
	empDenied := User{ID: "usr-emp2", Role: RoleEmployee, CanManageInventory: false}
	if CanManageInventory(empDenied) {
		t.Fatal("expected employee with CanManageInventory=false to be denied")
	}

	// 4. Customer cannot manage inventory
	customer := User{ID: "usr-cust", Role: RoleCustomer, CanManageInventory: true}
	if CanManageInventory(customer) {
		t.Fatal("expected customer to be denied even if CanManageInventory is set")
	}

	// 5. Empty role cannot manage inventory
	anon := User{ID: "usr-anon", Role: "", CanManageInventory: true}
	if CanManageInventory(anon) {
		t.Fatal("expected anonymous/empty role to be denied")
	}
}

func TestUpdateUserPermissions(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	service := NewService(store, nil, nil)

	owner, _ := store.UpsertUserByGoogleSub(ctx, "sub-owner", "owner@blissatelier.com", "Owner User", "")
	_ = store.UpdateUserRole(ctx, owner.ID, RoleOwner)
	owner, _ = store.GetUserByID(ctx, owner.ID)

	emp, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp", "employee@blissatelier.com", "Staff Emp", "")
	_ = store.UpdateUserRole(ctx, emp.ID, RoleEmployee)
	emp, _ = store.GetUserByID(ctx, emp.ID)

	cust, _ := store.UpsertUserByGoogleSub(ctx, "sub-cust", "customer@example.com", "Customer One", "")

	// 1. Owner grants permission to employee
	err := service.UpdateUserPermissions(ctx, owner, emp.ID, true, "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error updating user permissions: %v", err)
	}
	updatedEmp, _ := store.GetUserByID(ctx, emp.ID)
	if !updatedEmp.CanManageInventory {
		t.Fatal("expected employee CanManageInventory to be true")
	}

	// Verify audit log entry
	logs, err := store.QueryAuditLogs(ctx, 10, 0)
	if err != nil || len(logs) == 0 {
		t.Fatalf("expected audit log, got err: %v, len: %d", err, len(logs))
	}
	if logs[0].Action != "update_user_permissions" || logs[0].ResourceID != emp.ID {
		t.Fatalf("unexpected audit log entry: %+v", logs[0])
	}
	if v, ok := logs[0].Metadata["can_manage_inventory"].(bool); !ok || !v {
		t.Fatalf("unexpected metadata in audit log: %+v", logs[0].Metadata)
	}

	// 2. Owner revokes permission from employee
	err = service.UpdateUserPermissions(ctx, owner, emp.ID, false, "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error revoking user permissions: %v", err)
	}
	revokedEmp, _ := store.GetUserByID(ctx, emp.ID)
	if revokedEmp.CanManageInventory {
		t.Fatal("expected employee CanManageInventory to be false")
	}

	// 3. Employee CANNOT update permissions (ErrPermissionDenied)
	err = service.UpdateUserPermissions(ctx, emp, emp.ID, true, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when employee updates permissions, got %v", err)
	}

	// 4. Customer CANNOT update permissions (ErrPermissionDenied)
	err = service.UpdateUserPermissions(ctx, cust, emp.ID, true, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when customer updates permissions, got %v", err)
	}

	// 5. Cannot set permissions on customer (target must be employee)
	err = service.UpdateUserPermissions(ctx, owner, cust.ID, true, "127.0.0.1")
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole when target is customer, got %v", err)
	}

	// 6. Cannot set permissions on owner (target must be employee)
	err = service.UpdateUserPermissions(ctx, owner, owner.ID, true, "127.0.0.1")
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole when target is owner, got %v", err)
	}

	// 7. Non-existent target user returns error
	err = service.UpdateUserPermissions(ctx, owner, "non-existent-id", true, "127.0.0.1")
	if err == nil {
		t.Fatal("expected error when target user does not exist, got nil")
	}

	// 8. Demoted owner cannot update permissions in store
	staleOwnerSnapshot := *owner
	_ = store.UpdateUserRole(ctx, owner.ID, RoleCustomer)
	err = service.UpdateUserPermissions(ctx, &staleOwnerSnapshot, emp.ID, true, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when actor role is demoted in store, got %v", err)
	}

	// 9. Deactivated owner cannot update permissions in store
	_ = store.UpdateUserRole(ctx, owner.ID, RoleOwner)
	_ = store.UpdateUserStatus(ctx, owner.ID, false)
	err = service.UpdateUserPermissions(ctx, &staleOwnerSnapshot, emp.ID, true, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when actor is deactivated in store, got %v", err)
	}
}
