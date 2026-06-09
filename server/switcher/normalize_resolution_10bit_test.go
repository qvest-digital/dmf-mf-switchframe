package switcher

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================================================
// Regression guards: normalizeResolution must use format-aware sizing.
//
// normalizeResolution scales ProcessingFrames to the pipeline resolution.
// The bug: if it uses video.YUV420FrameSize(w,h) or ColorYUV420_8bit.FrameSize()
// instead of colorFmt.FrameSize(), 10-bit frames are truncated from w*h*4
// to w*h*3/2, destroying 62.5% of the data.
// ==========================================================================

// TestNormalizeResolutionGuard_10bit_CorrectBufferSize verifies that normalizeResolution
// produces a correctly sized 10-bit output buffer when scaling. A 16x8 10-bit frame
// scaled to 32x16 should produce 32*16*4=2048 bytes, NOT 32*16*3/2=768.
func TestNormalizeResolutionGuard_10bit_CorrectBufferSize(t *testing.T) {
	dstW, dstH := 32, 16
	srcW, srcH := 16, 8

	fmt10 := PipelineFormat{
		Width:  dstW,
		Height: dstH,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}

	pool := NewFramePool(4, dstW, dstH, ColorYUV422_10bit)
	sw := &Switcher{}
	sw.framePool.Store(pool)
	sw.pipelineFormat.Store(&fmt10)

	// Create a 10-bit source frame at smaller resolution.
	srcSize := ColorYUV422_10bit.FrameSize(srcW, srcH)
	srcData := make([]byte, srcSize)
	// Fill Y plane with 10-bit value 500 (> 255 to prove 10-bit path).
	yPlaneSize := srcW * srcH * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 500)
	}
	for i := yPlaneSize; i < srcSize; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   srcData,
		Format: ColorYUV422_10bit,
		Codec:  "h265",
		Width:  srcW,
		Height: srcH,
		PTS:    90000,
	}
	frame.SetRefs(1)

	result := sw.normalizeResolution(frame)

	expectedSize := ColorYUV422_10bit.FrameSize(dstW, dstH) // 32*16*4 = 2048
	wrongSize := ColorYUV420_8bit.FrameSize(dstW, dstH)     // 32*16*3/2 = 768

	require.Equal(t, expectedSize, len(result.Data),
		"normalizeResolution produced wrong size: got %d, want %d (8-bit truncation would give %d)",
		len(result.Data), expectedSize, wrongSize)

	// Verify the frame dimensions match target.
	assert.Equal(t, dstW, result.Width)
	assert.Equal(t, dstH, result.Height)
}

// TestNormalizeResolutionGuard_10bit_FormatPreserved verifies that the Format field
// on the output frame is preserved after scaling. If normalizeResolution defaults
// to ColorYUV420_8bit, downstream nodes would misinterpret the data.
func TestNormalizeResolutionGuard_10bit_FormatPreserved(t *testing.T) {
	dstW, dstH := 32, 16
	srcW, srcH := 16, 8

	fmt10 := PipelineFormat{
		Width:  dstW,
		Height: dstH,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}

	pool := NewFramePool(4, dstW, dstH, ColorYUV422_10bit)
	sw := &Switcher{}
	sw.framePool.Store(pool)
	sw.pipelineFormat.Store(&fmt10)

	srcSize := ColorYUV422_10bit.FrameSize(srcW, srcH)
	srcData := make([]byte, srcSize)
	for i := 0; i < srcSize; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   srcData,
		Format: ColorYUV422_10bit,
		Codec:  "h265",
		Width:  srcW,
		Height: srcH,
		PTS:    90000,
	}
	frame.SetRefs(1)

	result := sw.normalizeResolution(frame)

	assert.Equal(t, ColorYUV422_10bit, result.Format,
		"Format field must be preserved as 10-bit after scaling (got %v)", result.Format)
	assert.Equal(t, "h265", result.Codec,
		"Codec must be preserved after scaling (got %s)", result.Codec)
}

// TestNormalizeResolutionGuard_10bit_SentinelPreserved verifies that 10-bit Y values
// survive through normalizeResolution. A uniform Y=500 frame should scale to output
// where Y values are still near 500 (in 10-bit range). If truncated to 8-bit sizing,
// the Y data would be garbled because the buffer layout would be misinterpreted.
func TestNormalizeResolutionGuard_10bit_SentinelPreserved(t *testing.T) {
	dstW, dstH := 32, 16
	srcW, srcH := 16, 8

	fmt10 := PipelineFormat{
		Width:  dstW,
		Height: dstH,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}

	pool := NewFramePool(4, dstW, dstH, ColorYUV422_10bit)
	sw := &Switcher{}
	sw.framePool.Store(pool)
	sw.pipelineFormat.Store(&fmt10)

	// Source frame: uniform Y=500 (well above 8-bit max of 255).
	srcSize := ColorYUV422_10bit.FrameSize(srcW, srcH)
	srcData := make([]byte, srcSize)
	yPlaneSize := srcW * srcH * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 500)
	}
	for i := yPlaneSize; i < srcSize; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   srcData,
		Format: ColorYUV422_10bit,
		Codec:  "h265",
		Width:  srcW,
		Height: srcH,
		PTS:    90000,
	}
	frame.SetRefs(1)

	result := sw.normalizeResolution(frame)

	// Check several Y samples in the scaled output — they should all be near 500.
	// Bilinear scaling of a uniform field should preserve the value exactly or very close.
	for y := 0; y < dstH; y += 4 {
		for x := 0; x < dstW; x += 4 {
			off := (y*dstW + x) * 2
			if off+2 > len(result.Data) {
				t.Fatalf("buffer too short at Y(%d,%d): offset %d exceeds len %d",
					x, y, off+2, len(result.Data))
			}
			yVal := binary.LittleEndian.Uint16(result.Data[off:])
			assert.InDelta(t, 500, float64(yVal), 10,
				"scaled 10-bit Y at (%d,%d) should be near 500, got %d "+
					"(if truncated to 8-bit, data would be garbled)", x, y, yVal)
		}
	}
}

// TestNormalizeResolutionGuard_8bit_StillWorks verifies that 8-bit scaling is
// unaffected by the 10-bit format awareness (no regression).
func TestNormalizeResolutionGuard_8bit_StillWorks(t *testing.T) {
	dstW, dstH := 32, 16
	srcW, srcH := 16, 8

	fmt8 := PipelineFormat{
		Width:  dstW,
		Height: dstH,
		FPSNum: 30000,
		FPSDen: 1001,
		Color:  PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC},
	}

	pool := NewFramePool(4, dstW, dstH, ColorYUV420_8bit)
	sw := &Switcher{}
	sw.framePool.Store(pool)
	sw.pipelineFormat.Store(&fmt8)

	srcSize := ColorYUV420_8bit.FrameSize(srcW, srcH)
	srcData := make([]byte, srcSize)
	for i := 0; i < srcW*srcH; i++ {
		srcData[i] = 128
	}
	for i := srcW * srcH; i < srcSize; i++ {
		srcData[i] = 128
	}

	frame := &ProcessingFrame{
		Data:   srcData,
		Format: ColorYUV420_8bit,
		Codec:  "h264",
		Width:  srcW,
		Height: srcH,
		PTS:    90000,
	}
	frame.SetRefs(1)

	result := sw.normalizeResolution(frame)

	expectedSize := ColorYUV420_8bit.FrameSize(dstW, dstH)
	assert.Equal(t, expectedSize, len(result.Data),
		"8-bit normalizeResolution should produce %d bytes, got %d", expectedSize, len(result.Data))
}
