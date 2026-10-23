package output

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/backoff"
	"github.com/zsiec/switchframe/server/internal/mpegts"
	"github.com/zsiec/switchframe/server/srt"
)

const (
	defaultRingBufSize = 4 * 1024 * 1024 // 4MB
	idrGateTimeout     = 2 * time.Second // max time to wait for a keyframe before forcing output
)

// SRTCallerConfig holds configuration for SRT caller (push mode).
type SRTCallerConfig struct {
	Address        string
	Port           int
	Latency        int // ms, default 120
	StreamID       string
	Passphrase     string // enables SRT encryption when non-empty (10-79 chars)
	KeyLength      int    // AES key length in bytes: 16/24/32; 0 = srtgo default (16)
	RingBufferSize int    // bytes, default 4MB
	InputBW        int64  // bytes/sec, 0 = auto-estimate
	OverheadBW     int    // percent, 0 = use srtgo default (25%)
}

// SRTCaller pushes MPEG-TS data to a remote SRT receiver.
// It implements the Adapter interface.
type SRTCaller struct {
	config SRTCallerConfig

	mu              sync.Mutex
	conn            srtConn
	ctx             context.Context
	cancel          context.CancelFunc
	ringBuf         *ringBuffer
	bo              backoff.Backoff
	reconnectDone   chan struct{} // done channel of the running reconnect loop; nil when none runs
	reconnecting    atomic.Bool   // set while a reconnect loop runs; see startReconnectLoopLocked
	statsPolling    atomic.Bool   // guards against duplicate pollStats goroutines
	pendingIDR      atomic.Bool   // when true, drop writes until a keyframe arrives
	pendingIDRSince atomic.Int64  // UnixMilli timestamp when pendingIDR was set (for timeout)
	bytesWritten    atomic.Int64
	overflowCount   atomic.Int64 // number of ring buffer overflow events
	state           atomic.Pointer[AdapterState]
	lastError       atomic.Pointer[string]
	startedAt       time.Time

	// stats tracks SRT connection statistics. When non-nil, a background goroutine
	// polls the connection for stats every second and calls stats.UpdateSend().
	stats *srt.ConnStats

	// connectFn is overridden in tests to avoid real network I/O.
	connectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error)

	// onReconnect is called after a successful reconnection with a boolean
	// indicating whether the ring buffer overflowed (data was lost).
	onReconnect func(overflowed bool)
}

// NewSRTCaller creates an SRT caller adapter.
func NewSRTCaller(config SRTCallerConfig) *SRTCaller {
	if config.Latency == 0 {
		config.Latency = defaultSRTLatency
	}
	ringSize := config.RingBufferSize
	if ringSize == 0 {
		ringSize = defaultRingBufSize
	}

	c := &SRTCaller{
		config:  config,
		ringBuf: newRingBuffer(ringSize),
		bo:      backoff.Backoff{JitterPct: 0.20},
	}
	c.state.Store(ptrTo(StateStopped))
	c.lastError.Store(ptrTo(""))
	c.connectFn = defaultSRTConnect
	return c
}

// SetStats sets the ConnStats tracker for this caller. Must be called before Start().
func (c *SRTCaller) SetStats(cs *srt.ConnStats) {
	c.stats = cs
}

// ID returns the adapter identifier.
func (c *SRTCaller) ID() string { return "srt-caller" }

// Start connects to the remote SRT receiver and begins accepting writes.
// If the initial connection fails, the caller enters reconnecting state
// and retries in the background. Start itself does not return an error
// in this case — the reconnect loop handles recovery.
func (c *SRTCaller) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A later Start supersedes an earlier one: cancelling its context ends a
	// reconnect loop still running for it.
	if c.cancel != nil {
		c.cancel()
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.startedAt = time.Now()
	c.bytesWritten.Store(0)
	c.overflowCount.Store(0)
	c.state.Store(ptrTo(StateStarting))
	c.lastError.Store(ptrTo(""))

	conn, err := c.connectFn(c.ctx, c.config)
	if err != nil {
		c.state.Store(ptrTo(StateReconnecting))
		c.lastError.Store(ptrTo(err.Error()))
		c.startReconnectLoopLocked()
		return nil // Don't fail start — reconnect will keep trying
	}

	c.conn = conn
	c.state.Store(ptrTo(StateActive))
	c.bo.Reset()

	if c.stats != nil && c.statsPolling.CompareAndSwap(false, true) {
		go c.pollStats(c.ctx)
	}

	return nil
}

// Write sends TS data to the SRT connection.
// During reconnection, data is buffered in the ring buffer.
// Write never propagates SRT errors to the caller; instead it
// triggers a reconnect and buffers data.
func (c *SRTCaller) Write(tsData []byte) (int, error) {
	state := *c.state.Load()

	switch state {
	case StateActive:
		// IDR gating: after a reconnect, drop all writes until we see a
		// keyframe (RAI in the TS adaptation field). This prevents decoder
		// artifacts on the remote SRT receiver. If no keyframe arrives
		// within idrGateTimeout (2s), force-clear the gate to prevent
		// indefinite output hang (e.g., during transitions that produce
		// only P-frames).
		if c.pendingIDR.Load() {
			if !mpegts.ContainsKeyframe(tsData) {
				elapsed := time.Since(time.UnixMilli(c.pendingIDRSince.Load()))
				if elapsed <= idrGateTimeout {
					return len(tsData), nil // drop delta frames silently
				}
				// Timeout expired — force-clear IDR gate.
				slog.Warn("SRT IDR gate timeout expired, forcing output",
					"elapsed", elapsed, "address", c.config.Address)
				c.pendingIDR.Store(false)
			} else {
				c.pendingIDR.Store(false)
			}
		}

		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return len(tsData), nil // silently drop
		}

		n, err := conn.Write(tsData)
		if err != nil {
			slog.Warn("SRT write failed, entering reconnect", "error", err)
			c.state.Store(ptrTo(StateReconnecting))
			c.lastError.Store(ptrTo(err.Error()))
			c.mu.Lock()
			_, _ = c.ringBuf.Write(tsData)
			c.startReconnectLoopLocked()
			c.mu.Unlock()
			return len(tsData), nil // Don't propagate error to muxer
		}

		c.bytesWritten.Add(int64(n))
		return n, nil

	case StateReconnecting:
		c.mu.Lock()
		_, _ = c.ringBuf.Write(tsData)
		c.mu.Unlock()
		return len(tsData), nil

	default:
		return 0, fmt.Errorf("srt caller not active (state: %s)", state)
	}
}

// Close stops the SRT caller and releases resources. Safe to call
// without a prior Start or after the caller is already stopped.
func (c *SRTCaller) Close() error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	// Capture the reconnect done channel (if a loop is running) so we can
	// join it. We must release c.mu before waiting because reconnectLoop's
	// exit path also acquires c.mu.
	done := c.reconnectDone
	c.mu.Unlock()

	if done != nil {
		<-done
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	// Store StateStopped after the reconnect loop has finished so its
	// StateActive store cannot race in after Close returns.
	c.state.Store(ptrTo(StateStopped))
	return nil
}

// Status returns the current SRT caller status.
func (c *SRTCaller) Status() AdapterStatus {
	state := *c.state.Load()
	errStr := *c.lastError.Load()

	return AdapterStatus{
		State:        state,
		BytesWritten: c.bytesWritten.Load(),
		StartedAt:    c.startedAt,
		Error:        errStr,
	}
}

// SRTStatusSnapshot returns an SRTStatus for ControlRoomState.
func (c *SRTCaller) SRTStatusSnapshot() SRTStatus {
	status := c.Status()
	return SRTStatus{
		Active:        status.State == StateActive || status.State == StateReconnecting,
		Mode:          "caller",
		Address:       c.config.Address,
		Port:          c.config.Port,
		State:         string(status.State),
		BytesWritten:  status.BytesWritten,
		OverflowCount: c.overflowCount.Load(),
		Error:         status.Error,
	}
}

// startReconnectLoopLocked starts a reconnect loop unless one is running.
// The loop gets its own done channel and the caller's current context here,
// at spawn time: a loop that read them from the caller once it ran could
// pick up the channel of a loop started after it, and the second of the two
// to exit would close an already closed channel. c.mu must be held.
func (c *SRTCaller) startReconnectLoopLocked() {
	if !c.reconnecting.CompareAndSwap(false, true) {
		return
	}
	done := make(chan struct{})
	c.reconnectDone = done
	go c.reconnectLoop(c.ctx, done)
}

// reconnectLoop retries SRT connection with exponential backoff until ctx
// ends or a connection is made, and closes done when it exits.
// On success, if the ring buffer did not overflow, buffered data is
// flushed to the new connection. If it did overflow, the data is
// discarded (the Manager should wait for a keyframe).
func (c *SRTCaller) reconnectLoop(ctx context.Context, done chan struct{}) {
	defer func() {
		c.mu.Lock()
		if c.reconnectDone == done {
			c.reconnectDone = nil
		}
		c.mu.Unlock()
		close(done)
		c.reconnecting.Store(false)
		// Re-arm check: this loop stores StateActive and calls onReconnect
		// BEFORE the Store(false) above. A Write that failed inside that
		// window stored StateReconnecting but lost the CompareAndSwap
		// against the still-set flag, so no new loop was spawned. Without
		// this re-check the caller would be stuck in StateReconnecting
		// forever with no reconnect goroutine.
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.ctx != nil && c.ctx.Err() == nil && *c.state.Load() == StateReconnecting {
			c.startReconnectLoopLocked()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		c.mu.Lock()
		wait := c.bo.Next()
		c.mu.Unlock()
		slog.Info("SRT reconnecting", "backoff", wait, "address", c.config.Address)

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		conn, err := c.connectFn(ctx, c.config)
		if err != nil {
			c.lastError.Store(ptrTo(err.Error()))
			continue
		}

		// srt.Dial ignores ctx cancellation and can block up to ConnTimeout,
		// so Close may have run while this Dial was in flight. Re-check under
		// the lock before installing the connection; if Close cancelled the
		// context, close the just-dialed conn instead of leaking it (and
		// avoid resurrecting a StateActive caller after shutdown).
		c.mu.Lock()
		if ctx.Err() != nil {
			c.mu.Unlock()
			conn.Close()
			return
		}
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = conn

		overflowed := c.ringBuf.Overflowed()
		c.ringBuf.ReadAll() // discard buffered data
		c.mu.Unlock()

		// Always gate writes until a keyframe after reconnect.
		// The remote decoder has lost state — flushing buffered delta
		// frames would cause decode errors. Discard the ring buffer
		// and wait for the next keyframe (with a 2s timeout to prevent
		// indefinite hang if no keyframe arrives).
		c.pendingIDRSince.Store(time.Now().UnixMilli())
		c.pendingIDR.Store(true)

		if overflowed {
			c.overflowCount.Add(1)
		}

		c.state.Store(ptrTo(StateActive))
		c.lastError.Store(ptrTo(""))
		c.mu.Lock()
		c.bo.Reset()
		c.mu.Unlock()

		slog.Info("SRT reconnected", "address", c.config.Address, "overflowed", overflowed)

		// Start stats polling if not already running. Uses CAS to prevent
		// accumulating duplicate goroutines across reconnect cycles.
		if c.stats != nil && c.statsPolling.CompareAndSwap(false, true) {
			go c.pollStats(ctx)
		}

		if c.onReconnect != nil {
			c.onReconnect(overflowed)
		}
		return
	}
}

// pollStats polls the SRT connection for stats every second and updates ConnStats.
func (c *SRTCaller) pollStats(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.updateStats()
		}
	}
}

// updateStats fetches SRT stats from the connection and updates ConnStats.
func (c *SRTCaller) updateStats() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	statter, ok := conn.(SRTStatter)
	if !ok {
		return
	}
	cs := statter.SRTStats(true)
	if c.stats != nil {
		c.stats.UpdateSend(srt.SendStatsUpdate{
			RTTMs:           float64(cs.RTT) / float64(time.Millisecond),
			RTTVarMs:        float64(cs.RTTVar) / float64(time.Millisecond),
			SendRateMbps:    cs.MbpsSendRate,
			SendLossRatePct: cs.SendLossRate,
			PacketsSent:     int64(cs.SendPackets),
			PacketsSendLost: int64(cs.SendLoss),
			PacketsSendDrop: int64(cs.SendDropped),
			SendBufMs:       float64(cs.MsSndBuf) / float64(time.Millisecond),
			SendBufPackets:  cs.SendBufSize,
		})
	}
}

// defaultSRTConnect is a placeholder connection function. The real SRT
// connection wiring happens in main.go (Task 11). This keeps the output
// package free of srtgo imports for clean unit testing.
func defaultSRTConnect(_ context.Context, _ SRTCallerConfig) (srtConn, error) {
	return nil, errors.New("SRT connect not configured (set connectFn)")
}

// Compile-time check that SRTCaller satisfies the Adapter interface.
var _ Adapter = (*SRTCaller)(nil)
