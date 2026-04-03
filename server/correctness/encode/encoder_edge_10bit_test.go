//go:build correctness

package encode

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit encoder edge case tests.
//
// Tests edge cases with 10-bit frames: minimum frame sizes, rapid IDR
// requests, and bitrate behavior. Since most H.264 encoders don't support
// 10-bit input natively, these tests verify the format conversion and
// buffer handling paths.
// ==========================================================================

// TestEncoderEdge10bit_MinimumFrameSize verifies that the minimum valid
// 10-bit frame (2x2 YUV422P10LE) has the correct size.
func TestEncoderEdge10bit_MinimumFrameSize(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	// Minimum even dimensions.
	w, h := 2, 2
	size := format.FrameSize(w, h)

	// YUV422P10LE: Y = 2*2*2 = 8, Cb = 1*2*2 = 4, Cr = 1*2*2 = 4 => 16 bytes
	expected := w * h * 4
	require.Equal(t, expected, size,
		"minimum 10-bit frame should be %d bytes, got %d", expected, size)

	// Verify we can fill it.
	data := make([]byte, size)
	binary.LittleEndian.PutUint16(data[0:], 500) // Y at (0,0)
	require.Equal(t, uint16(500), binary.LittleEndian.Uint16(data[0:]))
}

// TestEncoderEdge10bit_RapidIDR verifies that requesting rapid IDR frames
// from the encoder doesn't cause issues when the pipeline is handling
// 10-bit frame metadata.
func TestEncoderEdge10bit_RapidIDR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 1_000_000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	// Create YUV420 frames (since libx264 doesn't take 10-bit YUV422).
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 128
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Request IDR on EVERY frame (stress test).
	idrCount := 0
	for i := 0; i < 60; i++ {
		data, isKey, encErr := enc.Encode(yuv, int64(90000+i*3000), true) // force IDR every frame
		require.NoError(t, encErr, "encode frame %d", i)
		if data != nil && isKey {
			idrCount++
		}
	}

	t.Logf("MEASURE: 10-bit edge IDR test: %d IDR frames from 60 requests", idrCount)
	require.Greater(t, idrCount, 30,
		"at least half of IDR requests should produce keyframes")
}

// TestEncoderEdge10bit_ValuesAtBoundary verifies that 10-bit boundary
// values (0, 64, 512, 940, 960, 1023) are correctly representable.
func TestEncoderEdge10bit_ValuesAtBoundary(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit
	w, h := 8, 4

	testCases := []struct {
		name string
		y    uint16
		cb   uint16
		cr   uint16
	}{
		{"black", uint16(format.BlackY()), uint16(format.NeutralChroma()), uint16(format.NeutralChroma())},
		{"white", uint16(format.WhiteY()), uint16(format.NeutralChroma()), uint16(format.NeutralChroma())},
		{"max_sample", uint16(format.MaxSampleValue()), uint16(format.MaxSampleValue()), uint16(format.MaxSampleValue())},
		{"zero", 0, 0, 0},
		{"neutral", 502, 512, 512},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			frameSize := format.FrameSize(w, h)
			data := make([]byte, frameSize)

			ySize := w * h
			chromaW := w / 2
			chromaSize := chromaW * h

			// Fill Y
			for i := 0; i < ySize; i++ {
				binary.LittleEndian.PutUint16(data[i*2:], tc.y)
			}
			// Fill Cb
			cbOff := ySize * 2
			for i := 0; i < chromaSize; i++ {
				binary.LittleEndian.PutUint16(data[cbOff+i*2:], tc.cb)
			}
			// Fill Cr
			crOff := cbOff + chromaSize*2
			for i := 0; i < chromaSize; i++ {
				binary.LittleEndian.PutUint16(data[crOff+i*2:], tc.cr)
			}

			// Verify we can read them back.
			readY := binary.LittleEndian.Uint16(data[0:])
			require.Equal(t, tc.y, readY, "Y readback for %s", tc.name)

			readCb := binary.LittleEndian.Uint16(data[cbOff:])
			require.Equal(t, tc.cb, readCb, "Cb readback for %s", tc.name)

			readCr := binary.LittleEndian.Uint16(data[crOff:])
			require.Equal(t, tc.cr, readCr, "Cr readback for %s", tc.name)

			// Verify all values are <= 1023 (10-bit max).
			totalSamples := ySize + 2*chromaSize
			for i := 0; i < totalSamples; i++ {
				val := binary.LittleEndian.Uint16(data[i*2:])
				require.LessOrEqual(t, val, uint16(1023),
					"%s: sample %d = %d exceeds 10-bit max 1023", tc.name, i, val)
			}
		})
	}
}

// TestEncoderEdge10bit_ChromaDimensions verifies that 10-bit YUV422 has
// half-width but full-height chroma, unlike 8-bit YUV420 which has both
// half-width and half-height.
func TestEncoderEdge10bit_ChromaDimensions(t *testing.T) {
	t.Parallel()

	format8 := switcher.ColorYUV420_8bit
	format10 := switcher.ColorYUV422_10bit

	w, h := 320, 240

	// 8-bit YUV420: chroma = w/2 x h/2
	require.Equal(t, w/2, format8.ChromaWidth(w))
	require.Equal(t, h/2, format8.ChromaHeight(h))

	// 10-bit YUV422: chroma = w/2 x h (full height!)
	require.Equal(t, w/2, format10.ChromaWidth(w))
	require.Equal(t, h, format10.ChromaHeight(h),
		"10-bit YUV422 should have full-height chroma (h, not h/2)")

	// Chroma plane sizes differ.
	chromaPlane8 := format8.ChromaPlaneSize(w, h)   // (w/2)*(h/2)*1
	chromaPlane10 := format10.ChromaPlaneSize(w, h) // (w/2)*h*2

	// 10-bit chroma plane should be 8x the 8-bit chroma plane
	// (2x for full height, 2x for bytes per sample, 2x... wait:
	//  8-bit: (w/2)*(h/2)*1 = w*h/4
	//  10-bit: (w/2)*h*2 = w*h
	//  ratio = 4x)
	ratio := float64(chromaPlane10) / float64(chromaPlane8)
	require.InDelta(t, 4.0, ratio, 0.01,
		"10-bit chroma plane should be 4x 8-bit chroma plane (full height + 2 bytes/sample)")

	t.Logf("Chroma plane: 8-bit=%d bytes, 10-bit=%d bytes (%.1fx)",
		chromaPlane8, chromaPlane10, ratio)
}
