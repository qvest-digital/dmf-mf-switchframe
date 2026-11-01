package mxl

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// The program's frames reach the writer a few milliseconds after their
// grain starts: the tick for grain k fires at the start of k, and the
// frame is composited and converted before it is handed over. After a
// stall, the writer repeats each grain as it starts, ahead of any frame for
// it; once frames flow again it must leave the stall, so that every frame
// after the first lands on its own grain instead of each being dropped
// behind a repeat of the one before.
func TestWriter_FramesAfterAStallLandOnTheirOwnGrains(t *testing.T) {
	rate := Rational{10, 1}
	period := 100 * time.Millisecond
	const latency = 30 * time.Millisecond
	const stall, after = 3, 8

	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, rate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	first := CurrentIndex(rate) + 1
	deliver := func(k uint64) {
		time.Sleep(time.Duration(NsUntilIndex(k, rate)) + latency)
		w.WriteVideo(frameYUV(byte(16+k-first)), 12, 2, framePTS(rate, k))
	}
	deliver(first)
	resume := first + 1 + stall
	for k := resume; k < resume+after; k++ {
		deliver(k)
	}
	time.Sleep(period / 2)
	cancel()
	_ = w.Close()

	byIndex := map[uint64][]byte{}
	for _, g := range mock.getGrains() {
		byIndex[g.index] = g.data
	}
	// The first frame after the stall meets its grain already repeated.
	for k := resume + 1; k < resume+after; k++ {
		got, ok := byIndex[k]
		if !ok {
			t.Fatalf("grain %d not written", k-first)
		}
		if want := frameV210(t, byte(16+k-first)); !bytes.Equal(got, want) {
			t.Fatalf("grain %d does not hold its own frame (%d frames dropped)",
				k-first, w.VideoIndexDrops())
		}
	}
	if n := w.VideoIndexDrops(); n > 1 {
		t.Fatalf("%d frames dropped after the stall, want at most 1", n)
	}
}
