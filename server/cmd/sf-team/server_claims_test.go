package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaims_ListsActive(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-1000", "agent-a", "intent", nil, false)

	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/claims", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 1 || body[0]["item_id"] != "BUG-1000" {
		t.Fatalf("body=%v", body)
	}
}

func TestTouches_ListsActive(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_, _ = d.touch(ctx, "agent-a", "server/x.go", "BUG-1001")

	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/touches?active=true", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 1 {
		t.Fatalf("body=%v", body)
	}
}
