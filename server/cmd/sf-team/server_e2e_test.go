package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestE2E_FullSPAFlow(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "thomas", "Thomas", "", 1)

	srv := newServer(d, serverConfig{ItemsDir: "testdata/items"})
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	doJSON := func(method, path string, body any) *http.Response {
		var b io.Reader
		if body != nil {
			buf, _ := json.Marshal(body)
			b = bytes.NewReader(buf)
		}
		req, _ := http.NewRequest(method, ts.URL+path, b)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SF-Agent", "thomas")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	r := doJSON(http.MethodPost, "/api/claim", map[string]any{"item": "BUG-E2E", "intent": "from UI"})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("claim: %d", r.StatusCode)
	}

	r = doJSON(http.MethodPost, "/api/say", map[string]any{"thread": "BUG-E2E", "body": "starting"})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("say: %d", r.StatusCode)
	}

	r = doJSON(http.MethodPost, "/api/progress", map[string]any{"item": "BUG-E2E", "pct": 50})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("progress: %d", r.StatusCode)
	}

	r = doJSON(http.MethodPost, "/api/release", map[string]any{"item": "BUG-E2E", "outcome": "done"})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("release: %d", r.StatusCode)
	}

	r = doJSON(http.MethodGet, "/api/claims", nil)
	var claims []map[string]any
	_ = json.NewDecoder(r.Body).Decode(&claims)
	for _, c := range claims {
		if c["item_id"] == "BUG-E2E" {
			t.Fatal("claim still active")
		}
	}
}
