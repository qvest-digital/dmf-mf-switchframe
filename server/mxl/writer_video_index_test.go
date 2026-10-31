package mxl

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// Program video is written at the grain index of its own PTS, as program
// audio is at the sample indices of its own: the two program flows, and two
// engines writing the same program, carry the same index for the same tick
// whatever the clock reads when a frame arrives.

var videoRate = clock.Rate{Num: 50, Den: 1}

// fixClock makes CurrentIndex read index, whatever the time.
func fixClock(t *testing.T, index uint64) {
	t.Helper()
	saved := CurrentIndex
	CurrentIndex = func(Rational) uint64 { return index }
	t.Cleanup(func() { CurrentIndex = saved })
}

// frameV210 is the V210 grain frameYUV(value) becomes.
func frameV210(t *testing.T, value byte) []byte {
	t.Helper()
	buf := make([]byte, V210LineStride(12)*2)
	if err := YUV420pToV210Into(frameYUV(value), buf, 12, 2); err != nil {
		t.Fatal(err)
	}
	return buf
}

func videoIndexWriter(t *testing.T) (*Writer, *mockDiscreteWriter) {
	t.Helper()
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{videoRate.Num, videoRate.Den})
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	t.Cleanup(func() { cancel(); _ = w.Close() })
	return w, mock
}

func waitGrains(t *testing.T, mock *mockDiscreteWriter, n int) []writtenGrain {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		g := mock.getGrains()
		if len(g) >= n || time.Now().After(deadline) {
			return g
		}
		time.Sleep(time.Millisecond)
	}
}

// Frames whose PTS stand for grains i, i+1 and i+2 are written at exactly
// those grains, with the clock nowhere near them.
func TestWriter_VideoIndexIsTheFramePTS(t *testing.T) {
	fixClock(t, 7)
	w, mock := videoIndexWriter(t)
	base := clock.TimestampToIndex(videoRate, clock.TAINow())
	for k := uint64(0); k < 3; k++ {
		w.WriteVideo(frameYUV(byte(16+k)), 12, 2, clock.IndexPTS(videoRate, base+k))
		got := waitGrains(t, mock, int(k)+1)
		if len(got) != int(k)+1 {
			t.Fatalf("%d grains after frame %d, want %d", len(got), k, k+1)
		}
		if got[k].index != base+k {
			t.Fatalf("frame %d written at %d, want %d (%d grains off)",
				k, got[k].index, base+k, int64(got[k].index)-int64(base+k))
		}
	}
}

// A burst of frames delivered at once does not move any of them off its
// own grain: each frame written is at the grain of its PTS, and the last
// one is written.
func TestWriter_VideoBurstKeepsIndices(t *testing.T) {
	base := clock.TimestampToIndex(videoRate, clock.TAINow())
	fixClock(t, base)
	w, mock := videoIndexWriter(t)
	const frames = 6
	for k := uint64(0); k < frames; k++ {
		w.WriteVideo(frameYUV(byte(16+k)), 12, 2, clock.IndexPTS(videoRate, base+k))
	}
	time.Sleep(100 * time.Millisecond)

	want := make([][]byte, frames)
	for k := range want {
		want[k] = frameV210(t, byte(16+k))
	}
	got := mock.getGrains()
	for _, g := range got {
		k := -1
		for i := range want {
			if bytes.Equal(g.data, want[i]) {
				k = i
			}
		}
		if k < 0 {
			t.Fatalf("grain %d holds no frame of the burst", g.index)
		}
		if g.index != base+uint64(k) {
			t.Fatalf("frame %d written at %d, want %d", k, g.index, base+uint64(k))
		}
	}
	if len(got) == 0 || got[len(got)-1].index != base+frames-1 {
		t.Fatalf("last frame of the burst not written; grains %v", got)
	}
}
