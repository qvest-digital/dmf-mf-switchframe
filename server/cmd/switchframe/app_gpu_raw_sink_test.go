package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/switcher"
)

// gpuRawSinkApp is an app whose GPU raw sink mirrors the switcher's raw
// video sink, as updateGPURawVideoSink wires it.
func gpuRawSinkApp(t *testing.T, sink switcher.RawVideoSink) *App {
	t.Helper()
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	sw.SetRawVideoSink(sink)
	a := &App{sw: sw, gpuRawVideoSink: &atomic.Pointer[gpu.RawSinkFunc]{}}
	a.updateGPURawVideoSink()
	if a.gpuRawVideoSink.Load() == nil {
		t.Fatal("no GPU raw sink wired")
	}
	return a
}

// rawSinkFrame is a 12x2 YUV420p frame whose luma is y.
func rawSinkFrame(y byte) []byte {
	yuv := make([]byte, 12*2*3/2)
	for i := range yuv[:24] {
		yuv[i] = y
	}
	for i := 24; i < len(yuv); i++ {
		yuv[i] = 128
	}
	return yuv
}

// The GPU pipeline's raw sink hands the program frame on with its PTS:
// the program MXL writer puts a frame on the grain of its PTS, and a frame
// without one is dropped as too late.
func TestGPURawSinkHandsOnTheFramePTS(t *testing.T) {
	var mu sync.Mutex
	var got []int64
	a := gpuRawSinkApp(t, func(pf *switcher.ProcessingFrame) {
		if pf.Width != 12 {
			return
		}
		mu.Lock()
		got = append(got, pf.PTS)
		mu.Unlock()
	})
	(*a.gpuRawVideoSink.Load())(rawSinkFrame(16), 12, 2, 123456)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != 123456 {
		t.Fatalf("raw video sink got PTS %v, want [123456]", got)
	}
}

// grainRecorder is an MXL flow writer that records each grain's index.
type grainRecorder struct {
	mu      sync.Mutex
	indices []uint64
	data    map[uint64][]byte
}

func (g *grainRecorder) WriteGrain(index uint64, data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.indices = append(g.indices, index)
	g.data[index] = append([]byte(nil), data...)
	return nil
}
func (g *grainRecorder) Close() error { return nil }

// The GPU pipeline feeds the program MXL writer as the CPU one does: one
// frame per tick, on the sync loop, a few milliseconds into the tick's
// grain. Every frame lands on its own grain and none is dropped; with no
// PTS each was dropped and every grain written late by the writer's fill.
func TestGPURawSinkFeedsTheMXLWriterWithoutDrops(t *testing.T) {
	rate := mxl.Rational{Numerator: 10, Denominator: 1}
	const grains = 8
	rec := &grainRecorder{data: map[uint64][]byte{}}
	w := mxl.NewWriter(mxl.WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(rec, rate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// The program writer's raw video sink, as initMXL wires it.
	a := gpuRawSinkApp(t, func(pf *switcher.ProcessingFrame) {
		if pf.Width == 12 {
			w.WriteVideo(pf.Data, pf.Width, pf.Height, pf.PTS)
		}
	})
	sink := *a.gpuRawVideoSink.Load()

	first := mxl.CurrentIndex(rate) + 1
	r := clock.Rate{Num: rate.Numerator, Den: rate.Denominator}
	for k := first; k < first+grains; k++ {
		time.Sleep(time.Duration(mxl.NsUntilIndex(k, rate)) + 3*time.Millisecond)
		sink(rawSinkFrame(byte(32+k-first)), 12, 2, clock.IndexPTS(r, k))
	}
	time.Sleep(150 * time.Millisecond)
	cancel()
	_ = w.Close()

	if n := w.VideoIndexDrops(); n != 0 {
		t.Fatalf("the MXL writer dropped %d of %d GPU program frames", n, grains)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for k := first + 1; k < first+grains; k++ {
		cur, ok := rec.data[k]
		if !ok {
			t.Fatalf("grain %d not written", k-first)
		}
		if prev := rec.data[k-1]; string(cur) == string(prev) {
			t.Fatalf("grain %d repeats grain %d instead of holding its own frame", k-first, k-first-1)
		}
	}
}
