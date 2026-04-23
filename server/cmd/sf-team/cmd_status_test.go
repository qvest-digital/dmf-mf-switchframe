package main

import (
	"context"
	"strings"
	"testing"
)

func TestStatus_ItemScoped(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-950", "agent-a", "working", nil, false)
	_ = d.say(ctx, "agent-a", "BUG-950", "say", "red test passed", nil, "normal")

	var sb strings.Builder
	if err := d.writeItemStatus(ctx, &sb, "BUG-950"); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "agent-a") || !strings.Contains(out, "red test passed") {
		t.Fatalf("out=%s", out)
	}
}
