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

	require.Equal(t, time.Second, c.nextBackoff())
	require.Equal(t, 2*time.Second, c.nextBackoff())
	require.Equal(t, 4*time.Second, c.nextBackoff())
	require.Equal(t, 8*time.Second, c.nextBackoff())
	require.Equal(t, 16*time.Second, c.nextBackoff())
	require.Equal(t, 30*time.Second, c.nextBackoff()) // cap
	require.Equal(t, 30*time.Second, c.nextBackoff()) // stays at cap
}

func TestSRTCaller_ResetBackoff(t *testing.T) {
	c := NewSRTCaller(SRTCallerConfig{
		Address: "srt.example.com",
		Port:    9998,
	})

	c.nextBackoff() // 1s
	c.nextBackoff() // 2s
	c.resetBackoff()
	require.Equal(t, time.Second, c.nextBackoff()) // back to 1s
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
