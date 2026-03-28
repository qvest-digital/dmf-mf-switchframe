//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeP210TestFrame creates a YUV422P10LE CPU buffer filled with constant values.
// Layout: Y plane (w*h uint16) + Cb plane (w/2*h uint16) + Cr plane (w/2*h uint16).
func makeP210TestFrame(w, h int, yVal, cbVal, crVal uint16) []byte {
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	buf := make([]byte, ySize+cbSize+crSize)

	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], yVal)
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(buf[ySize+i*2:], cbVal)
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(buf[ySize+cbSize+i*2:], crVal)
	}
	return buf
}

// uploadP210 is a test helper that creates a P210 frame and uploads data.
func uploadP210(t *testing.T, ctx *Context, pool *FramePool, data []byte, w, h int) *GPUFrame {
	t.Helper()
	frame, err := pool.Acquire()
	require.NoError(t, err)
	err = Upload10(ctx, frame, data, w, h)
	require.NoError(t, err)
	return frame
}

// readP210Sample reads a uint16 sample from a downloaded P210 YUV422P10LE buffer at the given Y plane index.
func readP210YSample(buf []byte, index int) uint16 {
	return binary.LittleEndian.Uint16(buf[index*2:])
}

// readP210CbSample reads a Cb sample from a downloaded P210 buffer.
func readP210CbSample(buf []byte, w, h, index int) uint16 {
	ySize := w * h * 2
	return binary.LittleEndian.Uint16(buf[ySize+index*2:])
}

// readP210CrSample reads a Cr sample from a downloaded P210 buffer.
func readP210CrSample(buf []byte, w, h, index int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	cbSize := chromaW * h * 2
	return binary.LittleEndian.Uint16(buf[ySize+cbSize+index*2:])
}

// p210DownloadSize returns the expected YUV422P10LE download buffer size.
func p210DownloadSize(w, h int) int {
	chromaW := w / 2
	return w*h*2 + chromaW*h*2*2
}

// ============================================================================
// Blend P210 tests
// ============================================================================

func TestBlendMixP210_Midpoint(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Frame A: white (Y=940), Frame B: black (Y=64)
	dataA := makeP210TestFrame(w, h, 940, 512, 512)
	dataB := makeP210TestFrame(w, h, 64, 512, 512)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	// Blend at 50%
	err = BlendMixP210(ctx, frameDst, frameA, frameB, 0.5)
	require.NoError(t, err)

	// Download and verify
	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	// (940*128 + 64*128 + 128) >> 8 = 502
	gotY := readP210YSample(dst, 0)
	assert.InDelta(t, 502, int(gotY), 2, "midpoint Y: got %d, want ~502", gotY)

	// Chroma should be unchanged (512 blended with 512 = 512)
	gotCb := readP210CbSample(dst, w, h, 0)
	assert.InDelta(t, 512, int(gotCb), 1, "midpoint Cb: got %d, want 512", gotCb)
}

func TestBlendMixP210_Endpoints(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(w, h, 300, 400, 600)
	dataB := makeP210TestFrame(w, h, 700, 200, 800)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	// pos=0.0 => 100% A
	err = BlendMixP210(ctx, frameDst, frameA, frameB, 0.0)
	require.NoError(t, err)

	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	gotY := readP210YSample(dst, 0)
	assert.InDelta(t, 300, int(gotY), 1, "pos=0 Y: got %d, want 300", gotY)

	// pos=1.0 => 100% B
	err = BlendMixP210(ctx, frameDst, frameA, frameB, 1.0)
	require.NoError(t, err)
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	gotY = readP210YSample(dst, 0)
	assert.InDelta(t, 700, int(gotY), 1, "pos=1 Y: got %d, want 700", gotY)
}

func TestBlendFTBP210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Start with white
	dataA := makeP210TestFrame(w, h, 940, 512, 512)
	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	// Full FTB (position=1.0) => 10-bit black (Y=64, UV=512)
	err = BlendFTBP210(ctx, frameDst, frameA, 1.0)
	require.NoError(t, err)

	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	gotY := readP210YSample(dst, 0)
	assert.InDelta(t, 64, int(gotY), 1, "FTB Y: got %d, want 64", gotY)

	gotCb := readP210CbSample(dst, w, h, 0)
	assert.InDelta(t, 512, int(gotCb), 1, "FTB Cb: got %d, want 512", gotCb)
}

// ============================================================================
// Scale P210 tests
// ============================================================================

func TestScaleBilinearP210_Identity(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create a frame with a known pattern
	data := makeP210TestFrame(w, h, 500, 300, 700)
	src := uploadP210(t, ctx, pool, data, w, h)
	defer src.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Scale to same size (identity)
	err = ScaleBilinearP210(ctx, dst, src)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, dst, result, w, h)
	require.NoError(t, err)

	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 500, int(gotY), 1, "scale identity Y: got %d, want 500", gotY)

	gotCb := readP210CbSample(result, w, h, 0)
	assert.InDelta(t, 300, int(gotCb), 1, "scale identity Cb: got %d, want 300", gotCb)
}

func TestScaleBilinearP210_Downscale(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 128, 64
	dstW, dstH := 64, 32

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewP210FramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	// Uniform frame — should remain uniform after downscale
	data := makeP210TestFrame(srcW, srcH, 600, 400, 500)
	src := uploadP210(t, ctx, srcPool, data, srcW, srcH)
	defer src.Release()
	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = ScaleBilinearP210(ctx, dst, src)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(dstW, dstH))
	err = Download10(ctx, dst, result, dstW, dstH)
	require.NoError(t, err)

	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 600, int(gotY), 2, "downscale Y: got %d, want 600", gotY)
}

// ============================================================================
// PIP Composite P210 tests
// ============================================================================

func TestPIPCompositeP210_FullOverlay(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Background: dark gray
	dataBg := makeP210TestFrame(w, h, 200, 512, 512)
	bg := uploadP210(t, ctx, pool, dataBg, w, h)
	defer bg.Release()

	// Overlay: white, covering full frame
	dataOv := makeP210TestFrame(w, h, 940, 512, 512)
	ov := uploadP210(t, ctx, pool, dataOv, w, h)
	defer ov.Release()

	// Composite overlay at full size, full alpha
	rect := Rect{X: 0, Y: 0, W: w, H: h}
	err = PIPCompositeP210(ctx, bg, ov, rect, 1.0)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, bg, result, w, h)
	require.NoError(t, err)

	// Background should now be the overlay value
	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 940, int(gotY), 2, "full overlay Y: got %d, want 940", gotY)
}

func TestPIPCompositeP210_50PercentAlpha(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataBg := makeP210TestFrame(w, h, 200, 512, 512)
	bg := uploadP210(t, ctx, pool, dataBg, w, h)
	defer bg.Release()

	dataOv := makeP210TestFrame(w, h, 800, 512, 512)
	ov := uploadP210(t, ctx, pool, dataOv, w, h)
	defer ov.Release()

	rect := Rect{X: 0, Y: 0, W: w, H: h}
	err = PIPCompositeP210(ctx, bg, ov, rect, 0.5)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, bg, result, w, h)
	require.NoError(t, err)

	// 50% of 200 + 50% of 800 = 500
	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 500, int(gotY), 3, "50%% alpha Y: got %d, want ~500", gotY)
}

// ============================================================================
// V210 <-> P210 tests
// ============================================================================

func TestV210ToP210_Lossless(t *testing.T) {
	ctx := testContext(t)

	// Width must be divisible by 6 for V210
	w, h := 48, 16
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Create a known V210 buffer: 10-bit values packed into 32-bit words.
	// V210 format: 6 pixels per 4 uint32 group.
	// Word 0: Cb0[9:0] | Y0[9:0] | Cr0[9:0] | xx
	// Word 1: Y1[9:0] | Cb2[9:0] | Y2[9:0] | xx
	// Word 2: Cr2[9:0] | Y3[9:0] | Cb4[9:0] | xx
	// Word 3: Y4[9:0] | Cr4[9:0] | Y5[9:0] | xx
	v210Stride := V210LineStride(w)
	v210Buf := make([]byte, v210Stride*h)

	// Fill with known values: Y=500, Cb=300, Cr=700
	groupsPerRow := w / 6
	for y := 0; y < h; y++ {
		for g := 0; g < groupsPerRow; g++ {
			off := y*v210Stride + g*16
			yVal := uint32(500)
			cbVal := uint32(300)
			crVal := uint32(700)

			w0 := (cbVal & 0x3FF) | ((yVal & 0x3FF) << 10) | ((crVal & 0x3FF) << 20)
			w1 := (yVal & 0x3FF) | ((cbVal & 0x3FF) << 10) | ((yVal & 0x3FF) << 20)
			w2 := (crVal & 0x3FF) | ((yVal & 0x3FF) << 10) | ((cbVal & 0x3FF) << 20)
			w3 := (yVal & 0x3FF) | ((crVal & 0x3FF) << 10) | ((yVal & 0x3FF) << 20)

			binary.LittleEndian.PutUint32(v210Buf[off:], w0)
			binary.LittleEndian.PutUint32(v210Buf[off+4:], w1)
			binary.LittleEndian.PutUint32(v210Buf[off+8:], w2)
			binary.LittleEndian.PutUint32(v210Buf[off+12:], w3)
		}
	}

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// V210 -> P210
	err = UploadV210ToP210(ctx, frame, v210Buf, w, h)
	require.NoError(t, err)

	// Download as YUV422P10LE and check values
	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	require.NoError(t, err)

	// Check Y samples — should be 500 (no bit shift, lossless 10-bit)
	gotY := readP210YSample(result, 0)
	assert.Equal(t, uint16(500), gotY, "V210->P210 Y should be lossless 500")

	// Check Cb samples
	gotCb := readP210CbSample(result, w, h, 0)
	assert.Equal(t, uint16(300), gotCb, "V210->P210 Cb should be lossless 300")

	// Check Cr samples
	gotCr := readP210CrSample(result, w, h, 0)
	assert.Equal(t, uint16(700), gotCr, "V210->P210 Cr should be lossless 700")
}

func TestP210ToV210_Lossless(t *testing.T) {
	ctx := testContext(t)

	w, h := 48, 16
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Upload known P210 data
	data := makeP210TestFrame(w, h, 500, 300, 700)
	frame := uploadP210(t, ctx, pool, data, w, h)
	defer frame.Release()

	// Download as V210
	v210Stride := V210LineStride(w)
	v210Buf := make([]byte, v210Stride*h)
	err = DownloadP210ToV210(ctx, v210Buf, frame, w, h)
	require.NoError(t, err)

	// Parse first V210 group and verify values
	w0 := binary.LittleEndian.Uint32(v210Buf[0:4])
	y0 := (w0 >> 10) & 0x3FF
	cb0 := w0 & 0x3FF
	cr0 := (w0 >> 20) & 0x3FF

	assert.Equal(t, uint32(500), y0, "P210->V210 Y0 should be 500")
	assert.Equal(t, uint32(300), cb0, "P210->V210 Cb0 should be 300")
	assert.Equal(t, uint32(700), cr0, "P210->V210 Cr0 should be 700")
}

// ============================================================================
// DSK overlay P210 tests
// ============================================================================

func TestDSKOverlayP210_OpaqueWhite(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Background: 10-bit black
	dataBg := makeP210TestFrame(w, h, 64, 512, 512)
	frame := uploadP210(t, ctx, pool, dataBg, w, h)
	defer frame.Release()

	// Create opaque white RGBA overlay (255, 255, 255, 255)
	rgbaPitch := w * 4
	rgba := make([]byte, rgbaPitch*h)
	for i := 0; i < w*h; i++ {
		rgba[i*4] = 255   // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 255 // A
	}

	err = DSKOverlayP210(ctx, frame, rgba, w, h, rgbaPitch, 256)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	require.NoError(t, err)

	// White in BT.709 10-bit limited range: Y=940
	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 940, int(gotY), 4, "DSK white Y: got %d, want ~940", gotY)
}

func TestDSKOverlayP210_Transparent(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Background: mid gray
	dataBg := makeP210TestFrame(w, h, 500, 512, 512)
	frame := uploadP210(t, ctx, pool, dataBg, w, h)
	defer frame.Release()

	// Fully transparent overlay — should leave background unchanged
	rgbaPitch := w * 4
	rgba := make([]byte, rgbaPitch*h) // all zeros including alpha

	err = DSKOverlayP210(ctx, frame, rgba, w, h, rgbaPitch, 256)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	require.NoError(t, err)

	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 500, int(gotY), 1, "transparent DSK Y: got %d, want 500", gotY)
}

// ============================================================================
// STMap warp P210 tests
// ============================================================================

func TestSTMapWarpP210_Identity(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create source with known value
	data := makeP210TestFrame(w, h, 600, 400, 700)
	src := uploadP210(t, ctx, pool, data, w, h)
	defer src.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Identity ST map: S(x,y) = x/(w-1), T(x,y) = y/(h-1)
	stmapS := make([]float32, w*h)
	stmapT := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			stmapS[y*w+x] = float32(x) / float32(w-1)
			stmapT[y*w+x] = float32(y) / float32(h-1)
		}
	}

	err = STMapWarpP210(ctx, dst, src, stmapS, stmapT, w, h)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, dst, result, w, h)
	require.NoError(t, err)

	// Identity map should preserve values
	gotY := readP210YSample(result, w/2+h/2*w) // center pixel
	assert.InDelta(t, 600, int(gotY), 2, "identity stmap Y: got %d, want 600", gotY)
}

// ============================================================================
// Nil/error guard tests
// ============================================================================

func TestBlendMixP210_NilContext(t *testing.T) {
	err := BlendMixP210(nil, &GPUFrame{}, &GPUFrame{}, &GPUFrame{}, 0.5)
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestBlendFTBP210_NilContext(t *testing.T) {
	err := BlendFTBP210(nil, &GPUFrame{}, &GPUFrame{}, 1.0)
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestScaleBilinearP210_NilContext(t *testing.T) {
	err := ScaleBilinearP210(nil, &GPUFrame{}, &GPUFrame{})
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestPIPCompositeP210_NilContext(t *testing.T) {
	err := PIPCompositeP210(nil, &GPUFrame{}, &GPUFrame{}, Rect{}, 1.0)
	assert.Equal(t, ErrGPUNotAvailable, err)
}
