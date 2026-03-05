package output

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slowAdapter is a test adapter that sleeps on every Write call.
type slowAdapter struct {
	mu       sync.Mutex
	writes   [][]byte
	delay    time.Duration
	state    AdapterState
	blocking bool // if true, blocks forever until unblocked
	unblock  chan struct{}
}

func newSlowAdapter(delay time.Duration) *slowAdapter {
	return &slowAdapter{
		delay: delay,
		state: StateStopped,
	}
}

func newBlockingAdapter() *slowAdapter {
	return &slowAdapter{
		blocking: true,
		unblock:  make(chan struct{}),
		state:    StateStopped,
	}
}

func (a *slowAdapter) ID() string { return "slow-test" }

func (a *slowAdapter) Start(_ context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = StateActive
	return nil
}

func (a *slowAdapter) Write(tsData []byte) (int, error) {
	if a.blocking {
		<-a.unblock
		return len(tsData), nil
	}
	time.Sleep(a.delay)
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]byte, len(tsData))
	copy(cp, tsData)
	a.writes = append(a.writes, cp)
	return len(tsData), nil
}

func (a *slowAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = StateStopped
	return nil
}

func (a *slowAdapter) Status() AdapterStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AdapterStatus{State: a.state}
}

func (a *slowAdapter) getWrites() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([][]byte, len(a.writes))
	copy(out, a.writes)
	return out
}

func TestAsyncAdapter_NonBlocking(t *testing.T) {
	// A slow adapter takes 100ms per write. Without async wrapping,
	// 5 writes would take >= 500ms. With async wrapping, the Write()
	// calls should return nearly instantly.
	inner := newSlowAdapter(100 * time.Millisecond)

	async := NewAsyncAdapter(inner, 64)
	require.NoError(t, async.Start(context.Background()))

	start := time.Now()
	for i := 0; i < 5; i++ {
		data := []byte{byte(i), 0xAA, 0xBB}
		n, err := async.Write(data)
		require.NoError(t, err)
		require.Equal(t, len(data), n)
	}
	elapsed := time.Since(start)
	require.Less(t, elapsed, 50*time.Millisecond,
		"all 5 writes should complete in under 50ms (non-blocking)")

	// Wait for the slow adapter to drain all 5 writes.
	// 5 writes * 100ms = 500ms, give some headroom.
	require.Eventually(t, func() bool {
		return len(inner.getWrites()) == 5
	}, 2*time.Second, 10*time.Millisecond, "all 5 writes should drain to inner adapter")

	// Verify the data integrity.
	writes := inner.getWrites()
	for i, w := range writes {
		require.Equal(t, byte(i), w[0], "write %d should have correct first byte", i)
		require.Equal(t, []byte{0xAA, 0xBB}, w[1:], "write %d should have correct payload", i)
	}

	async.Stop()
}

func TestAsyncAdapter_DropsOnFullBuffer(t *testing.T) {
	// Adapter that blocks forever. Buffer of 2. Send 5 packets.
	// At least some should be dropped.
	inner := newBlockingAdapter()

	async := NewAsyncAdapter(inner, 2)
	require.NoError(t, async.Start(context.Background()))

	for i := 0; i < 5; i++ {
		async.Write([]byte{byte(i)})
	}

	// Give a moment for the channel sends to settle.
	time.Sleep(10 * time.Millisecond)

	drops := async.Dropped()
	require.Greater(t, drops, int64(0), "should have dropped packets when buffer is full")

	// Unblock and clean up.
	close(inner.unblock)
	async.Stop()
}

func TestAsyncAdapter_DelegatesID(t *testing.T) {
	inner := newSlowAdapter(0)
	async := NewAsyncAdapter(inner, 8)
	require.Equal(t, "slow-test", async.ID())
}

func TestAsyncAdapter_DelegatesStatus(t *testing.T) {
	inner := newSlowAdapter(0)
	inner.state = StateActive
	async := NewAsyncAdapter(inner, 8)
	status := async.Status()
	require.Equal(t, StateActive, status.State)
}

func TestAsyncAdapter_DelegatesClose(t *testing.T) {
	inner := newSlowAdapter(0)
	inner.state = StateActive
	async := NewAsyncAdapter(inner, 8)
	require.NoError(t, async.Start(context.Background()))
	async.Stop()
	require.NoError(t, async.Close())
	require.Equal(t, StateStopped, inner.Status().State)
}

func TestAsyncAdapter_StartDelegatesToInner(t *testing.T) {
	inner := newSlowAdapter(0)
	async := NewAsyncAdapter(inner, 8)
	require.NoError(t, async.Start(context.Background()))
	require.Equal(t, StateActive, inner.Status().State)
	async.Stop()
}

func TestAsyncAdapter_CopiesData(t *testing.T) {
	// Verify that AsyncAdapter makes a copy of the data, so the caller
	// can safely reuse the slice after Write returns.
	inner := newSlowAdapter(10 * time.Millisecond)
	async := NewAsyncAdapter(inner, 64)
	require.NoError(t, async.Start(context.Background()))

	buf := []byte{0x01, 0x02, 0x03}
	async.Write(buf)
	// Mutate the original buffer immediately after write.
	buf[0] = 0xFF

	require.Eventually(t, func() bool {
		return len(inner.getWrites()) == 1
	}, time.Second, 5*time.Millisecond)

	writes := inner.getWrites()
	require.Equal(t, byte(0x01), writes[0][0],
		"inner adapter should see original data, not mutated version")

	async.Stop()
}

func TestAsyncAdapter_StopDrainsRemaining(t *testing.T) {
	// Fill buffer, then Stop(). All buffered items should be drained.
	var received atomic.Int64
	inner := newSlowAdapter(0)

	async := NewAsyncAdapter(inner, 64)
	require.NoError(t, async.Start(context.Background()))

	// Send 10 packets.
	for i := 0; i < 10; i++ {
		async.Write([]byte{byte(i)})
	}

	// Stop should block until all buffered items are drained.
	async.Stop()

	_ = received // not needed since we check inner.getWrites()
	writes := inner.getWrites()
	require.Equal(t, 10, len(writes), "all 10 packets should be drained on Stop")
}

func TestAsyncAdapter_ImplementsInterface(t *testing.T) {
	// Compile-time check that AsyncAdapter satisfies OutputAdapter.
	var _ OutputAdapter = (*AsyncAdapter)(nil)
}
