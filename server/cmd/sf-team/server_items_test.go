package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
