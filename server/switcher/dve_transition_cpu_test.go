package switcher

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// patterned returns a 4:2:0 frame whose luma differs column by column, so
// a horizontal move shows.
func patterned(w, h int, seed byte) []byte {
	f := make([]byte, ColorYUV420_8bit.FrameSize(w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			f[y*w+x] = 16 + byte((x*7+y*3)%200) + seed
		}
	}
	for i := w * h; i < len(f); i++ {
		f[i] = 128 + seed
	}
	return f
}

// TestCPUTransition_DVEIsNotAMix is the CPU-build failure an operator saw:
// every DVE transition came out as a plain mix on an engine without a GPU.
func TestCPUTransition_DVEIsNotAMix(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	defer sw.Close()
	sw.SetTransitionConfig(TransitionConfig{})
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	var mu sync.Mutex
	var frames [][]byte
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, append([]byte(nil), pf.Data...))
	})
	require.NoError(t, sw.BuildPipeline())

	pf := sw.PipelineFormat()
	w, h := pf.Width, pf.Height
	from, to := patterned(w, h, 0), patterned(w, h, 20)
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "dve-push-left", 60000, ""))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.5))

	send := func(key string, data []byte, pts int64) {
		sw.handleRawVideoFrame(key, &ProcessingFrame{
			Data: append([]byte(nil), data...), Format: ColorYUV420_8bit,
			Width: w, Height: h, PTS: pts, Codec: "h264",
		})
	}
	send("cam1", from, 100)
	send("cam2", to, 101)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) >= 1
	}, time.Second, 5*time.Millisecond)
	sw.AbortTransition()

	mu.Lock()
	got := frames[len(frames)-1]
	mu.Unlock()

	want := make([]byte, video.YUV420_8bit.FrameSize(w, h))
	require.True(t, dve.CPUTransitionForType("dve-push-left")(want, from, to, w, h, video.YUV420_8bit, 0.5))
	require.Equal(t, want, got, "program must carry the push, not a mix")

	mix := make([]byte, len(want))
	for i := range mix {
		mix[i] = byte((int(from[i]) + int(to[i]) + 1) / 2)
	}
	require.False(t, bytes.Equal(got[:w*h], mix[:w*h]))
}

// TestCPUTransition_3DIsRefused: a 3D DVE on an engine with no GPU fails
// with a reason instead of coming out as a mix, and the state says so.
func TestCPUTransition_3DIsRefused(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	defer sw.Close()
	sw.SetTransitionConfig(TransitionConfig{})
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	ctx := context.Background()
	require.NoError(t, sw.Cut(ctx, "cam1"))
	require.NoError(t, sw.SetPreview(ctx, "cam2"))

	require.Equal(t, "3D DVE transitions need a GPU engine", sw.State().DVE3DUnavailable)
	err := sw.StartTransition(ctx, "cam2", "dve-cube", 1000, "")
	require.ErrorIs(t, err, ErrDVE3DNeedsGPU)
	require.False(t, sw.State().InTransition)
	require.Equal(t, "cam1", sw.State().ProgramSource)

	require.NoError(t, sw.StartTransition(ctx, "cam2", "dve-push-left", 60000, ""), "2D DVE runs on the CPU")
	sw.AbortTransition()
}

func TestGPUTransition_3DIsAllowed(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	defer sw.Close()
	sw.SetTransitionConfig(TransitionConfig{})
	sw.SetGPUPipeline(&mockGPUPipelineRunner{})
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	ctx := context.Background()
	require.NoError(t, sw.Cut(ctx, "cam1"))
	require.Empty(t, sw.State().DVE3DUnavailable)
	require.NoError(t, sw.StartTransition(ctx, "cam2", "dve-cube", 60000, ""))
	sw.AbortTransition()
}
