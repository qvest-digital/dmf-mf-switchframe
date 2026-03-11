package output

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultRingBufSize = 4 * 1024 * 1024 // 4MB
	maxBackoff         = 30 * time.Second
	initialBackoff     = 1 * time.Second
)

// SRTCallerConfig holds configuration for SRT caller (push mode).
type SRTCallerConfig struct {
	Address        string
	Port           int
	Latency        int   // ms, default 120
	StreamID       string
	RingBufferSize int   // bytes, default 4MB
	InputBW        int64 // bytes/sec, 0 = auto-estimate
	OverheadBW     int   // percent, 0 = use srtgo default (25%)
}

// SRTCaller pushes MPEG-TS data to a remote SRT receiver.
// It implements the Adapter interface.
type SRTCaller struct {
	config SRTCallerConfig

	mu            sync.Mutex
	conn          srtConn
	ctx           context.Context
	cancel        context.CancelFunc
	ringBuf       *ringBuffer
	backoff       time.Duration
	reconnecting  atomic.Bool // guards against duplicate reconnect goroutines
	pendingIDR    atomic.Bool // when true, drop writes until a keyframe arrives
	bytesWritten  atomic.Int64
	overflowCount atomic.Int64 // number of ring buffer overflow events
	state         atomic.Pointer[AdapterState]
	lastError     atomic.Pointer[string]
	startedAt     time.Time

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
		backoff: initialBackoff,
	}
	c.state.Store(ptrTo(StateStopped))
	c.lastError.Store(ptrTo(""))
	c.connectFn = defaultSRTConnect
	return c
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
	c.resetBackoffLocked()
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
		// IDR gating: after a ring buffer overflow during reconnect, drop
		// all writes until we see a keyframe (RAI in the TS adaptation field).
		// This prevents decoder artifacts on the remote SRT receiver.
		if c.pendingIDR.Load() {
			if !containsKeyframe(tsData) {
				return len(tsData), nil // drop delta frames silently
			}
			c.pendingIDR.Store(false)
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

		backoff := c.nextBackoff()
		slog.Info("SRT reconnecting", "backoff", backoff, "address", c.config.Address)

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
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
		// and wait for the next keyframe.
		c.pendingIDR.Store(true)

		if overflowed {
			c.overflowCount.Add(1)
		}

		c.state.Store(ptrTo(StateActive))
		c.lastError.Store(ptrTo(""))
		c.resetBackoff()

		slog.Info("SRT reconnected", "address", c.config.Address, "overflowed", overflowed)

		if c.onReconnect != nil {
			c.onReconnect(overflowed)
		}
		return
	}
}

// nextBackoff returns the current backoff duration with ±20% jitter and
// advances to the next level. The progression is: 1s → 2s → 4s → 8s →
// 16s → 30s (capped). Jitter prevents thundering herd on reconnect.
// Thread-safe: protects c.backoff with c.mu.
func (c *SRTCaller) nextBackoff() time.Duration {
	c.mu.Lock()
	current := c.backoffLocked()
	c.mu.Unlock()
	return current
}

// backoffLocked returns the current jittered backoff and advances to the
// next level. Must be called with c.mu held.
func (c *SRTCaller) backoffLocked() time.Duration {
	current := c.backoff
	next := current * 2
	if next > maxBackoff {
		next = maxBackoff
	}
	c.backoff = next
	// Apply ±20% jitter
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(current) * jitter)
}

// resetBackoff returns the backoff duration to its initial value.
// Thread-safe: protects c.backoff with c.mu.
func (c *SRTCaller) resetBackoff() {
	c.mu.Lock()
	c.resetBackoffLocked()
	c.mu.Unlock()
}

// resetBackoffLocked resets backoff. Must be called with c.mu held.
func (c *SRTCaller) resetBackoffLocked() {
	c.backoff = initialBackoff
}

// defaultSRTConnect is a placeholder connection function. The real SRT
// connection wiring happens in main.go (Task 11). This keeps the output
// package free of srtgo imports for clean unit testing.
func defaultSRTConnect(_ context.Context, _ SRTCallerConfig) (srtConn, error) {
	return nil, errors.New("SRT connect not configured (set connectFn)")
}

const tsPacketSize = 188

// containsKeyframe scans 188-byte MPEG-TS packets in data for the
// Random Access Indicator (RAI) flag, which signals an IDR/keyframe.
//
// TS packet structure (relevant bytes):
//
//	Byte 0:    sync byte (0x47)
//	Byte 3:    bits 5-4 = adaptation field control
//	             0b10 (2) = adaptation only
//	             0b11 (3) = adaptation + payload
//	Byte 4:    adaptation field length
//	Byte 5:    adaptation flags, bit 6 = Random Access Indicator
func containsKeyframe(data []byte) bool {
	for i := 0; i+tsPacketSize <= len(data); i += tsPacketSize {
		if data[i] != 0x47 {
			continue // not a valid sync byte, skip
		}

		// Adaptation field control is in byte 3, bits 5-4.
		// Values: 2 = adaptation only, 3 = adaptation + payload.
		afc := (data[i+3] >> 4) & 0x03
		if afc < 2 {
			continue // no adaptation field
		}

		// Adaptation field length at byte 4.
		afLen := data[i+4]
		if afLen == 0 {
			continue // no flags in adaptation field
		}

		// RAI is bit 6 of the adaptation flags byte (byte 5).
		if data[i+5]&0x40 != 0 {
			return true
		}
	}
	return false
}

// Compile-time check that SRTCaller satisfies the Adapter interface.
var _ Adapter = (*SRTCaller)(nil)
