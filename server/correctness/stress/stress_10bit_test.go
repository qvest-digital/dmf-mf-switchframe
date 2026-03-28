//go:build correctness

package stress

import (
	"encoding/binary"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// ============================================================================
// 10-bit Stress Tests
//
// These tests exercise 10-bit operations under load and concurrency to verify
// correctness under pressure. All output assertions are based on mathematical
// properties, not implementation details.
// ============================================================================

// make10bitFrame creates a uniform YUV422P10LE frame.
func make10bitFrame(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, w*h*4)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}

// ============================================================================
// Rapid Blend Stress
// ============================================================================

// TestStress10bit_RapidBlend performs 1000 rapid BlendMix10 calls with random
// positions. Every output must have all Y samples in [0, 1023].
func TestStress10bit_RapidBlend(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	w, h := 320, 240
	frameSize := w * h * 4
	ySize := w * h

	frameA := make10bitFrame(w, h, 100, 400, 600)
	frameB := make10bitFrame(w, h, 900, 200, 800)
	dst := make([]byte, frameSize)

	rng := rand.New(rand.NewSource(42))

	const numIter = 1000
	violations := 0

	for i := 0; i < numIter; i++ {
		pos256 := rng.Intn(257) // 0 to 256 inclusive

		err := transition.BlendMix10(dst, frameA, frameB, w, h, pos256)
		require.NoError(t, err, "iteration %d", i)

		// Check all Y samples are in [0, 1023]
		for j := 0; j < ySize; j++ {
			val := binary.LittleEndian.Uint16(dst[j*2:])
			if val > 1023 {
				violations++
				if violations <= 5 {
					t.Logf("iter %d: Y[%d]=%d > 1023 (pos256=%d)", i, j, val, pos256)
				}
				break // one violation per frame is enough
			}
		}

		// Also check chroma planes
		chromaW := w / 2
		chromaSize := chromaW * h
		cbOff := ySize * 2
		crOff := cbOff + chromaSize*2
		for j := 0; j < chromaSize; j++ {
			cbVal := binary.LittleEndian.Uint16(dst[cbOff+j*2:])
			crVal := binary.LittleEndian.Uint16(dst[crOff+j*2:])
			if cbVal > 1023 || crVal > 1023 {
				violations++
				if violations <= 5 {
					t.Logf("iter %d: Cb[%d]=%d, Cr[%d]=%d (max 1023)", i, j, cbVal, j, crVal)
				}
				break
			}
		}
	}

	assert.Equal(t, 0, violations,
		"%d of %d iterations produced values exceeding 10-bit range [0, 1023]",
		violations, numIter)
}

// ============================================================================
// Concurrent Processing
// ============================================================================

// TestStress10bit_ConcurrentProcessing runs 4 goroutines each performing
// blend + scale operations on 10-bit frames. Verifies no panics, no races,
// all outputs valid.
func TestStress10bit_ConcurrentProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	t.Parallel()

	const numGoroutines = 4
	const itersPerGoroutine = 50

	w, h := 64, 32
	frameSize := w * h * 4
	ySize := w * h

	var wg sync.WaitGroup
	errCh := make(chan string, numGoroutines*itersPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(int64(id * 1000)))

			yA := uint16(100 + id*200)
			yB := uint16(900 - id*100)

			frameA := make10bitFrame(w, h, yA, 512, 512)
			frameB := make10bitFrame(w, h, yB, 512, 512)
			dst := make([]byte, frameSize)

			for i := 0; i < itersPerGoroutine; i++ {
				pos256 := rng.Intn(257)

				// Blend
				err := transition.BlendMix10(dst, frameA, frameB, w, h, pos256)
				if err != nil {
					errCh <- err.Error()
					continue
				}

				// Validate output range
				for j := 0; j < ySize; j++ {
					val := binary.LittleEndian.Uint16(dst[j*2:])
					if val > 1023 {
						errCh <- "Y exceeds 1023"
						break
					}
				}

				// Scale
				scaleDst := make([]byte, 32*16*4)
				err = transition.ScaleYUV422P10(dst, w, h, scaleDst, 32, 16)
				if err != nil {
					errCh <- err.Error()
					continue
				}

				// Validate scaled output range
				scaledYSize := 32 * 16
				for j := 0; j < scaledYSize; j++ {
					val := binary.LittleEndian.Uint16(scaleDst[j*2:])
					if val > 1023 {
						errCh <- "scaled Y exceeds 1023"
						break
					}
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	var errors []string
	for e := range errCh {
		errors = append(errors, e)
	}

	assert.Empty(t, errors,
		"concurrent 10-bit processing produced %d errors: %v", len(errors), errors)
}

// ============================================================================
// Blend Identity Under Stress
// ============================================================================

// TestStress10bit_IdentityUnderLoad verifies that blending a frame with itself
// produces the same output under repeated stress. A*p + A*(1-p) = A for any p.
func TestStress10bit_IdentityUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	w, h := 128, 64
	frameSize := w * h * 4

	frame := make10bitFrame(w, h, 512, 300, 700)
	dst := make([]byte, frameSize)

	rng := rand.New(rand.NewSource(99))

	for i := 0; i < 500; i++ {
		pos256 := rng.Intn(257)
		err := transition.BlendMix10(dst, frame, frame, w, h, pos256)
		require.NoError(t, err, "iteration %d", i)

		// Output must equal input (within ±1 for rounding)
		gotY := binary.LittleEndian.Uint16(dst[0:2])
		if gotY < 511 || gotY > 513 {
			t.Errorf("iteration %d (pos=%d): blend(A, A) Y=%d, expected 512±1",
				i, pos256, gotY)
		}
	}
}

// ============================================================================
// FTB Stress
// ============================================================================

// TestStress10bit_FTBSweep rapidly sweeps FTB through all positions 10 times.
// At every position, Y must be between source Y and black Y (64).
func TestStress10bit_FTBSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	w, h := 64, 32
	frameSize := w * h * 4

	srcY := uint16(800)
	src := make10bitFrame(w, h, srcY, 400, 600)
	dst := make([]byte, frameSize)

	violations := 0

	for sweep := 0; sweep < 10; sweep++ {
		for pos := 0; pos <= 256; pos++ {
			err := transition.BlendFTB10(dst, src, w, h, pos, true)
			require.NoError(t, err)

			gotY := binary.LittleEndian.Uint16(dst[0:2])

			// Y must be between black (64) and source (800), inclusive
			if gotY < 64 || gotY > srcY {
				violations++
				if violations <= 5 {
					t.Logf("sweep %d pos %d: Y=%d not in [64, %d]",
						sweep, pos, gotY, srcY)
				}
			}
		}
	}

	assert.Equal(t, 0, violations,
		"FTB sweep: %d violations where Y was outside [black, source] range", violations)
}
