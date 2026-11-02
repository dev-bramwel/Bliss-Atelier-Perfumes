package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyFailureIsSafe(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
	response := httptest.NewRecorder()
	handler(nil).ServeHTTP(response, request)
	if response.Code != 502 {
		t.Fatalf("expected 502, got %d", response.Code)
	}
	body := response.Body.String()
	if body != `{"error":"API unavailable"}` && body != `{"error":"API unavailable"}`+"\n" {
		t.Fatalf("unexpected body: %s", body)
	}
}
