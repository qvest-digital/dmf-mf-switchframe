package srt

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

// frameRecord is a frame as the test sender writes it: its index and PTS.
const frameRecord = 16

// frameReader is a stream decoder that reads frameRecords off the
// connection and hands each on as a video frame, with no FFmpeg: what
// the listener delivers, and when, is all the test looks at.
type frameReader struct {
	cfg StreamDecoderConfig
}

func (d *frameReader) Run() {
	buf := make([]byte, 1500)
	var pending []byte
	for {
		n, err := d.cfg.Reader.Read(buf)
		pending = append(pending, buf[:n]...)
		for len(pending) >= frameRecord {
			d.cfg.OnVideo(pending[:8], 0, 0, int64(binary.BigEndian.Uint64(pending[8:frameRecord])))
			pending = pending[frameRecord:]
		}
		if err != nil {
			return
		}
	}
}

func (d *frameReader) Stop() {
	if c, ok := d.cfg.Reader.(io.Closer); ok {
		_ = c.Close()
	}
}

// arrivals records the index of every frame a connection delivered and
// when it was delivered.
type arrivals struct {
	mu    sync.Mutex
	index []uint64
	at    []time.Time
}

func (a *arrivals) add(index uint64) {
	a.mu.Lock()
	a.index = append(a.index, index)
	a.at = append(a.at, time.Now())
	a.mu.Unlock()
}

func (a *arrivals) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.index)
}

// A sender that restarts reconnects to a listener that has been up for a
// while, and takes a while after the handshake to send its first packet,
// as an encoder warming up does. Its frames must still be delivered as
// promptly as the first connection's, from the first to the last, give or
// take a second for a loaded host.
//
// The listener's connections share its clock. Until a connection's first
// data packet sets its TSBPD time base, the base is 0, so a drift sample
// the sender's ACKACKs and keepalives gave before then read the clock's
// whole age at the handshake as drift. The first 1000 samples, 10 s of
// them, were averaged into one correction that held every packet back by
// warmup/10ms * age / 1000: at most 2.4 s here, about 2.1 s measured.
// At an age of 25 minutes 15 samples hold it back 22 s, past what the
// receive buffer holds at 8 Mbit/s, and the connection delivered nothing
// after that. A first connection, made while the listener was young, was
// spared.
func TestListenerReconnectDeliversOnTime(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 20 s of live SRT")
	}
	for _, r := range []struct{ num, den int64 }{
		{24000, 1001}, {25, 1}, {30000, 1001}, {50, 1}, {60000, 1001}, {120, 1}, {240, 1},
	} {
		t.Run(fmt.Sprintf("%d/%d", r.num, r.den), func(t *testing.T) {
			t.Parallel()
			listenerReconnect(t, time.Duration(r.den)*time.Second/time.Duration(r.num))
		})
	}
}

func listenerReconnect(t *testing.T, period time.Duration) {
	const (
		latency     = 120 * time.Millisecond
		listenerAge = 6 * time.Second // when the sender reconnects
		warmup      = 4 * time.Second // from the handshake to the first frame
		stream      = 9 * time.Second // of frames, past the first 1000 samples
		// slack is how much later than the first connection's a
		// reconnection's frames may be delivered: room for a loaded host,
		// well short of the hold the early samples caused.
		slack = time.Second
	)

	addr := findFreePort(t)
	var (
		mu    sync.Mutex
		conns []*arrivals
	)
	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          tempStore(t),
		DefaultLatency: latency,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			got := &arrivals{}
			src := NewSource(cfg, conn, NewConnStats(cfg.Mode, cfg.StreamID, cfg.LatencyMs), slog.Default())
			src.decoderFactory = func(c StreamDecoderConfig) (streamDecoder, error) {
				return &frameReader{cfg: c}, nil
			}
			src.OnRawVideo = func(_ string, index []byte, _, _ int, _ int64) {
				got.add(binary.BigEndian.Uint64(index))
			}
			if err := src.Start(context.Background()); err != nil {
				t.Errorf("start source: %v", err)
				return
			}
			t.Cleanup(src.Stop)
			// Taken once its source reads: the test sends only then, so a
			// slow accept does not count as a late delivery.
			mu.Lock()
			conns = append(conns, got)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	start := time.Now()
	go func() { runDone <- l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	// conn returns the arrivals of the nth connection the listener took.
	conn := func(n int) *arrivals {
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			mu.Lock()
			if len(conns) > n {
				got := conns[n]
				mu.Unlock()
				return got
			}
			mu.Unlock()
		}
		t.Fatalf("the listener took no connection %d within 5s", n+1)
		return nil
	}
	// send writes n frames, one a period, their PTS from pts0 on, and
	// returns when each was written.
	send := func(c *srtgo.Conn, n int, pts0 int64) []time.Time {
		sent := make([]time.Time, n)
		begin := time.Now()
		rec := make([]byte, frameRecord)
		for i := range n {
			time.Sleep(time.Until(begin.Add(time.Duration(i) * period)))
			binary.BigEndian.PutUint64(rec, uint64(i))
			binary.BigEndian.PutUint64(rec[8:], uint64(pts0+int64(i)*int64(period)*9/100_000))
			sent[i] = time.Now()
			if _, err := c.Write(rec); err != nil {
				t.Fatalf("write frame %d: %v", i, err)
			}
		}
		return sent
	}
	// delivered waits for all n frames of got, in order, and returns the
	// longest any took from sent to its delivery, and which.
	delivered := func(got *arrivals, sent []time.Time) (time.Duration, int) {
		for end := time.Now().Add(5 * time.Second); got.count() < len(sent) && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		}
		got.mu.Lock()
		defer got.mu.Unlock()
		if len(got.index) != len(sent) {
			t.Fatalf("%d of %d frames delivered", len(got.index), len(sent))
		}
		worst, at := time.Duration(0), 0
		for i, index := range got.index {
			if index != uint64(i) {
				t.Fatalf("frame %d delivered as the %dth, want in order", index, i)
			}
			if d := got.at[i].Sub(sent[i]); d > worst {
				worst, at = d, i
			}
		}
		return worst, at
	}

	// The first connection, while the listener is young.
	c1 := dialWithRetry(t, addr, "cam")
	got := conn(0)
	sent := send(c1, int(time.Second/period), 20*90000)
	w1, _ := delivered(got, sent)
	_ = c1.Close()

	// The sender restarts, its PTS afresh, as a restarted encoder's.
	time.Sleep(time.Until(start.Add(listenerAge)))
	c2 := dialWithRetry(t, addr, "cam")
	t.Cleanup(func() { _ = c2.Close() })
	got = conn(1)
	time.Sleep(warmup)
	sent = send(c2, int(stream/period), 126000)
	if worst, at := delivered(got, sent); worst > w1+slack {
		t.Fatalf("reconnection: frame %d delivered %v after it was written, want at most %v, the first connection's %v and %v", at, worst, w1+slack, w1, slack)
	}
}
