package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyFailureIsSafe(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
	response := httptest.NewRecorder()
	handler().ServeHTTP(response, request)
	if response.Code != 502 || response.Body.String() != "API unavailable\n" {
		t.Fatalf("unsafe proxy failure: %d %s", response.Code, response.Body)
	}
}
