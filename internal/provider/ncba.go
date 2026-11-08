package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var phoneCleanRegex = regexp.MustCompile(`[\s\-+]`)

// FormatKenyanPhone formats a phone number to the standard 254XXXXXXXXX format expected by NCBA / Safaricom.
func FormatKenyanPhone(phone string) (string, error) {
	p := phoneCleanRegex.ReplaceAllString(strings.TrimSpace(phone), "")
	if strings.HasPrefix(p, "0") {
		p = "254" + p[1:]
	}
	if !strings.HasPrefix(p, "254") || len(p) != 12 {
		return "", ErrInvalidPhone
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return "", ErrInvalidPhone
		}
	}
	return p, nil
}

// NCBAConfig contains the credentials and routing configuration for NCBA Bank mobile payments.
type NCBAConfig struct {
	BaseURL      string // e.g. "https://c2bapis.ncbagroup.com"
	Username     string // Basic auth username for /payments/api/v1/auth/token
	Password     string // Basic auth password
	PayBillNo    string // Typically "880100"
	AccountNo    string // Allocated NCBA Till shortcode / alias
	IPNUsername  string // Username expected in push notification JSON
	IPNPassword  string // Password expected in push notification JSON
	IPNSecretKey string // Shared secret key for push notification signature hash (max 50 chars)
}

// NCBAIPNPayload represents an incoming NCBA Paybill / Till Push Notification request.
type NCBAIPNPayload struct {
	TransType         string `json:"TransType"`
	TransID           string `json:"TransID"`
	FTRef             string `json:"FTRef"`
	TransTime         string `json:"TransTime"`
	TransAmount       string `json:"TransAmount"`
	BusinessShortCode string `json:"BusinessShortCode"`
	AccountNr         string `json:"AccountNr,omitempty"`
	BillRefNumber     string `json:"BillRefNumber"`
	Narrative         string `json:"Narrative"`
	Mobile            string `json:"Mobile"`
	PhoneNr           string `json:"PhoneNr,omitempty"`
	Name              string `json:"name"`
	CustomerName      string `json:"CustomerName,omitempty"`
	Username          string `json:"Username"`
	Password          string `json:"Password"`
	Hash              string `json:"Hash"`
}

// ParseAmount parses the TransAmount string into an integer KES amount.
func (p *NCBAIPNPayload) ParseAmount() (int, error) {
	s := strings.TrimSpace(p.TransAmount)
	if s == "" {
		return 0, errors.New("empty transaction amount")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid transaction amount format: %w", err)
	}
	if f <= 0 {
		return 0, errors.New("transaction amount must be positive")
	}
	return int(f + 0.5), nil
}

// GetMobile returns the normalized mobile phone number.
func (p *NCBAIPNPayload) GetMobile() string {
	if p.Mobile != "" {
		return p.Mobile
	}
	return p.PhoneNr
}

// GetBusinessShortCode returns the shortcode / credit account identifier.
func (p *NCBAIPNPayload) GetBusinessShortCode() string {
	if p.BusinessShortCode != "" {
		return p.BusinessShortCode
	}
	return p.AccountNr
}

// GetCustomerName returns the transaction initiator name.
func (p *NCBAIPNPayload) GetCustomerName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.CustomerName
}

// ComputeNCBAIPNHash calculates the signature hash for an NCBA push notification according to the official specification:
// Hash = Base64( LowercaseHex( SHA256( secretKey + TransType + TransID + TransTime + TransAmount + BusinessShortCode + BillRefNumber + Mobile + Name + "1" ) ) )
func ComputeNCBAIPNHash(secretKey string, p NCBAIPNPayload) string {
	bCode := p.GetBusinessShortCode()
	mobile := p.GetMobile()
	name := p.GetCustomerName()

	hashString := secretKey + p.TransType + p.TransID + p.TransTime + p.TransAmount + bCode + p.BillRefNumber + mobile + name + "1"
	sha := sha256.Sum256([]byte(hashString))
	hexDigest := hex.EncodeToString(sha[:]) // lowercase hex
	return base64.StdEncoding.EncodeToString([]byte(hexDigest))
}

// VerifyNCBAIPN verifies that the incoming notification's Username, Password, and Hash match configuration using constant-time comparisons.
func VerifyNCBAIPN(cfg NCBAConfig, p NCBAIPNPayload) bool {
	if cfg.IPNUsername == "" || cfg.IPNPassword == "" || cfg.IPNSecretKey == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(p.Username), []byte(cfg.IPNUsername)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(p.Password), []byte(cfg.IPNPassword)) != 1 {
		return false
	}
	expectedHash := ComputeNCBAIPNHash(cfg.IPNSecretKey, p)
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(p.Hash)), []byte(expectedHash)) == 1
}

// NCBAAdapter implements the PaymentProvider interface using NCBA Bank's Till STK Push and C2B APIs.
type NCBAAdapter struct {
	cfg    NCBAConfig
	client *Client

	mu          sync.RWMutex
	accessToken string
	tokenExpiry time.Time

	c2bMu          sync.RWMutex
	c2bAccessToken string
	c2bTokenExpiry time.Time
}

// NewNCBA creates an authorized NCBA payment adapter with bounded transport.
func NewNCBA(cfg NCBAConfig, client *Client) (*NCBAAdapter, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://c2bapis.ncbagroup.com"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.PayBillNo == "" {
		cfg.PayBillNo = "880100"
	}
	if client == nil {
		c, err := New(10 * time.Second)
		if err != nil {
			return nil, err
		}
		client = c
	}
	return &NCBAAdapter{
		cfg:    cfg,
		client: client,
	}, nil
}

func (a *NCBAAdapter) Close() {
	if a.client != nil {
		a.client.Close()
	}
}

// getSTKToken returns a valid JWT access token for STK Push, refreshing thread-safely if within 5 minutes of expiration.
func (a *NCBAAdapter) getSTKToken(ctx context.Context) (string, error) {
	a.mu.RLock()
	token := a.accessToken
	expiry := a.tokenExpiry
	a.mu.RUnlock()

	if token != "" && time.Now().Add(5*time.Minute).Before(expiry) {
		return token, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Double check after acquiring write lock
	if a.accessToken != "" && time.Now().Add(5*time.Minute).Before(a.tokenExpiry) {
		return a.accessToken, nil
	}

	tokenURL := a.cfg.BaseURL + "/payments/api/v1/auth/token"
	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	// Basic Auth credentials
	auth := base64.StdEncoding.EncodeToString([]byte(a.cfg.Username + ":" + a.cfg.Password))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Accept", "application/json")

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"` // seconds, e.g. 18000
		Status      int    `json:"status"`
		Message     string `json:"message"`
	}

	if err := a.client.DoJSON(ctx, req, &tokenResp); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return "", ErrProviderTimeout
		}
		return "", fmt.Errorf("token request failed: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", ErrProviderRejected
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 18000 // default 5 hours as documented in NCBA specification
	}

	a.accessToken = tokenResp.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	return a.accessToken, nil
}

// InitiateSTK triggers an STK push prompt on the customer's phone via NCBA Till STK API.
func (a *NCBAAdapter) InitiateSTK(ctx context.Context, req STKInitiateRequest) (*STKInitiateResponse, error) {
	if req.AmountKes <= 0 {
		return nil, ErrInvalidAmount
	}
	phone, err := FormatKenyanPhone(req.Phone)
	if err != nil {
		return nil, err
	}

	token, err := a.getSTKToken(ctx)
	if err != nil {
		return nil, err
	}

	initURL := a.cfg.BaseURL + "/payments/api/v1/stk-push/initiate"

	accountNo := a.cfg.AccountNo
	if accountNo == "" && req.AccountReference != "" {
		accountNo = req.AccountReference
	}

	payload := map[string]string{
		"TelephoneNo":     phone,
		"Amount":          strconv.Itoa(req.AmountKes),
		"PayBillNo":       a.cfg.PayBillNo,
		"AccountNo":       accountNo,
		"Network":         "Safaricom",
		"TransactionType": "CustomerPayBillOnline",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal initiate payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", initURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create initiate request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	var resp struct {
		TransactionID     *string `json:"TransactionID"`
		StatusCode        string  `json:"StatusCode"`
		StatusDescription string  `json:"StatusDescription"`
		ReferenceID       *string `json:"ReferenceID"`
	}

	err = a.client.DoJSON(ctx, httpReq, &resp)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, ErrProviderTimeout
		}
		// Network drop or read timeout during provider call -> outcome is ambiguous (unknown)
		return nil, ErrProviderTimeout
	}

	txID := ""
	if resp.TransactionID != nil {
		txID = *resp.TransactionID
	}
	refID := ""
	if resp.ReferenceID != nil {
		refID = *resp.ReferenceID
	}

	if resp.StatusCode != "0" {
		return &STKInitiateResponse{
			TransactionID:     txID,
			ReferenceID:       refID,
			StatusCode:        resp.StatusCode,
			StatusDescription: resp.StatusDescription,
		}, ErrProviderRejected
	}

	return &STKInitiateResponse{
		TransactionID:     txID,
		ReferenceID:       refID,
		StatusCode:        resp.StatusCode,
		StatusDescription: resp.StatusDescription,
		CustomerMessage:   "Please enter your M-Pesa PIN on your phone to complete payment.",
	}, nil
}

// QuerySTK queries the status of a previous STK push using its TransactionID.
func (a *NCBAAdapter) QuerySTK(ctx context.Context, req STKQueryRequest) (*STKQueryResponse, error) {
	if req.TransactionID == "" {
		return nil, errors.New("transaction ID is required for query")
	}

	token, err := a.getSTKToken(ctx)
	if err != nil {
		return nil, err
	}

	queryURL := a.cfg.BaseURL + "/payments/api/v1/stk-push/query"
	payload := map[string]string{
		"TransactionID": req.TransactionID,
	}
	payloadBytes, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", queryURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	var resp struct {
		Status      string `json:"status"`
		Description string `json:"description"`
	}

	if err := a.client.DoJSON(ctx, httpReq, &resp); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, ErrProviderTimeout
		}
		return nil, ErrProviderUnavailable
	}

	return &STKQueryResponse{
		Status:      resp.Status,
		Description: resp.Description,
	}, nil
}

// QueryReceipt queries transaction details by M-Pesa receipt reference via NCBA C2B Query API.
func (a *NCBAAdapter) QueryReceipt(ctx context.Context, req ReceiptQueryRequest) (*ReceiptQueryResponse, error) {
	if req.MpesaReference == "" {
		return nil, errors.New("mpesa reference is required")
	}

	// 1. Get C2B token
	a.c2bMu.RLock()
	c2bToken := a.c2bAccessToken
	c2bExpiry := a.c2bTokenExpiry
	a.c2bMu.RUnlock()

	if c2bToken == "" || time.Now().Add(5*time.Minute).After(c2bExpiry) {
		a.c2bMu.Lock()
		if a.c2bAccessToken == "" || time.Now().Add(5*time.Minute).After(a.c2bTokenExpiry) {
			c2bTokenURL := a.cfg.BaseURL + "/api/auth/generate-token"
			tokenReq, err := http.NewRequestWithContext(ctx, "GET", c2bTokenURL, nil)
			if err != nil {
				a.c2bMu.Unlock()
				return nil, err
			}
			auth := base64.StdEncoding.EncodeToString([]byte(a.cfg.Username + ":" + a.cfg.Password))
			tokenReq.Header.Set("Authorization", "Basic "+auth)
			var c2bTokenResp struct {
				Message      string `json:"message"`
				ResponseCode string `json:"responseCode"`
				Token        string `json:"token"`
			}
			if err := a.client.DoJSON(ctx, tokenReq, &c2bTokenResp); err != nil {
				a.c2bMu.Unlock()
				return nil, err
			}
			if c2bTokenResp.Token == "" {
				a.c2bMu.Unlock()
				return nil, ErrProviderRejected
			}
			a.c2bAccessToken = c2bTokenResp.Token
			a.c2bTokenExpiry = time.Now().Add(1 * time.Hour)
		}
		c2bToken = a.c2bAccessToken
		a.c2bMu.Unlock()
	}

	// 2. Query transaction
	queryURL := a.cfg.BaseURL + "/api/payments/query"
	shortcode := req.Shortcode
	if shortcode == "" {
		shortcode = a.cfg.PayBillNo
	}

	payload := map[string]string{
		"SHORTCODE":       shortcode,
		"TYPE":            "CORPORATE",
		"MPESA_REFERENCE": req.MpesaReference,
	}
	pBytes, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", queryURL, bytes.NewReader(pBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c2bToken)
	httpReq.Header.Set("Content-Type", "application/json")

	var queryResp struct {
		Message      string `json:"message"`
		Status       int    `json:"status"`
		Transactions []struct {
			Mobile            string  `json:"mobile"`
			TransAmount       float64 `json:"transAmount"`
			TransID           string  `json:"transID"`
			BusinessShortCode string  `json:"businessShortCode"`
			Name              string  `json:"name"`
			TransType         string  `json:"transType"`
			BillRefNumber     string  `json:"billRefNumber"`
			TransTime         string  `json:"transTime"`
		} `json:"transactions"`
	}

	if err := a.client.DoJSON(ctx, httpReq, &queryResp); err != nil {
		return nil, err
	}

	result := &ReceiptQueryResponse{
		Status: queryResp.Status,
	}
	for _, tx := range queryResp.Transactions {
		result.Transactions = append(result.Transactions, ReceiptTransaction{
			TransID:     tx.TransID,
			TransAmount: tx.TransAmount,
			Mobile:      tx.Mobile,
			TransTime:   tx.TransTime,
			BillRef:     tx.BillRefNumber,
			TransType:   tx.TransType,
			Name:        tx.Name,
		})
	}
	return result, nil
}
