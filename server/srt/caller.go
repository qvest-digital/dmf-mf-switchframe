package srt

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// CallerConfig configures an SRT caller for outbound pull connections.
type CallerConfig struct {
	// Store persists source configurations across restarts.
	Store *Store

	// DefaultLatency is the SRT latency used when the config has no latency set.
	DefaultLatency time.Duration

	// OnSource is called for each successful connection with the resolved config
	// and the raw srtgo connection. It must return a channel that closes when the
	// source stops (triggering reconnection).
	OnSource func(SourceConfig, *srtgo.Conn) <-chan struct{}

	// Log is the structured logger. If nil, slog.Default() is used.
	Log *slog.Logger
}

// Caller manages outbound SRT pull connections. When a pull is configured,
// the caller dials the remote address and calls OnSource. On disconnect,
// it auto-reconnects with exponential backoff (1s → 30s cap).
type Caller struct {
	cfg   CallerConfig
	log   *slog.Logger
	mu    sync.Mutex
	pulls map[string]*activePull
}

// activePull tracks a single outbound pull connection.
type activePull struct {
	config SourceConfig
	cancel context.CancelFunc
}

// NewCaller creates a Caller ready to manage outbound SRT connections.
func NewCaller(cfg CallerConfig) *Caller {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "srt-caller")

	return &Caller{
		cfg:   cfg,
		log:   log,
		pulls: make(map[string]*activePull),
	}
}

// Pull starts an outbound SRT pull connection. The config is validated, saved to
// the store for persistence, and a connect loop goroutine is started. If a pull
// for the same key already exists, it is cancelled and replaced.
func (c *Caller) Pull(ctx context.Context, config SourceConfig) error {
	// Force caller mode.
	config.Mode = ModeCaller

	if err := config.Validate(); err != nil {
		return fmt.Errorf("srt caller: invalid config: %w", err)
	}

	// Persist to store.
	if err := c.cfg.Store.Save(config); err != nil {
		return fmt.Errorf("srt caller: save config: %w", err)
	}

	c.mu.Lock()
	// Cancel any existing pull for this key.
	if existing, ok := c.pulls[config.Key]; ok {
		existing.cancel()
	}

	pullCtx, pullCancel := context.WithCancel(ctx)
	c.pulls[config.Key] = &activePull{
		config: config,
		cancel: pullCancel,
	}
	c.mu.Unlock()

	go c.connectLoop(pullCtx, config)

	return nil
}

// Stop cancels an active pull and removes it from the store.
func (c *Caller) Stop(key string) {
	c.mu.Lock()
	pull, ok := c.pulls[key]
	if ok {
		pull.cancel()
		delete(c.pulls, key)
	}
	c.mu.Unlock()

	if ok {
		// Remove from persistent store.
		if err := c.cfg.Store.Delete(key); err != nil {
			c.log.Warn("failed to delete source config from store", "key", key, "error", err)
		}
		c.log.Info("stopped pull", "key", key)
	}
}

// ActivePulls returns the configs of all active outbound pulls.
func (c *Caller) ActivePulls() []SourceConfig {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]SourceConfig, 0, len(c.pulls))
	for _, p := range c.pulls {
		out = append(out, p.config)
	}
	return out
}

// RestoreFromStore re-establishes pulls for all caller-mode configs in the store.
// Called at startup to resume pulls that were active before a restart.
func (c *Caller) RestoreFromStore(ctx context.Context) {
	configs := c.cfg.Store.List()
	for _, cfg := range configs {
		if cfg.Mode != ModeCaller {
			continue
		}
		c.log.Info("restoring pull from store", "key", cfg.Key, "address", cfg.Address)
		// Pull already saves to store, but the config is already there —
		// we pass through Pull for consistency (it overwrites with same data).
		if err := c.Pull(ctx, cfg); err != nil {
			c.log.Warn("failed to restore pull", "key", cfg.Key, "error", err)
		}
	}
}

// connectLoop dials the remote SRT address with exponential backoff. On success,
// it calls OnSource and waits for the source to stop before reconnecting.
func (c *Caller) connectLoop(ctx context.Context, config SourceConfig) {
	backoff := initialBackoff

	for {
		// Check if cancelled before attempting to connect.
		select {
		case <-ctx.Done():
			return
		default:
		}

		srtCfg := srtgo.DefaultConfig()
		srtCfg.StreamID = config.StreamID
		if config.LatencyMs > 0 {
			srtCfg.Latency = time.Duration(config.LatencyMs) * time.Millisecond
		} else if c.cfg.DefaultLatency > 0 {
			srtCfg.Latency = c.cfg.DefaultLatency
		}

		c.log.Info("dialing", "key", config.Key, "address", config.Address)
		conn, err := srtgo.Dial(config.Address, srtCfg)
		if err != nil {
			c.log.Warn("dial failed",
				"key", config.Key,
				"address", config.Address,
				"error", err,
				"backoff", backoff,
			)

			// Sleep with backoff, respecting context cancellation.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			// Exponential backoff with cap.
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Connection succeeded — reset backoff.
		backoff = initialBackoff
		c.log.Info("connected", "key", config.Key, "address", config.Address)

		// Call OnSource and get a channel that signals when the source stops.
		doneCh := c.cfg.OnSource(config, conn)

		// Wait for source to finish or context cancel.
		select {
		case <-doneCh:
			c.log.Info("source disconnected, will reconnect",
				"key", config.Key,
				"address", config.Address,
			)
			// Loop back to reconnect.
			continue
		case <-ctx.Done():
			// Context cancelled — close the connection and exit.
			conn.Close()
			return
		}
	}
}
