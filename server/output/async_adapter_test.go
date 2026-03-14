package output

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slowAdapter is a test adapter that sleeps on every Write call.
type slowAdapter struct {
	mu       sync.Mutex
	id       string
	writes   [][]byte
	delay    time.Duration
	state    AdapterState
	blocking bool // if true, blocks forever until unblocked
	unblock  chan struct{}
}

func newSlowAdapter(delay time.Duration) *slowAdapter {
	return &slowAdapter{
		id:    "slow-test",
		delay: delay,
		state: StateStopped,
	}
}

func newBlockingAdapter() *slowAdapter {
	return &slowAdapter{
		id:       "blocking-test",
		blocking: true,
		unblock:  make(chan struct{}),
		state:    StateStopped,
	}
}

func (a *slowAdapter) ID() string { return a.id }

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
		_, _ = async.Write([]byte{byte(i)})
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
	_, _ = async.Write(buf)
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
	inner := newSlowAdapter(0)

	async := NewAsyncAdapter(inner, 64)
	require.NoError(t, async.Start(context.Background()))

	// Send 10 packets.
	for i := 0; i < 10; i++ {
		_, _ = async.Write([]byte{byte(i)})
	}

	// Stop should block until all buffered items are drained.
	async.Stop()

	writes := inner.getWrites()
	require.Equal(t, 10, len(writes), "all 10 packets should be drained on Stop")
}

func TestAsyncAdapter_ImplementsInterface(t *testing.T) {
	// Compile-time check that AsyncAdapter satisfies Adapter.
	var _ Adapter = (*AsyncAdapter)(nil)
}

func TestAsyncAdapterDropLogRateLimit(t *testing.T) {
	// Adapter that blocks forever so all writes after buffer fills are dropped.
	inner := newBlockingAdapter()

	// Buffer of 1: first write fills the buffer, subsequent writes drop.
	async := NewAsyncAdapter(inner, 1)
	require.NoError(t, async.Start(context.Background()))

	// Drop many packets rapidly.
	const dropCount = 50
	for i := 0; i < dropCount+1; i++ {
		_, _ = async.Write([]byte{byte(i)})
	}

	// Verify drops occurred.
	require.GreaterOrEqual(t, async.Dropped(), int64(dropCount-1),
		"should have dropped many packets")

	// The rate-limited log should have fired at most once in this tight loop
	// since all drops happen within a single nanosecond-scale burst (well
	// under the 1-second rate limit window). We verify the field exists and
	// was updated (non-zero means the log path was hit at least once).
	lastLog := async.lastDropLog.Load()
	require.Greater(t, lastLog, int64(0),
		"lastDropLog should have been set at least once")

	// Unblock and clean up.
	close(inner.unblock)
	async.Stop()
}

func TestAsyncAdapter_DroppedCounterResetOnStop(t *testing.T) {
	// Start, cause drops, stop, start again. Dropped() should return 0
	// after stop so metrics are not inflated across start/stop cycles.
	inner := newBlockingAdapter()
	async := NewAsyncAdapter(inner, 1)
	require.NoError(t, async.Start(context.Background()))

	// Fill buffer + drop some packets.
	for i := 0; i < 5; i++ {
		_, _ = async.Write([]byte{byte(i)})
	}
	time.Sleep(10 * time.Millisecond)
	require.Greater(t, async.Dropped(), int64(0), "should have drops before stop")

	// Unblock and stop.
	close(inner.unblock)
	async.Stop()

	// After stop, dropped counter should be reset.
	require.Equal(t, int64(0), async.Dropped(),
		"Dropped() should be 0 after Stop()")
}

func TestSlowAdapterDoesntBlockFastAdapter(t *testing.T) {
	// Two adapters share a simulated muxer output callback: one fast (no delay)
	// and one very slow (50ms per write). We send 100 packets through the
	// callback. The fast adapter must receive all 100 packets. The slow adapter
	// will receive some and drop the rest (buffer size 4).
	// Critically, the callback must return near-instantly for each packet,
	// proving the slow adapter does not block the fast one.
	const packetCount = 100

	fastInner := newSlowAdapter(0) // instant writes
	fastInner.id = "fast-adapter"
	slowInner := newSlowAdapter(50 * time.Millisecond) // 50ms per write
	slowInner.id = "slow-adapter"

	fastAsync := NewAsyncAdapter(fastInner, 256) // large buffer, no drops
	slowAsync := NewAsyncAdapter(slowInner, 4)   // tiny buffer, will drop

	require.NoError(t, fastAsync.Start(context.Background()))
	require.NoError(t, slowAsync.Start(context.Background()))

	// Simulate the muxer output callback: fan-out to both adapters.
	start := time.Now()
	for i := 0; i < packetCount; i++ {
		data := []byte{byte(i), 0x47, 0xDA}
		_, _ = fastAsync.Write(data)
		_, _ = slowAsync.Write(data)
	}
	elapsed := time.Since(start)

	// The fan-out loop itself must be non-blocking. 100 iterations should
	// complete well under 50ms (each Write is a non-blocking channel send).
	require.Less(t, elapsed, 50*time.Millisecond,
		"fan-out to %d packets must be non-blocking (took %v)", packetCount, elapsed)

	// Wait for the fast adapter to drain all packets.
	require.Eventually(t, func() bool {
		return len(fastInner.getWrites()) == packetCount
	}, 2*time.Second, 10*time.Millisecond,
		"fast adapter must receive all %d packets", packetCount)

	// The slow adapter should have dropped some packets.
	slowDrops := slowAsync.Dropped()
	require.Greater(t, slowDrops, int64(0),
		"slow adapter must have dropped packets due to backpressure")

	// The slow adapter's received + dropped should equal the total sent.
	// Wait briefly so the slow adapter drains what it can.
	time.Sleep(50 * time.Millisecond)

	// Verify the fast adapter received every packet with correct data.
	fastWrites := fastInner.getWrites()
	require.Len(t, fastWrites, packetCount)
	for i, w := range fastWrites {
		require.Equal(t, byte(i), w[0],
			"fast adapter packet %d should have correct sequence byte", i)
	}

	// Clean up.
	fastAsync.Stop()
	slowAsync.Stop()

	t.Logf("fast adapter: %d received, 0 dropped", len(fastWrites))
	t.Logf("slow adapter: %d received, %d dropped",
		len(slowInner.getWrites()), slowDrops)
}
