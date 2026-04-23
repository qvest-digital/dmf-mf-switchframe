package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProgress_HistoryByID(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.reportProgress(ctx, "agent-a", "BUG-P", 30, "")
	_ = d.reportProgress(ctx, "agent-a", "BUG-P", 60, "half")

	srv := newServer(d, serverConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/progress/BUG-P", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("body=%v", body)
	}
}
