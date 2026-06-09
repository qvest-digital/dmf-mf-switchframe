package switcher

import (
	"sync"
	"testing"

	"github.com/zsiec/prism/distribution"
)

// TestFramePoolSwap_ConcurrentSetFormatAndHotPath is a -race regression for the
// data race on s.framePool: SetPipelineFormat reassigns the pool under configMu
// while hot-path readers (normalizeResolution / broadcastProcessed) load it
// without a lock. The field is now atomic.Pointer[FramePool]; this exercises a
// concurrent writer (SetPipelineFormat -> Store) against the normalizeResolution
// reader (Load + Acquire + stored-pool-pointer) under `-race`.
func TestFramePoolSwap_ConcurrentSetFormatAndHotPath(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Two 8-bit formats at different resolutions. SetPipelineFormat creates a
	// fresh FramePool sized to each; the source frame below is smaller than
	// both, so normalizeResolution always takes the Acquire path.
	fmtA := PipelineFormat{
		Name: "A", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
		Color: PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC},
	}
	fmtB := PipelineFormat{
		Name: "B", Width: 640, Height: 480, FPSNum: 30, FPSDen: 1,
		Color: PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC},
	}

	const iters = 1500
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: swap the pipeline format (and thus the frame pool) repeatedly.
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			f := fmtA
			if i%2 == 1 {
				f = fmtB
			}
			// Ignore ErrFormatDuringTransition — no transition is active here.
			_ = sw.SetPipelineFormat(f)
		}
	}()

	// Reader: drive normalizeResolution, the hot-path framePool reader.
	go func() {
		defer wg.Done()
		srcW, srcH := 16, 16
		srcSize := ColorYUV420_8bit.FrameSize(srcW, srcH)
		for i := 0; i < iters; i++ {
			pf := &ProcessingFrame{
				Data:   make([]byte, srcSize),
				Width:  srcW,
				Height: srcH,
				PTS:    int64(i),
				Format: ColorYUV420_8bit,
			}
			// normalizeResolution Loads s.framePool, may Acquire a buffer, and
			// stores the pool pointer on the returned frame. Release it back to
			// whichever pool it came from.
			out := sw.normalizeResolution(pf)
			out.ReleaseYUV()
		}
	}()

	wg.Wait()
}
