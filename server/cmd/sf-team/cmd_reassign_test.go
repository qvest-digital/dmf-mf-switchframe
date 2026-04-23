package main

import (
	"context"
	"testing"
)

func TestReassign_PostsDirectiveAndReleases(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)
	_ = d.claim(ctx, "BUG-100", "agent-a", "", nil, false)

	if err := d.reassign(ctx, "BUG-100", "agent-a", "agent-b"); err != nil {
		t.Fatal(err)
	}

	if err := d.claim(ctx, "BUG-100", "agent-b", "taking over", nil, false); err != nil {
		t.Fatalf("agent-b should be able to claim: %v", err)
	}

	var mentions string
	d.sqldb.QueryRow(`SELECT mentions FROM messages WHERE kind='say' AND thread='global' ORDER BY id DESC LIMIT 1`).Scan(&mentions)
	if mentions != `["agent-b"]` {
		t.Fatalf("mentions=%q want [\"agent-b\"]", mentions)
	}
}
