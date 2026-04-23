package main

import (
	"context"
	"testing"
)

func TestIntegration_RegisterClaimTouchSayReleaseDone(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	if err := d.register(ctx, "agent-a", "A", "/tmp/a", 1); err != nil {
		t.Fatal(err)
	}
	if err := d.register(ctx, "agent-b", "B", "/tmp/b", 2); err != nil {
		t.Fatal(err)
	}

	if err := d.claim(ctx, "BUG-INT", "agent-a", "shipping fix", []string{"server/x.go"}, false); err != nil {
		t.Fatal(err)
	}

	if err := d.claim(ctx, "BUG-INT", "agent-b", "me too", nil, false); err == nil {
		t.Fatal("expected claim conflict")
	}

	if err := d.ask(ctx, "agent-b", "BUG-INT", "agent-a", "ETA?"); err != nil {
		t.Fatal(err)
	}

	dg, err := d.tick(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(dg.Mentions)+len(dg.YourThreads) == 0 {
		t.Fatalf("agent-a should see agent-b's ask: %+v", dg)
	}

	if err := d.reportProgress(ctx, "agent-a", "BUG-INT", 60, "red done"); err != nil {
		t.Fatal(err)
	}
	if err := d.say(ctx, "agent-a", "BUG-INT", "answer", "~15 min", nil, "normal"); err != nil {
		t.Fatal(err)
	}

	if err := d.done(ctx, "BUG-INT", "agent-a", "landed abc123"); err != nil {
		t.Fatal(err)
	}

	var c int
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM claims WHERE item_id='BUG-INT'`).Scan(&c)
	if c != 0 {
		t.Fatalf("claim still active")
	}
	var outcome string
	d.sqldb.QueryRow(`SELECT outcome FROM claim_history WHERE item_id='BUG-INT'`).Scan(&outcome)
	if outcome != "done" {
		t.Fatalf("outcome=%q", outcome)
	}
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM touches WHERE item_id='BUG-INT' AND released_at IS NULL`).Scan(&c)
	if c != 0 {
		t.Fatalf("touches still open: %d", c)
	}
}
