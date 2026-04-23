package main

import "sync"

type event struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

type eventBus struct {
	mu     sync.RWMutex
	subs   map[chan event]struct{}
	closed bool
}

func newEventBus() *eventBus {
	return &eventBus{subs: map[chan event]struct{}{}}
}

func (b *eventBus) Subscribe() chan event {
	ch := make(chan event, 32)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(ch)
		return ch
	}
	b.subs[ch] = struct{}{}
	return ch
}

func (b *eventBus) Unsubscribe(ch chan event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *eventBus) Publish(e event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *eventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}
