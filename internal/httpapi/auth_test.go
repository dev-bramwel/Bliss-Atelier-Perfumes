package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
)

// in-memory test store for httpapi auth tests
type testAuthStore struct {
	mu          sync.Mutex
	users       map[string]*auth.User
	usersBySub  map[string]*auth.User
	usersByMail map[string]*auth.User
	txs         map[string]*auth.LoginTransaction
	sessions    map[string]*auth.UserSession
	auditLogs   []auth.AuditLogEntry
}

func newTestAuthStore() *testAuthStore {
	return &testAuthStore{
		users:       make(map[string]*auth.User),
		usersBySub:  make(map[string]*auth.User),
		usersByMail: make(map[string]*auth.User),
		txs:         make(map[string]*auth.LoginTransaction),
		sessions:    make(map[string]*auth.UserSession),
	}
}

func (m *testAuthStore) UpsertUserByGoogleSub(ctx context.Context, googleSub, email, name, pictureURL string) (*auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.usersBySub[googleSub]; ok {
		u.Email = email
		u.Name = name
		u.PictureURL = pictureURL
		m.usersByMail[strings.ToLower(email)] = u
		return u, nil
	}
	id := "usr_" + googleSub
	u := &auth.User{
		ID:         id,
		GoogleSub:  googleSub,
		Email:      email,
		Name:       name,
		PictureURL: pictureURL,
		Role:       auth.RoleCustomer,
		IsActive:   true,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	m.users[id] = u
	m.usersBySub[googleSub] = u
	m.usersByMail[strings.ToLower(email)] = u
	return u, nil
}

func (m *testAuthStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.users[id], nil
}

func (m *testAuthStore) GetUserByEmail(ctx context.Context, email string) (*auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usersByMail[strings.ToLower(email)], nil
}

func (m *testAuthStore) CountOwners(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, u := range m.users {
		if u.Role == auth.RoleOwner {
			count++
		}
	}
	return count, nil
}

func (m *testAuthStore) CountActiveOwners(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, u := range m.users {
		if u.Role == auth.RoleOwner && u.IsActive {
			count++
		}
	}
	return count, nil
}

func (m *testAuthStore) DeactivateUserAndRevokeSessions(ctx context.Context, userID string, actorRole string, isSelf bool, auditEntry *auth.AuditLogEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, auth.ErrSessionNotFound
	}

	if !isSelf && actorRole != "" {
		if !auth.CanManageUserStatus(actorRole, u.Role) {
			return nil, auth.ErrPermissionDenied
		}
	}

	if u.Role == auth.RoleOwner && u.IsActive {
		activeOwnerCount := 0
		for _, o := range m.users {
			if o.Role == auth.RoleOwner && o.IsActive {
				activeOwnerCount++
			}
		}
		if activeOwnerCount <= 1 {
			return nil, auth.ErrCannotDeactivateSoleOwner
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
		m.auditLogs = append([]auth.AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return revoked, nil
}

func (m *testAuthStore) ReactivateUser(ctx context.Context, userID string, actorRole string, auditEntry *auth.AuditLogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return auth.ErrSessionNotFound
	}

	if actorRole != "" {
		if !auth.CanManageUserStatus(actorRole, u.Role) {
			return auth.ErrPermissionDenied
		}
	}

	u.IsActive = true
	m.users[userID] = u

	if auditEntry != nil {
		m.auditLogs = append([]auth.AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return nil
}

func (m *testAuthStore) UpdateUserRole(ctx context.Context, id string, newRole string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.Role = newRole
		m.users[id] = u
		return nil
	}
	return auth.ErrSessionNotFound
}

func (m *testAuthStore) UpdateUserRoleAndRevokeSessions(ctx context.Context, userID string, newRole string, auditEntry *auth.AuditLogEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, auth.ErrSessionNotFound
	}

	if u.Role == auth.RoleOwner && u.IsActive && newRole != auth.RoleOwner {
		activeOwnerCount := 0
		for _, o := range m.users {
			if o.Role == auth.RoleOwner && o.IsActive {
				activeOwnerCount++
			}
		}
		if activeOwnerCount <= 1 {
			return nil, auth.ErrCannotDemoteSoleOwner
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
		m.auditLogs = append([]auth.AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return revoked, nil
}

func (m *testAuthStore) UpdateUserPermissions(ctx context.Context, actorID string, userID string, canManageInventory bool, auditEntry *auth.AuditLogEntry) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if actorID != "" {
		actor, ok := m.users[actorID]
		if !ok || !actor.IsActive || actor.Role != auth.RoleOwner {
			return nil, auth.ErrPermissionDenied
		}
	}

	u, ok := m.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}

	if u.Role != auth.RoleEmployee {
		return nil, fmt.Errorf("%w: permissions can only be modified for employees", auth.ErrInvalidRole)
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
		m.auditLogs = append([]auth.AuditLogEntry{*auditEntry}, m.auditLogs...)
	}

	return sessionHashes, nil
}

func (m *testAuthStore) UpdateUserStatus(ctx context.Context, id string, isActive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.IsActive = isActive
		m.users[id] = u
		return nil
	}
	return auth.ErrSessionNotFound
}

func (m *testAuthStore) ListUsers(ctx context.Context, filterRole string) ([]auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []auth.User
	for _, u := range m.users {
		if filterRole == "" || u.Role == filterRole {
			res = append(res, *u)
		}
	}
	return res, nil
}

func (m *testAuthStore) CreateLoginTransaction(ctx context.Context, tx auth.LoginTransaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs[tx.StateHash] = &tx
	return nil
}

func (m *testAuthStore) ConsumeLoginTransaction(ctx context.Context, stateHash string, browserBindingHash string) (*auth.LoginTransaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, ok := m.txs[stateHash]
	if !ok || tx.UsedAt != nil || tx.BrowserBindingHash != browserBindingHash || time.Now().After(tx.ExpiresAt) {
		return nil, auth.ErrSessionNotFound
	}
	now := time.Now()
	tx.UsedAt = &now
	return tx, nil
}

func (m *testAuthStore) CreateSession(ctx context.Context, session auth.UserSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[session.UserID]
	if !ok {
		return auth.ErrSessionNotFound
	}
	if !u.IsActive {
		return auth.ErrAccountDeactivated
	}
	m.sessions[session.TokenHash] = &session
	return nil
}

func (m *testAuthStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*auth.UserSession, *auth.User, error) {
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

func (m *testAuthStore) TouchSession(ctx context.Context, tokenHash string, newExpiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ses, ok := m.sessions[tokenHash]; ok {
		ses.ExpiresAt = newExpiresAt
		ses.LastSeenAt = time.Now()
	}
	return nil
}

func (m *testAuthStore) RevokeSessionByTokenHash(ctx context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ses, ok := m.sessions[tokenHash]; ok {
		ses.IsRevoked = true
	}
	return nil
}

func (m *testAuthStore) RevokeSessionsByUserID(ctx context.Context, userID string) ([]string, error) {
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

func (m *testAuthStore) RecordAuditLog(ctx context.Context, entry auth.AuditLogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditLogs = append(m.auditLogs, entry)
	return nil
}

func (m *testAuthStore) QueryAuditLogs(ctx context.Context, limit int, offset int) ([]auth.AuditLogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auditLogs, nil
}

func setupTestServer(t *testing.T) (*API, *auth.Service, *auth.MockOIDCVerifier, *testAuthStore) {
	store := newTestAuthStore()
	verifier := auth.NewMockOIDCVerifier()
	authService := auth.NewService(store, verifier, nil)

	secConfig := SecurityConfig{
		ManagerSecret: "legacy-manager-secret",
	}

	api := New("127.0.0.1:0", 5*time.Second, nil, secConfig, authService)
	return api, authService, verifier, store
}

func TestAuthRoutesEndToEnd(t *testing.T) {
	api, _, verifier, store := setupTestServer(t)

	// 1. GET /api/auth/google
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google?return_url=/admin/orders", nil)
	rr := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect from /api/auth/google, got %d", rr.Code)
	}

	redirectURL := rr.Header().Get("Location")
	if !strings.Contains(redirectURL, "state=") || !strings.Contains(redirectURL, "nonce=") {
		t.Fatalf("unexpected redirect URL: %s", redirectURL)
	}

	// Extract state & nonce
	var state, nonce string
	for _, part := range strings.Split(redirectURL, "&") {
		if strings.HasPrefix(part, "state=") {
			state = strings.TrimPrefix(part, "state=")
		}
		if strings.HasPrefix(part, "nonce=") {
			nonce = strings.TrimPrefix(part, "nonce=")
		}
	}

	var txCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bliss_oauth_tx" {
			txCookie = c
			break
		}
	}
	if txCookie == nil || txCookie.Value == "" {
		t.Fatal("expected bliss_oauth_tx cookie to be set")
	}

	// Register code in mock verifier
	authCode := "google-auth-code-123"
	verifier.RegisterToken(authCode, "id-tok-123", &auth.GoogleProfile{
		Subject: "google-sub-999",
		Email:   "shopper@example.com",
		Name:    "Shopper User",
	}, nonce)

	// 2. GET /api/auth/google/callback
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code="+authCode+"&state="+state+"&format=json", nil)
	callbackReq.AddCookie(txCookie)
	callbackRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(callbackRR, callbackReq)

	if callbackRR.Code != http.StatusOK {
		t.Fatalf("expected 200 from callback format=json, got %d: %s", callbackRR.Code, callbackRR.Body.String())
	}

	var callbackResp struct {
		Status    string     `json:"status"`
		User      *auth.User `json:"user"`
		CSRFToken string     `json:"csrf_token"`
	}
	if err := json.NewDecoder(callbackRR.Body).Decode(&callbackResp); err != nil {
		t.Fatalf("failed to decode callback JSON response: %v", err)
	}

	if callbackResp.User.GoogleSub != "google-sub-999" || callbackResp.User.Role != auth.RoleCustomer {
		t.Fatalf("unexpected user: %+v", callbackResp.User)
	}

	var sessionCookie *http.Cookie
	for _, c := range callbackRR.Result().Cookies() {
		if c.Name == "bliss_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("expected bliss_session cookie to be set")
	}

	// 3. GET /api/auth/me (Authenticated)
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(sessionCookie)
	meRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(meRR, meReq)

	if meRR.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/auth/me, got %d: %s", meRR.Code, meRR.Body.String())
	}

	var meResp struct {
		User      *auth.User `json:"user"`
		CSRFToken string     `json:"csrf_token"`
	}
	_ = json.NewDecoder(meRR.Body).Decode(&meResp)
	if meResp.User.ID != callbackResp.User.ID {
		t.Fatalf("expected matching user id in /api/auth/me")
	}

	// 4. POST /api/auth/logout without CSRF should fail (403 Forbidden)
	badLogoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	badLogoutReq.AddCookie(sessionCookie)
	badLogoutRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(badLogoutRR, badLogoutReq)

	if badLogoutRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for logout without CSRF token, got %d", badLogoutRR.Code)
	}

	// POST /api/auth/logout with CSRF should succeed
	goodLogoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	goodLogoutReq.AddCookie(sessionCookie)
	goodLogoutReq.Header.Set("X-CSRF-Token", meResp.CSRFToken)
	goodLogoutRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(goodLogoutRR, goodLogoutReq)

	if goodLogoutRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for logout with CSRF token, got %d", goodLogoutRR.Code)
	}

	// 5. Subsequent /api/auth/me should fail (401 Unauthorized)
	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	afterLogoutReq.AddCookie(sessionCookie)
	afterLogoutRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(afterLogoutRR, afterLogoutReq)

	if afterLogoutRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized after logout, got %d", afterLogoutRR.Code)
	}

	_ = store
}

func TestAdminRoutesAndRoleIsolation(t *testing.T) {
	api, authService, verifier, store := setupTestServer(t)
	ctx := context.Background()

	// Create owner user
	ownerUser, _ := store.UpsertUserByGoogleSub(ctx, "sub-owner", "owner@blissatelier.com", "Owner User", "")
	_ = store.UpdateUserRole(ctx, ownerUser.ID, auth.RoleOwner)
	ownerUser.Role = auth.RoleOwner

	// Create employee user
	empUser, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp", "employee@blissatelier.com", "Employee User", "")
	_ = store.UpdateUserRole(ctx, empUser.ID, auth.RoleEmployee)
	empUser.Role = auth.RoleEmployee

	// Create customer user
	custUser, _ := store.UpsertUserByGoogleSub(ctx, "sub-cust", "customer@example.com", "Customer User", "")

	// Helper to login and get session + csrf
	loginAs := func(user *auth.User) (*http.Cookie, string) {
		_, bindingCookie, _ := authService.BeginLogin(ctx, "/", "127.0.0.1", "test")
		code := "code-" + user.ID
		verifier.RegisterToken(code, "tok-"+user.ID, &auth.GoogleProfile{
			Subject: user.GoogleSub,
			Email:   user.Email,
			Name:    user.Name,
		}, "")
		// We use callback request
		r := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code="+code+"&state=state&format=json", nil)
		r.AddCookie(&http.Cookie{Name: "bliss_oauth_tx", Value: bindingCookie})

		// Manually create session in authService
		rawToken, _ := auth.GenerateSessionToken()
		csrfSecret, _ := auth.RandomString(32)
		ses := auth.UserSession{
			UserID:       user.ID,
			TokenHash:    auth.HashToken(rawToken),
			CSRFSecret:   csrfSecret,
			IPAddress:    "127.0.0.1",
			UserAgent:    "test",
			ExpiresAt:    time.Now().Add(time.Hour),
			MaxExpiresAt: time.Now().Add(24 * time.Hour),
		}
		_ = store.CreateSession(ctx, ses)
		return &http.Cookie{Name: "bliss_session", Value: rawToken}, auth.GenerateCSRFToken(csrfSecret)
	}

	ownerCookie, ownerCSRF := loginAs(ownerUser)
	empCookie, empCSRF := loginAs(empUser)
	custCookie, _ := loginAs(custUser)

	// 1. Customer cannot access /api/admin/users (403 Forbidden)
	custReq := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	custReq.AddCookie(custCookie)
	custRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(custRR, custReq)
	if custRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for customer accessing admin users, got %d", custRR.Code)
	}

	// 2. Upward Visibility: Owner lists all users
	ownerReq := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	ownerReq.AddCookie(ownerCookie)
	ownerRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerRR, ownerReq)
	if ownerRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for owner listing users, got %d", ownerRR.Code)
	}
	var ownerListResp struct {
		Users []auth.User `json:"users"`
	}
	_ = json.NewDecoder(ownerRR.Body).Decode(&ownerListResp)
	if len(ownerListResp.Users) < 3 {
		t.Fatalf("expected owner to see all users, got %d", len(ownerListResp.Users))
	}

	// 3. Downward Isolation: Employee cannot list users (403 Forbidden)
	empReq := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	empReq.AddCookie(empCookie)
	empRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(empRR, empReq)
	if empRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for employee accessing admin users, got %d", empRR.Code)
	}

	// 4. Downward Isolation: Employee attempting to promote someone to owner must be 403 Forbidden
	promotePayload := strings.NewReader(`{"role":"owner"}`)
	promoteReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+custUser.ID+"/role", promotePayload)
	promoteReq.AddCookie(empCookie)
	promoteReq.Header.Set("X-CSRF-Token", empCSRF)
	promoteRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(promoteRR, promoteReq)
	if promoteRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee attempts to promote to owner, got %d: %s", promoteRR.Code, promoteRR.Body.String())
	}

	// 4b. Downward Isolation: Employee attempting to demote or change role must be 403 Forbidden
	empDemotePayload := strings.NewReader(`{"role":"customer"}`)
	empDemoteReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+empUser.ID+"/role", empDemotePayload)
	empDemoteReq.AddCookie(empCookie)
	empDemoteReq.Header.Set("X-CSRF-Token", empCSRF)
	empDemoteRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(empDemoteRR, empDemoteReq)
	if empDemoteRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee attempts to change role, got %d: %s", empDemoteRR.Code, empDemoteRR.Body.String())
	}

	// 5. Owner can promote customer to employee
	ownerPromotePayload := strings.NewReader(`{"role":"employee"}`)
	ownerPromoteReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+custUser.ID+"/role", ownerPromotePayload)
	ownerPromoteReq.AddCookie(ownerCookie)
	ownerPromoteReq.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerPromoteRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerPromoteRR, ownerPromoteReq)
	if ownerPromoteRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner promotes customer to employee, got %d: %s", ownerPromoteRR.Code, ownerPromoteRR.Body.String())
	}

	// 6. Downward Isolation: Employee cannot access owner-only operations (e.g., catalog cache invalidation)
	empInvReq := httptest.NewRequest(http.MethodPost, "/api/catalog/invalidate", nil)
	empInvReq.AddCookie(empCookie)
	empInvReq.Header.Set("X-CSRF-Token", empCSRF)
	empInvRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(empInvRR, empInvReq)
	if empInvRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee accesses owner endpoint, got %d: %s", empInvRR.Code, empInvRR.Body.String())
	}

	// 7. CSRF Protection: Owner session accessing mutating owner endpoint without CSRF is rejected
	ownerInvNoCSRF := httptest.NewRequest(http.MethodPost, "/api/catalog/invalidate", nil)
	ownerInvNoCSRF.AddCookie(ownerCookie)
	ownerInvNoCSRFRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerInvNoCSRFRR, ownerInvNoCSRF)
	if ownerInvNoCSRFRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when owner accesses endpoint without CSRF, got %d", ownerInvNoCSRFRR.Code)
	}

	// 8. Owner session accessing mutating owner endpoint WITH CSRF succeeds and refreshes session cookie
	ownerInvWithCSRF := httptest.NewRequest(http.MethodPost, "/api/catalog/invalidate", nil)
	ownerInvWithCSRF.AddCookie(ownerCookie)
	ownerInvWithCSRF.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerInvWithCSRFRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerInvWithCSRFRR, ownerInvWithCSRF)
	if ownerInvWithCSRFRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner accesses endpoint with CSRF, got %d: %s", ownerInvWithCSRFRR.Code, ownerInvWithCSRFRR.Body.String())
	}
	if !strings.Contains(ownerInvWithCSRFRR.Header().Get("Set-Cookie"), "bliss_session") {
		t.Fatalf("expected sliding session cookie refresh in response Set-Cookie")
	}

	// 9. Exclusive Owner Authority: Owner can demote employee to customer
	ownerDemoteEmpPayload := strings.NewReader(`{"role":"customer"}`)
	ownerDemoteEmpReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+custUser.ID+"/role", ownerDemoteEmpPayload)
	ownerDemoteEmpReq.AddCookie(ownerCookie)
	ownerDemoteEmpReq.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerDemoteEmpRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerDemoteEmpRR, ownerDemoteEmpReq)
	if ownerDemoteEmpRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner demotes employee to customer, got %d: %s", ownerDemoteEmpRR.Code, ownerDemoteEmpRR.Body.String())
	}

	// 10. Owner with empty display name still succeeds via user ID identity fallback
	noNameOwner, _ := store.UpsertUserByGoogleSub(ctx, "sub-no-name-owner", "noname.owner@blissatelier.com", "", "")
	_ = store.UpdateUserRole(ctx, noNameOwner.ID, auth.RoleOwner)
	noNameCookie, noNameCSRF := loginAs(noNameOwner)

	noNameReq := httptest.NewRequest(http.MethodPost, "/api/catalog/invalidate", nil)
	noNameReq.AddCookie(noNameCookie)
	noNameReq.Header.Set("X-CSRF-Token", noNameCSRF)
	noNameRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(noNameRR, noNameReq)
	if noNameRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for owner with blank profile name, got %d: %s", noNameRR.Code, noNameRR.Body.String())
	}
}

func TestSelfDeactivationAndReactivation(t *testing.T) {
	api, _, verifier, store := setupTestServer(t)
	ctx := context.Background()

	loginAs := func(u *auth.User) (*http.Cookie, string) {
		token, err := auth.GenerateSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		csrfSecret, err := auth.RandomString(32)
		if err != nil {
			t.Fatal(err)
		}
		ses := auth.UserSession{
			UserID:       u.ID,
			TokenHash:    auth.HashToken(token),
			CSRFSecret:   csrfSecret,
			IPAddress:    "127.0.0.1",
			UserAgent:    "test-agent",
			IsRevoked:    false,
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			MaxExpiresAt: time.Now().Add(12 * time.Hour),
		}
		if err := store.CreateSession(ctx, ses); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: "bliss_session", Value: token}, auth.GenerateCSRFToken(csrfSecret)
	}

	initOAuth := func(email, sub string) (string, *http.Cookie) {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/google", nil)
		rr := httptest.NewRecorder()
		api.server.Handler.ServeHTTP(rr, req)
		redirectURL := rr.Header().Get("Location")
		var state, nonce string
		for _, part := range strings.Split(redirectURL, "&") {
			if strings.HasPrefix(part, "state=") {
				state = strings.TrimPrefix(part, "state=")
			}
			if strings.HasPrefix(part, "nonce=") {
				nonce = strings.TrimPrefix(part, "nonce=")
			}
		}
		var txCookie *http.Cookie
		for _, c := range rr.Result().Cookies() {
			if c.Name == "bliss_oauth_tx" {
				txCookie = c
				break
			}
		}
		authCode := "code-" + sub + "-" + state[:6]
		verifier.RegisterToken(authCode, "id-tok-"+sub, &auth.GoogleProfile{
			Subject: sub,
			Email:   email,
			Name:    "Test User",
		}, nonce)
		return "/api/auth/google/callback?code=" + authCode + "&state=" + state, txCookie
	}

	// 1. Create owner, employee, and customer
	owner, _ := store.UpsertUserByGoogleSub(ctx, "sub-owner-1", "owner@blissatelier.com", "Owner", "")
	_ = store.UpdateUserRole(ctx, owner.ID, auth.RoleOwner)
	owner, _ = store.GetUserByID(ctx, owner.ID)
	ownerCookie, ownerCSRF := loginAs(owner)

	emp, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp-1", "emp@blissatelier.com", "Employee", "")
	_ = store.UpdateUserRole(ctx, emp.ID, auth.RoleEmployee)
	emp, _ = store.GetUserByID(ctx, emp.ID)
	empCookie, empCSRF := loginAs(emp)

	cust, _ := store.UpsertUserByGoogleSub(ctx, "sub-cust-1", "customer@example.com", "Customer", "")
	custCookie, custCSRF := loginAs(cust)

	// 2. Sole owner cannot self-deactivate (409 Conflict)
	ownerDeactReq := httptest.NewRequest(http.MethodPost, "/api/auth/deactivate", nil)
	ownerDeactReq.AddCookie(ownerCookie)
	ownerDeactReq.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerDeactRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerDeactRR, ownerDeactReq)
	if ownerDeactRR.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when sole owner attempts self-deactivation, got %d: %s", ownerDeactRR.Code, ownerDeactRR.Body.String())
	}

	// 3. Customer self-deactivation without authentication returns 401
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/auth/deactivate", nil)
	unauthRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without session, got %d", unauthRR.Code)
	}

	// 4. Customer self-deactivation without CSRF returns 403
	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/auth/deactivate", nil)
	noCSRFReq.AddCookie(custCookie)
	noCSRFRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(noCSRFRR, noCSRFReq)
	if noCSRFRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden without CSRF token, got %d", noCSRFRR.Code)
	}

	// 5. Successful customer self-deactivation with session and CSRF
	deactReq := httptest.NewRequest(http.MethodPost, "/api/auth/deactivate", nil)
	deactReq.AddCookie(custCookie)
	deactReq.Header.Set("X-CSRF-Token", custCSRF)
	deactRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(deactRR, deactReq)
	if deactRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid self-deactivation, got %d: %s", deactRR.Code, deactRR.Body.String())
	}
	var deactResp map[string]any
	if err := json.Unmarshal(deactRR.Body.Bytes(), &deactResp); err != nil {
		t.Fatalf("failed to decode deactivation response: %v", err)
	}
	if deactResp["status"] != "deactivated" {
		t.Fatalf("expected status 'deactivated', got %v", deactResp["status"])
	}

	// Verify bliss_session cookie was cleared (MaxAge = -1)
	foundClearedCookie := false
	for _, c := range deactRR.Result().Cookies() {
		if c.Name == "bliss_session" && c.MaxAge < 0 {
			foundClearedCookie = true
			break
		}
	}
	if !foundClearedCookie {
		t.Fatalf("expected bliss_session cookie to be cleared with MaxAge < 0")
	}

	// 6. Old session token now rejected on /api/auth/me (401 Unauthorized)
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(custCookie)
	meRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(meRR, meReq)
	if meRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized with revoked session, got %d", meRR.Code)
	}

	// 7. Subsequent Google OAuth callback for deactivated customer returns 403 Forbidden
	// Test JSON callback
	cbURL, txCookie := initOAuth("customer@example.com", "sub-cust-1")
	cbJSONReq := httptest.NewRequest(http.MethodGet, cbURL+"&format=json", nil)
	cbJSONReq.AddCookie(txCookie)
	cbJSONRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(cbJSONRR, cbJSONReq)
	if cbJSONRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for deactivated account JSON callback, got %d: %s", cbJSONRR.Code, cbJSONRR.Body.String())
	}
	if !strings.Contains(cbJSONRR.Body.String(), "account is deactivated") {
		t.Fatalf("expected deactivated error in response, got %s", cbJSONRR.Body.String())
	}

	// Test HTML callback returns 403 with atelier HTML notice
	cbURL2, txCookie2 := initOAuth("customer@example.com", "sub-cust-1")
	cbHTMLReq := httptest.NewRequest(http.MethodGet, cbURL2, nil)
	cbHTMLReq.AddCookie(txCookie2)
	cbHTMLRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(cbHTMLRR, cbHTMLReq)
	if cbHTMLRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for deactivated account HTML callback, got %d", cbHTMLRR.Code)
	}
	if !strings.Contains(cbHTMLRR.Body.String(), "Account Deactivated") || !strings.Contains(cbHTMLRR.Body.String(), "support@blissatelier.com") {
		t.Fatalf("expected atelier deactivation notice with support instructions, got %s", cbHTMLRR.Body.String())
	}

	// 8. Employee can reactivate customer account (200 OK)
	reactivatePayload := strings.NewReader(`{"is_active":true}`)
	reactivateReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+cust.ID+"/status", reactivatePayload)
	reactivateReq.AddCookie(empCookie)
	reactivateReq.Header.Set("X-CSRF-Token", empCSRF)
	reactivateRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(reactivateRR, reactivateReq)
	if reactivateRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when employee reactivates customer, got %d: %s", reactivateRR.Code, reactivateRR.Body.String())
	}

	// Customer can now complete Google OAuth login successfully
	cbURL3, txCookie3 := initOAuth("customer@example.com", "sub-cust-1")
	cbSuccessReq := httptest.NewRequest(http.MethodGet, cbURL3+"&format=json", nil)
	cbSuccessReq.AddCookie(txCookie3)
	cbSuccessRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(cbSuccessRR, cbSuccessReq)
	if cbSuccessRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for reactivated customer OAuth callback, got %d: %s", cbSuccessRR.Code, cbSuccessRR.Body.String())
	}

	// 9. Employee CANNOT deactivate another employee (403 Forbidden)
	emp2, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp-2", "emp2@blissatelier.com", "Employee 2", "")
	_ = store.UpdateUserRole(ctx, emp2.ID, auth.RoleEmployee)
	empDeactEmpPayload := strings.NewReader(`{"is_active":false}`)
	empDeactEmpReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+emp2.ID+"/status", empDeactEmpPayload)
	empDeactEmpReq.AddCookie(empCookie)
	empDeactEmpReq.Header.Set("X-CSRF-Token", empCSRF)
	empDeactEmpRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(empDeactEmpRR, empDeactEmpReq)
	if empDeactEmpRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee attempts to manage another employee's status, got %d", empDeactEmpRR.Code)
	}
}

type mockCatalogCache struct {
	invalidated bool
}

func (m *mockCatalogCache) Invalidate(ctx context.Context) error {
	m.invalidated = true
	return nil
}

func TestUserPermissionsAndInventoryEndpoints(t *testing.T) {
	ctx := context.Background()
	store := newTestAuthStore()
	verifier := auth.NewMockOIDCVerifier()
	authService := auth.NewService(store, verifier, nil)
	cache := &mockCatalogCache{}
	ops := &mockOperations{}

	secConfig := SecurityConfig{
		ManagerSecret: "legacy-manager-secret",
	}

	api := New("127.0.0.1:0", 5*time.Second, nil, secConfig, authService, cache, ops)

	// Create owner
	owner, _ := store.UpsertUserByGoogleSub(ctx, "sub-owner", "owner@blissatelier.com", "Owner User", "")
	_ = store.UpdateUserRole(ctx, owner.ID, auth.RoleOwner)
	rawOwner, _ := auth.GenerateSessionToken()
	csrfOwnerSecret, _ := auth.RandomString(32)
	_ = store.CreateSession(ctx, auth.UserSession{
		UserID:       owner.ID,
		TokenHash:    auth.HashToken(rawOwner),
		CSRFSecret:   csrfOwnerSecret,
		ExpiresAt:    time.Now().Add(time.Hour),
		MaxExpiresAt: time.Now().Add(2 * time.Hour),
	})
	ownerCookie := &http.Cookie{Name: "bliss_session", Value: rawOwner}
	ownerCSRF := auth.GenerateCSRFToken(csrfOwnerSecret)

	// Create employee (without inventory permission initially)
	emp, _ := store.UpsertUserByGoogleSub(ctx, "sub-emp", "employee@blissatelier.com", "Employee User", "")
	_ = store.UpdateUserRole(ctx, emp.ID, auth.RoleEmployee)
	rawEmp, _ := auth.GenerateSessionToken()
	csrfEmpSecret, _ := auth.RandomString(32)
	_ = store.CreateSession(ctx, auth.UserSession{
		UserID:       emp.ID,
		TokenHash:    auth.HashToken(rawEmp),
		CSRFSecret:   csrfEmpSecret,
		ExpiresAt:    time.Now().Add(time.Hour),
		MaxExpiresAt: time.Now().Add(2 * time.Hour),
	})
	empCookie := &http.Cookie{Name: "bliss_session", Value: rawEmp}
	empCSRF := auth.GenerateCSRFToken(csrfEmpSecret)

	// Create customer
	cust, _ := store.UpsertUserByGoogleSub(ctx, "sub-cust", "customer@example.com", "Customer One", "")

	// 1. Employee cannot update permissions (403 Forbidden)
	empUpdateReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+emp.ID+"/permissions", strings.NewReader(`{"can_manage_inventory":true}`))
	empUpdateReq.AddCookie(empCookie)
	empUpdateReq.Header.Set("X-CSRF-Token", empCSRF)
	empUpdateRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(empUpdateRR, empUpdateReq)
	if empUpdateRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee attempts to update permissions, got %d", empUpdateRR.Code)
	}

	// 2. Owner can grant inventory permission to employee (200 OK)
	ownerUpdateReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+emp.ID+"/permissions", strings.NewReader(`{"can_manage_inventory":true}`))
	ownerUpdateReq.AddCookie(ownerCookie)
	ownerUpdateReq.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerUpdateRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerUpdateRR, ownerUpdateReq)
	if ownerUpdateRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner updates permissions, got %d: %s", ownerUpdateRR.Code, ownerUpdateRR.Body.String())
	}
	if !strings.Contains(ownerUpdateRR.Body.String(), `"can_manage_inventory":true`) {
		t.Fatalf("expected can_manage_inventory:true in response, got %s", ownerUpdateRR.Body.String())
	}
	empUser, _ := store.GetUserByID(ctx, emp.ID)
	if !empUser.CanManageInventory {
		t.Fatal("expected store employee user to have CanManageInventory=true")
	}

	// 3. Invalid CSRF token on permissions endpoint returns 403 Forbidden
	csrfBadReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+emp.ID+"/permissions", strings.NewReader(`{"can_manage_inventory":false}`))
	csrfBadReq.AddCookie(ownerCookie)
	csrfBadReq.Header.Set("X-CSRF-Token", "bad-csrf")
	csrfBadRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(csrfBadRR, csrfBadReq)
	if csrfBadRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden with bad CSRF token, got %d", csrfBadRR.Code)
	}

	// 4. Setting permissions on customer account returns 400 Bad Request
	custPermReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+cust.ID+"/permissions", strings.NewReader(`{"can_manage_inventory":true}`))
	custPermReq.AddCookie(ownerCookie)
	custPermReq.Header.Set("X-CSRF-Token", ownerCSRF)
	custPermRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(custPermRR, custPermReq)
	if custPermRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when setting permissions on customer, got %d", custPermRR.Code)
	}

	// 5. Test inventory management endpoints with permission:
	// Employee WITH permission can invalidate catalog (200 OK)
	invReq := httptest.NewRequest(http.MethodPost, "/catalog/invalidate", nil)
	invReq.AddCookie(empCookie)
	invReq.Header.Set("X-CSRF-Token", empCSRF)
	invRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(invRR, invReq)
	if invRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when employee with permission invalidates catalog, got %d: %s", invRR.Code, invRR.Body.String())
	}

	// Employee WITH permission can view inventory (200 OK)
	getInvReq := httptest.NewRequest(http.MethodGet, "/operations/inventory", nil)
	getInvReq.AddCookie(empCookie)
	getInvReq.Header.Set("X-CSRF-Token", empCSRF)
	getInvRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(getInvRR, getInvReq)
	if getInvRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when employee with permission views inventory, got %d: %s", getInvRR.Code, getInvRR.Body.String())
	}

	// 6. Owner revokes inventory permission from employee
	revokeReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+emp.ID+"/permissions", strings.NewReader(`{"can_manage_inventory":false}`))
	revokeReq.AddCookie(ownerCookie)
	revokeReq.Header.Set("X-CSRF-Token", ownerCSRF)
	revokeRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner revokes permission, got %d", revokeRR.Code)
	}

	// Employee WITHOUT permission is rejected on /catalog/invalidate (403 Forbidden)
	invReq2 := httptest.NewRequest(http.MethodPost, "/catalog/invalidate", nil)
	invReq2.AddCookie(empCookie)
	invReq2.Header.Set("X-CSRF-Token", empCSRF)
	invRR2 := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(invRR2, invReq2)
	if invRR2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee without permission invalidates catalog, got %d: %s", invRR2.Code, invRR2.Body.String())
	}

	// Employee WITHOUT permission is rejected on /operations/inventory (403 Forbidden)
	getInvReq2 := httptest.NewRequest(http.MethodGet, "/operations/inventory", nil)
	getInvReq2.AddCookie(empCookie)
	getInvReq2.Header.Set("X-CSRF-Token", empCSRF)
	getInvRR2 := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(getInvRR2, getInvReq2)
	if getInvRR2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when employee without permission views inventory, got %d: %s", getInvRR2.Code, getInvRR2.Body.String())
	}

	// Owner can always invalidate catalog (200 OK)
	ownerInvReq := httptest.NewRequest(http.MethodPost, "/catalog/invalidate", nil)
	ownerInvReq.AddCookie(ownerCookie)
	ownerInvReq.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerInvRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerInvRR, ownerInvReq)
	if ownerInvRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner invalidates catalog, got %d", ownerInvRR.Code)
	}

	// Owner can always view inventory (200 OK)
	ownerGetInvReq := httptest.NewRequest(http.MethodGet, "/operations/inventory", nil)
	ownerGetInvReq.AddCookie(ownerCookie)
	ownerGetInvReq.Header.Set("X-CSRF-Token", ownerCSRF)
	ownerGetInvRR := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(ownerGetInvRR, ownerGetInvReq)
	if ownerGetInvRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when owner views inventory, got %d", ownerGetInvRR.Code)
	}
}
