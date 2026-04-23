package main

import (
	"context"
	"testing"
)

func TestProgress_StoresLatest(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.reportProgress(ctx, "agent-a", "BUG-400", 30, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.reportProgress(ctx, "agent-a", "BUG-400", 60, "RED done"); err != nil {
		t.Fatal(err)
	}

	pct, note := d.latestProgress(ctx, "BUG-400")
	if pct != 60 || note != "RED done" {
		t.Fatalf("pct=%d note=%q", pct, note)
	}
}

func TestProgress_RejectsOutOfRange(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	if err := d.reportProgress(ctx, "agent-a", "BUG-400", 101, ""); err == nil {
		t.Fatal("expected range error")
	}
	if err := d.reportProgress(ctx, "agent-a", "BUG-400", -1, ""); err == nil {
		t.Fatal("expected range error")
	}
}
