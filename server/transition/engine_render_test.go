package transition

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/video"
)

// renderLeftRight draws from on the left half and to on the right, so the
// result can come from no blend.
func renderLeftRight(calls *int, ok bool) RenderFunc {
	return func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
		*calls++
		if !ok {
			return false
		}
		copy(dst, from[:f.FrameSize(w, h)])
		for y := 0; y < h; y++ {
			copy(dst[y*w+w/2:y*w+w], to[y*w+w/2:y*w+w])
		}
		return true
	}
}

// The sync loop renders transitions through BlendRawInto, so a DVE drawn on
// the CPU has to reach it there as well.
func TestBlendRawInto_UsesRender(t *testing.T) {
	calls := 0
	e := NewEngine(EngineConfig{Render: renderLeftRight(&calls, true)})
	e.StartSync("", "", Mix, 1000)
	const w, h = 64, 16
	a, b := makeSolidYUV420(w, h, 80), makeSolidYUV420(w, h, 160)
	got, gw, gh := e.BlendRawInto(nil, a, w, h, b, w, h, 0.5)
	require.Equal(t, 1, calls)
	require.Equal(t, [2]int{w, h}, [2]int{gw, gh})
	require.Equal(t, byte(80), got[0])
	require.Equal(t, byte(160), got[w-1], "the right half comes from the renderer, not a mix")
}

func TestBlendRawInto_RenderDeclinesFallsBackToBlend(t *testing.T) {
	calls := 0
	e := NewEngine(EngineConfig{Render: renderLeftRight(&calls, false)})
	e.StartSync("", "", Mix, 1000)
	const w, h = 64, 16
	got, _, _ := e.BlendRawInto(nil, makeSolidYUV420(w, h, 80), w, h, makeSolidYUV420(w, h, 160), w, h, 0.5)
	require.Equal(t, 1, calls)
	require.InDelta(t, 120, int(got[0]), 1, "a declined frame is mixed")
	require.InDelta(t, 120, int(got[w-1]), 1)
}

// IngestRawFrame drives the engine's own output path, which draws on a
// render goroutine.
func TestIngestRawFrame_UsesRender(t *testing.T) {
	for _, f := range []video.Format{video.YUV420_8bit, video.YUV422_10bit} {
		var mu sync.Mutex
		calls := 0
		var out []byte
		var gotFormat video.Format
		e := NewEngine(EngineConfig{
			Render: func(dst, from, to []byte, w, h int, fm video.Format, pos float64) bool {
				mu.Lock()
				defer mu.Unlock()
				calls++
				gotFormat = fm
				for i := range dst {
					dst[i] = 7
				}
				return true
			},
			Output: func(yuv []byte, w, h int, pts int64, kf bool) {
				mu.Lock()
				defer mu.Unlock()
				out = append([]byte(nil), yuv...)
			},
		})
		e.SetColorFormat(f)
		require.NoError(t, e.Start("a", "b", Mix, 60000))
		const w, h = 32, 16
		e.IngestRawFrame("a", make([]byte, f.FrameSize(w, h)), w, h, 1)
		e.IngestRawFrame("b", make([]byte, f.FrameSize(w, h)), w, h, 2)
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return out != nil
		}, time.Second, time.Millisecond)
		mu.Lock()
		require.Equal(t, 1, calls, "%v", f)
		require.Equal(t, f, gotFormat)
		require.Len(t, out, f.FrameSize(w, h))
		require.Equal(t, byte(7), out[0])
		mu.Unlock()
		e.Stop()
	}
}

// A render slower than the frame period must not hold up the source that
// triggers it: the frames it cannot draw in time are dropped, the newest
// waiting one is drawn next.
func TestIngestRawFrame_SlowRenderDoesNotDelaySource(t *testing.T) {
	var mu sync.Mutex
	var drawn []float64
	release := make(chan struct{})
	e := NewEngine(EngineConfig{
		Render: func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
			<-release
			mu.Lock()
			drawn = append(drawn, float64(from[0]))
			mu.Unlock()
			return true
		},
		FramePeriod: 10 * time.Millisecond,
		Output:      func(yuv []byte, w, h int, pts int64, kf bool) {},
	})
	require.NoError(t, e.Start("a", "b", Mix, 60000))
	defer e.Stop()
	const w, h = 32, 16
	start := time.Now()
	for i := 1; i <= 10; i++ {
		a := make([]byte, video.YUV420_8bit.FrameSize(w, h))
		a[0] = byte(i)
		e.IngestRawFrame("a", a, w, h, int64(i))
		e.IngestRawFrame("b", make([]byte, len(a)), w, h, int64(i))
	}
	require.Less(t, time.Since(start), 200*time.Millisecond, "ingest waited for the renderer")
	close(release)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(drawn) > 0 && drawn[len(drawn)-1] == 10
	}, time.Second, time.Millisecond, "the newest frame is drawn")
	mu.Lock()
	require.Less(t, len(drawn), 10, "frames that waited were dropped, not queued")
	mu.Unlock()
}

// The transition completes after its last frame is out: the switcher moves
// program when OnComplete fires, and a frame after that would show the
// transition again.
func TestIngestRawFrame_FinalFrameBeforeOnComplete(t *testing.T) {
	var mu sync.Mutex
	var events []string
	e := NewEngine(EngineConfig{
		Render: func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
			time.Sleep(5 * time.Millisecond)
			return true
		},
		Output: func(yuv []byte, w, h int, pts int64, kf bool) {
			mu.Lock()
			events = append(events, "output")
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {
			mu.Lock()
			events = append(events, fmt.Sprintf("complete aborted=%v", aborted))
			mu.Unlock()
		},
	})
	require.NoError(t, e.Start("a", "b", Mix, 100))
	const w, h = 32, 16
	frame := make([]byte, video.YUV420_8bit.FrameSize(w, h))
	e.IngestRawFrame("a", frame, w, h, 0)
	time.Sleep(150 * time.Millisecond) // past the end: the next frame is final
	e.IngestRawFrame("b", frame, w, h, 1)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 2
	}, time.Second, time.Millisecond)
	mu.Lock()
	require.Equal(t, []string{"output", "complete aborted=false"}, events)
	mu.Unlock()
	require.Equal(t, StateIdle, e.State())
}

// Abort and Stop end the render goroutine, also while it is drawing.
func TestRenderGoroutineEndsOnAbortAndStop(t *testing.T) {
	for _, end := range []string{"abort", "stop"} {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		outputs := atomic.Int32{}
		e := NewEngine(EngineConfig{
			Render: func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release
				return true
			},
			Output: func(yuv []byte, w, h int, pts int64, kf bool) { outputs.Add(1) },
		})
		require.NoError(t, e.Start("a", "b", Mix, 60000))
		const w, h = 32, 16
		frame := make([]byte, video.YUV420_8bit.FrameSize(w, h))
		e.IngestRawFrame("a", frame, w, h, 0)
		e.IngestRawFrame("b", frame, w, h, 1)
		<-started
		e.mu.Lock()
		r := e.render
		e.mu.Unlock()
		require.NotNil(t, r)
		if end == "abort" {
			e.Abort()
		} else {
			e.Stop()
		}
		close(release)
		require.Eventually(t, func() bool { return !r.running() }, time.Second, time.Millisecond, end)
		require.Zero(t, outputs.Load(), "%s: a frame drawn for an ended transition went out", end)
	}
}

// BlendRawInto's caller processes the returned buffer in place (keyers,
// grading), so it must render every frame afresh rather than hand back the
// processed frame as a repeat after a slow render.
func TestBlendRawInto_SlowRenderStillRendersEveryFrame(t *testing.T) {
	calls := 0
	e := NewEngine(EngineConfig{Render: slowRender(&calls, 25*time.Millisecond), FramePeriod: 10 * time.Millisecond})
	e.StartSync("", "", Mix, 1000)
	const w, h = 32, 16
	a, b := makeSolidYUV420(w, h, 80), makeSolidYUV420(w, h, 160)
	var buf []byte
	var got []byte
	for i := 0; i < 4; i++ {
		buf, _, _ = e.BlendRawInto(buf, a, w, h, b, w, h, 0.5)
		got = append(got, buf[0])
		for j := range buf {
			buf[j] += 100 // an in-place pipeline node
		}
	}
	require.Equal(t, []byte{1, 2, 3, 4}, got)
	require.Equal(t, 4, calls)
	require.True(t, e.overrunNoted.Load(), "the overrun is still logged")
}

func TestRender_NoPacingWithoutFramePeriod(t *testing.T) {
	calls := 0
	e := NewEngine(EngineConfig{Render: slowRender(&calls, 15*time.Millisecond)})
	e.StartSync("", "", Mix, 1000)
	const w, h = 32, 16
	a := makeSolidYUV420(w, h, 80)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf, _, _ = e.BlendRawInto(buf, a, w, h, a, w, h, 0.5)
	}
	require.Equal(t, 3, calls)
}

// BlendRawInto hands Render the engine's pixel format, as blendAndOutput
// does.
func TestBlendRawInto_RenderGetsColorFormat(t *testing.T) {
	var got video.Format
	e := NewEngine(EngineConfig{Render: func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
		got = f
		return true
	}})
	e.SetColorFormat(video.YUV422_10bit)
	e.StartSync("", "", Mix, 1000)
	const w, h = 32, 16
	fr := make([]byte, video.YUV422_10bit.FrameSize(w, h))
	out, _, _ := e.BlendRawInto(nil, fr, w, h, fr, w, h, 0.5)
	require.Equal(t, video.YUV422_10bit, got)
	require.Len(t, out, video.YUV422_10bit.FrameSize(w, h))
}

// slowRender fills dst with the call number; the first call takes long.
func slowRender(calls *int, first time.Duration) RenderFunc {
	return func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
		*calls++
		if *calls == 1 {
			time.Sleep(first)
		}
		for i := range dst {
			dst[i] = byte(*calls)
		}
		return true
	}
}

// Once the final frame is handed to the render goroutine the engine is
// idle, but the switcher's cut and abort still Stop it: a final frame and
// OnComplete landing after that would flash the old transition and
// complete whatever the switcher started since. Abort and ForceComplete
// complete the transition themselves instead.
func TestRenderFinalFrameStoppedAfterHandOff(t *testing.T) {
	for _, end := range []string{"stop", "abort", "force"} {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		var mu sync.Mutex
		var outputs int
		var completes []bool
		e := NewEngine(EngineConfig{
			Render: func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
				started <- struct{}{}
				<-release
				return true
			},
			Output: func(yuv []byte, w, h int, pts int64, kf bool) {
				mu.Lock()
				outputs++
				mu.Unlock()
			},
			OnComplete: func(aborted bool) {
				mu.Lock()
				completes = append(completes, aborted)
				mu.Unlock()
			},
		})
		require.NoError(t, e.Start("a", "b", Mix, 50))
		const w, h = 32, 16
		frame := make([]byte, video.YUV420_8bit.FrameSize(w, h))
		e.IngestRawFrame("a", frame, w, h, 0)
		time.Sleep(80 * time.Millisecond) // past the end: the next frame is final
		e.IngestRawFrame("b", frame, w, h, 1)
		<-started
		require.Equal(t, StateIdle, e.State(), "%s: the final frame is handed off", end)
		e.mu.Lock()
		r := e.finalRender
		e.mu.Unlock()
		require.NotNil(t, r, end)
		switch end {
		case "stop":
			e.Stop()
		case "abort":
			e.Abort()
		case "force":
			e.ForceComplete()
		}
		close(release)
		require.Eventually(t, func() bool { return !r.running() }, time.Second, time.Millisecond, end)
		mu.Lock()
		require.Zero(t, outputs, "%s: the final frame went out after the engine was stopped", end)
		switch end {
		case "stop":
			require.Empty(t, completes, "stop never completes")
		case "abort":
			require.Equal(t, []bool{true}, completes)
		case "force":
			require.Equal(t, []bool{false}, completes)
		}
		mu.Unlock()
	}
}
