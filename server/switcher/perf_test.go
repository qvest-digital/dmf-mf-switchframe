package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
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
