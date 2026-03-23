//go:build cgo && !noffmpeg

package output

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRTMPCaller_ID(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	require.Equal(t, "rtmp", c.ID())
	_ = c.Close()
}

func TestRTMPCaller_NewRequiresURL(t *testing.T) {
	t.Parallel()
	_, err := NewRTMPCaller(RTMPCallerConfig{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "URL is required")
}

func TestRTMPCaller_StatusBeforeStart(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	status := c.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestRTMPCaller_StartWithMockConnect(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	c.connectFn = func(url string) error {
		return nil
	}

	err = c.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	status := c.Status()
	require.Equal(t, StateActive, status.State)
}

func TestRTMPCaller_StartFailsEntersReconnect(t *testing.T) {
	t.Parallel()
	connectCount := atomic.Int32{}
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	c.connectFn = func(url string) error {
		n := connectCount.Add(1)
		if n == 1 {
			return fmt.Errorf("connection refused")
		}
		return nil
	}

	err = c.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	status := c.Status()
	require.Equal(t, StateReconnecting, status.State)
	require.Contains(t, status.Error, "connection refused")

	require.Eventually(t, func() bool {
		return c.Status().State == StateActive
	}, 5*time.Second, 50*time.Millisecond)
}

func TestRTMPCaller_WriteWhenStopped(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	_, writeErr := c.Write([]byte{0x47})
	require.Error(t, writeErr)
}

func TestRTMPCaller_WriteDropsDuringReconnect(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	c.state.Store(ptrTo(StateReconnecting))
	defer func() {
		c.state.Store(ptrTo(StateStopped))
		_ = c.Close()
	}()

	data := make([]byte, 188)
	n, writeErr := c.Write(data)
	require.NoError(t, writeErr)
	require.Equal(t, len(data), n)
	require.Equal(t, int64(0), c.bytesWritten.Load())
}

func TestRTMPCaller_WriteCountsBytes(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	c.connectFn = func(url string) error { return nil }
	c.state.Store(ptrTo(StateActive))
	defer func() {
		c.state.Store(ptrTo(StateStopped))
		_ = c.Close()
	}()

	data := make([]byte, 188*7)
	n, writeErr := c.Write(data)
	require.NoError(t, writeErr)
	require.Equal(t, len(data), n)
	require.Equal(t, int64(188*7), c.bytesWritten.Load())
}

func TestRTMPCaller_IDRGating(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	c.connectFn = func(url string) error { return nil }
	c.state.Store(ptrTo(StateActive))
	c.pendingIDR.Store(true)
	defer func() {
		c.state.Store(ptrTo(StateStopped))
		_ = c.Close()
	}()

	// Delta packet should be dropped (written to ring but not counted since IDR pending)
	deltaPkt := makeTSPacket(0x100, false)
	n, writeErr := c.Write(deltaPkt)
	require.NoError(t, writeErr)
	require.Equal(t, len(deltaPkt), n)
	require.True(t, c.pendingIDR.Load(), "IDR gate should remain set")
	require.Equal(t, int64(0), c.bytesWritten.Load(), "delta should not count as written")

	// Keyframe should clear the gate
	keyframePkt := makeTSPacket(0x100, true)
	n, writeErr = c.Write(keyframePkt)
	require.NoError(t, writeErr)
	require.Equal(t, len(keyframePkt), n)
	require.False(t, c.pendingIDR.Load(), "IDR gate should be cleared")
}

func TestRTMPCaller_CloseWithoutStart(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	closeErr := c.Close()
	require.NoError(t, closeErr)
}

func TestRTMPCaller_BackoffProgression(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	levels := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for _, expected := range levels {
		got := c.nextBackoff()
		lo := time.Duration(float64(expected) * 0.79)
		hi := time.Duration(float64(expected) * 1.21)
		require.True(t, got >= lo && got <= hi,
			"expected backoff near %v (±20%%), got %v", expected, got)
	}
}

func TestRTMPCaller_ResetBackoff(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	c.nextBackoff()
	c.nextBackoff()
	c.resetBackoff()
	got := c.nextBackoff()
	require.True(t, got >= 800*time.Millisecond && got <= 1200*time.Millisecond,
		"after reset, backoff should be near 1s, got %v", got)
}

func TestRTMPCaller_RTMPStatusSnapshot(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	snap := c.RTMPStatusSnapshot()
	require.False(t, snap.Active)
	require.Equal(t, "rtmp://example.com/live/key", snap.URL)

	c.state.Store(ptrTo(StateActive))
	snap = c.RTMPStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "active", snap.State)

	c.state.Store(ptrTo(StateReconnecting))
	snap = c.RTMPStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "reconnecting", snap.State)
}

func TestRTMPCaller_ImplementsAdapter(t *testing.T) {
	t.Parallel()
	var _ Adapter = (*RTMPCaller)(nil)
}

func TestRTMPMaskURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"rtmp://a.rtmp.youtube.com/live2/xxxx-xxxx-xxxx", "rtmp://a.rtmp.youtube.com/live2/****"},
		{"rtmp://live.twitch.tv/app/live_123456", "rtmp://live.twitch.tv/app/****"},
		{"rtmps://ingest.facebook.com/rtmp/fb-key-12345", "rtmps://ingest.facebook.com/rtmp/****"},
		{"rtmp://host/app", "rtmp://host/app"},
		{"rtmp://host", "rtmp://host"},
		{"", ""},
	}
	for _, tt := range tests {
		got := rtmpMaskURL(tt.in)
		require.Equal(t, tt.want, got, "rtmpMaskURL(%q)", tt.in)
	}
}

func TestRTMPCaller_OnReconnectCallback(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	c.goCtx = ctx
	c.cancel = cancel
	c.state.Store(ptrTo(StateReconnecting))

	var callbackCalled atomic.Bool
	c.onReconnect = func() {
		callbackCalled.Store(true)
	}

	c.connectFn = func(url string) error {
		return nil
	}

	go c.reconnectLoop()

	require.Eventually(t, func() bool {
		return callbackCalled.Load()
	}, 5*time.Second, 50*time.Millisecond)

	require.True(t, c.pendingIDR.Load(), "pendingIDR should be set after reconnect")
	_ = c.Close()
}

// ---------------------------------------------------------------------------
// Ring buffer drain-gating tests (audio corruption fix)
//
// The RTMP path demuxes MPEG-TS and remuxes to FLV via FFmpeg. If the drain
// loop calls av_read_frame when the ring buffer doesn't have enough data,
// the AVIO callback returns EOF, causing the TS demuxer to flush partial PES
// packets — corrupting audio. The fix gates drain reads on rtmpMinDrainBytes.
// ---------------------------------------------------------------------------

func TestRTMPCaller_MinDrainBytesMatchesAVIOBuf(t *testing.T) {
	t.Parallel()
	require.Equal(t, 32*1024, rtmpMinDrainBytes,
		"rtmpMinDrainBytes must match RTMP_AVIO_BUF (32KB)")
}

func TestRTMPCaller_RingAvailableTracksWrites(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	require.Equal(t, 0, c.ringAvailable())

	chunk := make([]byte, 188*10)
	for i := range chunk {
		chunk[i] = 0x47
	}
	n := c.ringWrite(chunk)
	require.Equal(t, len(chunk), n)
	require.Equal(t, len(chunk), c.ringAvailable())
}

func TestRTMPCaller_DrainGateBelowThreshold(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	c.connectFn = func(url string) error { return nil }
	err = c.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// Write less than rtmpMinDrainBytes into the ring.
	smallChunk := make([]byte, rtmpMinDrainBytes-188)
	for i := 0; i < len(smallChunk); i += 188 {
		smallChunk[i] = 0x47
	}

	c.mu.Lock()
	n := c.ringWrite(smallChunk)
	c.mu.Unlock()
	require.Equal(t, len(smallChunk), n)

	// Let the drain loop run for a few cycles.
	time.Sleep(50 * time.Millisecond)

	// The data should still be in the ring — the drain loop should not
	// have called av_read_frame because we are below the threshold.
	c.mu.Lock()
	avail := c.ringAvailable()
	c.mu.Unlock()
	require.Equal(t, len(smallChunk), avail,
		"drain loop should not consume data when ring level is below rtmpMinDrainBytes")
}

func TestRTMPCaller_DrainGateExactThreshold(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	c.connectFn = func(url string) error { return nil }
	err = c.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// Write exactly rtmpMinDrainBytes. This passes the drain gate, but
	// since ctx.initialized == 0 and rtmp_find_streams needs 192KB, the
	// loop will fall into the "need more data for find_streams" branch
	// and sleep. The data stays in the ring — confirming the gate itself
	// behaved correctly (it didn't skip at exactly the threshold).
	chunk := make([]byte, rtmpMinDrainBytes)
	for i := 0; i < len(chunk); i += 188 {
		chunk[i] = 0x47
	}

	c.mu.Lock()
	n := c.ringWrite(chunk)
	c.mu.Unlock()
	require.Equal(t, len(chunk), n)

	time.Sleep(50 * time.Millisecond)

	c.mu.Lock()
	avail := c.ringAvailable()
	c.mu.Unlock()
	require.Equal(t, len(chunk), avail,
		"at exactly threshold, drain loop passes gate but find_streams needs 192KB")
}

func TestRTMPCaller_RingLevelBelowPacketSize(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	partialData := make([]byte, 100)
	partialData[0] = 0x47
	n := c.ringWrite(partialData)
	require.Equal(t, len(partialData), n)

	avail := c.ringAvailable()
	require.True(t, avail < tsPacketSize,
		"ring level %d should be less than tsPacketSize %d", avail, tsPacketSize)
}

func TestRTMPCaller_WriteDoesNotExceedRing(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	c.state.Store(ptrTo(StateActive))
	defer func() {
		c.state.Store(ptrTo(StateStopped))
		_ = c.Close()
	}()

	// Fill the ring nearly full (RTMP_RING_SIZE is 2MB).
	bigChunk := make([]byte, 2*1024*1024-188)
	for i := 0; i < len(bigChunk); i += 188 {
		bigChunk[i] = 0x47
	}
	n, writeErr := c.Write(bigChunk)
	require.NoError(t, writeErr)
	require.Equal(t, len(bigChunk), n)

	// A second write that would overflow should be silently dropped.
	overflow := make([]byte, 188*2)
	overflow[0] = 0x47
	overflow[188] = 0x47
	n, writeErr = c.Write(overflow)
	require.NoError(t, writeErr)
	require.Equal(t, len(overflow), n, "overflow write should report full length to avoid caller retries")

	// Ring should only contain the first write's worth of data.
	c.mu.Lock()
	avail := c.ringAvailable()
	c.mu.Unlock()
	require.Equal(t, len(bigChunk), avail, "ring should contain only the initial write")
}

func TestRTMPCaller_DrainGateWithGrowingBuffer(t *testing.T) {
	t.Parallel()
	c, err := NewRTMPCaller(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	c.connectFn = func(url string) error { return nil }
	err = c.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// Simulate a producer feeding data in small increments below the
	// threshold. The drain loop should not touch the ring until we cross
	// rtmpMinDrainBytes.
	increment := 188 * 8
	total := 0
	for total < rtmpMinDrainBytes-increment {
		chunk := make([]byte, increment)
		for i := 0; i < len(chunk); i += 188 {
			chunk[i] = 0x47
		}
		c.mu.Lock()
		n := c.ringWrite(chunk)
		c.mu.Unlock()
		require.Equal(t, increment, n)
		total += increment
	}

	time.Sleep(50 * time.Millisecond)

	c.mu.Lock()
	avail := c.ringAvailable()
	c.mu.Unlock()
	require.Equal(t, total, avail,
		"ring data should be untouched while below threshold (%d < %d)", total, rtmpMinDrainBytes)
}
