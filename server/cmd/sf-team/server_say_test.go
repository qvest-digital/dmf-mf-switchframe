package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSay_InsertsMessage(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "thomas", "Thomas", "", 1)

	srv := newServer(d, serverConfig{})
	body := `{"thread":"global","body":"from UI"}`
	req := httptest.NewRequest(http.MethodPost, "/api/say", bytes.NewBufferString(body))
	req.Header.Set("X-SF-Agent", "thomas")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}

	var c int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='say' AND body='from UI'`).Scan(&c)
	if c != 1 {
		t.Fatalf("message not persisted")
	}
}
