package main

import (
	"context"
	"errors"
	"testing"
)

func TestRelease_MovesToHistory(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-050", "agent-a", "", nil, false)

	if err := d.release(ctx, "BUG-050", "agent-a", "done"); err != nil {
		t.Fatalf("release: %v", err)
	}

	var c int
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM claims WHERE item_id='BUG-050'`).Scan(&c)
	if c != 0 {
		t.Fatalf("claim still active, count=%d", c)
	}

	var outcome string
	d.sqldb.QueryRow(`SELECT outcome FROM claim_history WHERE item_id='BUG-050'`).Scan(&outcome)
	if outcome != "done" {
		t.Fatalf("outcome=%q want done", outcome)
	}
}

func TestRelease_RejectsNotYours(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)
	_ = d.claim(ctx, "BUG-051", "agent-a", "", nil, false)

	err := d.release(ctx, "BUG-051", "agent-b", "released")
	if !errors.Is(err, errNotYours) {
		t.Fatalf("want errNotYours, got %v", err)
	}
}

func TestRelease_UntouchesAllForItem(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-052", "agent-a", "", []string{"server/a.go", "server/b.go"}, false)

	if err := d.release(ctx, "BUG-052", "agent-a", "released"); err != nil {
		t.Fatal(err)
	}

	var active int
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM touches WHERE item_id='BUG-052' AND released_at IS NULL`).Scan(&active)
	if active != 0 {
		t.Fatalf("active touches=%d want 0", active)
	}
}

func TestRelease_RejectsNeverClaimed(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	err := d.release(ctx, "NEVER-EXISTED", "agent-a", "done")
	if !errors.Is(err, errNotClaimed) {
		t.Fatalf("want errNotClaimed, got %v", err)
	}
}

func TestRelease_RollbackOnClosedDB(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-099", "agent-a", "", nil, false)

	d.sqldb.Close()
	if err := d.release(ctx, "BUG-099", "agent-a", "done"); err == nil {
		t.Fatal("expected release to fail against a closed DB")
	}
}
