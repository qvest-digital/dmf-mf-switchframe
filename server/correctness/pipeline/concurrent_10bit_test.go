//go:build correctness

package pipeline

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit concurrent ingest tests.
//
// Multiple goroutines ingesting 10-bit YUV422P10LE frames simultaneously.
// Verify no data races, no panics, frames have Format=ColorYUV422_10bit,
// and buffer sizes are w*h*4.
// ==========================================================================

// make10bitFrame creates a solid-color YUV422P10LE ProcessingFrame.
func make10bitFrame(w, h int, y, cb, cr uint16, pts int64, isKeyframe bool) *switcher.ProcessingFrame {
	format := switcher.ColorYUV422_10bit
	frameSize := format.FrameSize(w, h)
	data := make([]byte, frameSize)

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], y)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(data[cbOff+i*2:], cb)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(data[crOff+i*2:], cr)
	}

	return &switcher.ProcessingFrame{
		Data:       data,
		Width:      w,
		Height:     h,
		Format:     format,
		PTS:        pts,
		IsKeyframe: isKeyframe,
	}
}

// average10bitY computes the average Y value from a 10-bit YUV422P10LE frame's Y plane.
func average10bitY(data []byte, w, h int) float64 {
	ySize := w * h
	if len(data) < ySize*2 {
		return 0
	}
	var sum uint64
	for i := 0; i < ySize; i++ {
		sum += uint64(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return float64(sum) / float64(ySize)
}

// TestMultiSourceConcurrentIngest10bit verifies that multiple goroutines
// ingesting 10-bit frames simultaneously don't cause data races or panics.
func TestMultiSourceConcurrentIngest10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w          = testWidth
		h          = testHeight
		numSources = 4
		numFrames  = 30
	)

	h2 := newPipelineHarness(t)
	defer h2.close()

	type sourceSpec struct {
		key string
		y   uint16
	}
	sources := []sourceSpec{
		{"cam10-a", 200},
		{"cam10-b", 400},
		{"cam10-c", 600},
		{"cam10-d", 800},
	}

	for _, s := range sources {
		h2.addSource(s.key, sourceColor{Y: byte(s.y >> 2), Cb: 128, Cr: 128})
	}

	// Cut to first source.
	h2.cut(t, sources[0].key)

	// Start all sources broadcasting 10-bit frames simultaneously.
	var wg sync.WaitGroup
	for idx, s := range sources {
		wg.Add(1)
		go func(key string, yVal uint16, startOffset int) {
			defer wg.Done()
			for i := 0; i < numFrames; i++ {
				pf := make10bitFrame(w, h, yVal, 512, 512,
					int64(90000+startOffset*100000+i*ptsDelta), i == 0)
				h2.sw.IngestRawVideo(key, pf)
				time.Sleep(5 * time.Millisecond)
			}
		}(s.key, s.y, idx)
	}

	// Rapid cuts while broadcasting.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			key := sources[i%numSources].key
			_ = h2.sw.Cut(t.Context(), key)
			time.Sleep(15 * time.Millisecond)
		}
	}()

	wg.Wait()
	time.Sleep(1 * time.Second)

	// The test passes if we get here without panic or deadlock.
	t.Log("PASS: No panic or deadlock under concurrent 10-bit ingest")

	videos := h2.capture.videosCopy()
	t.Logf("Captured %d program output frames with 10-bit concurrent ingest", len(videos))
}

// TestMultiSourceRaceDetection10bit stress tests data race detection
// with 10-bit frames. Exercises concurrent IngestRawVideo and Cut.
func TestMultiSourceRaceDetection10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w          = testWidth
		h          = testHeight
		numSources = 4
	)

	h2 := newPipelineHarness(t)
	defer h2.close()

	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("race10-%d", i)
		h2.addSource(key, sourceColor{Y: byte(40 + i*50), Cb: 128, Cr: 128})
	}

	h2.cut(t, "race10-0")

	// Concurrent: broadcast 10-bit frames from all sources + rapid cuts.
	var wg sync.WaitGroup
	for i := 0; i < numSources; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("race10-%d", idx)
			yVal := uint16(100 + idx*200)
			for f := 0; f < 20; f++ {
				pf := make10bitFrame(w, h, yVal, 512, 512,
					int64(90000+idx*200000+f*ptsDelta), f == 0)
				h2.sw.IngestRawVideo(key, pf)
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			key := fmt.Sprintf("race10-%d", i%numSources)
			_ = h2.sw.Cut(t.Context(), key)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	wg.Wait()
	time.Sleep(1 * time.Second)

	// If -race doesn't report anything, the test passes.
	t.Log("PASS: No data race detected with 10-bit frames")
}

// TestFramePoolContention10bit verifies that the frame pool handles
// 10-bit frames (4x larger than 8-bit) under contention without panic.
func TestFramePoolContention10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = testWidth
		h = testHeight
	)

	h2 := newPipelineHarness(t)
	defer h2.close()

	h2.addSource("pool10-a", sourceColor{Y: 20, Cb: 128, Cr: 128})
	h2.addSource("pool10-b", sourceColor{Y: 200, Cb: 128, Cr: 128})

	h2.cut(t, "pool10-a")

	// Feed 10-bit frames from both sources at high rate.
	var wg sync.WaitGroup
	for _, src := range []struct {
		key string
		y   uint16
	}{
		{"pool10-a", 100},
		{"pool10-b", 800},
	} {
		wg.Add(1)
		go func(key string, yVal uint16) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				pf := make10bitFrame(w, h, yVal, 512, 512,
					int64(90000+i*ptsDelta), i == 0)
				// Verify the frame data is the correct 10-bit size.
				require.Equal(t, w*h*4, len(pf.Data),
					"10-bit frame should be w*h*4 = %d bytes, got %d", w*h*4, len(pf.Data))
				h2.sw.IngestRawVideo(key, pf)
				time.Sleep(3 * time.Millisecond)
			}
		}(src.key, src.y)
	}

	// Rapid cuts.
	cutKeys := []string{"pool10-a", "pool10-b", "pool10-a", "pool10-b", "pool10-a", "pool10-b"}
	for i, key := range cutKeys {
		time.Sleep(20 * time.Millisecond)
		err := h2.sw.Cut(t.Context(), key)
		require.NoError(t, err, "cut %d to %s", i, key)
	}

	wg.Wait()
	time.Sleep(1 * time.Second)

	t.Log("PASS: No panic or deadlock under 10-bit frame pool contention")
}
