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

func TestNext_SkipsItemWithFutureNotBefore(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	it, err := d.nextUnclaimed(ctx, "testdata/items_notbefore")
	if err != nil {
		t.Fatal(err)
	}
	// Fixture set: BET-TEST-FUTURE (P0, not-before 2099) must be skipped,
	// BUG-TEST-PAST (P0, not-before 2020) takes priority.
	if it.ID != "BUG-TEST-PAST" {
		t.Fatalf("got %s, want BUG-TEST-PAST (future-gated P0 must be skipped)", it.ID)
	}
}

func TestNext_AllGatedReturnsNoUnclaimed(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	// Claim everything except the future-gated item; nextUnclaimed must
	// treat the gated item as unavailable and surface errNoUnclaimed.
	_ = d.claim(ctx, "BUG-TEST-PAST", "agent-a", "", nil, false)
	_ = d.claim(ctx, "BUG-TEST-NORMAL", "agent-a", "", nil, false)

	_, err := d.nextUnclaimed(ctx, "testdata/items_notbefore")
	if err == nil {
		t.Fatal("expected errNoUnclaimed when only a future-gated item remains")
	}
}

func TestNext_PastNotBeforeIsPickable(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	// Claim the P0 past-gated so only the future-gated (higher priority)
	// and the ungated P1 remain. The gated P0 must be skipped, so the
	// ungated P1 wins.
	_ = d.claim(ctx, "BUG-TEST-PAST", "agent-a", "", nil, false)

	it, err := d.nextUnclaimed(ctx, "testdata/items_notbefore")
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "BUG-TEST-NORMAL" {
		t.Fatalf("got %s, want BUG-TEST-NORMAL (future-gated P0 skipped, ungated P1 wins)", it.ID)
	}
}
