package main

import (
	"context"
	"testing"
)

func TestSay_DefaultsToGlobal(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.say(ctx, "agent-a", "global", "say", "hello team", nil, "normal"); err != nil {
		t.Fatal(err)
	}

	var c int
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='say' AND thread='global'`).Scan(&c)
	if c != 1 {
		t.Fatalf("count=%d", c)
	}
}

func TestSay_PersistsMentions(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.say(ctx, "agent-a", "global", "say", "@agent-b heads up", []string{"agent-b"}, "normal"); err != nil {
		t.Fatal(err)
	}

	var mentions string
	d.sqldb.QueryRow(`SELECT mentions FROM messages WHERE thread='global' AND kind='say'`).Scan(&mentions)
	if mentions != `["agent-b"]` {
		t.Fatalf("mentions=%q", mentions)
	}
}
