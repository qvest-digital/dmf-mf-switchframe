package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKnock_Posts(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "thomas", "Thomas", "", 1)
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 2)

	srv := newServer(d, serverConfig{})
	body := `{"to_agent":"agent-a","body":"pause please"}`
	req := httptest.NewRequest(http.MethodPost, "/api/knock", bytes.NewBufferString(body))
	req.Header.Set("X-SF-Agent", "thomas")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var c int
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='knock'`).Scan(&c)
	if c != 1 {
		t.Fatalf("no knock persisted")
	}
}

func TestClaim_InsertsViaHTTP(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "thomas", "Thomas", "", 1)

	srv := newServer(d, serverConfig{})
	body := `{"item":"BUG-HTTP","intent":"from UI","touches":["server/a.go"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/claim", bytes.NewBufferString(body))
	req.Header.Set("X-SF-Agent", "thomas")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var id string
	d.sqldb.QueryRow(`SELECT item_id FROM claims WHERE agent_id='thomas'`).Scan(&id)
	if id != "BUG-HTTP" {
		t.Fatalf("claim missing")
	}
}

func TestClaim_ConflictReturns409(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "thomas", "Thomas", "", 2)
	_ = d.claim(ctx, "BUG-TAKEN", "agent-a", "", nil, false)

	srv := newServer(d, serverConfig{})
	body := `{"item":"BUG-TAKEN","intent":"me too"}`
	req := httptest.NewRequest(http.MethodPost, "/api/claim", bytes.NewBufferString(body))
	req.Header.Set("X-SF-Agent", "thomas")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d want 409", rec.Code)
	}
}
