package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func watchItems(ctx context.Context, dir string, bus *eventBus) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer func() { _ = w.Close() }()
	if err := w.Add(dir); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-w.Events:
			if !strings.HasSuffix(ev.Name, ".md") {
				continue
			}
			bus.Publish(event{
				Kind: "item",
				Payload: map[string]any{
					"path": filepath.Base(ev.Name),
					"op":   ev.Op.String(),
				},
			})
		case <-w.Errors:
		}
	}
}
