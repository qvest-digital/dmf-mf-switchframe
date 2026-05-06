package main

import (
	"context"
	"testing"
	"time"
)

func TestReclaim_ExpiresAfter30Min(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	baseTime := time.Unix(1000000, 0)
	d.now = func() time.Time { return baseTime }

	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-200", "agent-a", "", nil, false)

	d.now = func() time.Time { return baseTime.Add(31 * time.Minute) }

	reclaimed, err := d.reclaimStale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0] != "BUG-200" {
		t.Fatalf("reclaimed=%v", reclaimed)
	}

	var c int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM claims WHERE item_id='BUG-200'`).Scan(&c)
	if c != 0 {
		t.Fatalf("claim still present")
	}
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM claim_history WHERE item_id='BUG-200' AND outcome='reclaimed'`).Scan(&c)
	if c != 1 {
		t.Fatalf("history missing")
	}
}

func TestReclaim_LongClaimExpiresAfter2h(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	baseTime := time.Unix(1000000, 0)
	d.now = func() time.Time { return baseTime }

	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-201", "agent-a", "", nil, true)

	d.now = func() time.Time { return baseTime.Add(31 * time.Minute) }
	reclaimed, _ := d.reclaimStale(ctx)
	if len(reclaimed) != 0 {
		t.Fatalf("long claim reclaimed too early: %v", reclaimed)
	}

	d.now = func() time.Time { return baseTime.Add(121 * time.Minute) }
	reclaimed, _ = d.reclaimStale(ctx)
	if len(reclaimed) != 1 {
		t.Fatalf("long claim not reclaimed after 2h")
	}
}
