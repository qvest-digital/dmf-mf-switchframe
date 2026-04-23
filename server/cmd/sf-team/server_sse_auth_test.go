package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth_TokenQueryParamOnlyForEvents(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/agents?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("agents with ?token= should be 401, got %d", rec.Code)
	}
}
