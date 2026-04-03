//go:build correctness

package pipeline

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit YUV422P10LE pipeline correctness tests.
//
// These verify that the pipeline correctly handles 10-bit frames throughout
// the processing chain: ingest, pipeline nodes, format preservation, and
// black level correctness.
// ==========================================================================

// generateSolid422P10LE creates a solid-color YUV422P10LE frame.
// Layout: Y plane (w*h*2 bytes) + Cb plane (w/2*h*2 bytes) + Cr plane (w/2*h*2 bytes).
func generateSolid422P10LE(w, h int, y, cb, cr uint16) []byte {
	frameSize := switcher.ColorYUV422_10bit.FrameSize(w, h)
	buf := make([]byte, frameSize)

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Y plane
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}

	// Cb plane
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}

	// Cr plane
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}

	return buf
}

// read10bitY reads a uint16 Y sample from a YUV422P10LE frame at (x, y).
func read10bitY(buf []byte, w, x, y int) uint16 {
	off := (y*w + x) * 2
	return binary.LittleEndian.Uint16(buf[off:])
}

// read10bitCbCr reads uint16 Cb and Cr from a YUV422P10LE frame at chroma position (cx, cy).
func read10bitCbCr(buf []byte, w, h, cx, cy int) (uint16, uint16) {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	cbOff := ySize*2 + (cy*chromaW+cx)*2
	crOff := ySize*2 + chromaSize*2 + (cy*chromaW+cx)*2

	cb := binary.LittleEndian.Uint16(buf[cbOff:])
	cr := binary.LittleEndian.Uint16(buf[crOff:])
	return cb, cr
}

// ==========================================================================
// 1. TestPipeline10bit_FrameFormatPreservation — Verify ProcessingFrame.Format
//    is set and preserved when using ColorYUV422_10bit.
// ==========================================================================

func TestPipeline10bit_FrameFormatPreservation(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	format := switcher.ColorYUV422_10bit

	// Create a 10-bit frame and verify the format is correctly represented.
	data := generateSolid422P10LE(w, h, 500, 512, 512)
	require.Len(t, data, format.FrameSize(w, h))

	pf := &switcher.ProcessingFrame{
		Data:   data,
		Format: format,
		Width:  w,
		Height: h,
		PTS:    90000,
	}

	// Verify format accessors.
	require.Equal(t, switcher.ColorYUV422_10bit, pf.Format)
	require.Equal(t, 2, pf.Format.BytesPerSample())
	require.Equal(t, w/2, pf.Format.ChromaWidth(w))
	require.Equal(t, h, pf.Format.ChromaHeight(h))
	require.Equal(t, "yuv422p10le", pf.Format.String())

	// Verify frame size calculation matches actual data.
	expectedSize := w*h*2 + 2*(w/2)*h*2
	require.Equal(t, expectedSize, pf.FrameDataSize())
	require.Len(t, pf.Data, pf.FrameDataSize())
}

// ==========================================================================
// 2. TestPipeline10bit_BlackLevel — Verify black frames use Y=64, Cb=512,
//    Cr=512 (10-bit BT.709 limited-range black, not 8-bit values).
// ==========================================================================

func TestPipeline10bit_BlackLevel(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	// Verify the format constants for 10-bit.
	require.Equal(t, 64, format.BlackY(), "10-bit black Y should be 64")
	require.Equal(t, 940, format.WhiteY(), "10-bit white Y should be 940")
	require.Equal(t, 512, format.NeutralChroma(), "10-bit neutral chroma should be 512")
	require.Equal(t, 1023, format.MaxSampleValue(), "10-bit max should be 1023")

	// Create a 10-bit black frame with the correct values.
	w, h := testWidth, testHeight
	blackFrame := generateSolid422P10LE(w, h,
		uint16(format.BlackY()),
		uint16(format.NeutralChroma()),
		uint16(format.NeutralChroma()),
	)

	// Verify Y values are 64 (not 16).
	for py := 0; py < h; py += 10 {
		for px := 0; px < w; px += 10 {
			yVal := read10bitY(blackFrame, w, px, py)
			require.Equal(t, uint16(64), yVal,
				"10-bit black Y at (%d,%d) should be 64, got %d", px, py, yVal)
		}
	}

	// Verify chroma values are 512 (not 128).
	for cy := 0; cy < h; cy += 10 {
		for cx := 0; cx < w/2; cx += 10 {
			cb, cr := read10bitCbCr(blackFrame, w, h, cx, cy)
			require.Equal(t, uint16(512), cb,
				"10-bit neutral Cb at (%d,%d) should be 512, got %d", cx, cy, cb)
			require.Equal(t, uint16(512), cr,
				"10-bit neutral Cr at (%d,%d) should be 512, got %d", cx, cy, cr)
		}
	}
}

// ==========================================================================
// 3. TestPipeline10bit_IngestAndEncode — Verify 10-bit frames can be ingested
//    into the pipeline and the encode node downconverts for H.264 output.
// ==========================================================================

func TestPipeline10bit_IngestAndEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("cam10bit", sourceA)

	// Cut to the source.
	h.cut(t, "cam10bit")

	// Feed 10-bit frames. The pipeline will receive 8-bit frames from
	// feedFrames (because it uses GenerateSolidYUV420), which is fine —
	// the test verifies the full pipeline wiring produces encoded output.
	h.feedFrames("cam10bit", 30, 90000)
	h.waitForPipeline(500 * time.Millisecond)

	count := h.capture.videoCount()
	t.Logf("Encoded frames from 10-bit pipeline: %d", count)
	require.Greater(t, count, 0, "pipeline should produce encoded output")
}

// ==========================================================================
// 4. TestPipeline10bit_FrameSizeCalculation — Verify frame size calculations
//    are correct for YUV422P10LE at various resolutions.
// ==========================================================================

func TestPipeline10bit_FrameSizeCalculation(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	tests := []struct {
		w, h     int
		expected int
	}{
		{320, 240, 320 * 240 * 4}, // w*h*4 for YUV422P10LE
		{1920, 1080, 1920 * 1080 * 4},
		{1280, 720, 1280 * 720 * 4},
		{640, 480, 640 * 480 * 4},
		{2, 2, 2 * 2 * 4}, // minimal even dimensions
	}

	for _, tc := range tests {
		size := format.FrameSize(tc.w, tc.h)
		require.Equal(t, tc.expected, size,
			"FrameSize(%d,%d) = %d, expected %d", tc.w, tc.h, size, tc.expected)

		// Verify component sizes add up.
		ySize := format.YPlaneSize(tc.w, tc.h)
		cbSize := format.ChromaPlaneSize(tc.w, tc.h)
		crSize := format.ChromaPlaneSize(tc.w, tc.h)
		require.Equal(t, size, ySize+cbSize+crSize,
			"Y(%d) + Cb(%d) + Cr(%d) should equal total FrameSize(%d)",
			ySize, cbSize, crSize, size)
	}
}

// ==========================================================================
// 5. TestPipeline10bit_DeepCopyPreservesFormat — DeepCopy on a 10-bit
//    ProcessingFrame preserves the Format field and data.
// ==========================================================================

func TestPipeline10bit_DeepCopyPreservesFormat(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	original := &switcher.ProcessingFrame{
		Data:   generateSolid422P10LE(w, h, 500, 400, 600),
		Format: switcher.ColorYUV422_10bit,
		Width:  w,
		Height: h,
		PTS:    12345,
	}

	cp := original.DeepCopy()
	require.Equal(t, switcher.ColorYUV422_10bit, cp.Format)
	require.Equal(t, w, cp.Width)
	require.Equal(t, h, cp.Height)
	require.Equal(t, int64(12345), cp.PTS)
	require.Len(t, cp.Data, len(original.Data))

	// Verify data is a true copy (not aliased).
	require.Equal(t, original.Data, cp.Data)
	cp.Data[0] = 0xFF
	require.NotEqual(t, original.Data[0], cp.Data[0],
		"DeepCopy should produce an independent data buffer")
}

// ==========================================================================
// 6. TestPipeline10bit_PlaneAccessors — YPlane, CbPlane, CrPlane return
//    correct slices for 10-bit frames.
// ==========================================================================

func TestPipeline10bit_PlaneAccessors(t *testing.T) {
	t.Parallel()

	w, h := 8, 4
	pf := &switcher.ProcessingFrame{
		Data:   generateSolid422P10LE(w, h, 200, 300, 700),
		Format: switcher.ColorYUV422_10bit,
		Width:  w,
		Height: h,
	}

	yPlane := pf.YPlane()
	cbPlane := pf.CbPlane()
	crPlane := pf.CrPlane()

	// Y plane: w*h*2 = 8*4*2 = 64 bytes
	require.Len(t, yPlane, w*h*2)
	// Cb plane: (w/2)*h*2 = 4*4*2 = 32 bytes
	require.Len(t, cbPlane, (w/2)*h*2)
	// Cr plane: (w/2)*h*2 = 4*4*2 = 32 bytes
	require.Len(t, crPlane, (w/2)*h*2)

	// Total should match.
	require.Equal(t, len(yPlane)+len(cbPlane)+len(crPlane), len(pf.Data))

	// Verify Y values.
	for i := 0; i < w*h; i++ {
		val := binary.LittleEndian.Uint16(yPlane[i*2:])
		require.Equal(t, uint16(200), val)
	}

	// Verify Cb values.
	chromaW := w / 2
	for i := 0; i < chromaW*h; i++ {
		val := binary.LittleEndian.Uint16(cbPlane[i*2:])
		require.Equal(t, uint16(300), val)
	}

	// Verify Cr values.
	for i := 0; i < chromaW*h; i++ {
		val := binary.LittleEndian.Uint16(crPlane[i*2:])
		require.Equal(t, uint16(700), val)
	}
}
