package main

import (
	"context"
	"testing"
)

func TestWho_ListsActiveAgentsWithClaims(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "Alice", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "Bob", "/tmp/b", 2)
	_ = d.claim(ctx, "BUG-900", "agent-a", "investigating", nil, false)

	rows, err := d.whoList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
}
