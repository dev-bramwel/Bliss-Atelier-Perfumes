package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoleHierarchyAndIsolation(t *testing.T) {
	// 1. Role Rank checks
	if RoleRank(RoleOwner) <= RoleRank(RoleManager) {
		t.Fatal("owner should outrank manager")
	}
	if RoleRank(RoleManager) <= RoleRank(RoleEmployee) {
		t.Fatal("manager should outrank employee")
	}
	if RoleRank(RoleEmployee) <= RoleRank(RoleCustomer) {
		t.Fatal("employee should outrank customer")
	}

	// 2. Upward Visibility & Downward Isolation in Role Management
	// Owner can manage all roles
	if !CanManageRole(RoleOwner, RoleManager) || !CanManageRole(RoleOwner, RoleEmployee) || !CanManageRole(RoleOwner, RoleCustomer) || !CanManageRole(RoleOwner, RoleOwner) {
		t.Fatal("owner should be able to manage all roles")
	}
	// Manager can manage employee and customer, but ISOLATED from managing manager or owner
	if !CanManageRole(RoleManager, RoleEmployee) || !CanManageRole(RoleManager, RoleCustomer) {
		t.Fatal("manager should be able to manage employee and customer")
	}
	if CanManageRole(RoleManager, RoleOwner) {
		t.Fatal("downward isolation violation: manager must not manage owner role")
	}
	if CanManageRole(RoleManager, RoleManager) {
		t.Fatal("downward isolation violation: manager must not manage peer manager role")
	}
	// Employee and Customer have zero role management capability
	if CanManageRole(RoleEmployee, RoleCustomer) || CanManageRole(RoleCustomer, RoleCustomer) {
		t.Fatal("employee and customer must not have role management authority")
	}

	// 3. Audit Log Visibility
	// Owner can inspect all
	if !CanInspectAuditLogs(RoleOwner, RoleOwner) || !CanInspectAuditLogs(RoleOwner, RoleManager) || !CanInspectAuditLogs(RoleOwner, RoleEmployee) {
		t.Fatal("owner must have upward visibility over all audit logs")
	}
	// Manager can inspect manager, employee, customer; ISOLATED from owner
	if !CanInspectAuditLogs(RoleManager, RoleManager) || !CanInspectAuditLogs(RoleManager, RoleEmployee) || !CanInspectAuditLogs(RoleManager, RoleCustomer) {
		t.Fatal("manager should be able to inspect operational logs")
	}
	if CanInspectAuditLogs(RoleManager, RoleOwner) {
		t.Fatal("downward isolation violation: manager must not inspect owner audit logs")
	}
	// Employee and customer cannot inspect audit logs
	if CanInspectAuditLogs(RoleEmployee, RoleEmployee) || CanInspectAuditLogs(RoleCustomer, RoleCustomer) {
		t.Fatal("employee and customer must not inspect audit logs")
	}

	// 4. Critical Business Invariants
	if !CanMutateCatalogPricing(RoleOwner) || CanMutateCatalogPricing(RoleManager) || CanMutateCatalogPricing(RoleEmployee) {
		t.Fatal("only owner can alter base catalog pricing")
	}
	if !CanApproveOpeningStock(RoleOwner) || CanApproveOpeningStock(RoleManager) || CanApproveOpeningStock(RoleEmployee) {
		t.Fatal("only owner can approve inventory opening stock counts")
	}
	if !CanReviewPaymentExceptions(RoleOwner) || !CanReviewPaymentExceptions(RoleManager) || CanReviewPaymentExceptions(RoleEmployee) {
		t.Fatal("only owner and manager can review payment exceptions")
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
		if u.Role == RoleOwner && u.IsActive {
			count++
		}
	}
	return count, nil
}

func (m *memStore) UpdateUserRole(ctx context.Context, id string, newRole string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Role = newRole
	return nil
}

func (m *memStore) UpdateUserStatus(ctx context.Context, id string, isActive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.IsActive = isActive
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
	user, ses, rawToken, csrfToken, err := service.CompleteLogin(ctx, state, testCode, bindingCookie, "127.0.0.1", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("CompleteLogin failed: %v", err)
	}

	if user.GoogleSub != "google-sub-12345" || user.Role != RoleCustomer {
		t.Fatalf("expected initial user role to be customer, got %s", user.Role)
	}
	if ses == nil || rawToken == "" || csrfToken == "" {
		t.Fatal("expected valid session, token, and csrf token")
	}

	// 3. Single-use replay protection
	_, _, _, _, err = service.CompleteLogin(ctx, state, testCode, bindingCookie, "127.0.0.1", "Mozilla/5.0")
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

	// Manager candidate
	mgrProfile := &GoogleProfile{
		Subject: "google-sub-mgr",
		Email:   "manager@blissatelier.com",
		Name:    "Store Manager",
	}
	mgrUser, _ := store.UpsertUserByGoogleSub(ctx, mgrProfile.Subject, mgrProfile.Email, mgrProfile.Name, "")
	_ = service.UpdateUserRole(ctx, updatedUser, mgrUser.ID, RoleManager, "127.0.0.1")
	mgrUpdated, _ := store.GetUserByID(ctx, mgrUser.ID)

	// Downward Isolation: Manager cannot promote anyone to owner
	err = service.UpdateUserRole(ctx, mgrUpdated, empUser.ID, RoleOwner, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when manager attempts to promote to owner, got %v", err)
	}

	// Downward Isolation: Manager cannot modify owner's role
	err = service.UpdateUserRole(ctx, mgrUpdated, updatedUser.ID, RoleCustomer, "127.0.0.1")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission denied when manager attempts to modify owner, got %v", err)
	}

	// 7. Audit log downward isolation
	// Manager inspecting audit logs should see employee/manager events, but not owner-exclusive events
	logs, err := service.InspectAuditLogs(ctx, mgrUpdated, 10, 0)
	if err != nil {
		t.Fatalf("failed to inspect audit logs: %v", err)
	}
	for _, l := range logs {
		if l.ActorRole == RoleOwner {
			t.Fatalf("downward isolation breach: manager saw owner audit log: %v", l)
		}
	}
}
