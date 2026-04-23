package main

import (
	"context"
	"testing"
)

func TestKnock_Priority(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "thomas", "Thomas", "", 1)
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 2)

	if err := d.knock(ctx, "thomas", "agent-a", "pause when you tick"); err != nil {
		t.Fatal(err)
	}

	var kind, priority, mentions string
	d.sqldb.QueryRow(`SELECT kind, priority, mentions FROM messages WHERE thread='global' AND kind='knock'`).Scan(&kind, &priority, &mentions)
	if kind != "knock" || priority != "high" {
		t.Fatalf("kind=%q priority=%q", kind, priority)
	}
	if mentions != `["agent-a"]` {
		t.Fatalf("mentions=%q", mentions)
	}
}
