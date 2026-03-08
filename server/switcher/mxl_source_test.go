package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestRegisterMXLSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")

	state := sw.State()
	require.Len(t, state.Sources, 1)
	src, ok := state.Sources["mxl-cam1"]
	require.True(t, ok, "Sources should contain 'mxl-cam1'")
	require.Equal(t, "mxl-cam1", src.Key)

	// MXL sources have no relay or viewer (unlike regular sources).
	sw.mu.RLock()
	ss := sw.sources["mxl-cam1"]
	require.NotNil(t, ss)
	require.Nil(t, ss.relay, "MXL source should have nil relay")
	require.Nil(t, ss.viewer, "MXL source should have nil viewer")
	sw.mu.RUnlock()
}

func TestRegisterMXLSource_Position(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterMXLSource("mxl-cam1")

	state := sw.State()
	require.Len(t, state.Sources, 2)
	require.Equal(t, 2, state.Sources["mxl-cam1"].Position,
		"MXL source should get next position after existing sources")
}

func TestRegisterMXLSource_HealthRegistered(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")

	// Health monitor should know about the source (it starts as offline since
	// no frames have arrived yet).
	status := sw.health.computeStatus("mxl-cam1", time.Now())
	require.Equal(t, SourceOffline, status, "newly registered MXL source should be offline (no frames)")
}

func TestIngestRawVideo_EnqueuesWork(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")
	require.NoError(t, sw.Cut(context.Background(), "mxl-cam1"))

	// Create a ProcessingFrame with known YUV data (4x4 frame = 24 bytes YUV420).
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

	sw.IngestRawVideo("mxl-cam1", pf)

	// The frame should be enqueued and processed through encodeAndBroadcastTransition,
	// ultimately reaching the program relay.
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"frame should reach program relay via pipeline encode")

	viewer.mu.Lock()
	got := viewer.videos[len(viewer.videos)-1]
	require.Equal(t, int64(1000), got.PTS, "PTS should be preserved")
	viewer.mu.Unlock()
}

func TestIngestRawVideo_NonProgramSourceDropped(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")
	sw.RegisterMXLSource("mxl-cam2")
	require.NoError(t, sw.Cut(context.Background(), "mxl-cam1"))

	initialFiltered := sw.routeFiltered.Load()

	// Ingest a frame for the non-program source.
	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		GroupID:    1,
	}
	sw.IngestRawVideo("mxl-cam2", pf)

	require.Equal(t, initialFiltered+1, sw.routeFiltered.Load(),
		"non-program source frame should increment routeFiltered counter")
}

func TestIngestRawVideo_UnknownSourceDropped(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	initialFiltered := sw.routeFiltered.Load()

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
	}
	sw.IngestRawVideo("nonexistent", pf)

	require.Equal(t, initialFiltered+1, sw.routeFiltered.Load(),
		"unknown source frame should increment routeFiltered counter")
}

func TestIngestRawVideo_FTBActiveDropped(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")
	require.NoError(t, sw.Cut(context.Background(), "mxl-cam1"))

	// Simulate FTB active by setting state directly.
	sw.mu.Lock()
	sw.state = StateFTB
	sw.mu.Unlock()

	initialFiltered := sw.routeFiltered.Load()

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
	}
	sw.IngestRawVideo("mxl-cam1", pf)

	require.Equal(t, initialFiltered+1, sw.routeFiltered.Load(),
		"FTB active should cause frame to be filtered")
}

func TestIngestRawVideo_HealthTracking(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")
	require.NoError(t, sw.Cut(context.Background(), "mxl-cam1"))

	// Before any frames, lastFrameAgoMs returns -1 (no frame recorded).
	require.Equal(t, int64(-1), sw.health.lastFrameAgoMs("mxl-cam1"),
		"no frame should be recorded yet")

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		GroupID:    1,
	}
	sw.IngestRawVideo("mxl-cam1", pf)

	// After ingesting, the health monitor should have recorded the frame.
	agoMs := sw.health.lastFrameAgoMs("mxl-cam1")
	require.NotEqual(t, int64(-1), agoMs,
		"health monitor should record frame arrival after IngestRawVideo")
	require.LessOrEqual(t, agoMs, int64(100),
		"frame should have been recorded very recently")
}

func TestMXLSource_UnregisterSafe(t *testing.T) {
	// Ensure UnregisterSource handles MXL sources (nil relay/viewer) without panic.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")
	require.NotPanics(t, func() {
		sw.UnregisterSource("mxl-cam1")
	}, "UnregisterSource should not panic for MXL sources with nil relay/viewer")

	state := sw.State()
	require.Empty(t, state.Sources, "MXL source should be removed after UnregisterSource")
}

func TestMXLSource_DebugSnapshotSafe(t *testing.T) {
	// Ensure DebugSnapshot handles MXL sources (nil viewer) without panic.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.RegisterMXLSource("mxl-cam1")
	require.NotPanics(t, func() {
		snap := sw.DebugSnapshot()
		sources, ok := snap["sources"].(map[string]any)
		require.True(t, ok)
		_, ok = sources["mxl-cam1"]
		require.True(t, ok, "MXL source should appear in debug snapshot")
	}, "DebugSnapshot should not panic for MXL sources with nil viewer")
}
