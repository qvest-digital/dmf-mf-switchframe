package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEventBus_PublishToSubscriber(t *testing.T) {
	bus := newEventBus()
	defer bus.Close()

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bus.Publish(event{Kind: "message", Payload: map[string]any{"thread": "global"}})

	select {
	case e := <-ch:
		if e.Kind != "message" {
			t.Fatalf("kind=%q", e.Kind)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received")
	}
}

func TestEventBus_FanOutToMultiple(t *testing.T) {
	bus := newEventBus()
	defer bus.Close()

	subs := []chan event{bus.Subscribe(), bus.Subscribe(), bus.Subscribe()}
	defer func() {
		for _, s := range subs {
			bus.Unsubscribe(s)
		}
	}()

	bus.Publish(event{Kind: "claim"})

	var wg sync.WaitGroup
	for i, s := range subs {
		wg.Add(1)
		go func(i int, ch chan event) {
			defer wg.Done()
			select {
			case e := <-ch:
				if e.Kind != "claim" {
					t.Errorf("sub %d got %q", i, e.Kind)
				}
			case <-time.After(100 * time.Millisecond):
				t.Errorf("sub %d: no event", i)
			}
		}(i, s)
	}
	wg.Wait()
}

func TestEventBus_DoesNotBlockOnSlowSubscriber(t *testing.T) {
	bus := newEventBus()
	defer bus.Close()

	slow := bus.Subscribe() // never drained
	fast := bus.Subscribe()
	defer bus.Unsubscribe(slow)
	defer bus.Unsubscribe(fast)

	const n = 100
	done := make(chan struct{})
	received := 0
	go func() {
		for range fast {
			received++
			if received == n {
				close(done)
				return
			}
		}
	}()

	for i := 0; i < n; i++ {
		bus.Publish(event{Kind: "spam"})
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("fast sub only got %d/%d — slow sub starved it", received, n)
	}

	if got := len(slow); got != 32 {
		t.Fatalf("slow sub len=%d want 32 (should have saturated at buffer cap)", got)
	}
}

func TestEventBus_SubscribeAfterClose_ReturnsClosedChannel(t *testing.T) {
	bus := newEventBus()
	bus.Close()

	ch := bus.Subscribe()

	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel after Subscribe post-Close")
	}
}

func TestPublish_EmitsAfterCommit(t *testing.T) {
	bus := newEventBus()
	d, _ := newTestDB(t)
	d.publisher = bus

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	// drain any register events
drainLoop:
	for {
		select {
		case <-sub:
		case <-time.After(10 * time.Millisecond):
			break drainLoop
		}
	}

	_ = d.claim(ctx, "BUG-PUB", "agent-a", "pub test", nil, false)

	select {
	case e := <-sub:
		if e.Kind != "claim" {
			t.Fatalf("kind=%q", e.Kind)
		}
		if e.Payload["item_id"] != "BUG-PUB" {
			t.Fatalf("item_id=%v", e.Payload["item_id"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no claim event")
	}
}

var _ publisherIface = (*eventBus)(nil)
