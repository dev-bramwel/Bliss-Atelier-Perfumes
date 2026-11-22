package httpapi

import (
	"context"
	"encoding/json"
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

func (m *testAuthStore) UpdateUserRole(ctx context.Context, id string, newRole string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.Role = newRole
		return nil
	}
	return auth.ErrSessionNotFound
}

func (m *testAuthStore) UpdateUserStatus(ctx context.Context, id string, isActive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.IsActive = isActive
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
	if empInvRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when employee accesses owner endpoint, got %d: %s", empInvRR.Code, empInvRR.Body.String())
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
