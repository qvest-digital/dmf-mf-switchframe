package main

import (
	"context"
	"testing"
)

func TestTick_UpdatesReadsPointer(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)

	_, _ = d.sqldb.Exec(`INSERT OR REPLACE INTO reads (agent_id, thread, last_msg_id)
		SELECT 'agent-a', 'global', COALESCE(MAX(id), 0) FROM messages WHERE thread='global'`)

	_ = d.say(ctx, "agent-b", "global", "say", "hi", nil, "normal")

	dg, _ := d.tick(ctx, "agent-a")
	if len(dg.Global) != 1 {
		t.Fatalf("first tick: global=%d want 1", len(dg.Global))
	}

	dg, _ = d.tick(ctx, "agent-a")
	if len(dg.Global) != 0 {
		t.Fatalf("second tick: global=%d want 0", len(dg.Global))
	}
}
