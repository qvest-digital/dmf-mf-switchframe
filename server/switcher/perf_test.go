package switcher

import (
	"testing"
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
