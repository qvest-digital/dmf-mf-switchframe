package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_ServesHealthCheck(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: ""})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestServer_RejectsWrongToken(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestServer_AcceptsCorrectToken(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{Token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", rec.Code)
	}
}
