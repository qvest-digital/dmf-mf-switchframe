package main

import (
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

	for i := 0; i < 100; i++ {
		bus.Publish(event{Kind: "spam"})
	}

	select {
	case <-fast:
		// good
	case <-time.After(200 * time.Millisecond):
		t.Fatal("fast subscriber blocked by slow one")
	}
}
