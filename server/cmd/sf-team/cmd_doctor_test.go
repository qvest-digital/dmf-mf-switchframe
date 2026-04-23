package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDoctor_FlagsStaleClaims(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	t0 := time.Unix(1000, 0)
	d.now = func() time.Time { return t0 }
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-X", "agent-a", "", nil, false)
	d.now = func() time.Time { return t0.Add(45 * time.Minute) }

	findings, err := d.doctor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "BUG-X") {
		t.Fatalf("no stale claim flagged: %s", joined)
	}
}
