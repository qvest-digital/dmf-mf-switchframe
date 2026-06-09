package output

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/srt"
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

// mockStatsListenerConn implements srtConn + SRTStatter for testing listener stats polling.
type mockStatsListenerConn struct {
	mu        sync.Mutex
	received  [][]byte
	closed    atomic.Bool
	pollCount atomic.Int32
	stats     SRTConnStats
}

func (m *mockStatsListenerConn) Write(data []byte) (int, error) {
	m.mu.Lock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.received = append(m.received, cp)
	m.mu.Unlock()
	return len(data), nil
}

func (m *mockStatsListenerConn) Close() {
	m.closed.Store(true)
}

func (m *mockStatsListenerConn) SRTStats(clear bool) SRTConnStats {
	m.pollCount.Add(1)
	return m.stats
}

func TestSRTListener_StatsPolling(t *testing.T) {
	t.Parallel()
	mock := &mockStatsListenerConn{
		stats: SRTConnStats{
			RTT:          30 * time.Millisecond,
			MbpsSendRate: 6.5,
			SendLossRate: 0.02,
			SendPackets:  2000,
			SendLoss:     20,
			SendDropped:  3,
			Retransmits:  8,
			MsSndBuf:     60 * time.Millisecond,
			SendBufSize:  48,
			FlightSize:   24,
		},
	}

	cs := srt.NewConnStats("send", "output-listener", 120)

	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	l.SetStats(cs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	err := l.AddConnection("stats-conn", mock)
	require.NoError(t, err)

	// Wait for at least one stats poll (1-second ticker + margin).
	require.Eventually(t, func() bool {
		return mock.pollCount.Load() >= 1
	}, 3*time.Second, 50*time.Millisecond)

	snap := cs.Snapshot()
	require.InDelta(t, 30.0, snap.RTTMs, 0.1, "RTT should be 30ms")
	require.InDelta(t, 6.5, snap.SendRateMbps, 0.01)
	require.InDelta(t, 0.02, snap.SendLossRatePct, 0.001)
	require.Equal(t, int64(2000), snap.PacketsSent)
	require.Equal(t, int64(20), snap.PacketsSendLost)
	require.Equal(t, int64(3), snap.PacketsSendDrop)
}

func TestSRTListener_StatsPollingStopsOnRemove(t *testing.T) {
	t.Parallel()
	mock := &mockStatsListenerConn{}
	cs := srt.NewConnStats("send", "output-listener", 120)

	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	l.SetStats(cs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	err := l.AddConnection("poll-stop", mock)
	require.NoError(t, err)

	// Wait for at least one poll.
	require.Eventually(t, func() bool {
		return mock.pollCount.Load() >= 1
	}, 3*time.Second, 50*time.Millisecond)

	// Remove the connection (cancels the per-connection context).
	l.RemoveConnection("poll-stop")
	countAtRemove := mock.pollCount.Load()

	// Wait to verify no more polls happen.
	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, countAtRemove, mock.pollCount.Load(),
		"stats polling should stop after RemoveConnection")
}

func TestSRTListener_NoStatsWithoutSetStats(t *testing.T) {
	t.Parallel()
	mock := &mockStatsListenerConn{}

	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	// Intentionally do NOT call SetStats.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	err := l.AddConnection("no-stats", mock)
	require.NoError(t, err)

	// Wait a bit — the stats poll goroutine should not have been started.
	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, int32(0), mock.pollCount.Load(),
		"no stats polling should occur without SetStats")

	l.RemoveConnection("no-stats")
}

func TestSRTListener_StatsPollingWithNonStatterConn(t *testing.T) {
	t.Parallel()
	// A plain mockListenerConn does not implement SRTStatter.
	mock := &mockListenerConn{id: "plain"}
	cs := srt.NewConnStats("send", "output-listener", 120)

	l := NewSRTListener(SRTListenerConfig{Port: 9999, MaxConns: 8})
	l.SetStats(cs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.ctx = ctx
	l.cancel = cancel
	l.state.Store(ptrTo(StateActive))

	// Should not panic — connStatsPoll exits immediately if conn is not SRTStatter.
	err := l.AddConnection("plain", mock)
	require.NoError(t, err)

	// Wait a bit to confirm no crash.
	time.Sleep(200 * time.Millisecond)

	snap := cs.Snapshot()
	require.Equal(t, float64(0), snap.RTTMs, "no stats should be updated with non-statter conn")

	l.RemoveConnection("plain")
}

// TestSRTListener_AcceptLoopErrorSetsErrorState verifies that a failed
// accept loop (e.g., the bind fails because the port is already in use)
// surfaces as StateError with the error message, instead of the listener
// reporting StateActive forever while no client can ever connect.
func TestSRTListener_AcceptLoopErrorSetsErrorState(t *testing.T) {
	t.Parallel()
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	l.acceptFn = func(ctx context.Context, config SRTListenerConfig) error {
		return fmt.Errorf("srt listen :9999: address already in use")
	}

	require.NoError(t, l.Start(context.Background()))
	defer func() { _ = l.Close() }()

	require.Eventually(t, func() bool {
		return l.Status().State == StateError
	}, 2*time.Second, 10*time.Millisecond,
		"listener must report StateError after the accept loop fails to bind")
	require.Contains(t, l.Status().Error, "address already in use")

	snap := l.SRTStatusSnapshot()
	require.False(t, snap.Active, "failed listener must not report Active")
}

// TestSRTListener_AcceptLoopErrorAfterCloseKeepsStopped verifies that an
// accept-loop error that loses the race with Close does not overwrite the
// terminal StateStopped.
func TestSRTListener_AcceptLoopErrorAfterCloseKeepsStopped(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	l := NewSRTListener(SRTListenerConfig{Port: 9999})
	l.acceptFn = func(ctx context.Context, config SRTListenerConfig) error {
		<-release
		return fmt.Errorf("late accept error")
	}

	require.NoError(t, l.Start(context.Background()))
	require.NoError(t, l.Close())
	close(release)

	// Give the accept goroutine time to observe the error path.
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, StateStopped, l.Status().State,
		"a late accept error must not overwrite StateStopped")
}

// TestSRTListener_AcceptLoopErrorRacingCloseConvergesToStopped stresses the
// window where the accept goroutine's Active->Error transition races Close's
// Store of StateStopped. The transition must be a CompareAndSwap on the loaded
// state pointer: a plain load-then-store could load StateActive before Close
// stores StateStopped and then overwrite the terminal state with StateError.
// After both Close and the accept goroutine finish, the state must always
// converge to StateStopped.
func TestSRTListener_AcceptLoopErrorRacingCloseConvergesToStopped(t *testing.T) {
	t.Parallel()
	for i := 0; i < 200; i++ {
		l := NewSRTListener(SRTListenerConfig{Port: 9999})
		l.acceptFn = func(ctx context.Context, config SRTListenerConfig) error {
			return fmt.Errorf("immediate accept error")
		}

		// Start spawns the accept goroutine, which errors immediately and
		// races the Close below for the state transition.
		require.NoError(t, l.Start(context.Background()))
		require.NoError(t, l.Close())

		// Transient StateError is allowed (accept goroutine CAS won before
		// Close stored Stopped), but the final state must be StateStopped.
		require.Eventually(t, func() bool {
			return l.Status().State == StateStopped
		}, 2*time.Second, 100*time.Microsecond,
			"iteration %d: accept error racing Close must not leave the listener in a non-Stopped state", i)
	}
}
