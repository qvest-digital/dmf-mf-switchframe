package switcher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tests that pin down the contract that protects the GPU source
// manager's entry.current from concurrent producer/sync-loop writers.
// The producer goroutine (IngestRawVideo) is the sole canonical writer.
// Every downstream handler that used to also IngestYUV has been pruned
// to avoid racing with the producer and reordering the content visible
// through RunFromCache.

func TestHandleRawVideoFrame_DoesNotIngestToGPU(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	tracker := &trackingGPUSourceManager{}
	sw.SetGPUSourceManager(tracker)

	// Register a source so handleRawVideoFrame doesn't early-return on
	// the unknown-source filter.
	sourceRelay := newTestRelay()
	sw.RegisterSource("test_src", sourceRelay)

	pf := &ProcessingFrame{
		Data:   make([]byte, 320*240*3/2),
		Width:  320,
		Height: 240,
		PTS:    12345,
		Format: ColorYUV420_8bit,
	}

	sw.handleRawVideoFrame("test_src", pf)

	require.Empty(t, tracker.calls(),
		"handleRawVideoFrame must not feed the GPU source manager — "+
			"the caller (IngestRawVideo or the decoder callback) is "+
			"the sole writer of entry.current; re-ingesting here races "+
			"with the producer and corrupts content/PTS binding in "+
			"RunFromCache")
}

func TestIngestRawVideo_FeedsGPU_WithoutFrameSync(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	tracker := &trackingGPUSourceManager{}
	sw.SetGPUSourceManager(tracker)

	sourceRelay := newTestRelay()
	sw.RegisterSource("test_src", sourceRelay)

	pf := &ProcessingFrame{
		Data:   make([]byte, 320*240*3/2),
		Width:  320,
		Height: 240,
		PTS:    12345,
		Format: ColorYUV420_8bit,
	}

	sw.IngestRawVideo("test_src", pf)

	require.Equal(t, []string{"test_src"}, tracker.calls(),
		"IngestRawVideo must feed GPU source manager exactly once when "+
			"frame sync is not active (the fallback path relies on this "+
			"to populate entry.current before the pipeline runs)")
}

func TestIngestRawVideo_FeedsGPU_WithFrameSync(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	tracker := &trackingGPUSourceManager{}
	sw.SetGPUSourceManager(tracker)

	sourceRelay := newTestRelay()
	sw.RegisterSource("test_src", sourceRelay)
	sw.SetFrameSync(true, 33333*time.Microsecond)

	pf := &ProcessingFrame{
		Data:   make([]byte, 320*240*3/2),
		Width:  320,
		Height: 240,
		PTS:    12345,
		Format: ColorYUV420_8bit,
	}

	initial := len(tracker.calls())
	sw.IngestRawVideo("test_src", pf)

	calls := tracker.calls()
	require.Equal(t, initial+1, len(calls),
		"IngestRawVideo must feed GPU source manager exactly once when "+
			"frame sync is active; the later tick-loop-driven "+
			"handleRawVideoFrame call must not re-feed")
	require.Equal(t, "test_src", calls[len(calls)-1])
}

func TestDecoderCallback_FeedsGPU(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	tracker := &trackingGPUSourceManager{}
	sw.SetGPUSourceManager(tracker)

	sourceRelay := newTestRelay()
	sw.RegisterSource("test_src", sourceRelay)
	sw.SetFrameSync(true, 33333*time.Microsecond)

	cb := sw.makeDecoderCallback("test_src")
	pf := &ProcessingFrame{
		Data:   make([]byte, 320*240*3/2),
		Width:  320,
		Height: 240,
		PTS:    12345,
		Format: ColorYUV420_8bit,
	}

	initial := len(tracker.calls())
	cb("test_src", pf)

	calls := tracker.calls()
	require.Equal(t, initial+1, len(calls),
		"decoder callback must feed GPU source manager exactly once — "+
			"it bypasses Switcher.IngestRawVideo to preserve its viewer-"+
			"level frameSync/delayBuffer routing, but is still the "+
			"producer-side entry point and therefore owns the GPU feed")
	require.Equal(t, "test_src", calls[len(calls)-1])
}
