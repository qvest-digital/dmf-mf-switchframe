package main

import (
	"context"
	"testing"
)

func TestDone_EmitsDoneMessageAndReleases(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-090", "agent-a", "", nil, false)

	if err := d.done(ctx, "BUG-090", "agent-a", "shipped in commit abcdef"); err != nil {
		t.Fatal(err)
	}

	var c int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='done' AND thread='BUG-090'`).Scan(&c)
	if c != 1 {
		t.Fatalf("expected 1 done msg, got %d", c)
	}

	var outcome string
	_ = d.sqldb.QueryRow(`SELECT outcome FROM claim_history WHERE item_id='BUG-090'`).Scan(&outcome)
	if outcome != "done" {
		t.Fatalf("outcome=%q", outcome)
	}
}

func TestBlocked_EmitsBlockedMessageAndReleases(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-091", "agent-a", "", nil, false)

	if err := d.blocked(ctx, "BUG-091", "agent-a", "waiting on hw decoder"); err != nil {
		t.Fatal(err)
	}

	var c int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='blocked' AND thread='BUG-091'`).Scan(&c)
	if c != 1 {
		t.Fatalf("expected 1 blocked msg, got %d", c)
	}
}

func TestDone_AtomicOnReleaseFailure(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.done(ctx, "BUG-092", "agent-a", "summary"); err == nil {
		t.Fatal("expected done to fail on unclaimed item")
	}

	var c int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='done' AND thread='BUG-092'`).Scan(&c)
	if c != 0 {
		t.Fatalf("done message leaked despite release failure: count=%d", c)
	}
}
