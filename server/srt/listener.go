package srt

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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

	// MaxSources limits the number of concurrent active sources. 0 = unlimited.
	MaxSources int

	// OnSource is called for each accepted connection with the resolved config
	// and the raw srtgo connection. The callback owns the connection lifecycle.
	OnSource func(SourceConfig, *srtgo.Conn)

	// Log is the structured logger. If nil, slog.Default() is used.
	Log *slog.Logger
}

// Listener accepts incoming SRT push connections. It uses srtgo's Server
// abstraction for connection lifecycle management.
type Listener struct {
	cfg ListenerConfig
	log *slog.Logger
	mu          sync.Mutex
	activeCount int
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
		_ = ln.Close()
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

		// Check MaxSources limit before processing.
		if l.cfg.MaxSources > 0 {
			l.mu.Lock()
			count := l.activeCount
			l.mu.Unlock()
			if count >= l.cfg.MaxSources {
				l.log.Warn("max sources reached, rejecting connection",
					"max", l.cfg.MaxSources,
					"remote", conn.RemoteAddr(),
				)
				_ = conn.Close()
				continue
			}
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

		// Track active source count.
		l.mu.Lock()
		l.activeCount++
		l.mu.Unlock()

		// Wrap the OnSource callback to decrement active count when done.
		wrappedCfg := cfg
		wrappedConn := conn
		l.cfg.OnSource(wrappedCfg, wrappedConn)
	}
}

// ReleaseSource decrements the active source count. Call this when a source
// connection is closed to allow new connections when MaxSources is set.
func (l *Listener) ReleaseSource() {
	l.mu.Lock()
	if l.activeCount > 0 {
		l.activeCount--
	}
	l.mu.Unlock()
}

// ActiveCount returns the current number of active sources.
func (l *Listener) ActiveCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.activeCount
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
