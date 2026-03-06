package output

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockSRTConn implements the srtConn interface for testing without real network I/O.
type mockSRTConn struct {
	connected atomic.Bool
	written   atomic.Int64
	writeFn   func([]byte) (int, error) // optional override
	closed    atomic.Bool
}

func (m *mockSRTConn) Write(data []byte) (int, error) {
	if m.writeFn != nil {
		return m.writeFn(data)
	}
	m.written.Add(int64(len(data)))
	return len(data), nil
}

func (m *mockSRTConn) Close() {
	m.closed.Store(true)
}

func TestSRTCaller_ID(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	require.Equal(t, "srt-caller", c.ID())
}

func TestSRTCaller_StatusBeforeStart(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	status := c.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestSRTCaller_WriteWithMockConn(t *testing.T) {
	mock := &mockSRTConn{}
	mock.connected.Store(true)

	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	c.conn = mock
	c.state.Store(StateActive)

	data := make([]byte, 188*7)
	n, err := c.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	status := c.Status()
	require.Equal(t, int64(188*7), status.BytesWritten)
}

func TestSRTCaller_WriteBuffersDuringReconnect(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 4096,
	})
	c.state.Store(StateReconnecting)
	c.ringBuf = newRingBuffer(4096)

	data := make([]byte, 188)
	n, err := c.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	// Data should be in ring buffer
	require.Equal(t, 188, c.ringBuf.Len())
}

func TestSRTCaller_Close(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.conn = mock
	c.state.Store(StateActive)

	err := c.Close()
	require.NoError(t, err)

	require.True(t, mock.closed.Load())
	status := c.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestSRTCaller_BackoffProgression(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})

	// With jitter, values won't be exact. Check that they're in
	// the expected ±20% range for each level.
	levels := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,  // cap
		30 * time.Second,  // stays at cap
	}
	for _, expected := range levels {
		got := c.nextBackoff()
		lo := time.Duration(float64(expected) * 0.79)
		hi := time.Duration(float64(expected) * 1.21)
		require.True(t, got >= lo && got <= hi,
			"expected backoff near %v (±20%%), got %v", expected, got)
	}
}

func TestSRTCaller_BackoffHasJitter(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	// Collect 100 backoff values at the same level
	// They should NOT all be identical (jitter)
	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		c.backoff = 1 * time.Second // reset to same starting point
		d := c.nextBackoff()
		seen[d] = true
	}
	require.True(t, len(seen) > 1, "backoff should have jitter (got %d unique values)", len(seen))
}

func TestSRTCaller_ResetBackoff(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})

	c.nextBackoff() // ~1s
	c.nextBackoff() // ~2s
	c.resetBackoff()
	got := c.nextBackoff() // should be back near 1s
	// With ±20% jitter: 800ms - 1200ms
	require.True(t, got >= 800*time.Millisecond && got <= 1200*time.Millisecond,
		"after reset, backoff should be near 1s, got %v", got)
}

func TestSRTCaller_StartWithMockConnect(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		mock.connected.Store(true)
		return mock, nil
	}

	err := c.Start(context.Background())
	require.NoError(t, err)
	defer c.Close()

	status := c.Status()
	require.Equal(t, StateActive, status.State)
}

func TestSRTCaller_StartFailsEntersReconnect(t *testing.T) {
	connectCount := atomic.Int32{}
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})

	mock := &mockSRTConn{}
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		n := connectCount.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("connection refused")
		}
		mock.connected.Store(true)
		return mock, nil
	}

	err := c.Start(context.Background())
	require.NoError(t, err) // start itself doesn't fail
	defer c.Close()

	// Should be in reconnecting state initially
	status := c.Status()
	require.Equal(t, StateReconnecting, status.State)
	require.Contains(t, status.Error, "connection refused")

	// Wait for reconnect to succeed
	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)
}

func TestSRTCaller_WriteErrorTriggersReconnect(t *testing.T) {
	writeCount := atomic.Int32{}
	mock := &mockSRTConn{
		writeFn: func(data []byte) (int, error) {
			n := writeCount.Add(1)
			if n == 1 {
				return 0, fmt.Errorf("broken pipe")
			}
			return len(data), nil
		},
	}

	reconnectMock := &mockSRTConn{}
	connectCount := atomic.Int32{}

	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 4096,
	})
	c.conn = mock
	c.state.Store(StateActive)
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		connectCount.Add(1)
		return reconnectMock, nil
	}

	// First write fails, should trigger reconnect
	data := make([]byte, 188)
	n, err := c.Write(data)
	require.NoError(t, err) // error not propagated to caller
	require.Equal(t, len(data), n)

	// Wait for reconnect to succeed
	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)

	c.Close()
}

func TestSRTCaller_SRTStatusSnapshot(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})

	// Before start
	snap := c.SRTStatusSnapshot()
	require.False(t, snap.Active)
	require.Equal(t, "caller", snap.Mode)
	require.Equal(t, "srt.example.com", snap.Address)
	require.Equal(t, 9998, snap.Port)

	// When active
	c.state.Store(StateActive)
	snap = c.SRTStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "active", snap.State)

	// When reconnecting
	c.state.Store(StateReconnecting)
	snap = c.SRTStatusSnapshot()
	require.True(t, snap.Active) // still considered active
	require.Equal(t, "reconnecting", snap.State)
}

func TestSRTCaller_DefaultConfig(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})

	require.Equal(t, defaultSRTLatency, c.config.Latency)
	require.NotNil(t, c.ringBuf)
}

func TestSRTCaller_ImplementsOutputAdapter(t *testing.T) {
	var _ OutputAdapter = (*SRTCaller)(nil)
}

func TestSRTCaller_WriteWhenStopped(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	_, err := c.Write([]byte{0x47})
	require.Error(t, err)
}

func TestSRTCaller_CloseWithoutStart(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})
	err := c.Close()
	require.NoError(t, err)
}

func TestSRTCaller_OnReconnectCalledWithOverflowFalse(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 4096,
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write small data (no overflow)
	c.ringBuf.Write(make([]byte, 188))

	var callbackOverflowed atomic.Bool
	callbackOverflowed.Store(true) // default to true so we can verify it was set to false
	var callbackCalled atomic.Bool
	c.onReconnect = func(overflowed bool) {
		callbackOverflowed.Store(overflowed)
		callbackCalled.Store(true)
	}

	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return callbackCalled.Load()
	}, 5*time.Second, 50*time.Millisecond)

	require.False(t, callbackOverflowed.Load())
	c.Close()
}

func TestSRTCaller_OnReconnectCalledWithOverflowTrue(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 256, // small buffer
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write more data than the buffer can hold to trigger overflow
	c.ringBuf.Write(make([]byte, 512))

	var callbackOverflowed atomic.Bool
	var callbackCalled atomic.Bool
	c.onReconnect = func(overflowed bool) {
		callbackOverflowed.Store(overflowed)
		callbackCalled.Store(true)
	}

	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return callbackCalled.Load()
	}, 5*time.Second, 50*time.Millisecond)

	require.True(t, callbackOverflowed.Load())
	c.Close()
}

func TestSRTCaller_OnReconnectNilIsNoop(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 4096,
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)
	// onReconnect is nil by default

	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)

	// No panic — nil callback is safe
	c.Close()
}

// makeTSPacket creates a 188-byte MPEG-TS packet with specified PID.
// If keyframe is true, it sets the Random Access Indicator (RAI) flag
// in the adaptation field, which signals an IDR/keyframe.
func makeTSPacket(pid uint16, keyframe bool) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47 // sync byte

	// PID in bytes 1-2 (13 bits: 5 bits in byte 1, 8 bits in byte 2)
	pkt[1] = byte(pid>>8) & 0x1F
	pkt[2] = byte(pid & 0xFF)

	if keyframe {
		// Adaptation field control = 0x30 (adaptation + payload)
		pkt[3] = 0x30
		// Adaptation field length
		pkt[4] = 7
		// Adaptation field flags: RAI = bit 6 = 0x40
		pkt[5] = 0x40
	} else {
		// No adaptation field, payload only = 0x10
		pkt[3] = 0x10
	}
	return pkt
}

func TestContainsKeyframe(t *testing.T) {
	t.Run("single keyframe packet", func(t *testing.T) {
		pkt := makeTSPacket(0x100, true)
		require.True(t, containsKeyframe(pkt))
	})

	t.Run("single delta packet", func(t *testing.T) {
		pkt := makeTSPacket(0x100, false)
		require.False(t, containsKeyframe(pkt))
	})

	t.Run("keyframe among delta packets", func(t *testing.T) {
		data := make([]byte, 0, 188*3)
		data = append(data, makeTSPacket(0x100, false)...)
		data = append(data, makeTSPacket(0x100, true)...)
		data = append(data, makeTSPacket(0x100, false)...)
		require.True(t, containsKeyframe(data))
	})

	t.Run("all delta packets", func(t *testing.T) {
		data := make([]byte, 0, 188*3)
		data = append(data, makeTSPacket(0x100, false)...)
		data = append(data, makeTSPacket(0x100, false)...)
		data = append(data, makeTSPacket(0x100, false)...)
		require.False(t, containsKeyframe(data))
	})

	t.Run("empty data", func(t *testing.T) {
		require.False(t, containsKeyframe(nil))
		require.False(t, containsKeyframe([]byte{}))
	})

	t.Run("data shorter than TS packet", func(t *testing.T) {
		require.False(t, containsKeyframe([]byte{0x47, 0x00}))
	})

	t.Run("adaptation field length zero", func(t *testing.T) {
		// Adaptation field present but length=0 means no flags byte
		pkt := makeTSPacket(0x100, false)
		pkt[3] = 0x30 // adaptation + payload
		pkt[4] = 0    // adaptation field length = 0
		require.False(t, containsKeyframe(pkt))
	})
}

func TestSRTCaller_IDRGating_DropsAfterOverflow(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 256, // small buffer to force overflow
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write more data than the buffer can hold to trigger overflow
	c.ringBuf.Write(make([]byte, 512))

	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	var callbackCalled atomic.Bool
	c.onReconnect = func(overflowed bool) {
		callbackCalled.Store(true)
	}

	// Trigger reconnect
	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)

	// After overflow reconnect, pendingIDR should be true
	require.True(t, c.pendingIDR.Load(), "pendingIDR should be set after overflow reconnect")

	// Write a delta (non-keyframe) TS packet — should be dropped silently
	deltaPkt := makeTSPacket(0x100, false)
	n, err := c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, len(deltaPkt), n)
	require.Zero(t, mock.written.Load(), "delta packet should be dropped while IDR gate is active")

	// pendingIDR should still be true
	require.True(t, c.pendingIDR.Load(), "pendingIDR should remain set after delta packet")

	// Write a keyframe TS packet — should pass through and clear the gate
	keyframePkt := makeTSPacket(0x100, true)
	n, err = c.Write(keyframePkt)
	require.NoError(t, err)
	require.Equal(t, len(keyframePkt), n)
	require.Equal(t, int64(len(keyframePkt)), mock.written.Load(), "keyframe packet should be written")

	// pendingIDR should be cleared
	require.False(t, c.pendingIDR.Load(), "pendingIDR should be cleared after keyframe")

	// Subsequent delta packets should now pass through
	deltaPkt2 := makeTSPacket(0x100, false)
	n, err = c.Write(deltaPkt2)
	require.NoError(t, err)
	require.Equal(t, len(deltaPkt2), n)
	require.Equal(t, int64(len(keyframePkt)+len(deltaPkt2)), mock.written.Load(),
		"delta packet should pass through after IDR gate is cleared")

	c.Close()
}

func TestSRTCaller_IDRGating_NotSetWithoutOverflow(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 4096,
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write small data (no overflow)
	c.ringBuf.Write(make([]byte, 188))

	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	var callbackCalled atomic.Bool
	c.onReconnect = func(overflowed bool) {
		callbackCalled.Store(true)
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return callbackCalled.Load()
	}, 5*time.Second, 50*time.Millisecond)

	// No overflow — pendingIDR should NOT be set
	require.False(t, c.pendingIDR.Load(), "pendingIDR should not be set when no overflow occurred")

	// Delta packets should pass through immediately
	deltaPkt := makeTSPacket(0x100, false)
	n, err := c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, len(deltaPkt), n)
	// Written bytes = flushed buffer (188) + delta packet (188)
	require.Equal(t, int64(188+188), mock.written.Load(),
		"delta should pass through when no IDR gate is active")

	c.Close()
}

func TestSRTCaller_ReconnectFlushesBuffer(t *testing.T) {
	mock := &mockSRTConn{}
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 4096,
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write some data to the ring buffer
	testData := make([]byte, 188*3)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	c.ringBuf.Write(testData)

	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	// Trigger reconnect
	go c.reconnectLoop()

	// Wait for reconnect to succeed
	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)

	// Buffer data should have been flushed to the mock connection
	require.Equal(t, int64(len(testData)), mock.written.Load())

	c.Close()
}
