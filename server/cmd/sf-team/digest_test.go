package main

import (
	"context"
	"testing"
)

func TestDigest_IncludesMentions(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)

	_ = d.say(ctx, "agent-b", "global", "say", "@agent-a heads up", []string{"agent-a"}, "normal")
	_ = d.say(ctx, "agent-b", "global", "say", "unrelated", nil, "normal")

	dg, err := d.digestFor(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(dg.Mentions) != 1 {
		t.Fatalf("mentions=%d want 1", len(dg.Mentions))
	}
	if dg.Mentions[0].Body != "@agent-a heads up" {
		t.Fatalf("body=%q", dg.Mentions[0].Body)
	}
}

func TestDigest_IncludesYourItemThreads(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)
	_ = d.claim(ctx, "BUG-600", "agent-a", "", nil, false)

	_, _ = d.sqldb.Exec(`INSERT OR REPLACE INTO reads (agent_id, thread, last_msg_id)
		SELECT 'agent-a', 'BUG-600', COALESCE(MAX(id), 0) FROM messages WHERE thread='BUG-600'`)

	_ = d.say(ctx, "agent-b", "BUG-600", "say", "found something", nil, "normal")

	dg, err := d.digestFor(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(dg.YourThreads) != 1 {
		t.Fatalf("your-threads=%d", len(dg.YourThreads))
	}
}

func TestDigest_SurfacesHighPriorityFirst(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "thomas", "T", "", 1)
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 2)

	_ = d.say(ctx, "agent-a", "global", "say", "unrelated 1", nil, "normal")
	_ = d.knock(ctx, "thomas", "agent-a", "pause when you tick")
	_ = d.say(ctx, "agent-a", "global", "say", "unrelated 2", nil, "normal")

	dg, _ := d.digestFor(ctx, "agent-a")
	if len(dg.Knocks) != 1 {
		t.Fatalf("knocks=%d", len(dg.Knocks))
	}
}

func TestDigest_ListsActiveClaimsAndConflicts(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)
	_ = d.claim(ctx, "BUG-700", "agent-a", "", nil, false)
	_ = d.claim(ctx, "BUG-701", "agent-b", "", nil, false)
	_, _ = d.touch(ctx, "agent-a", "shared.go", "")
	_, _ = d.touch(ctx, "agent-b", "shared.go", "")

	dg, _ := d.digestFor(ctx, "agent-a")
	if len(dg.ActiveClaims) != 2 {
		t.Fatalf("active=%d want 2", len(dg.ActiveClaims))
	}
	if len(dg.FileConflicts) != 1 || dg.FileConflicts[0] != "shared.go" {
		t.Fatalf("conflicts=%v", dg.FileConflicts)
	}
}
