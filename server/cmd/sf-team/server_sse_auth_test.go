package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth_TokenQueryParamAcceptedOnGet(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/agents?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET agents with ?token= should succeed, got %d", rec.Code)
	}
}

func TestAuth_TokenQueryParamRejectedOnPost(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/api/say?token=secret", bytes.NewBufferString(`{"body":"x"}`))
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST with ?token= should 401 (mutations require header), got %d", rec.Code)
	}
}

func TestAuth_WrongTokenStill401(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/agents?token=wrong", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token still 401, got %d", rec.Code)
	}
}
