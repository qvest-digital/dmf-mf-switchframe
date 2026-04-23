package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func setActivityNow(d *db, ts int64) {
	d.now = func() time.Time { return time.Unix(ts, 0) }
}

func TestItemActivity_MergedAndNewestFirst(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	if err := d.register(ctx, "agent-a", "A", "/tmp/a", 1); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := d.register(ctx, "agent-b", "B", "/tmp/b", 2); err != nil {
		t.Fatalf("register b: %v", err)
	}

	setActivityNow(&d, 100)
	if err := d.claim(ctx, "BUG-700", "agent-a", "initial", nil, false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	setActivityNow(&d, 110)
	if _, err := d.touch(ctx, "agent-a", "server/x.go", "BUG-700"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	setActivityNow(&d, 120)
	if err := d.reportProgress(ctx, "agent-a", "BUG-700", 50, "halfway"); err != nil {
		t.Fatalf("progress: %v", err)
	}
	setActivityNow(&d, 130)
	if err := d.say(ctx, "agent-a", "BUG-700", "say", "a note", nil, "normal"); err != nil {
		t.Fatalf("say: %v", err)
	}
	setActivityNow(&d, 140)
	if err := d.release(ctx, "BUG-700", "agent-a", "released"); err != nil {
		t.Fatalf("release: %v", err)
	}

	srv := newServer(d, serverConfig{ItemsDir: t.TempDir()})
	req := httptest.NewRequest(http.MethodGet, "/api/items/BUG-700/activity", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}

	var events []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(events) < 5 {
		t.Fatalf("got %d events, want >= 5: %v", len(events), events)
	}
	var prev float64 = 1 << 62
	for _, e := range events {
		ts, _ := e["ts"].(float64)
		if ts > prev {
			t.Fatalf("events not newest-first: ts=%v prev=%v events=%v", ts, prev, events)
		}
		prev = ts
	}
	kinds := map[string]bool{}
	for _, e := range events {
		kinds[e["kind"].(string)] = true
	}
	for _, want := range []string{"claim", "release", "progress", "touch", "say"} {
		if !kinds[want] {
			t.Fatalf("missing kind %q in events: %v (full: %v)", want, kinds, events)
		}
	}
}

func TestItemActivity_BeforeCursorPaginates(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	if err := d.register(ctx, "a", "A", "/tmp/a", 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	for i, ts := range []int64{100, 110, 120, 130, 140} {
		setActivityNow(&d, ts)
		if err := d.reportProgress(ctx, "a", "BUG-800", i*10, ""); err != nil {
			t.Fatalf("progress ts=%d: %v", ts, err)
		}
	}

	srv := newServer(d, serverConfig{ItemsDir: t.TempDir()})

	req := httptest.NewRequest(http.MethodGet, "/api/items/BUG-800/activity?limit=2", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	var page1 []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &page1); err != nil {
		t.Fatalf("page1 decode: %v body=%s", err, rec.Body.String())
	}
	if len(page1) != 2 || page1[0]["ts"].(float64) != 140 || page1[1]["ts"].(float64) != 130 {
		t.Fatalf("page1=%v", page1)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/items/BUG-800/activity?limit=2&before=130", nil)
	rec2 := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec2, req2)
	var page2 []map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("page2 decode: %v body=%s", err, rec2.Body.String())
	}
	if len(page2) != 2 || page2[0]["ts"].(float64) != 120 || page2[1]["ts"].(float64) != 110 {
		t.Fatalf("page2=%v", page2)
	}
}
