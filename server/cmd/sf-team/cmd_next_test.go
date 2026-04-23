package main

import (
	"context"
	"testing"
)

func TestNext_PicksHighestPriorityUnclaimed(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-TEST-002", "agent-a", "", nil, false)

	it, err := d.nextUnclaimed(ctx, "testdata/items")
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "BUG-TEST-001" {
		t.Fatalf("got %s want BUG-TEST-001", it.ID)
	}
}

func TestNext_ReturnsErrorWhenAllClaimed(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.claim(ctx, "BUG-TEST-001", "agent-a", "", nil, false)
	_ = d.claim(ctx, "BUG-TEST-002", "agent-a", "", nil, false)

	_, err := d.nextUnclaimed(ctx, "testdata/items")
	if err == nil {
		t.Fatal("expected error when all claimed")
	}
}

func TestNext_PrefersP0OverP1(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	it, err := d.nextUnclaimed(ctx, "testdata/items")
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "BUG-TEST-002" {
		t.Fatalf("got %s want BUG-TEST-002 (P0 before P1)", it.ID)
	}
}
