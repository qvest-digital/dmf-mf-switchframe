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

// gateEncoder encodes every frame as a keyframe. Once armed, the next
// Encode closes held and waits for gate to close.
type gateEncoder struct {
	inner transition.VideoEncoder
	mu    sync.Mutex
	gate  chan struct{}
	held  chan struct{}
}

func (e *gateEncoder) arm() (held, gate chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.gate, e.held = make(chan struct{}), make(chan struct{})
	return e.held, e.gate
}

func (e *gateEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.mu.Lock()
	gate, held := e.gate, e.held
	e.gate, e.held = nil, nil
	e.mu.Unlock()
	if gate != nil {
		close(held)
		<-gate
	}
	data, _, err := e.inner.Encode(yuv, pts, forceIDR)
	return data, true, err
}
func (e *gateEncoder) Close() {}

// The async encoder emits frames after the tick that queued them. A shed
// tick's repeat, or a frame without reference counting such as broadcast
// black, is the program's frame for its tick, so it follows every frame
// of an earlier tick, including one the encoder still holds. Otherwise
// the program steps back in time once the held frame comes out.
func TestProgramFollowsTheFrameTheEncoderHolds(t *testing.T) {
	t.Run("repeat on a shed tick", func(t *testing.T) {
		programOrderWithHeldFrame(t, func(s *Switcher, ingest func(int64, bool)) {
			s.ticksBehind = 1
		})
	})
	t.Run("unmanaged frame", func(t *testing.T) {
		programOrderWithHeldFrame(t, func(s *Switcher, ingest func(int64, bool)) {
			ingest(3, false)
		})
	})
}

// programOrderWithHeldFrame encodes tick 1, holds tick 2's frame in the
// encoder and runs tick 3 after third has set it up. Tick 3 must not wait
// for the encoder, and the program PTS must be those of ticks 1, 2 and 3
// in that order.
func programOrderWithHeldFrame(t *testing.T, third func(s *Switcher, ingest func(n int64, managed bool))) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()
	enc := &gateEncoder{inner: transition.NewMockEncoder()}
	s.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return enc, nil
	})
	require.NoError(t, s.BuildPipeline())
	s.RegisterMXLSource("cam1")
	require.NoError(t, s.Cut(context.Background(), "cam1"))
	viewer := newMockProgramViewer("program-order")
	s.programRelay.AddViewer(viewer)

	period := int64(3000) // 30 fps
	tick := func(n int64) {
		s.processTick(clock.Tick{VideoPTS: n * period, ReadPTS: n * period,
			FrameDuration: 33333 * time.Microsecond})
	}
	ingest := func(n int64, managed bool) {
		pf := &ProcessingFrame{
			Data:  make([]byte, ColorYUV420_8bit.FrameSize(320, 240)),
			Width: 320, Height: 240, PTS: n * period, DTS: n * period,
			IsKeyframe: true, Format: ColorYUV420_8bit,
		}
		if managed {
			pf.SetRefs(1)
		}
		s.IngestRawVideo("cam1", pf)
	}
	waitFrames := func(n int) {
		t.Helper()
		require.Eventually(t, func() bool { return len(viewer.VideoFrames()) >= n },
			2*time.Second, time.Millisecond, "program frames")
	}

	// Tick 1 is encoded and out.
	ingest(1, true)
	tick(1)
	waitFrames(1)

	// Tick 2's frame is held in the encoder while tick 3 runs.
	held, gate := enc.arm()
	ingest(2, true)
	tick(2)
	<-held
	third(s, ingest)
	done := make(chan struct{})
	go func() { tick(3); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("tick 3 waited for the encoder to finish tick 2's frame")
	}
	close(gate)
	<-done
	waitFrames(3)

	var got []int64
	for _, f := range viewer.VideoFrames() {
		got = append(got, f.PTS)
	}
	require.Equal(t, []int64{period, 2 * period, 3 * period}, got,
		"program PTS in the order they reached the program")
}
