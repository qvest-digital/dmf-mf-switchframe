//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDSKCompositeFullFrame(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Fill frame with black
	require.NoError(t, FillBlack(ctx, frame))

	// Create a red RGBA overlay (R=255, G=0, B=0, A=255)
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A (fully opaque)
	}

	overlay, err := UploadOverlay(ctx, rgba, w, h)
	require.NoError(t, err)
	defer FreeOverlay(overlay)

	// Composite at full alpha
	err = DSKCompositeFullFrame(ctx, frame, overlay, 1.0)
	require.NoError(t, err)

	// Download and verify — red in BT.709: Y≈81, Cb≈90, Cr≈240
	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, frame, w, h))

	centerY := result[h/2*w+w/2]
	assert.InDelta(t, 81, int(centerY), 5, "red overlay Y should be ~81 (BT.709)")

	// Check Cb (should be ~90 for red)
	cbOffset := w * h
	centerCb := result[cbOffset+h/4*w/2+w/4]
	assert.InDelta(t, 90, int(centerCb), 5, "red overlay Cb should be ~90")

	t.Logf("DSK full-frame red overlay: Y=%d, Cb=%d", centerY, centerCb)
}

func TestDSKCompositeWithGlobalAlpha(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Fill frame with mid-gray (Y=128)
	grayYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		grayYUV[i] = 128
	}
	for i := w * h; i < len(grayYUV); i++ {
		grayYUV[i] = 128
	}
	require.NoError(t, Upload(ctx, frame, grayYUV, w, h))

	// White RGBA overlay
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 255 // A
	}

	overlay, err := UploadOverlay(ctx, rgba, w, h)
	require.NoError(t, err)
	defer FreeOverlay(overlay)

	// Composite at 0% global alpha — should preserve background
	err = DSKCompositeFullFrame(ctx, frame, overlay, 0.0)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, frame, w, h))
	assert.Equal(t, byte(128), result[h/2*w+w/2], "0%% alpha should preserve background")
}

func TestDSKCompositeRect(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 640, 480
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Fill frame with black
	require.NoError(t, FillBlack(ctx, frame))

	// Create small green overlay (100x50)
	overlayW, overlayH := 100, 50
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 0   // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A
	}

	overlay, err := UploadOverlay(ctx, rgba, overlayW, overlayH)
	require.NoError(t, err)
	defer FreeOverlay(overlay)

	// Place in lower-third region
	rect := Rect{X: 50, Y: 380, W: 200, H: 80}
	err = DSKCompositeRect(ctx, frame, overlay, rect, 1.0)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, frame, w, h))

	// Inside rect should have green Y value (~145 for BT.709 green)
	insideY := result[420*w+150] // inside the rect
	assert.Greater(t, insideY, byte(50), "inside DSK rect should show overlay content, got Y=%d", insideY)

	// Outside rect should remain black
	outsideY := result[50*w+50]
	assert.Equal(t, byte(16), outsideY, "outside DSK rect should remain black")

	t.Logf("DSK rect: inside Y=%d, outside Y=%d", insideY, outsideY)
}

func TestDSKNilArgs(t *testing.T) {
	require.ErrorIs(t, DSKCompositeFullFrame(nil, nil, nil, 1.0), ErrGPUNotAvailable)
	require.ErrorIs(t, DSKCompositeRect(nil, nil, nil, Rect{}, 1.0), ErrGPUNotAvailable)
}

func TestUploadFreeOverlay(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	rgba := make([]byte, 100*50*4)
	for i := range rgba {
		rgba[i] = byte(i % 256)
	}

	overlay, err := UploadOverlay(ctx, rgba, 100, 50)
	require.NoError(t, err)
	assert.Equal(t, 100, overlay.Width)
	assert.Equal(t, 50, overlay.Height)
	assert.NotNil(t, overlay.DevPtr)

	FreeOverlay(overlay)
	assert.Nil(t, overlay.DevPtr)

	// Double free should be safe
	FreeOverlay(overlay)
}
