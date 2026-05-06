package main

import (
	"context"
	"strings"
	"testing"
)

func TestAsk_RequiresMention(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.ask(ctx, "agent-a", "global", "agent-b", "why pick 30 threshold?"); err != nil {
		t.Fatal(err)
	}

	var kind, mentions string
	_ = d.sqldb.QueryRow(`SELECT kind, mentions FROM messages WHERE thread='global' AND kind='ask'`).Scan(&kind, &mentions)
	if kind != "ask" {
		t.Fatalf("kind=%q", kind)
	}
	if !strings.Contains(mentions, "agent-b") {
		t.Fatalf("mentions=%q", mentions)
	}
}

func TestAnswer_PrependsReRef(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.say(ctx, "agent-a", "global", "ask", "first", nil, "normal")

	if err := d.answer(ctx, "agent-a", 1, "my reply"); err != nil {
		t.Fatal(err)
	}

	var body string
	_ = d.sqldb.QueryRow(`SELECT body FROM messages WHERE kind='answer'`).Scan(&body)
	if !strings.HasPrefix(body, "re:1 ") {
		t.Fatalf("body=%q expected prefix re:1", body)
	}
}

func TestReviewRequest_Kind(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.reviewRequest(ctx, "agent-a", "BUG-300", "agent-c"); err != nil {
		t.Fatal(err)
	}

	var kind, mentions string
	_ = d.sqldb.QueryRow(`SELECT kind, mentions FROM messages WHERE thread='BUG-300'`).Scan(&kind, &mentions)
	if kind != "review_req" {
		t.Fatalf("kind=%q", kind)
	}
	if !strings.Contains(mentions, "agent-c") {
		t.Fatalf("mentions=%q", mentions)
	}
}
