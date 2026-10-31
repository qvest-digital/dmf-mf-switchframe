//go:build cgo

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/switcher"
)

// programVideoRecorder records the PTS of every program video frame and
// kicks next for each one.
type programVideoRecorder struct {
	mu   sync.Mutex
	pts  []int64
	next chan int64
}

func (r *programVideoRecorder) ID() string { return "cadence-recorder" }
func (r *programVideoRecorder) SendVideo(f *media.VideoFrame) {
	r.mu.Lock()
	r.pts = append(r.pts, f.PTS)
	r.mu.Unlock()
	select {
	case r.next <- f.PTS:
	default:
	}
}
func (r *programVideoRecorder) SendAudio(*media.AudioFrame)    {}
func (r *programVideoRecorder) SendCaptions(*ccx.CaptionFrame) {}
func (r *programVideoRecorder) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}

func (r *programVideoRecorder) all() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.pts...)
}

// waitFrames waits for n program frames after the first seen, and returns
// how many there are then.
func (r *programVideoRecorder) waitFrames(t *testing.T, seen, n int) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if got := len(r.all()); got >= seen+n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d program frames, want %d", len(r.all()), seen+n)
		}
		time.Sleep(time.Millisecond)
	}
}

// A cut from the built-in colour bars back to a live raw source keeps the
// program on its own grid: every program frame lies a whole number of
// frame periods after the one before, across every cut, whichever
// source's frames the program showed before. The test runs the app's
// engine and program encoder as the default configuration builds them,
// on a reference clock that ticks on demand, so no tick is dropped
// however slow the host is. A program grain can still go without a frame
// where the switcher sheds load: a tick after one that overran its
// period (a predicted frame is not repeated), a frame the async encoder
// was too busy to take, or one it returned nothing for. The grains
// missing never outnumber those.
func TestCutFromBuiltinBackToLiveKeepsProgramCadence(t *testing.T) {
	a := &App{programRelay: distribution.NewRelay()}
	a.cfg.Format, a.cfg.ColorFormat = "1080p29.97", "standard" // the flag defaults
	if err := a.initCoreEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.sw.Close()
		_ = a.mixer.Close()
	})
	// The program encoder, as initSubsystems builds it.
	color := a.sw.PipelineFormat().Color
	a.sw.SetPipelineCodecs(encoderFactoryForCodecWithOpts(color.Codec, nil, color.ColorFormat), color.Codec)
	if err := a.sw.BuildPipeline(); err != nil {
		t.Fatal(err)
	}
	pf := a.sw.PipelineFormat()
	period := int64(90000) * int64(pf.FPSDen) / int64(pf.FPSNum) // 3003 at 29.97
	a.sw.SetReferenceClock(clock.NewReferenceClock(pf.FPSNum, pf.FPSDen, 48000, clock.PTSBase, false))
	rec := &programVideoRecorder{next: make(chan int64, 1)}
	a.programRelay.AddViewer(rec)
	ctx := context.Background()
	if err := a.sw.Cut(ctx, switcher.BuiltinSourceKeyColorbars); err != nil {
		t.Fatal(err)
	}

	// The live source delivers a frame for each program frame, stamped
	// with that frame's media time, so it keeps pace with the program
	// however fast the clock runs.
	const live = "mxl:live"
	a.sw.RegisterMXLSource(live)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		size := pf.Width * pf.Height * 3 / 2
		for {
			var pts int64
			select {
			case <-stop:
				return
			case pts = <-rec.next:
			}
			buf := make([]byte, size)
			for i := range buf[:pf.Width*pf.Height] {
				buf[i] = 128
			}
			frame := &switcher.ProcessingFrame{
				Data: buf, Width: pf.Width, Height: pf.Height,
				PTS: pts, DTS: pts, IsKeyframe: true,
				Format: switcher.ColorYUV420_8bit,
			}
			frame.SetRefs(1) // managed, as every producer ingests
			a.sw.IngestRawVideo(live, frame)
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	a.sw.Start()
	seen := rec.waitFrames(t, 0, 5) // the live source has frames before the cut
	for _, cut := range []struct {
		source string
		frames int
	}{{live, 15}, {switcher.BuiltinSourceKeyColorbars, 30}, {live, 30}} {
		if err := a.sw.Cut(ctx, cut.source); err != nil {
			t.Fatal(err)
		}
		seen = rec.waitFrames(t, seen, cut.frames)
	}

	pts := rec.all()
	vp, _ := a.sw.DebugSnapshot()["video_pipeline"].(map[string]any)
	encodeDrop, _ := vp["encode_drop"].(int64)
	encodeNil, _ := vp["encode_nil"].(int64)
	shed := a.sw.PerfSample().ProcDropped + encodeDrop + encodeNil
	missing := int64(0)
	for i := 1; i < len(pts); i++ {
		// T(n) rounds to the nanosecond and PTS to the 90 kHz tick, so a
		// step may be one tick off a whole number of periods; never more.
		d := pts[i] - pts[i-1]
		k := (d + period/2) / period
		if k < 1 || d < k*period-1 || d > k*period+1 {
			t.Fatalf("program PTS step %d of %d is %d ticks (%d -> %d), want a whole number of frame periods %d +-1",
				i, len(pts)-1, d, pts[i-1], pts[i], period)
		}
		missing += k - 1
	}
	if missing > shed {
		t.Fatalf("%d program grains have no frame, but the switcher shed only %d (ticks %d, encoder busy %d, encoder empty %d)",
			missing, shed, shed-encodeDrop-encodeNil, encodeDrop, encodeNil)
	}
}
