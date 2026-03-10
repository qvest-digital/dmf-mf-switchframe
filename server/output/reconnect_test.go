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
// 1. Data written during disconnect is buffered but discarded on reconnect (remote decoder has no state).
// 2. After reconnect, writes are gated until a keyframe (IDR).
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
	c.state.Store(ptrTo(StateActive))
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
	c.state.Store(ptrTo(StateReconnecting))
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

	// Phase 4: Buffered data should be discarded — the remote decoder has no
	// state after reconnect, so flushing delta frames would cause errors.
	require.Equal(t, int64(0), mock2.written.Load(),
		"buffered data should be discarded on reconnect, not flushed")

	// Verify the ring buffer is now empty (ReadAll clears it).
	require.Equal(t, 0, c.ringBuf.Len(), "ring buffer should be empty after reconnect")

	// Phase 5: After reconnect, pendingIDR is set — always gate until
	// keyframe after any reconnect. Delta frames should be gated.
	require.True(t, c.pendingIDR.Load(),
		"pendingIDR should be set after any reconnect")

	// Write a delta -- should be gated.
	n, err := c.Write(pkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(0), mock2.written.Load(),
		"delta write should be gated after reconnect")

	// Write a keyframe to clear the gate.
	keyPkt := makeTSPacket(0x100, true)
	n, err = c.Write(keyPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188), mock2.written.Load(),
		"keyframe should pass through and clear IDR gate")
	require.False(t, c.pendingIDR.Load(), "pendingIDR should be cleared after keyframe")

	// Now a delta goes directly to the new connection.
	n, err = c.Write(pkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188*2), mock2.written.Load(),
		"new write should go directly to the reconnected connection")

	_ = c.Close()
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
	c.state.Store(ptrTo(StateReconnecting))

	// Write enough data to overflow the ring buffer.
	bigData := make([]byte, 512)
	_, _ = c.ringBuf.Write(bigData)
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

	_ = c.Close()
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
	c.state.Store(ptrTo(StateReconnecting))

	// Overflow the ring buffer.
	_, _ = c.ringBuf.Write(make([]byte, 256))
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

	_ = c.Close()
}

func TestReconnect_NoOverflowDiscardsAndGates(t *testing.T) {
	// Setup: large buffer that will NOT overflow.
	c := NewSRTCaller(SRTCallerConfig{
		Address:        "srt.example.com",
		Port:           9998,
		RingBufferSize: 8192,
	})
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	c.state.Store(ptrTo(StateReconnecting))

	// Write exactly 3 TS packets — well within the buffer capacity.
	for i := 0; i < 3; i++ {
		_, _ = c.ringBuf.Write(makeTSPacket(0x100, i == 0))
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

	// Buffered data should be discarded — remote decoder has no state.
	require.Equal(t, int64(0), mock.written.Load(),
		"buffered data should be discarded on reconnect, not flushed")

	mu.Lock()
	require.False(t, callbackOverflowed,
		"onReconnect callback should report overflowed=false")
	mu.Unlock()

	// pendingIDR is always set after reconnect.
	// The remote decoder has lost state and needs a keyframe.
	require.True(t, c.pendingIDR.Load(),
		"pendingIDR should be set after any reconnect")

	// Delta frames should be gated.
	deltaPkt := makeTSPacket(0x100, false)
	n, err := c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(0), mock.written.Load(),
		"delta frame should be gated after reconnect")

	// Keyframe clears the gate.
	keyframePkt := makeTSPacket(0x100, true)
	n, err = c.Write(keyframePkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188), mock.written.Load(),
		"keyframe should pass through")
	require.False(t, c.pendingIDR.Load(), "gate should be cleared after keyframe")

	// Subsequent delta frames pass through.
	n, err = c.Write(deltaPkt)
	require.NoError(t, err)
	require.Equal(t, 188, n)
	require.Equal(t, int64(188*2), mock.written.Load(),
		"delta frame should pass through after gate cleared")

	_ = c.Close()
}
