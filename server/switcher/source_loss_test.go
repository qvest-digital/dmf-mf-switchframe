package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
)

// A source turns to black once it has delivered nothing for
// sourceLossTimeout; until then it holds its last frame. The time a
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
