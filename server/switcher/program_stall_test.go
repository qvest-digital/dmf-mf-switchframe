package switcher

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// stallOut is one frame the pipeline's raw sink saw.
type stallOut struct {
	y   byte // first luma sample: identifies the picture
	pts int64
	at  time.Time
}

type stallRig struct {
	sw  *Switcher
	mu  sync.Mutex
	out []stallOut
}

// newStallRig builds a switcher with a pipeline whose raw sink records
// every frame, wall-clock PTS on as the app runs it, and the stall filler
// running with a short black timeout.
func newStallRig(t *testing.T, blackAfter time.Duration) *stallRig {
	t.Helper()
	r := &stallRig{sw: newTestSwitcher(t, newTestRelay())}
	r.sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	r.sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		r.mu.Lock()
		r.out = append(r.out, stallOut{y: pf.Data[0], pts: pf.PTS, at: time.Now()})
		r.mu.Unlock()
	})
	require.NoError(t, r.sw.BuildPipeline())
	r.sw.EnableWallClockVideoPTS()
	r.sw.stall.blackAfter = blackAfter
	r.sw.startProgramStallFiller()
	return r
}

// send delivers one 320x240 frame of uniform luma y from source key.
func (r *stallRig) send(key string, y byte, pts int64) {
	data := make([]byte, ColorYUV420_8bit.FrameSize(320, 240))
	for i := range data[:320*240] {
		data[i] = y
	}
	for i := 320 * 240; i < len(data); i++ {
		data[i] = 128
	}
	r.sw.handleRawVideoFrame(key, &ProcessingFrame{Data: data, Width: 320, Height: 240, PTS: pts, DTS: pts, IsKeyframe: true, Codec: "h264"})
}

// feed sends n frames of luma y at the pipeline rate.
func (r *stallRig) feed(key string, y byte, n int, pts int64) int64 {
	period := r.sw.framePeriod()
	for i := 0; i < n; i++ {
		r.send(key, y, pts)
		pts += r.sw.framePTSTicks()
		time.Sleep(period)
	}
	return pts
}

func (r *stallRig) since(t0 time.Time) []stallOut {
	r.mu.Lock()
	defer r.mu.Unlock()
	var res []stallOut
	for _, o := range r.out {
		if !o.at.Before(t0) {
			res = append(res, o)
		}
	}
	return res
}

func (r *stallRig) all() []stallOut { return r.since(time.Time{}) }

func requireMonotonicPTS(t *testing.T, out []stallOut) {
	t.Helper()
	for i := 1; i < len(out); i++ {
		require.Greater(t, out[i].pts, out[i-1].pts, "PTS must rise at frame %d: %d after %d", i, out[i].pts, out[i-1].pts)
	}
}

// A program source that stops delivering frames no longer stops the
// output: its last picture is repeated at the pipeline rate, then black.
func TestProgramStall_FreezeThenBlack(t *testing.T) {
	r := newStallRig(t, 400*time.Millisecond)
	r.sw.RegisterSource("cam1", newTestRelay())
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	r.feed("cam1", 200, 10, 90000)
	stallStart := time.Now()

	time.Sleep(time.Second)
	out := r.since(stallStart)
	period := r.sw.framePeriod()
	// About one frame per period after the stall threshold.
	want := int((time.Second - stallFrames*period) / period)
	require.InDelta(t, want, len(out), 4, "filled frames in 1 s at %v", period)

	var frozen, black int
	for _, o := range out {
		switch o.y {
		case 200:
			frozen++
			require.True(t, o.at.Before(stallStart.Add(400*time.Millisecond+2*period)), "held picture after the black timeout")
		case 16:
			black++
		default:
			t.Fatalf("unexpected picture luma %d", o.y)
		}
	}
	require.Greater(t, frozen, 5, "the last picture is held first")
	require.Greater(t, black, 5, "then black")
	requireMonotonicPTS(t, r.all())
}

// While frames flow the filler feeds nothing, so it cannot add frames
// between the source's own.
func TestProgramStall_NothingFilledWhileFramesFlow(t *testing.T) {
	r := newStallRig(t, time.Second)
	r.sw.RegisterSource("cam1", newTestRelay())
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	r.feed("cam1", 100, 30, 90000)
	require.Zero(t, r.sw.stallFillFrames.Load())
	require.Len(t, r.all(), 30)
}

// A source resuming after a stall continues the program's PTS, even when
// its own PTS is far behind the filled frames', and filling stops.
func TestProgramStall_ResumeKeepsPTSMonotonic(t *testing.T) {
	r := newStallRig(t, 200*time.Millisecond)
	r.sw.RegisterSource("cam1", newTestRelay())
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	r.feed("cam1", 100, 5, 900000)
	time.Sleep(500 * time.Millisecond) // held, then black
	filled := r.sw.stallFillFrames.Load()
	require.Greater(t, filled, int64(5))

	resume := time.Now()
	r.feed("cam1", 150, 10, 1000) // PTS restarting near zero
	out := r.since(resume)
	require.NotEmpty(t, out)
	requireMonotonicPTS(t, r.all())
	require.LessOrEqual(t, r.sw.stallFillFrames.Load()-filled, int64(1), "filling stops once frames flow")
	require.Equal(t, byte(150), out[len(out)-1].y)
}

// A cut away from a stalled source never shows its held picture: a stalled
// new source goes to black, a live one shows its own frames.
func TestProgramStall_CutStopsTheHeldPicture(t *testing.T) {
	r := newStallRig(t, 10*time.Second)
	r.sw.RegisterSource("cam1", newTestRelay())
	r.sw.RegisterSource("cam2", newTestRelay())
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	r.feed("cam1", 200, 5, 90000)
	time.Sleep(300 * time.Millisecond)
	require.Greater(t, r.sw.stallFillFrames.Load(), int64(3), "cam1 is being held")

	// cam2 never delivers: black, never cam1's picture.
	require.NoError(t, r.sw.Cut(context.Background(), "cam2"))
	cut := time.Now()
	time.Sleep(400 * time.Millisecond)
	out := r.since(cut.Add(r.sw.framePeriod())) // a frame mid-pipeline at the cut may land just after it
	require.NotEmpty(t, out)
	for _, o := range out {
		require.Equal(t, byte(16), o.y, "after the cut to a stalled source only black")
	}

	// Back to cam1, which is live now: its frames, no held picture.
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	cut = time.Now()
	r.feed("cam1", 90, 10, 500000)
	for _, o := range r.since(cut.Add(r.sw.framePeriod())) {
		require.Equal(t, byte(90), o.y)
	}
	requireMonotonicPTS(t, r.all())
}

// The filler feeds nothing during a transition, nor in reference clock
// mode, whose sync loop paces output itself.
func TestProgramStall_IdleDuringTransitionAndRefClock(t *testing.T) {
	r := newStallRig(t, time.Second)
	r.sw.stopProgramStallFiller() // driven by hand below
	r.sw.RegisterSource("cam1", newTestRelay())
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	r.feed("cam1", 100, 3, 90000)
	time.Sleep(200 * time.Millisecond)

	var seen uint64
	var since time.Time
	step := func() int64 {
		before := r.sw.stallFillFrames.Load()
		r.sw.fillStep(time.Now(), r.sw.framePeriod(), &seen, &since)
		return r.sw.stallFillFrames.Load() - before
	}
	require.Zero(t, step(), "a new epoch gets its grace period first")
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int64(1), step(), "stalled: one frame per step")

	setState := func(st State) {
		r.sw.configMu.Lock()
		r.sw.updateHotState(func(hs *hotState) { hs.state = st })
		r.sw.configMu.Unlock()
	}
	setState(StateTransitioning)
	require.Zero(t, step(), "nothing during a transition")
	setState(StateIdle)

	r.sw.useRefClock.Store(true)
	require.Zero(t, step(), "nothing in reference clock mode")
	r.sw.useRefClock.Store(false)
	require.Equal(t, int64(1), step())
}

// An FTB hold filters every frame; output stays black at the pipeline rate
// instead of stopping.
func TestProgramStall_FTBHoldFeedsBlack(t *testing.T) {
	r := newStallRig(t, 10*time.Second)
	r.sw.stopProgramStallFiller()
	r.sw.RegisterSource("cam1", newTestRelay())
	require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
	r.feed("cam1", 200, 3, 90000)
	r.sw.configMu.Lock()
	r.sw.updateHotState(func(hs *hotState) { hs.state = StateFTB })
	r.sw.configMu.Unlock()

	var seen uint64
	var since time.Time
	r.sw.fillStep(time.Now(), r.sw.framePeriod(), &seen, &since)
	time.Sleep(200 * time.Millisecond)
	t0 := time.Now()
	r.sw.fillStep(time.Now(), r.sw.framePeriod(), &seen, &since)
	require.Eventually(t, func() bool { return len(r.since(t0)) == 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, byte(16), r.since(t0)[0].y, "black, not the held picture")
}

// Black is in the pipeline's format: 10-bit 4:2:2 black is Y=64, Cb=Cr=512.
func TestProgramStall_BlackInPipelineFormat(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	f := PipelineFormat{Name: "pro", Width: 64, Height: 32, FPSNum: 50, FPSDen: 1,
		Color: PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC}}
	sw.pipelineFormat.Store(&f)
	b := sw.pipelineBlack()
	defer b.ReleaseYUV()
	require.Equal(t, ColorYUV422_10bit, b.Format)
	require.Equal(t, 64, b.Width)
	require.Len(t, b.Data, ColorYUV422_10bit.FrameSize(64, 32))
	ySize := ColorYUV422_10bit.YPlaneSize(64, 32)
	require.Equal(t, uint16(64), binary.LittleEndian.Uint16(b.Data[0:]))
	require.Equal(t, uint16(64), binary.LittleEndian.Uint16(b.Data[ySize-2:]))
	require.Equal(t, uint16(512), binary.LittleEndian.Uint16(b.Data[ySize:]))
	require.Equal(t, uint16(512), binary.LittleEndian.Uint16(b.Data[len(b.Data)-2:]))

	f8 := PipelineFormat{Name: "std", Width: 64, Height: 32, FPSNum: 50, FPSDen: 1,
		Color: PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC}}
	sw.pipelineFormat.Store(&f8)
	b8 := sw.pipelineBlack()
	defer b8.ReleaseYUV()
	require.Len(t, b8.Data, ColorYUV420_8bit.FrameSize(64, 32))
	require.Equal(t, byte(16), b8.Data[0])
	require.Equal(t, byte(128), b8.Data[len(b8.Data)-1])
}

// Close stops the filler before the processing channel closes: closing a
// switcher that is filling must not send on a closed channel, and leaves
// no filler goroutine behind.
func TestProgramStall_CloseWhileFilling(t *testing.T) {
	for i := 0; i < 10; i++ {
		r := newStallRig(t, 50*time.Millisecond)
		r.sw.RegisterSource("cam1", newTestRelay())
		require.NoError(t, r.sw.Cut(context.Background(), "cam1"))
		r.feed("cam1", 100, 2, 90000)
		time.Sleep(time.Duration(100+10*i) * time.Millisecond)
		require.Greater(t, r.sw.stallFillFrames.Load(), int64(0))
		r.sw.Close()
		done := make(chan struct{})
		go func() { r.sw.stall.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("filler goroutine still running after Close")
		}
	}
}
