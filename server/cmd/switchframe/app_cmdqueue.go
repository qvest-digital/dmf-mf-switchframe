package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/cmdqueue"
)

// initCommandQueue creates the PTP-timed command queue, wires a dispatcher
// with "cut" and "preview" actions, and connects the queue to the switcher's
// frame-loop drain. When a state directory is available, a WAL is attached
// for crash recovery.
func (a *App) initCommandQueue() error {
	var opts []cmdqueue.Option

	// Default staleness threshold is 2s. When active-active peer engine
	// configuration is added (future), increase to 10s to accommodate
	// cross-region command propagation latency.
	maxAge := cmdqueue.DefaultMaxAge // 2s
	opts = append(opts, cmdqueue.WithMaxAge(maxAge))

	// Attach WAL for crash recovery if we have a state directory.
	if a.cfg.StateDir != "" {
		walPath := filepath.Join(a.cfg.StateDir, "cmdqueue.wal")
		wal, err := cmdqueue.NewWAL(walPath)
		if err != nil {
			slog.Warn("command queue WAL init failed, proceeding without WAL", "error", err, "path", walPath)
		} else {
			a.cmdQueueWAL = wal
			opts = append(opts, cmdqueue.WithWAL(wal))
			slog.Info("command queue WAL initialized", "path", walPath)
		}
	}

	a.cmdQueue = cmdqueue.New(clock.Now, opts...)

	// Build dispatcher with "cut" and "preview" actions matching the
	// timedHandler registrations in control/api.go.
	dispatcher := cmdqueue.NewDispatcher()
	dispatcher.Register("cut", func(payload json.RawMessage) error {
		var req struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.Cut(context.Background(), req.Source)
	})
	dispatcher.Register("preview", func(payload json.RawMessage) error {
		var req struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetPreview(context.Background(), req.Source)
	})
	a.cmdDispatcher = dispatcher

	// Wire into the switcher's frame-loop drain so queued commands execute
	// with frame-level accuracy (~33ms at 30fps).
	a.sw.SetCommandQueue(a.cmdQueue, a.cmdDispatcher)

	slog.Info("command queue initialized",
		"max_age_us", maxAge,
		"wal", a.cmdQueueWAL != nil)

	return nil
}
