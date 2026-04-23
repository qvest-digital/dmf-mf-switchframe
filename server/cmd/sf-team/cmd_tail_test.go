package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTailOneShot_RespectsSinceAndThread(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	t0 := time.Unix(1000, 0)
	d.now = func() time.Time { return t0 }
	_ = d.say(ctx, "agent-a", "global", "say", "old", nil, "normal")
	d.now = func() time.Time { return t0.Add(5 * time.Minute) }
	_ = d.say(ctx, "agent-a", "global", "say", "new", nil, "normal")
	_ = d.say(ctx, "agent-a", "BUG-1000", "say", "item chat", nil, "normal")

	var sb strings.Builder
	if err := d.tailOneShot(ctx, &sb, "global", t0.Add(2*time.Minute).Unix(), nil); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "new") || strings.Contains(out, "old") || strings.Contains(out, "item chat") {
		t.Fatalf("out=%s", out)
	}
}
