package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestItems_ListsFromTestdata(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{ItemsDir: "testdata/items"})

	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var body []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) < 2 {
		t.Fatalf("expected at least 2 fixtures, got %d", len(body))
	}
}

func TestItems_SingleByID(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{ItemsDir: "testdata/items"})

	req := httptest.NewRequest(http.MethodGet, "/api/items/BUG-TEST-001", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["id"] != "BUG-TEST-001" {
		t.Fatalf("id=%v", body["id"])
	}
	if body["ac_total"] == nil {
		t.Fatalf("ac_total missing")
	}
}

func TestItemDetail_ReturnsBodyACDepsAndClaim(t *testing.T) {
	d, _ := newTestDB(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "BUG-500.md")
	content := `---
id: BUG-500
title: thing
priority: P0
status: in_progress
blocked-by: [BUG-100]
relates-to: [DEBT-001]
---

## Problem

Do the thing.

## Acceptance criteria

- [x] one
- [ ] two
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp", 1)
	_ = d.claim(ctx, "BUG-500", "agent-a", "working on it", nil, false)

	srv := newServer(d, serverConfig{ItemsDir: dir})
	req := httptest.NewRequest(http.MethodGet, "/api/items/BUG-500", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["body_markdown"].(string) == "" || !strings.Contains(body["body_markdown"].(string), "## Problem") {
		t.Fatalf("body_markdown=%q", body["body_markdown"])
	}
	ac := body["ac"].([]any)
	if len(ac) != 2 || ac[0].(map[string]any)["text"] != "one" || ac[0].(map[string]any)["checked"] != true {
		t.Fatalf("ac=%v", ac)
	}
	if body["blocked_by"].([]any)[0] != "BUG-100" {
		t.Fatalf("blocked_by=%v", body["blocked_by"])
	}
	cc := body["current_claim"].(map[string]any)
	if cc["agent_id"] != "agent-a" || cc["intent"] != "working on it" {
		t.Fatalf("current_claim=%v", cc)
	}
}

func TestItemDetail_NoClaim_CurrentClaimIsNull(t *testing.T) {
	d, _ := newTestDB(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "BUG-600.md")
	if err := os.WriteFile(p, []byte("---\nid: BUG-600\ntitle: t\npriority: P2\nstatus: open\n---\n\nbody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newServer(d, serverConfig{ItemsDir: dir})
	req := httptest.NewRequest(http.MethodGet, "/api/items/BUG-600", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["current_claim"] != nil {
		t.Fatalf("current_claim should be null; got %v", body["current_claim"])
	}
}
