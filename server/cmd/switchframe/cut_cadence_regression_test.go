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
	"github.com/zsiec/switchframe/server/switcher"
)

// programVideoRecorder records the PTS of every program video frame and
// when it arrived.
type programVideoRecorder struct {
	mu   sync.Mutex
	pts  []int64
	wall []time.Time
}

func (r *programVideoRecorder) ID() string { return "cadence-recorder" }
func (r *programVideoRecorder) SendVideo(f *media.VideoFrame) {
	r.mu.Lock()
	r.pts = append(r.pts, f.PTS)
	r.wall = append(r.wall, time.Now())
	r.mu.Unlock()
}
func (r *programVideoRecorder) SendAudio(*media.AudioFrame)    {}
func (r *programVideoRecorder) SendCaptions(*ccx.CaptionFrame) {}
func (r *programVideoRecorder) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}

func (r *programVideoRecorder) since(t time.Time) (pts []int64, wall []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, w := range r.wall {
		if !w.Before(t) {
			pts = append(pts, r.pts[i])
			wall = append(wall, w)
		}
	}
	return pts, wall
}

func (r *programVideoRecorder) last() (int64, time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pts) == 0 {
		return 0, time.Time{}, false
	}
	return r.pts[len(r.pts)-1], r.wall[len(r.wall)-1], true
}

// A cut from the built-in colour bars back to a live raw source keeps the
// program at its own rate: one frame period per frame, media time
// advancing with wall time, whichever source's frames the program
// showed before. The test runs the app's engine and program encoder as
// the default configuration builds them.
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
	// The program runs from construction or from Start, as the engine has it.
	if st, ok := any(a.sw).(interface{ Start() }); ok {
		st.Start()
	}
	rec := &programVideoRecorder{}
	a.programRelay.AddViewer(rec)
	ctx := context.Background()

	pf := a.sw.PipelineFormat()
	period := int64(90000) * int64(pf.FPSDen) / int64(pf.FPSNum) // 3003 at 29.97
	frameDur := time.Duration(int64(time.Second) * int64(pf.FPSDen) / int64(pf.FPSNum))

	// The program's own timeline, read off the bars, so the live source's
	// PTS is on it whatever epoch the program counts from.
	if err := a.sw.Cut(ctx, switcher.BuiltinSourceKeyColorbars); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var basePTS int64
	var baseWall time.Time
	for {
		if p, w, ok := rec.last(); ok {
			basePTS, baseWall = p, w
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no program frame from the colour bars")
		}
		time.Sleep(10 * time.Millisecond)
	}

	const live = "mxl:live"
	a.sw.RegisterMXLSource(live)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk := time.NewTicker(frameDur)
		defer tk.Stop()
		size := pf.Width * pf.Height * 3 / 2
		for {
			select {
			case <-stop:
				return
			case now := <-tk.C:
				pts := basePTS + int64(now.Sub(baseWall)/frameDur)*period
				buf := make([]byte, size)
				for i := range buf[:pf.Width*pf.Height] {
					buf[i] = 128
				}
				a.sw.IngestRawVideo(live, &switcher.ProcessingFrame{
					Data: buf, Width: pf.Width, Height: pf.Height,
					PTS: pts, DTS: pts, IsKeyframe: true,
					Format: switcher.ColorYUV420_8bit,
				})
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	if err := a.sw.Cut(ctx, live); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := a.sw.Cut(ctx, switcher.BuiltinSourceKeyColorbars); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := a.sw.Cut(ctx, live); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(200 * time.Millisecond) // past the cut itself
	time.Sleep(1200 * time.Millisecond)

	pts, wall := rec.since(start)
	if len(pts) < 20 {
		t.Fatalf("%d program frames in 1 s after the cut, want about 30", len(pts))
	}
	// A step is one frame period, or a whole number of them where the
	// clock dropped a late tick; never a fraction of one.
	drops := 0
	for i := 1; i < len(pts); i++ {
		d := pts[i] - pts[i-1]
		k := (d + period/2) / period
		if k < 1 || d < k*period-k || d > k*period+k {
			t.Fatalf("program PTS step %d is %d ticks (%d -> %d), want one frame period %d +-1",
				i, d, pts[i-1], pts[i], period)
		}
		drops += int(k - 1)
	}
	if drops > 3 {
		t.Fatalf("%d ticks dropped in 1 s", drops)
	}
	mediaSec := float64(pts[len(pts)-1]-pts[0]) / 90000
	wallSec := wall[len(wall)-1].Sub(wall[0]).Seconds()
	if r := mediaSec / wallSec; r < 0.98 || r > 1.02 {
		t.Fatalf("program media time advanced %.3f s in %.3f s of wall time (%.2fx), want 1x +-2%%",
			mediaSec, wallSec, r)
	}
}
