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
	RingBufferSize int   // bytes, default 4MB
	InputBW        int64 // bytes/sec, 0 = auto-estimate
	OverheadBW     int   // percent, 0 = use srtgo default (25%)
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
	reconnecting    atomic.Bool  // guards against duplicate reconnect goroutines
	statsPolling    atomic.Bool  // guards against duplicate pollStats goroutines
	pendingIDR      atomic.Bool  // when true, drop writes until a keyframe arrives
	pendingIDRSince atomic.Int64 // UnixMilli timestamp when pendingIDR was set (for timeout)
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
		c.reconnecting.Store(true)
		go c.reconnectLoop()
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
			c.mu.Unlock()
			// CAS guard prevents duplicate reconnect goroutines.
			if c.reconnecting.CompareAndSwap(false, true) {
				go c.reconnectLoop()
			}
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
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

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

// reconnectLoop retries SRT connection with exponential backoff.
// On success, if the ring buffer did not overflow, buffered data is
// flushed to the new connection. If it did overflow, the data is
// discarded (the Manager should wait for a keyframe).
func (c *SRTCaller) reconnectLoop() {
	defer c.reconnecting.Store(false)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		wait := c.bo.Next()
		slog.Info("SRT reconnecting", "backoff", wait, "address", c.config.Address)

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(wait):
		}

		conn, err := c.connectFn(c.ctx, c.config)
		if err != nil {
			c.lastError.Store(ptrTo(err.Error()))
			continue
		}

		c.mu.Lock()
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
		c.bo.Reset()

		slog.Info("SRT reconnected", "address", c.config.Address, "overflowed", overflowed)

		// Start stats polling if not already running. Uses CAS to prevent
		// accumulating duplicate goroutines across reconnect cycles.
		if c.stats != nil && c.statsPolling.CompareAndSwap(false, true) {
			go c.pollStats(c.ctx)
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
