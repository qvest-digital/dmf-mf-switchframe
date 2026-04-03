package transition

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// ==========================================================================
// Regression guards: prevent 10-bit frame truncation to 8-bit sizing.
//
// The pattern that causes bugs: code using video.YUV420FrameSize(w,h) (always w*h*3/2)
// or 8-bit sizing in paths that receive 10-bit frames (w*h*4).
// When 10-bit data is processed with 8-bit sizing, the buffer is truncated
// to 37.5% of its actual size, destroying the Cr plane.
//
// These tests use a "sentinel pattern": write a known value (0xAB) into the
// LAST QUARTER of a 10-bit buffer (the Cr plane's second half). If any code
// truncates to 8-bit size (w*h*3/2), this region is destroyed.
//
//   10-bit frame (w*h*4 bytes):
//   [  Y plane: w*h*2   ][  Cb plane: w/2*h*2  ][  Cr plane: w/2*h*2  ]
//                                                ^--- sentinel here ---^
//                        ^--- 8-bit truncation point: w*h*3/2 ---^
// ==========================================================================

// TestEngineFormatGuard_IngestRawFrame_10bit_FullBufferPreserved verifies that
// IngestRawFrame stores the FULL 10-bit buffer (w*h*4 bytes) in latestYUVA/B.
// If the engine uses 8-bit sizing (w*h*3/2), the sentinel in the Cr plane is lost.
func TestEngineFormatGuard_IngestRawFrame_10bit_FullBufferPreserved(t *testing.T) {
	w, h := 16, 8
	frameSize := video.YUV422_10bit.FrameSize(w, h) // w*h*4 = 512
	eightBitSize := video.YUV420FrameSize(w, h)     // w*h*3/2 = 192

	// Sanity: 10-bit is much larger than 8-bit.
	require.Greater(t, frameSize, eightBitSize,
		"10-bit frame (%d) must be larger than 8-bit (%d)", frameSize, eightBitSize)

	// Build a 10-bit frame with sentinel bytes in the last quarter (Cr plane tail).
	yuv := make([]byte, frameSize)
	sentinel := byte(0xAB)
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		yuv[i] = sentinel
	}

	engine := NewEngine(EngineConfig{
		Output:     func([]byte, int, int, int64, bool) {},
		HintWidth:  w,
		HintHeight: h,
	})
	engine.SetColorFormat(video.YUV422_10bit)

	err := engine.Start("src-a", "src-b", Mix, 1000)
	require.NoError(t, err)
	defer engine.Abort()

	engine.IngestRawFrame("src-a", yuv, w, h, 90000)

	engine.mu.RLock()
	storedA := engine.latestYUVA
	engine.mu.RUnlock()

	// Guard 1: buffer must be full 10-bit size.
	require.Equal(t, frameSize, len(storedA),
		"latestYUVA truncated: got %d, want %d (was 10-bit frame truncated to 8-bit size %d?)",
		len(storedA), frameSize, eightBitSize)

	// Guard 2: sentinel data in last quarter must be intact.
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		require.Equal(t, sentinel, storedA[i],
			"10-bit data lost at byte %d: got 0x%02X, want 0x%02X (buffer likely truncated to 8-bit size)",
			i, storedA[i], sentinel)
	}
}

// TestEngineFormatGuard_IngestRawFrame_10bit_SourceB verifies the same sentinel
// guard for the B source (latestYUVB).
func TestEngineFormatGuard_IngestRawFrame_10bit_SourceB(t *testing.T) {
	w, h := 16, 8
	frameSize := video.YUV422_10bit.FrameSize(w, h)

	yuv := make([]byte, frameSize)
	sentinel := byte(0xCD)
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		yuv[i] = sentinel
	}

	engine := NewEngine(EngineConfig{
		Output:     func([]byte, int, int, int64, bool) {},
		HintWidth:  w,
		HintHeight: h,
	})
	engine.SetColorFormat(video.YUV422_10bit)

	err := engine.Start("src-a", "src-b", Mix, 1000)
	require.NoError(t, err)
	defer engine.Abort()

	engine.IngestRawFrame("src-b", yuv, w, h, 90000)

	engine.mu.RLock()
	storedB := engine.latestYUVB
	engine.mu.RUnlock()

	require.Equal(t, frameSize, len(storedB),
		"latestYUVB truncated: got %d, want %d", len(storedB), frameSize)

	for i := frameSize * 3 / 4; i < frameSize; i++ {
		require.Equal(t, sentinel, storedB[i],
			"10-bit data lost at byte %d in source B", i)
	}
}

// TestEngineFormatGuard_IngestRawFrame_10bit_ScaleUsesCorrectSize verifies that
// when a mismatched-resolution 10-bit frame arrives, the engine scales it to the
// correct 10-bit size (not 8-bit). A 16x8 frame ingested into a 32x16 engine
// should produce a 32*16*4=2048 byte buffer, not 32*16*3/2=768.
func TestEngineFormatGuard_IngestRawFrame_10bit_ScaleUsesCorrectSize(t *testing.T) {
	engineW, engineH := 32, 16
	srcW, srcH := 16, 8

	expectedSize := video.YUV422_10bit.FrameSize(engineW, engineH) // 32*16*4 = 2048
	wrongSize := video.YUV420FrameSize(engineW, engineH)           // 32*16*3/2 = 768

	srcFrame := make([]byte, video.YUV422_10bit.FrameSize(srcW, srcH))
	// Fill Y plane with a distinct 10-bit value (500 > 255, proves 10-bit path).
	yPlaneSize := srcW * srcH * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(srcFrame[i:], 500)
	}
	// Neutral chroma.
	for i := yPlaneSize; i < len(srcFrame); i += 2 {
		binary.LittleEndian.PutUint16(srcFrame[i:], 512)
	}

	engine := NewEngine(EngineConfig{
		Output:     func([]byte, int, int, int64, bool) {},
		HintWidth:  engineW,
		HintHeight: engineH,
	})
	engine.SetColorFormat(video.YUV422_10bit)

	err := engine.Start("src-a", "src-b", Mix, 1000)
	require.NoError(t, err)
	defer engine.Abort()

	engine.IngestRawFrame("src-a", srcFrame, srcW, srcH, 90000)

	engine.mu.RLock()
	storedA := engine.latestYUVA
	engine.mu.RUnlock()

	require.Equal(t, expectedSize, len(storedA),
		"scaled 10-bit frame has wrong size: got %d, want %d (8-bit would be %d)",
		len(storedA), expectedSize, wrongSize)
}

// TestEngineFormatGuard_BlendOutput_10bit_FullSizeFrame verifies that blended
// output from a 10-bit transition is the full 10-bit size (w*h*4), not 8-bit
// (w*h*3/2). Also checks that blended Y values are in the 10-bit range.
func TestEngineFormatGuard_BlendOutput_10bit_FullSizeFrame(t *testing.T) {
	w, h := 16, 16
	expectedSize := video.YUV422_10bit.FrameSize(w, h) // 16*16*4 = 1024
	wrongSize := video.YUV420FrameSize(w, h)           // 16*16*3/2 = 384

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
	engine.SetColorFormat(video.YUV422_10bit)

	err := engine.Start("src-a", "src-b", Mix, 1000)
	require.NoError(t, err)
	defer engine.Abort()

	// Source A: Y=200, Source B: Y=800 (both > 255, purely 10-bit values).
	frameA := makeEngine10bitFrame(w, h, 200, 512, 512)
	frameB := makeEngine10bitFrame(w, h, 800, 512, 512)

	engine.IngestRawFrame("src-a", frameA, w, h, 90000)
	engine.IngestRawFrame("src-b", frameB, w, h, 90000)

	mu.Lock()
	defer mu.Unlock()

	require.NotNil(t, outputYUV, "10-bit blend should produce output")

	// Guard: output must be full 10-bit size.
	assert.Equal(t, expectedSize, len(outputYUV),
		"blend output truncated: got %d, want %d (8-bit would be %d)",
		len(outputYUV), expectedSize, wrongSize)

	// Verify blended Y is plausible 10-bit value (between A=200 and B=800).
	firstY := binary.LittleEndian.Uint16(outputYUV[:2])
	assert.Greater(t, firstY, uint16(100),
		"blended Y should be in 10-bit range, got %d", firstY)
	assert.Less(t, firstY, uint16(900),
		"blended Y should be in 10-bit range, got %d", firstY)
}

// TestEngineFormatGuard_FTB_10bit_OutputSize verifies FTB produces full 10-bit output.
func TestEngineFormatGuard_FTB_10bit_OutputSize(t *testing.T) {
	w, h := 16, 16
	expectedSize := video.YUV422_10bit.FrameSize(w, h)

	var mu sync.Mutex
	var outputYUV []byte

	engine := NewEngine(EngineConfig{
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			defer mu.Unlock()
			if outputYUV == nil {
				outputYUV = make([]byte, len(yuv))
				copy(outputYUV, yuv)
			}
		},
		HintWidth:  w,
		HintHeight: h,
	})
	engine.SetColorFormat(video.YUV422_10bit)

	err := engine.Start("src-a", "", FTB, 1000)
	require.NoError(t, err)
	defer engine.Abort()

	frame := makeEngine10bitFrame(w, h, 700, 512, 512)
	engine.IngestRawFrame("src-a", frame, w, h, 90000)

	mu.Lock()
	defer mu.Unlock()

	require.NotNil(t, outputYUV, "FTB should produce output")
	assert.Equal(t, expectedSize, len(outputYUV),
		"FTB output should be 10-bit size (%d), got %d", expectedSize, len(outputYUV))
}

// TestEngineFormatGuard_BlackFrame_10bit_CorrectSize verifies the black frame
// fallback is correctly sized for 10-bit (w*h*4, not w*h*3/2).
func TestEngineFormatGuard_BlackFrame_10bit_CorrectSize(t *testing.T) {
	w, h := 32, 16
	expectedSize := video.YUV422_10bit.FrameSize(w, h) // 32*16*4 = 2048
	wrongSize := video.YUV420FrameSize(w, h)           // 32*16*3/2 = 768

	engine := NewEngine(EngineConfig{
		Output:     func([]byte, int, int, int64, bool) {},
		HintWidth:  w,
		HintHeight: h,
	})
	engine.SetColorFormat(video.YUV422_10bit)

	err := engine.Start("src-a", "", FTB, 1000)
	require.NoError(t, err)
	defer engine.Abort()

	engine.mu.Lock()
	black := engine.getBlackFrame()
	engine.mu.Unlock()

	require.Equal(t, expectedSize, len(black),
		"10-bit black frame has wrong size: got %d, want %d (8-bit would be %d)",
		len(black), expectedSize, wrongSize)

	// Verify black Y=64 (10-bit BT.709), not Y=16 (8-bit).
	yVal := binary.LittleEndian.Uint16(black[:2])
	assert.Equal(t, uint16(64), yVal,
		"10-bit black Y should be 64, got %d (8-bit would be 16)", yVal)

	// Verify chroma=512 (10-bit neutral), not 128 (8-bit).
	yPlaneEnd := w * h * 2
	cbVal := binary.LittleEndian.Uint16(black[yPlaneEnd : yPlaneEnd+2])
	assert.Equal(t, uint16(512), cbVal,
		"10-bit neutral chroma should be 512, got %d (8-bit would be 128)", cbVal)
}

// TestEngineFormatGuard_VideoFormatFrameSize_Consistency verifies that
// video.Format.FrameSize() returns the correct values and catches any
// regression to hardcoded w*h*3/2.
func TestEngineFormatGuard_VideoFormatFrameSize_Consistency(t *testing.T) {
	tests := []struct {
		name   string
		format video.Format
		w, h   int
		want   int
	}{
		{"8bit_16x8", video.YUV420_8bit, 16, 8, 16 * 8 * 3 / 2},
		{"10bit_16x8", video.YUV422_10bit, 16, 8, 16 * 8 * 4},
		{"8bit_1920x1080", video.YUV420_8bit, 1920, 1080, 1920 * 1080 * 3 / 2},
		{"10bit_1920x1080", video.YUV422_10bit, 1920, 1080, 1920 * 1080 * 4},
		{"8bit_320x240", video.YUV420_8bit, 320, 240, 320 * 240 * 3 / 2},
		{"10bit_320x240", video.YUV422_10bit, 320, 240, 320 * 240 * 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.format.FrameSize(tc.w, tc.h)
			require.Equal(t, tc.want, got,
				"%s: FrameSize(%d,%d) = %d, want %d", tc.name, tc.w, tc.h, got, tc.want)
		})
	}

	// Also verify the ratio: 10-bit must always be larger than 8-bit
	// (specifically 8/3 times larger for YUV422P10 vs YUV420).
	for _, res := range [][2]int{{16, 8}, {320, 240}, {1920, 1080}} {
		w, h := res[0], res[1]
		size8 := video.YUV420_8bit.FrameSize(w, h)
		size10 := video.YUV422_10bit.FrameSize(w, h)
		require.Greater(t, size10, size8,
			"10-bit (%d) must be larger than 8-bit (%d) at %dx%d", size10, size8, w, h)
	}
}
