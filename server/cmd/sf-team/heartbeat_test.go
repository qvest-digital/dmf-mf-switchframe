package main

import (
	"context"
	"testing"
	"time"
)

func TestHeartbeat_BumpsAgentAndOwnClaims(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	t0 := time.Unix(1000, 0)
	d.now = func() time.Time { return t0 }
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-500", "agent-a", "", nil, false)

	t1 := t0.Add(5 * time.Minute)
	d.now = func() time.Time { return t1 }
	lost, err := d.heartbeat(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 0 {
		t.Fatalf("lost=%v", lost)
	}

	var agentTick, claimTouch int64
	_ = d.sqldb.QueryRow(`SELECT last_tick_at FROM agents WHERE id='agent-a'`).Scan(&agentTick)
	_ = d.sqldb.QueryRow(`SELECT last_touch FROM claims WHERE item_id='BUG-500'`).Scan(&claimTouch)
	if agentTick != t1.Unix() || claimTouch != t1.Unix() {
		t.Fatalf("tick=%d claim=%d want %d", agentTick, claimTouch, t1.Unix())
	}
}

func TestHeartbeat_ReportsLostClaim(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	t0 := time.Unix(1000, 0)
	d.now = func() time.Time { return t0 }
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-501", "agent-a", "", nil, false)

	d.now = func() time.Time { return t0.Add(31 * time.Minute) }
	_, _ = d.reclaimStale(ctx)

	lost, err := d.heartbeat(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0] != "BUG-501" {
		t.Fatalf("lost=%v want [BUG-501]", lost)
	}
}
