package output

import (
	"context"
	"fmt"
	"log/slog"
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
	Latency        int    // ms, default 120
	StreamID       string
	RingBufferSize int    // bytes, default 4MB
}

// SRTCaller pushes MPEG-TS data to a remote SRT receiver.
// It implements the OutputAdapter interface.
type SRTCaller struct {
	config SRTCallerConfig

	mu           sync.Mutex
	conn         srtConn
	ctx          context.Context
	cancel       context.CancelFunc
	ringBuf      *ringBuffer
	backoff      time.Duration
	bytesWritten atomic.Int64
	state        atomic.Value // AdapterState
	lastError    atomic.Value // string
	startedAt    time.Time

	// connectFn is overridden in tests to avoid cgo.
	connectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error)
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
	c.state.Store(StateStopped)
	c.lastError.Store("")
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
	c.state.Store(StateStarting)
	c.lastError.Store("")

	conn, err := c.connectFn(c.ctx, c.config)
	if err != nil {
		c.state.Store(StateReconnecting)
		c.lastError.Store(err.Error())
		go c.reconnectLoop()
		return nil // Don't fail start — reconnect will keep trying
	}

	c.conn = conn
	c.state.Store(StateActive)
	c.resetBackoff()
	return nil
}

// Write sends TS data to the SRT connection.
// During reconnection, data is buffered in the ring buffer.
// Write never propagates SRT errors to the caller; instead it
// triggers a reconnect and buffers data.
func (c *SRTCaller) Write(tsData []byte) (int, error) {
	state := c.state.Load().(AdapterState)

	switch state {
	case StateActive:
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return len(tsData), nil // silently drop
		}

		n, err := conn.Write(tsData)
		if err != nil {
			slog.Warn("SRT write failed, entering reconnect", "error", err)
			c.state.Store(StateReconnecting)
			c.lastError.Store(err.Error())
			c.mu.Lock()
			c.ringBuf.Write(tsData)
			c.mu.Unlock()
			go c.reconnectLoop()
			return len(tsData), nil // Don't propagate error to muxer
		}

		c.bytesWritten.Add(int64(n))
		return n, nil

	case StateReconnecting:
		c.mu.Lock()
		c.ringBuf.Write(tsData)
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

	c.state.Store(StateStopped)
	return nil
}

// Status returns the current SRT caller status.
func (c *SRTCaller) Status() AdapterStatus {
	state := c.state.Load().(AdapterState)
	errStr, _ := c.lastError.Load().(string)

	return AdapterStatus{
		State:        state,
		BytesWritten: c.bytesWritten.Load(),
		StartedAt:    c.startedAt,
		Error:        errStr,
	}
}

// SRTStatusSnapshot returns an SRTOutputStatus for ControlRoomState.
func (c *SRTCaller) SRTStatusSnapshot() SRTOutputStatus {
	status := c.Status()
	return SRTOutputStatus{
		Active:       status.State == StateActive || status.State == StateReconnecting,
		Mode:         "caller",
		Address:      c.config.Address,
		Port:         c.config.Port,
		State:        string(status.State),
		BytesWritten: status.BytesWritten,
		Error:        status.Error,
	}
}

// Overflowed reports whether the ring buffer overflowed during the last
// reconnect period. The OutputManager uses this to decide whether to
// wait for the next keyframe before resuming writes.
func (c *SRTCaller) Overflowed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ringBuf.Overflowed()
}

// reconnectLoop retries SRT connection with exponential backoff.
// On success, if the ring buffer did not overflow, buffered data is
// flushed to the new connection. If it did overflow, the data is
// discarded (the OutputManager should wait for a keyframe).
func (c *SRTCaller) reconnectLoop() {
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
			c.lastError.Store(err.Error())
			continue
		}

		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = conn

		overflowed := c.ringBuf.Overflowed()
		buffered := c.ringBuf.ReadAll()
		c.mu.Unlock()

		// Only flush buffered data if no overflow occurred.
		// On overflow the data is stale and the OutputManager
		// should wait for the next keyframe before sending.
		if !overflowed && len(buffered) > 0 {
			if _, err := conn.Write(buffered); err != nil {
				slog.Warn("SRT flush failed after reconnect", "error", err)
			}
		}

		c.state.Store(StateActive)
		c.lastError.Store("")
		c.resetBackoff()

		slog.Info("SRT reconnected", "address", c.config.Address)
		return
	}
}

// nextBackoff returns the current backoff duration and advances to the next.
// The progression is: 1s → 2s → 4s → 8s → 16s → 30s (capped).
func (c *SRTCaller) nextBackoff() time.Duration {
	current := c.backoff
	next := c.backoff * 2
	if next > maxBackoff {
		next = maxBackoff
	}
	c.backoff = next
	return current
}

// resetBackoff returns the backoff duration to its initial value.
func (c *SRTCaller) resetBackoff() {
	c.backoff = initialBackoff
}

// defaultSRTConnect is a placeholder connection function. The real SRT
// connection wiring happens in main.go (Task 11). This keeps the output
// package free of cgo imports for clean unit testing.
func defaultSRTConnect(_ context.Context, _ SRTCallerConfig) (srtConn, error) {
	return nil, fmt.Errorf("SRT connect not configured (set connectFn)")
}

// Compile-time check that SRTCaller satisfies the OutputAdapter interface.
var _ OutputAdapter = (*SRTCaller)(nil)
