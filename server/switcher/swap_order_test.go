package switcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/transition"
)

// A pipeline rebuild drains the old pipeline's encode queue while the new
// pipeline already takes ticks, and both feed the one program. A frame or
// repeat queued on the new pipeline is later than every frame left in the
// old queue, so it reaches the program after them. While the old queue
// drains, the new queue holds its ticks and drops what does not fit,
// counted, rather than reorder.
func TestProgramStaysInOrderAcrossAPipelineRebuild(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()
	enc := &gateEncoder{inner: transition.NewMockEncoder()}
	s.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return enc, nil
	})
	require.NoError(t, s.BuildPipeline())
	s.RegisterMXLSource("cam1")
	require.NoError(t, s.Cut(context.Background(), "cam1"))
	viewer := newMockProgramViewer("swap-order")
	s.programRelay.AddViewer(viewer)

	period := int64(3000) // 30 fps
	tick := func(n int64) {
		s.processTick(clock.Tick{VideoPTS: n * period, ReadPTS: n * period,
			FrameDuration: 33333 * time.Microsecond})
	}
	ingest := func(n int64) {
		pf := &ProcessingFrame{
			Data:  make([]byte, ColorYUV420_8bit.FrameSize(320, 240)),
			Width: 320, Height: 240, PTS: n * period, DTS: n * period,
			IsKeyframe: true, Format: ColorYUV420_8bit,
		}
		pf.SetRefs(1)
		s.IngestRawVideo("cam1", pf)
	}
	frames := func() int { return len(viewer.VideoFrames()) }

	// Tick 1 is encoded and out.
	ingest(1)
	tick(1)
	require.Eventually(t, func() bool { return frames() >= 1 },
		2*time.Second, time.Millisecond, "tick 1 on the program")

	// The old pipeline's encoder holds tick 2's frame across the rebuild.
	held, gate := enc.arm()
	release := sync.OnceFunc(func() { close(gate) })
	defer release() // a failed assertion must not leave Close waiting on the encoder
	ingest(2)
	tick(2)
	<-held
	s.RebuildPipeline()

	// Tick 3 is shed and repeats the last frame; ticks 4 to 7 carry
	// frames. The new queue holds four items, so tick 7 is dropped.
	dropsBefore := s.pipeEncodeDrop.Load()
	s.ticksBehind = 1
	tick(3)
	for n := int64(4); n <= 7; n++ {
		ingest(n)
		tick(n)
	}
	require.Never(t, func() bool { return frames() > 1 },
		100*time.Millisecond, time.Millisecond,
		"the new pipeline reached the program while the old one held tick 2")
	require.Equal(t, int64(1), s.pipeEncodeDrop.Load()-dropsBefore,
		"ticks past the new queue's capacity are dropped and counted")

	release()
	require.Eventually(t, func() bool { return frames() >= 6 },
		2*time.Second, time.Millisecond, "program frames after the old queue drained")

	var got []int64
	for _, f := range viewer.VideoFrames() {
		got = append(got, f.PTS)
	}
	require.Equal(t, []int64{period, 2 * period, 3 * period, 4 * period, 5 * period, 6 * period}, got,
		"program PTS in the order they reached the program")
}
