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
