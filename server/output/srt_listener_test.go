package output

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockListenerConn struct {
	id       string
	mu       sync.Mutex
	received [][]byte
	closed   atomic.Bool
	writeFn  func([]byte) (int, error)
}

func (m *mockListenerConn) Write(data []byte) (int, error) {
	if m.writeFn != nil {
		return m.writeFn(data)
	}
	m.mu.Lock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.received = append(m.received, cp)
	m.mu.Unlock()
	return len(data), nil
}

func (m *mockListenerConn) Close() {
	m.closed.Store(true)
}

func TestSRTListener_ID(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	require.Equal(t, "srt-listener", l.ID())
}

func TestSRTListener_StatusBeforeStart(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	status := l.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestSRTListener_AddAndRemoveConnection(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	mock := &mockListenerConn{id: "test-1"}
	err := l.AddConnection("test-1", mock)
	require.NoError(t, err)

	require.Equal(t, 1, l.ConnectionCount())

	l.RemoveConnection("test-1")
	require.Equal(t, 0, l.ConnectionCount())
}

func TestSRTListener_FanOut(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	mock1 := &mockListenerConn{id: "c1"}
	mock2 := &mockListenerConn{id: "c2"}
	require.NoError(t, l.AddConnection("c1", mock1))
	require.NoError(t, l.AddConnection("c2", mock2))

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	data := []byte("test-ts-data")
	n, err := l.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	// Give goroutines time to process
	time.Sleep(50 * time.Millisecond)

	mock1.mu.Lock()
	require.Len(t, mock1.received, 1)
	require.Equal(t, data, mock1.received[0])
	mock1.mu.Unlock()

	mock2.mu.Lock()
	require.Len(t, mock2.received, 1)
	require.Equal(t, data, mock2.received[0])
	mock2.mu.Unlock()
}

func TestSRTListener_MaxConnections(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	require.NoError(t, l.AddConnection("c1", &mockListenerConn{id: "c1"}))
	require.NoError(t, l.AddConnection("c2", &mockListenerConn{id: "c2"}))

	err := l.AddConnection("c3", &mockListenerConn{id: "c3"})
	require.Error(t, err, "should reject connection beyond max")
	require.Equal(t, 2, l.ConnectionCount())
}

func TestSRTListener_Close(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	ctx, cancel := context.WithCancel(context.Background())
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	mock := &mockListenerConn{id: "c1"}
	require.NoError(t, l.AddConnection("c1", mock))

	err := l.Close()
	require.NoError(t, err)
	require.Equal(t, StateStopped, *l.state.Load())
	require.True(t, mock.closed.Load())
}

func TestSRTListener_SlowClientDrops(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	// Mock that blocks on Write (simulating slow client)
	blockCh := make(chan struct{})
	slow := &mockListenerConn{
		id: "slow",
		writeFn: func(data []byte) (int, error) {
			<-blockCh // block forever
			return len(data), nil
		},
	}
	require.NoError(t, l.AddConnection("slow", slow))
	time.Sleep(10 * time.Millisecond)

	// Fill the channel buffer
	for i := 0; i < 100; i++ {
		_, _ = l.Write([]byte("data"))
	}

	// Should not block — slow client drops are non-blocking
	done := make(chan struct{})
	go func() {
		_, _ = l.Write([]byte("final"))
		close(done)
	}()

	select {
	case <-done:
		// Good — Write returned without blocking
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Write blocked on slow client")
	}

	close(blockCh)
}

func TestSRTListener_WriteErrorRemovesClient(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	errConn := &mockListenerConn{
		id: "err-client",
		writeFn: func(data []byte) (int, error) {
			return 0, fmt.Errorf("connection reset")
		},
	}
	require.NoError(t, l.AddConnection("err-client", errConn))
	require.Equal(t, 1, l.ConnectionCount())

	// Send data — the writer goroutine should detect the error and remove
	_, _ = l.Write([]byte("trigger-error"))

	// Wait for the writer goroutine to detect the error
	time.Sleep(50 * time.Millisecond)

	require.Equal(t, 0, l.ConnectionCount())
	require.True(t, errConn.closed.Load())
}

func TestSRTListener_Start(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := l.Start(ctx)
	require.NoError(t, err)

	status := l.Status()
	require.Equal(t, StateActive, status.State)
	require.NotZero(t, status.StartedAt)

	require.NoError(t, l.Close())
}

func TestSRTListener_SRTStatusSnapshot(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 4})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	require.NoError(t, l.AddConnection("c1", &mockListenerConn{id: "c1"}))
	_, _ = l.Write([]byte("data"))

	snap := l.SRTStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "listener", snap.Mode)
	require.Equal(t, 9999, snap.Port)
	require.Equal(t, 1, snap.Connections)
	require.Equal(t, int64(4), snap.BytesWritten)
}

func TestSRTListener_RemoveNonexistent(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	// Should not panic
	l.RemoveConnection("nonexistent")
	require.Equal(t, 0, l.ConnectionCount())
}

func TestSRTListener_ConcurrentFanOut(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	const numConns = 4
	const numWrites = 50
	mocks := make([]*mockListenerConn, numConns)

	for i := 0; i < numConns; i++ {
		m := &mockListenerConn{id: fmt.Sprintf("c%d", i)}
		mocks[i] = m
		require.NoError(t, l.AddConnection(m.id, m))
	}

	time.Sleep(10 * time.Millisecond)

	// Concurrent writes
	var wg sync.WaitGroup
	for i := 0; i < numWrites; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = fmt.Fprintf(l, "data-%d", idx)
		}(i)
	}
	wg.Wait()

	// Give goroutines time to drain
	time.Sleep(100 * time.Millisecond)

	for _, m := range mocks {
		m.mu.Lock()
		require.Equal(t, numWrites, len(m.received),
			"connection %s should receive all writes", m.id)
		m.mu.Unlock()
	}
}

func TestSRTListener_DefaultConfig(t *testing.T) {
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	require.Equal(t, defaultMaxConns, l.config.MaxConns)
	require.Equal(t, defaultSRTLatency, l.config.Latency)
}

func TestSRTListener_BandwidthHintsStored(t *testing.T) {
	t.Parallel()
	l := NewSRTListener(SRTListenerConfig{
		Port:       9999,
		InputBW:    625000, // 5 Mbps in bytes/sec
		OverheadBW: 15,     // 15% overhead
	})
	require.Equal(t, int64(625000), l.config.InputBW)
	require.Equal(t, 15, l.config.OverheadBW)
}

func TestSRTListener_BandwidthHintsZeroByDefault(t *testing.T) {
	t.Parallel()
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	require.Equal(t, int64(0), l.config.InputBW)
	require.Equal(t, 0, l.config.OverheadBW)
}

func TestSRTListener_BandwidthHintsPassedToAcceptFn(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var capturedConfig SRTListenerConfig

	l := NewSRTListener(SRTListenerConfig{
		Port:       9999,
		InputBW:    625000,
		OverheadBW: 15,
	})
	l.acceptFn = func(ctx context.Context, config SRTListenerConfig) error {
		mu.Lock()
		capturedConfig = config
		mu.Unlock()
		return nil
	}

	err := l.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	// Give goroutine time to call acceptFn
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return capturedConfig.Port == 9999
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, int64(625000), capturedConfig.InputBW)
	require.Equal(t, 15, capturedConfig.OverheadBW)
}
