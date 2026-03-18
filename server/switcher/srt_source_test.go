package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestRegisterSRTSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	sw.RegisterSRTSource("srt:cam1")

	state := sw.State()
	require.Len(t, state.Sources, 1)
	src, ok := state.Sources["srt:cam1"]
	require.True(t, ok, "Sources should contain 'srt:cam1'")
	require.Equal(t, "srt:cam1", src.Key)

	// SRT sources have no relay or viewer (raw YUV via IngestRawVideo).
	sw.mu.RLock()
	ss := sw.sources["srt:cam1"]
	require.NotNil(t, ss)
	require.Nil(t, ss.relay, "SRT source should have nil relay")
	require.Nil(t, ss.viewer, "SRT source should have nil viewer")
	sw.mu.RUnlock()
}

func TestRegisterSRTSource_Label(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	sw.RegisterSRTSource("srt:my-camera")

	sw.mu.RLock()
	ss := sw.sources["srt:my-camera"]
	require.NotNil(t, ss)
	require.Equal(t, "my-camera", ss.label, "label should strip 'srt:' prefix")
	sw.mu.RUnlock()
}

func TestRegisterSRTSource_Position(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSRTSource("srt:cam1")

	state := sw.State()
	require.Len(t, state.Sources, 2)
	require.Equal(t, 2, state.Sources["srt:cam1"].Position,
		"SRT source should get next position after existing sources")
}

func TestRegisterSRTSource_HealthRegistered(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	sw.RegisterSRTSource("srt:cam1")

	// Health monitor should know about the source (it starts as offline since
	// no frames have arrived yet).
	status := sw.health.computeStatus("srt:cam1", time.Now())
	require.Equal(t, SourceOffline, status, "newly registered SRT source should be offline (no frames)")
}

func TestSRTSource_UnregisterSafe(t *testing.T) {
	// Ensure UnregisterSource handles SRT sources (nil relay/viewer) without panic.
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	sw.RegisterSRTSource("srt:cam1")
	require.NotPanics(t, func() {
		sw.UnregisterSource("srt:cam1")
	}, "UnregisterSource should not panic for SRT sources with nil relay/viewer")

	state := sw.State()
	require.Empty(t, state.Sources, "SRT source should be removed after UnregisterSource")
}

func TestSRTSource_IngestRawVideo(t *testing.T) {
	// Verify SRT sources can ingest raw YUV frames and they reach the program relay.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	sw.RegisterSRTSource("srt:cam1")
	require.NoError(t, sw.Cut(context.Background(), "srt:cam1"))

	yuv := make([]byte, 4*4*3/2) // 24 bytes for 4x4 YUV420
	for i := range yuv {
		yuv[i] = byte(i)
	}
	pf := &ProcessingFrame{
		YUV:        yuv,
		Width:      4,
		Height:     4,
		PTS:        1000,
		DTS:        1000,
		IsKeyframe: true,
		GroupID:    1,
		Codec:      "h264",
	}

	sw.IngestRawVideo("srt:cam1", pf)

	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"frame should reach program relay via pipeline encode")
}
