package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_EmitsOnItemFileWrite(t *testing.T) {
	dir := t.TempDir()
	bus := newEventBus()
	defer bus.Close()

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchItems(ctx, dir, bus)

	time.Sleep(50 * time.Millisecond)

	path := filepath.Join(dir, "BUG-WATCH.md")
	if err := os.WriteFile(path, []byte("---\nid: BUG-WATCH\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-sub:
			if e.Kind == "item" {
				return
			}
		case <-deadline:
			t.Fatal("no item event within 2s")
		}
	}
}
