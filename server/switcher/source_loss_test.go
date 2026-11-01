package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
)

// A source turns to black once it has delivered nothing for
// SourceLossTimeout; until then it holds its last frame. The time a
// source has been lost is its own: cutting away from a lost source to
// one that holds a frame does not carry the lost source's timer over.
func TestCutDoesNotInheritAnotherSourceLossTimer(t *testing.T) {
	s := newTestSwitcherDirect(distribution.NewRelay())
	defer s.Close()
	s.RegisterMXLSource("mxl:a")
	s.RegisterMXLSource("mxl:b")
	ctx := context.Background()

	pf := s.PipelineFormat()
	r := clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}
	feed := func(key string, fill byte) {
		s.IngestRawVideo(key, &ProcessingFrame{
			Data: makeTestYUV(320, 240, fill), Width: 320, Height: 240,
			PTS: clock.NowPTS(), Format: ColorYUV420_8bit,
		})
	}
	// pop takes key's frame at a tick whose read point is now and returns
	// its first luma byte.
	pop := func(key string) byte {
		read := clock.TimestampToIndex(r, clock.TAINow()) + 1
		f, _ := s.popProgramFrame(s.frameSync, key, clock.Tick{
			VideoPTS:      clock.IndexPTS(r, read+clock.ReaderDelayGrains),
			ReadPTS:       clock.IndexPTS(r, read),
			FrameDuration: pf.FrameDuration(),
		})
		defer f.ReleaseYUV()
		return f.Data[0]
	}
	// hold pops key every 50 ms for d.
	hold := func(key string, d time.Duration) byte {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			pop(key)
		}
		return pop(key)
	}

	// B is on program and delivers.
	if err := s.Cut(ctx, "mxl:b"); err != nil {
		t.Fatal(err)
	}
	feed("mxl:b", 200)
	if y := pop("mxl:b"); y != 200 {
		t.Fatalf("B on program shows luma %d, want its frame's 200", y)
	}

	// A goes to program, delivers once and is lost for 4 s: frozen.
	if err := s.Cut(ctx, "mxl:a"); err != nil {
		t.Fatal(err)
	}
	feed("mxl:a", 100)
	if y := hold("mxl:a", 4*time.Second); y != 100 {
		t.Fatalf("A lost for 4 s shows luma %d, want its last frame's 100", y)
	}

	// Back to B, which holds its frame and delivers nothing new for 2 s.
	if err := s.Cut(ctx, "mxl:b"); err != nil {
		t.Fatal(err)
	}
	if y := hold("mxl:b", 2*time.Second); y != 200 {
		t.Fatalf("B 2 s without a frame shows luma %d, want its last frame's 200: it took over A's loss timer", y)
	}
}

// Removing the program source leaves the program black, not stopped: the
// tick is the program clock, so every tick still runs a frame through the
// pipeline and with it every output.
func TestRemovedProgramSourceRunsBlackEveryTick(t *testing.T) {
	s := tickSwitcher(t)
	node := recordPipeline(t, s)
	onProgram(t, s, "mxl:cam")

	g := grainNow(s)
	s.IngestRawVideo("mxl:cam", mxlFrame(s, g, 50))
	s.processVideoTick(tickAt(s, g+clock.ReaderDelayGrains))
	if y := node.lastLuma(t); y != 50 {
		t.Fatalf("program shows luma %d, want the source's 50", y)
	}

	s.UnregisterSource("mxl:cam")
	for i := uint64(1); i <= 5; i++ {
		tick := tickAt(s, g+clock.ReaderDelayGrains+i)
		node.mu.Lock()
		before := len(node.pts)
		node.mu.Unlock()
		s.processVideoTick(tick)
		node.mu.Lock()
		n, pts, y := len(node.pts), node.pts[len(node.pts)-1], node.luma[len(node.luma)-1]
		node.mu.Unlock()
		if n != before+1 {
			t.Fatalf("tick %d after the program source was removed ran %d frames, want 1", i, n-before)
		}
		if pts != tick.VideoPTS || y != 16 {
			t.Fatalf("tick %d ran PTS %d luma %d, want black (16) at the tick's PTS %d", i, pts, y, tick.VideoPTS)
		}
	}
}
