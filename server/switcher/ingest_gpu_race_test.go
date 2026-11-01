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

func TestIngestRawVideo_FeedsGPUOnce(t *testing.T) {
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

	require.NoError(t, sw.Cut(t.Context(), "test_src"))
	sw.IngestRawVideo("test_src", pf)
	// Ticks that show the frame, and repeat it, must not feed it again.
	time.Sleep(200 * time.Millisecond)

	var calls []string
	for _, k := range tracker.calls() {
		if k == "test_src" { // the built-in sources feed it too
			calls = append(calls, k)
		}
	}
	require.Equal(t, []string{"test_src"}, calls,
		"IngestRawVideo must feed GPU source manager exactly once; a "+
			"second writer races with the producer and corrupts the "+
			"content/PTS binding in RunFromCache")
}
