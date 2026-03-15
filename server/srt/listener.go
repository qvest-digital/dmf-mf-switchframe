package srt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

// ListenerConfig configures an SRT listener for incoming push connections.
type ListenerConfig struct {
	// Addr is the address to listen on (e.g., ":6464").
	Addr string

	// Store persists source configurations across restarts.
	Store *Store

	// DefaultLatency is the SRT latency for the listener socket.
	DefaultLatency time.Duration

	// OnSource is called for each accepted connection with the resolved config
	// and the raw srtgo connection. The callback owns the connection lifecycle.
	OnSource func(SourceConfig, *srtgo.Conn)

	// Log is the structured logger. If nil, slog.Default() is used.
	Log *slog.Logger
}

// Listener accepts incoming SRT push connections. It uses srtgo's Server
// abstraction for connection lifecycle management.
type Listener struct {
	cfg    ListenerConfig
	server *srtgo.Server
	log    *slog.Logger
}

// NewListener creates a Listener ready to accept SRT connections.
// Call Run() to start the accept loop.
func NewListener(cfg ListenerConfig) (*Listener, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("srt listener: addr is required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("srt listener: store is required")
	}
	if cfg.OnSource == nil {
		return nil, fmt.Errorf("srt listener: OnSource callback is required")
	}

	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "srt-listener")

	return &Listener{
		cfg: cfg,
		log: log,
	}, nil
}

// Run starts the SRT listener and blocks until ctx is cancelled.
// It accepts incoming connections, resolves source configs, and invokes
// the OnSource callback for each.
func (l *Listener) Run(ctx context.Context) error {
	srtCfg := srtgo.DefaultConfig()
	if l.cfg.DefaultLatency > 0 {
		srtCfg.Latency = l.cfg.DefaultLatency
	}

	ln, err := srtgo.Listen(l.cfg.Addr, srtCfg)
	if err != nil {
		return fmt.Errorf("srt listen on %s: %w", l.cfg.Addr, err)
	}
	l.log.Info("listening", "addr", l.cfg.Addr)

	// Close the listener when context is cancelled.
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled — clean shutdown.
				return nil
			}
			l.log.Warn("accept error", "error", err)
			continue
		}

		streamID := conn.StreamID()
		suffix := ExtractStreamKey(streamID)
		key := KeyPrefix + suffix

		l.log.Info("accepted connection",
			"key", key,
			"streamID", streamID,
			"remote", conn.RemoteAddr(),
		)

		// Resolve config: restore from store if exists, otherwise create new.
		cfg := l.resolveConfig(key, streamID, suffix)

		// Persist the config (ensures new sources are stored).
		if err := l.cfg.Store.Save(cfg); err != nil {
			l.log.Warn("failed to save source config", "key", key, "error", err)
		}

		// Invoke the OnSource callback. The callback owns the connection lifecycle.
		l.cfg.OnSource(cfg, conn)
	}
}

// Close is a no-op for Listener since Run handles cleanup via context cancellation.
// Provided for interface consistency.
func (l *Listener) Close() error {
	return nil
}

// resolveConfig looks up an existing config in the store, restoring persisted
// label/position/delay. If not found, creates a new config with defaults.
func (l *Listener) resolveConfig(key, streamID, suffix string) SourceConfig {
	if existing, ok := l.cfg.Store.Get(key); ok {
		// Restore persisted config. Update streamID in case it changed format
		// but resolved to the same key.
		existing.StreamID = streamID
		existing.Mode = ModeListener
		return existing
	}

	// New source — create config with defaults.
	latencyMs := int(l.cfg.DefaultLatency / time.Millisecond)
	return SourceConfig{
		Key:       key,
		Mode:      ModeListener,
		StreamID:  streamID,
		Label:     suffix,
		LatencyMs: latencyMs,
	}
}
