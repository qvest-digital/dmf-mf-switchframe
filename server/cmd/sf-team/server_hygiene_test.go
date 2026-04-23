package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_RejectsWrongMethod(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/agents", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d want 405", rec.Code)
	}
}
