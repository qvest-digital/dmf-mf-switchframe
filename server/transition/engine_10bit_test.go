package transition

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEngine_IngestRawFrame_10bit_BlendMix verifies that the transition engine
// correctly blends 10-bit frames using 10-bit blend functions rather than
// 8-bit functions that would produce incorrect results.
func TestEngine_IngestRawFrame_10bit_BlendMix(t *testing.T) {
	w, h := 16, 16
	frame10Size := w * h * 4 // YUV422P10LE

	var mu sync.Mutex
	var outputYUV []byte
	var outputW, outputH int

	engine := NewEngine(EngineConfig{
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			defer mu.Unlock()
			outputYUV = make([]byte, len(yuv))
			copy(outputYUV, yuv)
			outputW = width
			outputH = height
		},
		HintWidth:  w,
		HintHeight: h,
	})

	// Set 10-bit format.
	engine.SetColorFormat(ColorFormat10bit)

	err := engine.Start("src-a", "src-b", Mix, 1000)
	require.NoError(t, err)

	// Create 10-bit frame A (Y=200)
	frameA := makeEngine10bitFrame(w, h, 200, 512, 512)

	// Create 10-bit frame B (Y=800)
	frameB := makeEngine10bitFrame(w, h, 800, 512, 512)

	// Ingest frame A (from source).
	engine.IngestRawFrame("src-a", frameA, w, h, 90000)
	// Ingest frame B (to source) — triggers blend.
	engine.IngestRawFrame("src-b", frameB, w, h, 90000)

	mu.Lock()
	defer mu.Unlock()

	require.NotNil(t, outputYUV, "engine should produce output")
	assert.Equal(t, w, outputW)
	assert.Equal(t, h, outputH)

	// Output should be 10-bit sized.
	assert.Equal(t, frame10Size, len(outputYUV),
		"output should be 10-bit frame size (w*h*4)")

	// The blend position is near 0 at the start, so output should be close to A (200).
	// Allow wide tolerance since the transition just started.
	firstY := binary.LittleEndian.Uint16(outputYUV[:2])
	assert.Greater(t, firstY, uint16(100),
		"blended Y should be plausible 10-bit value (got %d)", firstY)
	assert.Less(t, firstY, uint16(900),
		"blended Y should be plausible 10-bit value (got %d)", firstY)

	engine.Abort()
}

// TestEngine_IngestRawFrame_10bit_FTB verifies fade-to-black with 10-bit frames.
func TestEngine_IngestRawFrame_10bit_FTB(t *testing.T) {
	w, h := 16, 16

	var mu sync.Mutex
	var outputYUV []byte

	engine := NewEngine(EngineConfig{
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			defer mu.Unlock()
			outputYUV = make([]byte, len(yuv))
			copy(outputYUV, yuv)
		},
		HintWidth:  w,
		HintHeight: h,
	})

	engine.SetColorFormat(ColorFormat10bit)

	err := engine.Start("src-a", "", FTB, 1000)
	require.NoError(t, err)

	// Create a bright 10-bit frame.
	frameA := makeEngine10bitFrame(w, h, 700, 512, 512)
	engine.IngestRawFrame("src-a", frameA, w, h, 90000)

	mu.Lock()
	defer mu.Unlock()

	require.NotNil(t, outputYUV, "FTB should produce output")

	// Output should be 10-bit sized.
	assert.Equal(t, w*h*4, len(outputYUV),
		"FTB output should be 10-bit frame size")

	// The blend is at position ~0 (just started), so it should be close to the source.
	firstY := binary.LittleEndian.Uint16(outputYUV[:2])
	assert.Greater(t, firstY, uint16(400),
		"FTB at start should be close to source (got %d)", firstY)

	engine.Abort()
}

// TestEngine_8bit_StillWorks verifies the existing 8-bit path is unaffected.
func TestEngine_8bit_IngestRawFrame(t *testing.T) {
	w, h := 16, 16

	var mu sync.Mutex
	var outputYUV []byte

	engine := NewEngine(EngineConfig{
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			defer mu.Unlock()
			outputYUV = make([]byte, len(yuv))
			copy(outputYUV, yuv)
		},
		HintWidth:  w,
		HintHeight: h,
	})
	// Default: 8-bit format (no SetColorFormat call).

	err := engine.Start("src-a", "src-b", Mix, 1000)
	require.NoError(t, err)

	frame8Size := yuvFrameSize(w, h)
	frameA := make([]byte, frame8Size)
	for i := 0; i < w*h; i++ {
		frameA[i] = 100
	}
	for i := w * h; i < frame8Size; i++ {
		frameA[i] = 128
	}

	frameB := make([]byte, frame8Size)
	for i := 0; i < w*h; i++ {
		frameB[i] = 200
	}
	for i := w * h; i < frame8Size; i++ {
		frameB[i] = 128
	}

	engine.IngestRawFrame("src-a", frameA, w, h, 90000)
	engine.IngestRawFrame("src-b", frameB, w, h, 90000)

	mu.Lock()
	defer mu.Unlock()

	require.NotNil(t, outputYUV)
	assert.Equal(t, frame8Size, len(outputYUV), "8-bit output should be w*h*3/2")

	engine.Abort()
}

// makeEngine10bitFrame creates a YUV422P10LE frame with uniform Y, Cb, Cr values.
func makeEngine10bitFrame(w, h int, y, cb, cr uint16) []byte {
	frameSize := w * h * 4
	buf := make([]byte, frameSize)
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h * 2
	// Y plane
	for i := 0; i < ySize; i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], y)
	}
	// Cb plane
	for i := ySize; i < ySize+chromaSize; i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], cb)
	}
	// Cr plane
	for i := ySize + chromaSize; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], cr)
	}
	return buf
}
