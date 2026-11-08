package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

func TestTrustedProxyCheckerAndSafeClientIP(t *testing.T) {
	checker := NewTrustedProxyChecker("10.0.0.0/8,172.16.0.0/12")

	// Loopback is always trusted
	if !checker.IsTrusted("127.0.0.1") || !checker.IsTrusted("::1") {
		t.Errorf("expected loopback to be trusted")
	}
	// Configured CIDRs
	if !checker.IsTrusted("10.1.2.3") || !checker.IsTrusted("172.16.5.6") {
		t.Errorf("expected configured CIDRs to be trusted")
	}
	// Untrusted public IP
	if checker.IsTrusted("203.0.113.195") {
		t.Errorf("expected public IP to be untrusted")
	}

	// 1. Untrusted caller trying to spoof XFF must be ignored
	spoofedReq := httptest.NewRequest("GET", "/catalog", nil)
	spoofedReq.RemoteAddr = "198.51.100.5:4321"
	spoofedReq.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	ip := SafeClientIP(spoofedReq, checker)
	if ip != "198.51.100.5" {
		t.Fatalf("expected untrusted remote addr to be used, got %s", ip)
	}

	// 2. Trusted proxy caller forwards client IP via XFF
	trustedReq := httptest.NewRequest("GET", "/catalog", nil)
	trustedReq.RemoteAddr = "127.0.0.1:4321"
	trustedReq.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")
	ip = SafeClientIP(trustedReq, checker)
	if ip != "203.0.113.50" {
		t.Fatalf("expected client IP from XFF via trusted proxy, got %s", ip)
	}
}

func TestExtractGuestToken(t *testing.T) {
	raw, _, err := orders.GenerateGuestToken()
	if err != nil {
		t.Fatal(err)
	}

	// Header X-Guest-Token
	req1 := httptest.NewRequest("GET", "/orders/123", nil)
	req1.Header.Set("X-Guest-Token", raw)
	if ExtractGuestToken(req1) != raw {
		t.Errorf("expected extracted token %s, got %s", raw, ExtractGuestToken(req1))
	}

	// Authorization: Bearer
	req2 := httptest.NewRequest("GET", "/orders/123", nil)
	req2.Header.Set("Authorization", "Bearer "+raw)
	if ExtractGuestToken(req2) != raw {
		t.Errorf("expected extracted token %s, got %s", raw, ExtractGuestToken(req2))
	}

	// Unrelated auth header
	req3 := httptest.NewRequest("GET", "/orders/123", nil)
	req3.Header.Set("Authorization", "Bearer other-token")
	if ExtractGuestToken(req3) != "" {
		t.Errorf("expected empty token for non-gt token, got %s", ExtractGuestToken(req3))
	}
}

func TestAuthorizeOrderAccess(t *testing.T) {
	ctx := context.Background()
	orderID := "ord-test-auth"
	validToken, _, _ := orders.GenerateGuestToken()

	mockStore := &mockOrderService{
		validateAccessFn: func(ctx context.Context, id, token string) (bool, error) {
			if id == orderID && token == validToken {
				return true, nil
			}
			return false, nil
		},
	}

	testMgrSecret := "test-mgr-secret-key-123"

	// 1a. Unauthenticated manager request (only X-Manager-Id without secret) fails
	unauthMgrReq := httptest.NewRequest("GET", "/orders/"+orderID, nil)
	unauthMgrReq.Header.Set("X-Manager-Id", "manager-1")
	ok, err := AuthorizeOrderAccess(ctx, unauthMgrReq, orderID, mockStore, testMgrSecret)
	if err != nil || ok {
		t.Fatalf("expected unauthenticated manager request to be rejected")
	}

	// 1b. Manager with invalid secret fails
	badSecReq := httptest.NewRequest("GET", "/orders/"+orderID, nil)
	badSecReq.Header.Set("X-Manager-Id", "manager-1")
	badSecReq.Header.Set("Authorization", "Bearer wrong-secret")
	ok, err = AuthorizeOrderAccess(ctx, badSecReq, orderID, mockStore, testMgrSecret)
	if err != nil || ok {
		t.Fatalf("expected manager request with invalid secret to be rejected")
	}

	// 1c. Manager with valid secret succeeds
	mgrReq := httptest.NewRequest("GET", "/orders/"+orderID, nil)
	mgrReq.Header.Set("X-Manager-Id", "manager-1")
	mgrReq.Header.Set("Authorization", "Bearer "+testMgrSecret)
	ok, err = AuthorizeOrderAccess(ctx, mgrReq, orderID, mockStore, testMgrSecret)
	if err != nil || !ok {
		t.Fatalf("expected authenticated manager access to be authorized")
	}

	// 2. Valid guest token is authorized
	guestReq := httptest.NewRequest("GET", "/orders/"+orderID, nil)
	guestReq.Header.Set("X-Guest-Token", validToken)
	ok, err = AuthorizeOrderAccess(ctx, guestReq, orderID, mockStore, testMgrSecret)
	if err != nil || !ok {
		t.Fatalf("expected valid guest token to be authorized")
	}

	// 3. Invalid guest token is rejected
	badGuestReq := httptest.NewRequest("GET", "/orders/"+orderID, nil)
	badGuestReq.Header.Set("X-Guest-Token", "gt_0000000000000000000000000000000000000000000000000000000000000000")
	ok, err = AuthorizeOrderAccess(ctx, badGuestReq, orderID, mockStore, testMgrSecret)
	if err != nil || ok {
		t.Fatalf("expected invalid guest token to be rejected")
	}

	// 4. Missing token is rejected
	emptyReq := httptest.NewRequest("GET", "/orders/"+orderID, nil)
	ok, err = AuthorizeOrderAccess(ctx, emptyReq, orderID, mockStore, testMgrSecret)
	if err != nil || ok {
		t.Fatalf("expected missing token to be rejected")
	}

	// 5. Guest bearer token for Order A CANNOT bypass manager check to access Order B
	bearerGuestReq := httptest.NewRequest("GET", "/orders/other-order", nil)
	bearerGuestReq.Header.Set("Authorization", "Bearer "+validToken)
	ok, err = AuthorizeOrderAccess(ctx, bearerGuestReq, "other-order", mockStore, testMgrSecret)
	if err != nil || ok {
		t.Fatalf("critical vulnerability: guest bearer token bypassed order scoping and acted as manager!")
	}
}
