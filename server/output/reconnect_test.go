package output

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Integration-level SRT reconnection tests. The unit tests in srt_caller_test.go
// cover basic reconnect flow and overflow callbacks. These tests verify:
// 1. Data written during disconnect is preserved in the ring buffer and flushed on reconnect.
// 2. After overflow, the first write post-reconnect is gated until a keyframe (IDR).
// 3. The onReconnect callback fires with overflowed=true after buffer overflow.

func TestReconnect_RingBufferPreservesData(t *testing.T) {
	// Phase 1: Write data to an active connection.
	mock1 := &mockSRTConn{}
	mock1.connected.Store(true)

	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 8192,
	})
	c.conn = mock1
	c.state.Store(StateActive)
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel

	// Write 5 packets to the active connection.
	pkt := makeTSPacket(0x100, false)
	for i := 0; i < 5; i++ {
		n, err := c.Write(pkt)
		require.NoError(t, err)
		require.Equal(t, 188, n)
	}
	require.Equal(t, int64(188*5), mock1.written.Load(),
		"all 5 packets should be written to the first connection")

	// Phase 2: Simulate disconnect — put caller into reconnecting state
	// and write more packets (which go to the ring buffer).
	c.state.Store(StateReconnecting)
	c.ringBuf.Reset() // clear any stale data

	for i := 0; i < 3; i++ {
		n, err := c.Write(pkt)
		require.NoError(t, err)
		require.Equal(t, 188, n)
	}
	require.Equal(t, 188*3, c.ringBuf.Len(),
		"3 packets should be buffered in the ring buffer during reconnect")

	// Phase 3: Reconnect — set up a new mock connection and trigger reconnect loop.
	mock2 := &mockSRTConn{}
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock2, nil
	}

	go c.reconnectLoop()

	// Wait for reconnect to succeed.
	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)

	// Phase 4: Verify the buffered data was flushed to the new connection.
	require.Equal(t, int64(188*3), mock2.written.Load(),
		"buffered data should be flushed to the new connection")

	// Verify the ring buffer is now empty.
	require.Equal(t, 0, c.ringBuf.Len(), "ring buffer should be empty after flush")

	// Phase 5: New writes go directly to the new connection.
	n, err := c.Write(pkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188*4), mock2.written.Load(),
		"new write should go directly to the reconnected connection")

	c.Close()
}

func TestReconnect_ResumesFromKeyframe(t *testing.T) {
	// Setup: small ring buffer that will overflow.
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 256, // small enough to overflow with a few TS packets
	})
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write enough data to overflow the ring buffer.
	bigData := make([]byte, 512)
	c.ringBuf.Write(bigData)
	require.True(t, c.ringBuf.Overflowed(), "ring buffer should have overflowed")

	// Reconnect with a fresh mock.
	mock := &mockSRTConn{}
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	var callbackCalled atomic.Bool
	c.onReconnect = func(overflowed bool) {
		callbackCalled.Store(true)
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)

	// After overflow reconnect, pendingIDR should be set.
	require.True(t, c.pendingIDR.Load(),
		"pendingIDR should be set after overflow reconnect")

	// Overflow data should NOT be flushed (it's stale).
	require.Equal(t, int64(0), mock.written.Load(),
		"stale overflow data should not be flushed")

	// Delta frames should be dropped.
	deltaPkt := makeTSPacket(0x100, false)
	n, err := c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(0), mock.written.Load(),
		"delta frame should be dropped while IDR gate is active")

	// Another delta — still dropped.
	n, err = c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(0), mock.written.Load(),
		"second delta frame should also be dropped")

	// Keyframe should pass through and clear the gate.
	keyframePkt := makeTSPacket(0x100, true)
	n, err = c.Write(keyframePkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188), mock.written.Load(),
		"keyframe should be written to the connection")
	require.False(t, c.pendingIDR.Load(),
		"pendingIDR should be cleared after keyframe")

	// Subsequent delta frames should pass through.
	n, err = c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188*2), mock.written.Load(),
		"delta frame should pass through after IDR gate is cleared")

	c.Close()
}

func TestReconnect_OverflowCallback(t *testing.T) {
	// Setup: small buffer that will overflow.
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 128,
	})
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Overflow the ring buffer.
	c.ringBuf.Write(make([]byte, 256))
	require.True(t, c.ringBuf.Overflowed())

	// Track the callback.
	var mu sync.Mutex
	var callbackFired bool
	var callbackOverflowed bool

	c.onReconnect = func(overflowed bool) {
		mu.Lock()
		callbackFired = true
		callbackOverflowed = overflowed
		mu.Unlock()
	}

	mock := &mockSRTConn{}
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	go c.reconnectLoop()

	// Wait for the callback to fire.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return callbackFired
	}, 5*time.Second, 50*time.Millisecond)

	mu.Lock()
	require.True(t, callbackOverflowed,
		"onReconnect callback should report overflowed=true")
	mu.Unlock()

	c.Close()
}

func TestReconnect_NoOverflowFlushesProperly(t *testing.T) {
	// Setup: large buffer that will NOT overflow.
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 8192,
	})
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(StateReconnecting)

	// Write exactly 3 TS packets — well within the buffer capacity.
	for i := 0; i < 3; i++ {
		c.ringBuf.Write(makeTSPacket(0x100, i == 0))
	}
	require.False(t, c.ringBuf.Overflowed(), "ring buffer should not overflow")
	require.Equal(t, 188*3, c.ringBuf.Len())

	var mu sync.Mutex
	var callbackFired bool
	var callbackOverflowed bool

	c.onReconnect = func(overflowed bool) {
		mu.Lock()
		callbackFired = true
		callbackOverflowed = overflowed
		mu.Unlock()
	}

	mock := &mockSRTConn{}
	c.connectFn = func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
		return mock, nil
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return callbackFired
	}, 5*time.Second, 50*time.Millisecond)

	// No overflow: data should have been flushed.
	require.Equal(t, int64(188*3), mock.written.Load(),
		"buffered data should be flushed when no overflow")

	mu.Lock()
	require.False(t, callbackOverflowed,
		"onReconnect callback should report overflowed=false")
	mu.Unlock()

	// pendingIDR should NOT be set (no overflow).
	require.False(t, c.pendingIDR.Load(),
		"pendingIDR should not be set when no overflow occurred")

	// Delta frames should pass through immediately.
	deltaPkt := makeTSPacket(0x100, false)
	n, err := c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188*4), mock.written.Load(),
		"delta frame should pass through without IDR gate")

	c.Close()
}
