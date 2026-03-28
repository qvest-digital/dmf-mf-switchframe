//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFillRectP210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Fill entire frame with white (Y=940, Cb=512, Cr=512) first
	white := makeP210TestFrame(w, h, 940, 512, 512)
	require.NoError(t, Upload10(ctx, frame, white, w, h))

	// Fill a rectangle in the center with black (Y=64, Cb=512, Cr=512)
	rect := Rect{X: 16, Y: 8, W: 32, H: 16}
	err = FillRectP210(ctx, frame, rect, 64, 512, 512)
	require.NoError(t, err)

	// Download and verify
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, frame, result, w, h))

	// Inside rect: Y should be 64 (black)
	// Center of rect: row 16, col 32
	insideRow := 16
	insideCol := 32
	insideIdx := (insideRow*w + insideCol) * 2
	insideY := binary.LittleEndian.Uint16(result[insideIdx:])
	assert.Equal(t, uint16(64), insideY, "inside rect Y should be 64 (black), got %d", insideY)

	// Outside rect: Y should be 940 (white)
	outsideRow := 2
	outsideCol := 4
	outsideIdx := (outsideRow*w + outsideCol) * 2
	outsideY := binary.LittleEndian.Uint16(result[outsideIdx:])
	assert.Equal(t, uint16(940), outsideY, "outside rect Y should be 940 (white), got %d", outsideY)

	t.Logf("FillRectP210: inside Y=%d (want 64), outside Y=%d (want 940)", insideY, outsideY)
}

func TestDrawBorderP210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Fill with mid-gray (Y=502, Cb=512, Cr=512)
	gray := makeP210TestFrame(w, h, 502, 512, 512)
	require.NoError(t, Upload10(ctx, frame, gray, w, h))

	// Draw a white border (Y=940) with thickness=4 around a centered rect.
	thickness := 4
	rect := Rect{X: 16, Y: 8, W: 32, H: 16}
	err = DrawBorderP210(ctx, frame, rect, 940, 512, 512, thickness)
	require.NoError(t, err)

	// Download and verify
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, frame, result, w, h))

	// A pixel just above the rect top edge should be white (Y=940).
	// Border at row = rectY-1, col = rectX + rectW/2
	borderRow := rect.Y - 1
	borderCol := rect.X + rect.W/2
	borderIdx := (borderRow*w + borderCol) * 2
	borderY := binary.LittleEndian.Uint16(result[borderIdx:])
	assert.Equal(t, uint16(940), borderY, "border pixel at (%d,%d) should be Y=940, got %d",
		borderRow, borderCol, borderY)

	// Interior pixel should be unchanged (gray Y=502).
	interiorRow := rect.Y + rect.H/2
	interiorCol := rect.X + rect.W/2
	interiorIdx := (interiorRow*w + interiorCol) * 2
	interiorY := binary.LittleEndian.Uint16(result[interiorIdx:])
	assert.Equal(t, uint16(502), interiorY, "interior pixel at (%d,%d) should be Y=502, got %d",
		interiorRow, interiorCol, interiorY)

	// A pixel well outside both border and rect should be unchanged (gray Y=502).
	outsideRow := 2
	outsideCol := 4
	outsideIdx := (outsideRow*w + outsideCol) * 2
	outsideY := binary.LittleEndian.Uint16(result[outsideIdx:])
	assert.Equal(t, uint16(502), outsideY, "outside pixel at (%d,%d) should be Y=502, got %d",
		outsideRow, outsideCol, outsideY)

	t.Logf("DrawBorderP210: border Y=%d (want 940), interior Y=%d (want 502), outside Y=%d (want 502)",
		borderY, interiorY, outsideY)
}

func TestFillRectP210_UV(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Fill entire frame with neutral (Y=502, Cb=512, Cr=512)
	neutral := makeP210TestFrame(w, h, 502, 512, 512)
	require.NoError(t, Upload10(ctx, frame, neutral, w, h))

	// Fill a rect with a distinctive chroma value (red: Cb=200, Cr=800)
	rect := Rect{X: 16, Y: 8, W: 32, H: 16}
	err = FillRectP210(ctx, frame, rect, 502, 200, 800)
	require.NoError(t, err)

	// Download and verify UV values
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	result := make([]byte, ySize+cbSize+cbSize)
	require.NoError(t, Download10(ctx, frame, result, w, h))

	// Inside rect: Cb should be 200 at chroma position
	// Row 16 (inside rect), chroma col 8 (=luma col 16, the left edge of rect)
	insideCbRow := 16
	insideCbCol := 8 // chroma x = luma x / 2 = 16/2
	insideCbIdx := ySize + (insideCbRow*chromaW+insideCbCol)*2
	insideCb := binary.LittleEndian.Uint16(result[insideCbIdx:])
	assert.Equal(t, uint16(200), insideCb, "inside rect Cb should be 200, got %d", insideCb)

	// Inside rect: Cr should be 800
	insideCrIdx := ySize + cbSize + (insideCbRow*chromaW+insideCbCol)*2
	insideCr := binary.LittleEndian.Uint16(result[insideCrIdx:])
	assert.Equal(t, uint16(800), insideCr, "inside rect Cr should be 800, got %d", insideCr)

	// Outside rect: Cb should be 512 (neutral)
	outsideCbRow := 2
	outsideCbCol := 2
	outsideCbIdx := ySize + (outsideCbRow*chromaW+outsideCbCol)*2
	outsideCb := binary.LittleEndian.Uint16(result[outsideCbIdx:])
	assert.Equal(t, uint16(512), outsideCb, "outside rect Cb should be 512, got %d", outsideCb)

	t.Logf("FillRectP210 UV: inside Cb=%d Cr=%d (want 200/800), outside Cb=%d (want 512)",
		insideCb, insideCr, outsideCb)
}

func TestGPUFillRectDispatch_NV12(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	assert.Equal(t, GPUFormatNV12, frame.Format)

	// Fill white
	white := make([]byte, yuvFrameSize(w, h))
	for i := 0; i < w*h; i++ {
		white[i] = 235
	}
	for i := w * h; i < len(white); i++ {
		white[i] = 128
	}
	require.NoError(t, Upload(ctx, frame, white, w, h))

	// Dispatch FillRect — should route to NV12 variant
	rect := Rect{X: 16, Y: 8, W: 32, H: 16}
	err = GPUFillRectDispatch(ctx, frame, rect, ColorBlack)
	require.NoError(t, err)

	result := make([]byte, yuvFrameSize(w, h))
	require.NoError(t, Download(ctx, result, frame, w, h))

	insideY := result[16*w+32] // center of rect
	assert.Equal(t, byte(16), insideY, "dispatch NV12 inside rect should be black Y=16")

	outsideY := result[2*w+4]
	assert.Equal(t, byte(235), outsideY, "dispatch NV12 outside rect should be white Y=235")
}

func TestGPUFillRectDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format)

	// Fill with white
	white := makeP210TestFrame(w, h, 940, 512, 512)
	require.NoError(t, Upload10(ctx, frame, white, w, h))

	// Dispatch FillRect — should route to P210 variant.
	// ColorBlack has Y=16, which gets promoted to 16<<2=64 for 10-bit.
	rect := Rect{X: 16, Y: 8, W: 32, H: 16}
	err = GPUFillRectDispatch(ctx, frame, rect, ColorBlack)
	require.NoError(t, err)

	// Download and verify
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, frame, result, w, h))

	// Inside: Y should be 64 (16<<2)
	insideIdx := (16*w + 32) * 2
	insideY := binary.LittleEndian.Uint16(result[insideIdx:])
	assert.Equal(t, uint16(64), insideY, "dispatch P210 inside rect Y should be 64 (16<<2), got %d", insideY)

	// Outside: Y should be 940
	outsideIdx := (2*w + 4) * 2
	outsideY := binary.LittleEndian.Uint16(result[outsideIdx:])
	assert.Equal(t, uint16(940), outsideY, "dispatch P210 outside rect Y should be 940, got %d", outsideY)
}

func TestGPUDrawBorderDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Fill with gray (Y=502)
	gray := makeP210TestFrame(w, h, 502, 512, 512)
	require.NoError(t, Upload10(ctx, frame, gray, w, h))

	// Dispatch DrawBorder — Y=235 gets promoted to 235<<2=940
	thickness := 4
	rect := Rect{X: 16, Y: 8, W: 32, H: 16}
	borderColor := YUVColor{Y: 235, Cb: 128, Cr: 128}
	err = GPUDrawBorderDispatch(ctx, frame, rect, borderColor, thickness)
	require.NoError(t, err)

	// Download
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, frame, result, w, h))

	// Border pixel (just above rect): should be 940 (235<<2)
	borderRow := rect.Y - 1
	borderCol := rect.X + rect.W/2
	borderIdx := (borderRow*w + borderCol) * 2
	borderY := binary.LittleEndian.Uint16(result[borderIdx:])
	assert.Equal(t, uint16(940), borderY, "dispatch P210 border Y should be 940 (235<<2), got %d", borderY)

	// Interior: should be unchanged 502
	interiorRow := rect.Y + rect.H/2
	interiorCol := rect.X + rect.W/2
	interiorIdx := (interiorRow*w + interiorCol) * 2
	interiorY := binary.LittleEndian.Uint16(result[interiorIdx:])
	assert.Equal(t, uint16(502), interiorY, "dispatch P210 interior Y should be 502, got %d", interiorY)
}
