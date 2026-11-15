package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCVerifier abstracts Google OpenID Connect token verification and code exchange.
type OIDCVerifier interface {
	AuthCodeURL(state string, nonce string, codeChallenge string) string
	ExchangeCode(ctx context.Context, code string, codeVerifier string) (string, error)
	VerifyIDToken(ctx context.Context, rawIDToken string, expectedNonce string) (*GoogleProfile, error)
}

// GoogleOIDCConfig contains Google OAuth2 and OIDC application settings.
type GoogleOIDCConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// ProductionGoogleVerifier implements OIDCVerifier using standard reviewed libraries.
type ProductionGoogleVerifier struct {
	oauth2Config oauth2.Config
	verifier     *oidc.IDTokenVerifier
}

// NewProductionGoogleVerifier initializes a provider-backed Google OIDC verifier.
func NewProductionGoogleVerifier(ctx context.Context, cfg GoogleOIDCConfig) (*ProductionGoogleVerifier, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("google client ID and client secret are required")
	}

	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("failed to query google oidc discovery provider: %w", err)
	}

	oidcConfig := &oidc.Config{
		ClientID: cfg.ClientID,
	}
	verifier := provider.Verifier(oidcConfig)

	oauthConfig := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	return &ProductionGoogleVerifier{
		oauth2Config: oauthConfig,
		verifier:     verifier,
	}, nil
}

// AuthCodeURL constructs the Google authorization URL with state, nonce, and S256 PKCE challenge.
func (p *ProductionGoogleVerifier) AuthCodeURL(state string, nonce string, codeChallenge string) string {
	opts := []oauth2.AuthCodeOption{
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	}
	return p.oauth2Config.AuthCodeURL(state, opts...)
}

// ExchangeCode performs the server-side code exchange with Google using the PKCE verifier.
func (p *ProductionGoogleVerifier) ExchangeCode(ctx context.Context, code string, codeVerifier string) (string, error) {
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	}
	token, err := p.oauth2Config.Exchange(ctx, code, opts...)
	if err != nil {
		return "", fmt.Errorf("code exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", errors.New("no id_token in token response")
	}
	return rawIDToken, nil
}

// VerifyIDToken cryptographically validates signature, issuer, audience, and expiry, then unpacks profile claims.
func (p *ProductionGoogleVerifier) VerifyIDToken(ctx context.Context, rawIDToken string, expectedNonce string) (*GoogleProfile, error) {
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("id token verification failed: %w", err)
	}

	if expectedNonce != "" && idToken.Nonce != expectedNonce {
		return nil, errors.New("id token nonce mismatch")
	}

	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to unpack id token claims: %w", err)
	}

	if claims.Subject == "" {
		return nil, errors.New("empty sub in id token")
	}

	return &GoogleProfile{
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}

// MockOIDCVerifier provides an in-memory test double for testing authorization-code and token verification.
type MockOIDCVerifier struct {
	mu          sync.Mutex
	AuthURLStub string
	Tokens      map[string]string         // code -> rawIDToken
	Profiles    map[string]*GoogleProfile // rawIDToken -> Profile
	Nonces      map[string]string         // rawIDToken -> expectedNonce
	ExchangeErr error
	VerifyErr   error
}

// NewMockOIDCVerifier initializes an empty mock verifier.
func NewMockOIDCVerifier() *MockOIDCVerifier {
	return &MockOIDCVerifier{
		AuthURLStub: "https://accounts.google.com/o/oauth2/v2/auth?mock=1",
		Tokens:      make(map[string]string),
		Profiles:    make(map[string]*GoogleProfile),
		Nonces:      make(map[string]string),
	}
}

func (m *MockOIDCVerifier) AuthCodeURL(state string, nonce string, codeChallenge string) string {
	if m.AuthURLStub == "" || m.AuthURLStub == "https://accounts.google.com/o/oauth2/v2/auth?mock=1" {
		return fmt.Sprintf("/api/auth/google/callback?mock=1&state=%s&nonce=%s&challenge=%s", state, nonce, codeChallenge)
	}
	return fmt.Sprintf("%s&state=%s&nonce=%s&challenge=%s", m.AuthURLStub, state, nonce, codeChallenge)
}

func (m *MockOIDCVerifier) RegisterToken(code string, rawIDToken string, profile *GoogleProfile, nonce string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Tokens[code] = rawIDToken
	m.Profiles[rawIDToken] = profile
	m.Nonces[rawIDToken] = nonce
}

func (m *MockOIDCVerifier) ExchangeCode(ctx context.Context, code string, codeVerifier string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ExchangeErr != nil {
		return "", m.ExchangeErr
	}
	tok, ok := m.Tokens[code]
	if !ok {
		if strings.HasPrefix(code, "mock-") {
			return "raw-jwt-" + code, nil
		}
		return "", errors.New("invalid or expired mock code")
	}
	return tok, nil
}

func (m *MockOIDCVerifier) VerifyIDToken(ctx context.Context, rawIDToken string, expectedNonce string) (*GoogleProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.VerifyErr != nil {
		return nil, m.VerifyErr
	}
	profile, ok := m.Profiles[rawIDToken]
	if !ok {
		if strings.HasPrefix(rawIDToken, "raw-jwt-mock-") {
			role := strings.TrimPrefix(rawIDToken, "raw-jwt-mock-")
			name := "Bliss Customer"
			email := "customer@blissatelier.com"
			sub := "sub-mock-customer"
			if strings.Contains(role, "owner") {
				name = "Bliss Owner"
				email = "owner@blissatelier.com"
				sub = "sub-mock-owner"
			} else if strings.Contains(role, "manager") {
				name = "Bliss Manager"
				email = "manager@blissatelier.com"
				sub = "sub-mock-manager"
			} else if strings.Contains(role, "employee") {
				name = "Bliss Employee"
				email = "employee@blissatelier.com"
				sub = "sub-mock-employee"
			}
			return &GoogleProfile{
				Subject:       sub,
				Email:         email,
				EmailVerified: true,
				Name:          name,
			}, nil
		}
		return nil, errors.New("unknown or invalid mock id_token")
	}
	if exp, exists := m.Nonces[rawIDToken]; exists && expectedNonce != "" && exp != expectedNonce {
		return nil, errors.New("mock id token nonce mismatch")
	}
	return profile, nil
}
