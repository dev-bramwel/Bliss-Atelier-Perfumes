package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFixtureModes(t *testing.T) {
	for _, scenario := range []struct {
		mode, body string
		code       int
	}{{"", `{"amount":2500}`, 200}, {"reject", `{"amount":2500}`, 422}, {"malformed", `{"amount":2500}`, 200}, {"", `{"amount":-1}`, 400}, {"", `{"amount":2500,"phone":"not accepted"}`, 400}} {
		request := httptest.NewRequest("POST", "/payments", strings.NewReader(scenario.body))
		request.Header.Set("X-Fixture-Mode", scenario.mode)
		response := httptest.NewRecorder()
		handler().ServeHTTP(response, request)
		if response.Code != scenario.code {
			t.Fatalf("mode %s got %d", scenario.mode, response.Code)
		}
	}
}
