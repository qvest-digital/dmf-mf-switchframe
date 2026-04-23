package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgents_ReturnsRegistered(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "Alice", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "Bob", "/tmp/b", 2)

	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("got %d agents", len(body))
	}
}
