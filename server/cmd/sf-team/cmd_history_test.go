package main

import (
	"context"
	"testing"
)

func TestHistory_IncludesClaimsAndMessages(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-990", "agent-a", "first", nil, false)
	_ = d.say(ctx, "agent-a", "BUG-990", "say", "progress note", nil, "normal")
	_ = d.release(ctx, "BUG-990", "agent-a", "done")

	entries, err := d.historyFor(ctx, "BUG-990")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 3 {
		t.Fatalf("entries=%d", len(entries))
	}
}
