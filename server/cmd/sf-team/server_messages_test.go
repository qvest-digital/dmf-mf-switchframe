package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMessages_FiltersByThread(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.say(ctx, "agent-a", "global", "say", "g1", nil, "normal")
	_ = d.say(ctx, "agent-a", "BUG-X", "say", "item1", nil, "normal")

	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/messages?thread=BUG-X", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 1 || body[0]["body"] != "item1" {
		t.Fatalf("body=%v", body)
	}
}

func TestMessages_RespectsLimit(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	for i := 0; i < 5; i++ {
		_ = d.say(ctx, "agent-a", "global", "say", "m", nil, "normal")
	}

	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/messages?thread=global&limit=2", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("limit honored: got %d", len(body))
	}
}
