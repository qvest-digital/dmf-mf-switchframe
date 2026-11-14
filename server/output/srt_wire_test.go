package output

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	srt "github.com/zsiec/srtgo"
)

// Compile-time interface check.
var _ srtConn = (*srtgoConn)(nil)

func TestSRTConnect_InvalidAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := SRTConnect(ctx, SRTCallerConfig{
		Address: "192.0.2.1", // TEST-NET, unreachable
		Port:    0,           // invalid port
		Latency: 120,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "srt dial")
}

func TestManagerSetSRTWiring(t *testing.T) {
	mgr := NewManager()

	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			return nil, nil
		},
		func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error {
			return nil
		},
	)

	assert.NotNil(t, mgr.srtConnectFn)
	assert.NotNil(t, mgr.srtAcceptFn)
}

// startSRTListenerOutput runs an SRT listener output on an ephemeral port
// and returns it with the address it bound.
func startSRTListenerOutput(t *testing.T) (*SRTListener, *net.UDPAddr) {
	t.Helper()
	lns := make(chan *srt.Listener, 1)
	srtListen = func(addr string, cfg srt.Config) (*srt.Listener, error) {
		ln, err := srt.Listen(addr, cfg)
		if err == nil {
			lns <- ln
		}
		return ln, err
	}
	t.Cleanup(func() { srtListen = srt.Listen })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := NewSRTListener(SRTListenerConfig{Port: 0})
	require.NoError(t, l.Start(ctx))
	t.Cleanup(func() { _ = l.Close() })
	go func() { _ = SRTAcceptLoop(ctx, l.config, l) }()

	select {
	case ln := <-lns:
		port := ln.Addr().(*net.UDPAddr).Port
		return l, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	case <-time.After(5 * time.Second):
		t.Fatal("SRT listener output never bound")
		return nil, nil
	}
}

// acceptOne listens on an ephemeral loopback port and returns the address
// and a channel that yields the first connection accepted there.
func acceptOne(t *testing.T) (*net.UDPAddr, <-chan *srt.Conn) {
	t.Helper()
	ln, err := srt.Listen("127.0.0.1:0", srt.Config{Latency: 20 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	ch := make(chan *srt.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			t.Cleanup(func() { _ = c.Close() })
			ch <- c
		}
	}()
	return ln.Addr().(*net.UDPAddr), ch
}

// burstSpread feeds an SRT output 1 s of steady frames, framePkts packets
// every frameInterval, and then one write of burstPkts packets, the way an
// encoder emits a keyframe. It returns the rate pacing reports right before
// the keyframe, and logs how long the receiver took from the first to the
// last packet of the keyframe that it received.
func burstSpread(t *testing.T, write func([]byte), rx *srt.Conn, frameInterval time.Duration, framePkts, burstPkts int, pacing func() float64) float64 {
	t.Helper()
	var mu sync.Mutex
	var first, last time.Time
	n := 0
	go func() {
		buf := make([]byte, 1500)
		for {
			k, err := rx.Read(buf)
			if err != nil {
				return
			}
			if k == 0 || buf[0] != 1 {
				continue
			}
			now := time.Now()
			mu.Lock()
			if n == 0 {
				first = now
			}
			last = now
			n++
			mu.Unlock()
		}
	}()

	frame := make([]byte, framePkts*1316)
	for start := time.Now(); time.Since(start) < time.Second; {
		write(frame)
		time.Sleep(frameInterval)
	}
	mbps := pacing()
	burst := make([]byte, burstPkts*1316)
	for i := 0; i < len(burst); i += 1316 {
		burst[i] = 1
	}
	write(burst)

	// Unpaced, the burst arrives within milliseconds of the receiver's
	// latency; paced at the steady rate, it takes seconds.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := n == burstPkts
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	require.NotZero(t, n, "no packet of the burst arrived")
	t.Logf("pacing at %.0f Mbit/s; %d of %d burst packets arrived over %v", mbps, n, burstPkts, last.Sub(first))
	return mbps
}

// pacingMbps returns the rate an output connection currently paces at.
func pacingMbps(c srtConn) float64 {
	return c.(*chunkedConn).inner.(*srtgoConn).conn.Stats(false).MbpsMaxBW
}

// An SRT output paces its packets at a maximum bandwidth. srtgo v0.4.0
// derives it, when neither MaxBW nor InputBW is set, from the input rate it
// sampled, plus 25%: a keyframe written after a stretch of small frames then
// leaves at the small frames' rate, and reaches the receiver spread over
// seconds instead of milliseconds. Without an InputBW, the outputs pace at
// srtgo's default line rate. Pacing counts bytes, not frames, so the cases
// write frames at 23.976 to 240 fps and keyframes of 0.5 and 1 MB. The test
// checks the sender's pacing rate right before the keyframe: 1000 Mbit/s at
// the line rate, a few Mbit/s without it. It logs the keyframe's arrival
// spread, a few ms at the line rate and over a second at the steady rate,
// without asserting on it: on a loaded host the receiver's own scheduling
// stretches the spread past a second.
func TestSRTOutputPacesBurstAtLineRate(t *testing.T) {
	cases := []struct {
		name          string
		frameInterval time.Duration
		framePkts     int // 1316-byte packets per frame
		burstPkts     int
	}{
		{"23.976fps-0.5Mbps-0.5MB-keyframe", time.Second * 1001 / 24000, 2, 400},
		{"60fps-1.3Mbps-1MB-keyframe", time.Second / 60, 2, 800},
		{"120fps-2.5Mbps-1MB-keyframe", time.Second / 120, 2, 800},
		{"240fps-5Mbps-1MB-keyframe", time.Second / 240, 2, 800},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("listener", func(t *testing.T) {
				l, addr := startSRTListenerOutput(t)
				rx, err := srt.Dial(addr.String(), srt.Config{Latency: 500 * time.Millisecond, ConnTimeout: 3 * time.Second})
				require.NoError(t, err)
				defer func() { _ = rx.Close() }()
				require.Eventually(t, func() bool { return l.ConnectionCount() == 1 }, 3*time.Second, 10*time.Millisecond)

				pacing := func() float64 {
					l.mu.Lock()
					defer l.mu.Unlock()
					for _, lc := range l.conns {
						return pacingMbps(lc.conn)
					}
					return 0
				}
				mbps := burstSpread(t, func(b []byte) { _, _ = l.Write(b) }, rx, tc.frameInterval, tc.framePkts, tc.burstPkts, pacing)
				assert.GreaterOrEqual(t, mbps, 1000.0, "pacing rate after the steady frames")
			})
			t.Run("caller", func(t *testing.T) {
				addr, accepted := acceptOne(t)
				conn, err := SRTConnect(context.Background(), SRTCallerConfig{Address: "127.0.0.1", Port: addr.Port, Latency: 500})
				require.NoError(t, err)
				defer conn.Close()
				rx := <-accepted

				pacing := func() float64 { return pacingMbps(conn) }
				mbps := burstSpread(t, func(b []byte) { _, _ = conn.Write(b) }, rx, tc.frameInterval, tc.framePkts, tc.burstPkts, pacing)
				assert.GreaterOrEqual(t, mbps, 1000.0, "pacing rate after the steady frames")
			})
		})
	}
}

// The adapters close SRT connections while holding their own locks, so a
// Close that waits blocks every Write to the output meanwhile. srtgo
// v0.4.0 waits up to 1 s on Close for unacknowledged data unless lingering
// is turned off. A receiver that vanished without a shutdown leaves exactly
// such data behind.
func TestSRTOutputCloseDoesNotLinger(t *testing.T) {
	strand := func(t *testing.T, upstream *net.UDPAddr, write func([]byte), dial func(addr string)) {
		t.Helper()
		px := newSRTTimestampProxy(t, upstream)
		dial(px.addr())
		px.close() // the receiver is gone, without a shutdown
		write(make([]byte, 50*1316))
		time.Sleep(100 * time.Millisecond)
	}

	t.Run("listener", func(t *testing.T) {
		l, addr := startSRTListenerOutput(t)
		strand(t, addr, func(b []byte) { _, _ = l.Write(b) }, func(a string) {
			rx, err := srt.Dial(a, srt.Config{Latency: 20 * time.Millisecond, ConnTimeout: 3 * time.Second})
			require.NoError(t, err)
			t.Cleanup(func() { _ = rx.Close() })
			require.Eventually(t, func() bool { return l.ConnectionCount() == 1 }, 3*time.Second, 10*time.Millisecond)
		})

		start := time.Now()
		require.NoError(t, l.Close())
		assert.Less(t, time.Since(start), 300*time.Millisecond)
	})
	t.Run("caller", func(t *testing.T) {
		addr, accepted := acceptOne(t)
		var conn srtConn
		strand(t, addr, func(b []byte) { _, _ = conn.Write(b) }, func(a string) {
			host, port, err := net.SplitHostPort(a)
			require.NoError(t, err)
			var p int
			_, err = fmt.Sscan(port, &p)
			require.NoError(t, err)
			conn, err = SRTConnect(context.Background(), SRTCallerConfig{Address: host, Port: p, Latency: 20})
			require.NoError(t, err)
			<-accepted
		})

		start := time.Now()
		conn.Close()
		assert.Less(t, time.Since(start), 300*time.Millisecond)
	})
}

// TestSRTAcceptLoopReturnsWhenListenerCloses covers a listener that closes
// while the output's context is still live. srtgo's Accept then fails at
// once on every call, so a loop that logs the error and accepts again spins
// on a core for as long as the process runs.
func TestSRTAcceptLoopReturnsWhenListenerCloses(t *testing.T) {
	lns := make(chan *srt.Listener, 1)
	srtListen = func(addr string, cfg srt.Config) (*srt.Listener, error) {
		ln, err := srt.Listen(addr, cfg)
		if err == nil {
			lns <- ln
		}
		return ln, err
	}
	t.Cleanup(func() { srtListen = srt.Listen })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := NewSRTListener(SRTListenerConfig{Port: 0})
	require.NoError(t, l.Start(ctx))
	defer func() { _ = l.Close() }()
	done := make(chan error, 1)
	go func() { done <- SRTAcceptLoop(ctx, l.config, l) }()

	select {
	case ln := <-lns:
		_ = ln.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("listener never bound")
	}
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop kept accepting on a closed listener")
	}
}
