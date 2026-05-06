package main

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestClaim_FirstAgentWins(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)

	if err := d.claim(ctx, "BUG-029", "agent-a", "ship the fix", nil, false); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	err := d.claim(ctx, "BUG-029", "agent-b", "me too", nil, false)
	if !errors.Is(err, errClaimTaken) {
		t.Fatalf("second claim: want errClaimTaken, got %v", err)
	}
}

func TestClaim_RacePickExactlyOneWinner(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)

	var wg sync.WaitGroup
	wins := make(chan string, 2)
	for _, who := range []string{"agent-a", "agent-b"} {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			if err := d.claim(ctx, "BUG-030", a, "race", nil, false); err == nil {
				wins <- a
			}
		}(who)
	}
	wg.Wait()
	close(wins)
	count := 0
	for range wins {
		count++
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", count)
	}
}

func TestClaim_EmitsClaimMessages(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if err := d.claim(ctx, "BUG-031", "agent-a", "intent here", nil, false); err != nil {
		t.Fatal(err)
	}

	var gCount, iCount int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='claim' AND thread='global'`).Scan(&gCount)
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind='claim' AND thread='BUG-031'`).Scan(&iCount)

	if gCount != 1 || iCount != 1 {
		t.Fatalf("claim msgs: global=%d item=%d (want 1/1)", gCount, iCount)
	}
}

func TestClaim_WithTouchesInsertsRows(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	touches := []string{"server/switcher/source_decoder.go", "server/control/api.go"}
	if err := d.claim(ctx, "BUG-032", "agent-a", "", touches, false); err != nil {
		t.Fatal(err)
	}

	var count int
	_ = d.sqldb.QueryRow(`SELECT COUNT(*) FROM touches WHERE agent_id='agent-a' AND item_id='BUG-032' AND released_at IS NULL`).Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 touches, got %d", count)
	}
}
