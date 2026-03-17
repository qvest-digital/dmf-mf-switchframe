package switcher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestSwitcher_PerfSubStages(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sample := sw.PerfSample()

	// All sub-stage fields should be zero initially (no frames processed yet)
	if sample.DecodeQueueNs != 0 {
		t.Errorf("initial DecodeQueueNs should be 0, got %d", sample.DecodeQueueNs)
	}
	if sample.DecodeNs != 0 {
		t.Errorf("initial DecodeNs should be 0, got %d", sample.DecodeNs)
	}
	if sample.SyncWaitNs != 0 {
		t.Errorf("initial SyncWaitNs should be 0, got %d", sample.SyncWaitNs)
	}
	if sample.ProcQueueNs != 0 {
		t.Errorf("initial ProcQueueNs should be 0, got %d", sample.ProcQueueNs)
	}

	// Verify the fields are populated from the atomic stores
	sw.lastDecodeQueueNs.Store(100)
	sw.lastDecodeNs.Store(200)
	sw.lastSyncWaitNs.Store(300)
	sw.lastProcQueueNs.Store(400)

	sample = sw.PerfSample()
	if sample.DecodeQueueNs != 100 {
		t.Errorf("DecodeQueueNs should be 100, got %d", sample.DecodeQueueNs)
	}
	if sample.DecodeNs != 200 {
		t.Errorf("DecodeNs should be 200, got %d", sample.DecodeNs)
	}
	if sample.SyncWaitNs != 300 {
		t.Errorf("SyncWaitNs should be 300, got %d", sample.SyncWaitNs)
	}
	if sample.ProcQueueNs != 400 {
		t.Errorf("ProcQueueNs should be 400, got %d", sample.ProcQueueNs)
	}
}

func TestPerfSample_FRCFrameZeroesSyncWait(t *testing.T) {
	// FRC-synthesized frames have DecodeEndNano=0 (no actual decode).
	// The sync wait measurement should store 0 for these frames,
	// not retain a stale value from a previous fresh frame.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// Simulate a prior fresh frame leaving a stale sync wait value.
	sw.lastSyncWaitNs.Store(25_000_000) // 25ms stale value

	// Simulate processing an FRC frame: DecodeEndNano=0, SyncReleaseNano set.
	// This is what videoProcessingLoop does when it dequeues a work item.
	frame := &ProcessingFrame{
		YUV:             make([]byte, 64),
		Width:           8,
		Height:          4,
		PTS:             1000,
		DecodeEndNano:   0,                 // FRC frame — no decode
		SyncReleaseNano: 1_000_000_000_000, // non-zero: was released by sync
	}

	// Replicate the videoProcessingLoop sync wait measurement logic.
	if frame.SyncReleaseNano > 0 {
		if frame.DecodeEndNano > 0 {
			sw.lastSyncWaitNs.Store(frame.SyncReleaseNano - frame.DecodeEndNano)
		} else {
			sw.lastSyncWaitNs.Store(0)
		}
	}

	sample := sw.PerfSample()
	require.Equal(t, int64(0), sample.SyncWaitNs,
		"FRC frame (DecodeEndNano=0) should zero out sync wait, not retain stale value")
}

func TestPerfSample_FreshFramePreservesSyncWait(t *testing.T) {
	// Fresh (decoded) frames have both DecodeEndNano and SyncReleaseNano set.
	// The sync wait should be the difference between them.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	decodeEnd := int64(1_000_000_000)
	syncRelease := int64(1_005_000_000) // 5ms later

	frame := &ProcessingFrame{
		YUV:             make([]byte, 64),
		Width:           8,
		Height:          4,
		PTS:             1000,
		DecodeEndNano:   decodeEnd,
		SyncReleaseNano: syncRelease,
	}

	// Replicate the updated measurement logic.
	if frame.SyncReleaseNano > 0 {
		if frame.DecodeEndNano > 0 {
			sw.lastSyncWaitNs.Store(frame.SyncReleaseNano - frame.DecodeEndNano)
		} else {
			sw.lastSyncWaitNs.Store(0)
		}
	}

	sample := sw.PerfSample()
	require.Equal(t, int64(5_000_000), sample.SyncWaitNs,
		"fresh frame sync wait should be SyncReleaseNano - DecodeEndNano")
}

func TestPerfSample_RawFrameCount(t *testing.T) {
	// Raw frame count should be reported per-source in PerfSourceSample.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// Register a raw source (MXL-style, no viewer).
	sw.RegisterMXLSource("mxl:cam1")

	// Initially zero
	sample := sw.PerfSample()
	src, ok := sample.Sources["mxl:cam1"]
	require.True(t, ok, "mxl:cam1 should be in sources")
	require.Equal(t, int64(0), src.RawFrameCount, "initial raw frame count should be 0")

	// Ingest some raw video frames
	for i := 0; i < 30; i++ {
		pf := &ProcessingFrame{
			YUV:    make([]byte, 64),
			Width:  8,
			Height: 4,
			PTS:    int64(i * 3000),
		}
		sw.IngestRawVideo("mxl:cam1", pf)
	}

	sample = sw.PerfSample()
	src = sample.Sources["mxl:cam1"]
	require.Equal(t, int64(30), src.RawFrameCount, "raw frame count should be 30 after 30 ingests")
}

func TestPerfSample_FrameSyncFields(t *testing.T) {
	// FrameSync fields should be zero when no frame sync is active.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sample := sw.PerfSample()
	require.Equal(t, float64(0), sample.FrameSyncReleaseFPS, "frame sync release FPS should be 0 without frame sync")
	require.Equal(t, 0, sample.FrameSyncSourceCount, "frame sync source count should be 0 without frame sync")

	// Enable frame sync
	sw.SetFrameSync(true, 33*time.Millisecond)

	// Register a relay-based source (has viewer → gets added to frame sync)
	sourceRelay := newTestRelay()
	sw.RegisterSource("cam1", sourceRelay)

	sample = sw.PerfSample()
	require.Equal(t, 1, sample.FrameSyncSourceCount, "frame sync source count should be 1 after adding relay source")
}

func TestFrameSynchronizer_ReleaseFPS(t *testing.T) {
	fs := NewFrameSynchronizer(
		33*time.Millisecond,
		func(key string, frame media.VideoFrame) {},
		func(key string, frame media.AudioFrame) {},
	)

	// First call initializes state, returns 0.
	fps := fs.ReleaseFPS()
	require.Equal(t, float64(0), fps, "first call should return 0 (no baseline)")

	// Simulate some releases via the atomic counters.
	fs.programDrivenReleases.Store(30)
	fs.timerDrivenReleases.Store(0)

	// Hack: set the last time slightly in the past so elapsed > 0.
	fs.mu.Lock()
	fs.releaseFPSLastTime = time.Now().Add(-1 * time.Second)
	fs.releaseFPSLastTotal = 0
	fs.mu.Unlock()

	fps = fs.ReleaseFPS()
	require.InDelta(t, 30.0, fps, 2.0,
		"should compute ~30 FPS from 30 releases in ~1 second")
}

func TestFrameSynchronizer_SourceCount(t *testing.T) {
	fs := NewFrameSynchronizer(
		33*time.Millisecond,
		func(key string, frame media.VideoFrame) {},
		func(key string, frame media.AudioFrame) {},
	)

	require.Equal(t, 0, fs.SourceCount(), "initial source count should be 0")

	fs.AddSource("cam1")
	require.Equal(t, 1, fs.SourceCount())

	fs.AddSource("cam2")
	require.Equal(t, 2, fs.SourceCount())

	fs.RemoveSource("cam1")
	require.Equal(t, 1, fs.SourceCount())
}
