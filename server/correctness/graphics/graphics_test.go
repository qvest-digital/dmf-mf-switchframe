//go:build correctness

package graphics

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
)

const (
	testWidth  = 320
	testHeight = 240
)

// setupCompositor creates a Compositor with a known resolution provider and pipeline FPS.
func setupCompositor(w, h int) *graphics.Compositor {
	c := graphics.NewCompositor()
	c.SetResolutionProvider(func() (int, int) { return w, h })
	c.SetPipelineFPS(30, 1)
	return c
}

// makeRGBA creates an RGBA buffer where every pixel has the given R,G,B,A values.
func makeRGBA(w, h int, r, g, b, a byte) []byte {
	buf := make([]byte, w*h*4)
	for i := 0; i < len(buf); i += 4 {
		buf[i] = r
		buf[i+1] = g
		buf[i+2] = b
		buf[i+3] = a
	}
	return buf
}

// copyFrame returns a deep copy of a byte slice.
func copyFrame(src []byte) []byte {
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// assertYUVInBroadcastRange performs a fast scan of Y, Cb, Cr planes and
// reports the first out-of-range pixel found. Much faster than per-pixel
// require calls because it avoids testify overhead on every sample.
func assertYUVInBroadcastRange(t *testing.T, yuv []byte, w, h int) {
	t.Helper()
	ySize := w * h
	chromaSize := (w / 2) * (h / 2)
	cbStart := ySize
	crStart := cbStart + chromaSize

	// Check Y plane: [16, 235]
	for i := 0; i < ySize; i++ {
		if yuv[i] < 16 || yuv[i] > 235 {
			t.Fatalf("Y[%d]=%d out of legal range [16,235] at pixel (%d,%d)",
				i, yuv[i], i%w, i/w)
		}
	}
	// Check Cb plane: [16, 240]
	for i := 0; i < chromaSize; i++ {
		if yuv[cbStart+i] < 16 || yuv[cbStart+i] > 240 {
			t.Fatalf("Cb[%d]=%d out of legal range [16,240]", i, yuv[cbStart+i])
		}
	}
	// Check Cr plane: [16, 240]
	for i := 0; i < chromaSize; i++ {
		if yuv[crStart+i] < 16 || yuv[crStart+i] > 240 {
			t.Fatalf("Cr[%d]=%d out of legal range [16,240]", i, yuv[crStart+i])
		}
	}
}

// ==========================================================================
// 1. TestDSKPassthroughAlpha0 — overlay with alpha=0 should not modify the
//    program frame at all (byte-exact passthrough).
// ==========================================================================

func TestDSKPassthroughAlpha0(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Overlay: all pixels alpha=0 (fully transparent).
	rgba := makeRGBA(testWidth, testHeight, 0, 0, 0, 0)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "transparent"))
	require.NoError(t, c.On(id))

	// Create a non-trivial program frame (mid-gray).
	program := testutil.GenerateSolidYUV420(testWidth, testHeight, 128, 128, 128)
	expected := copyFrame(program)

	var blendScratch []byte
	var colLUTScratch []int
	result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)

	require.True(t, testutil.YUVFrameEqual(expected, result),
		"alpha=0 overlay must produce byte-exact passthrough of the program frame")
}

// ==========================================================================
// 2. TestDSKOpaqueAlpha1 — overlay with alpha=255 should fully replace the
//    program pixels with the overlay color (converted to YUV).
// ==========================================================================

func TestDSKOpaqueAlpha1(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Overlay: pure red, fully opaque.
	rgba := makeRGBA(testWidth, testHeight, 255, 0, 0, 255)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "red-opaque"))
	require.NoError(t, c.On(id))

	// Program: black (Y=16, Cb=128, Cr=128 in BT.709 limited range).
	program := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	var blendScratch []byte
	var colLUTScratch []int
	result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)

	// With alpha=255, the blend formula uses:
	//   A' = 255 + (255>>7) = 255+1 = 256
	//   a256 = (256 * 256) >> 8 = 256
	// So inv = 256 - 256 = 0, meaning output = overlayY entirely.
	//
	// BT.709 Y for (R=255,G=0,B=0): overlayY = 16 + ((47*255 + 0 + 0 + 128) >> 8)
	//   = 16 + ((11985+128)>>8) = 16 + (12113>>8) = 16 + 47 = 63
	// BT.709 Cb for (R=255,G=0,B=0): (-26*255 - 0 + 0 + 128)>>8 + 128
	//   = (-6630+128)>>8 + 128 = (-6502>>8) + 128 = -25 + 128 = 103
	// BT.709 Cr for (R=255,G=0,B=0): (112*255 - 0 - 0 + 128)>>8 + 128
	//   = (28560+128)>>8 + 128 = (28688>>8) + 128 = 112 + 128 = 240

	// Check Y plane: all pixels should be the overlay Y value.
	// Allow +/-1 for rounding differences between generic and SIMD kernels.
	y0, cb0, cr0 := testutil.YUVPixelAt(result, testWidth, testHeight, 0, 0)

	expectedY := byte(63)
	expectedCb := byte(103)
	expectedCr := byte(240)

	require.InDelta(t, float64(expectedY), float64(y0), 1.0,
		"opaque red overlay Y: got %d, want %d", y0, expectedY)
	require.InDelta(t, float64(expectedCb), float64(cb0), 1.0,
		"opaque red overlay Cb: got %d, want %d", cb0, expectedCb)
	require.InDelta(t, float64(expectedCr), float64(cr0), 1.0,
		"opaque red overlay Cr: got %d, want %d", cr0, expectedCr)

	// Verify all Y pixels are consistent (same value across the frame).
	for row := 0; row < testHeight; row++ {
		for col := 0; col < testWidth; col++ {
			yVal := result[row*testWidth+col]
			require.InDelta(t, float64(y0), float64(yVal), 0.0,
				"Y plane inconsistency at (%d,%d): got %d, expected %d", col, row, yVal, y0)
		}
	}
}

// ==========================================================================
// 3. TestDSKBlendAlpha128 — overlay with alpha=128 should produce a blend
//    of program and overlay colors, matching the formula within +/-1.
// ==========================================================================

func TestDSKBlendAlpha128(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Overlay: pure green, alpha=128.
	rgba := makeRGBA(testWidth, testHeight, 0, 255, 0, 128)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "green-half"))
	require.NoError(t, c.On(id))

	// Program: mid-gray (Y=128, Cb=128, Cr=128).
	program := testutil.GenerateSolidYUV420(testWidth, testHeight, 128, 128, 128)
	programCopy := copyFrame(program)

	var blendScratch []byte
	var colLUTScratch []int
	result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)

	// Compute expected blend manually:
	// A = 128, A' = 128 + (128>>7) = 128+1 = 129
	// alphaScale256 = 256 (fadePosition=1.0 → int(1.0*256+0.5) = 256)
	// a256 = (129 * 256) >> 8 = 33024 >> 8 = 129
	// inv = 256 - 129 = 127
	//
	// BT.709 Y for (R=0,G=255,B=0): overlayY = 16 + ((0 + 157*255 + 0 + 128) >> 8)
	//   = 16 + ((40035+128)>>8) = 16 + (40163>>8) = 16 + 156 = 172
	//
	// Blended Y = (128*127 + 172*129 + 128) >> 8
	//           = (16256 + 22188 + 128) >> 8 = 38572 >> 8 = 150
	//
	// BT.709 Cb for (0,255,0): overlayCb = ((-0 - 86*255 + 0 + 128) >> 8) + 128
	//   = ((-21930+128)>>8) + 128 = (-21802>>8)+128 = -85+128 = 43
	// Blended Cb = (128*127 + 43*129 + 128) >> 8
	//            = (16256 + 5547 + 128) >> 8 = 21931 >> 8 = 85
	//
	// BT.709 Cr for (0,255,0): overlayCr = ((0 - 102*255 - 0 + 128) >> 8) + 128
	//   = ((-26010+128)>>8) + 128 = (-25882>>8)+128 = -101+128 = 27
	// Blended Cr = (128*127 + 27*129 + 128) >> 8
	//            = (16256 + 3483 + 128) >> 8 = 19867 >> 8 = 77

	y0, cb0, cr0 := testutil.YUVPixelAt(result, testWidth, testHeight, 0, 0)

	require.InDelta(t, 150.0, float64(y0), 2.0,
		"alpha=128 blend Y: got %d, want ~150", y0)
	require.InDelta(t, 85.0, float64(cb0), 2.0,
		"alpha=128 blend Cb: got %d, want ~85", cb0)
	require.InDelta(t, 77.0, float64(cr0), 2.0,
		"alpha=128 blend Cr: got %d, want ~77", cr0)

	// Verify the result differs from the original program.
	require.False(t, testutil.YUVFrameEqual(programCopy, result),
		"alpha=128 blend must modify the program frame")
}

// ==========================================================================
// 4. TestLayerOrdering — two layers with different z-orders and overlapping
//    regions. The higher z-order layer (composited last) wins.
// ==========================================================================

func TestLayerOrdering(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id1, err := c.AddLayer()
	require.NoError(t, err)
	id2, err := c.AddLayer()
	require.NoError(t, err)

	// Layer 1 (z=0): fully opaque red.
	rgba1 := makeRGBA(testWidth, testHeight, 255, 0, 0, 255)
	require.NoError(t, c.SetOverlay(id1, rgba1, testWidth, testHeight, "red"))
	require.NoError(t, c.On(id1))

	// Layer 2 (z=1): fully opaque blue, composited AFTER layer 1.
	rgba2 := makeRGBA(testWidth, testHeight, 0, 0, 255, 255)
	require.NoError(t, c.SetOverlay(id2, rgba2, testWidth, testHeight, "blue"))
	require.NoError(t, c.On(id2))

	// Explicitly set z-orders.
	require.NoError(t, c.SetLayerZOrder(id1, 0))
	require.NoError(t, c.SetLayerZOrder(id2, 1))

	// Program: black.
	program := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	var blendScratch []byte
	var colLUTScratch []int
	result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)

	// Since layer 2 (blue) is composited last with alpha=255, it overwrites layer 1 (red).
	// Expected: all pixels should match blue overlay values.
	//
	// BT.709 Y for (R=0,G=0,B=255): overlayY = 16 + ((0 + 0 + 16*255 + 128) >> 8)
	//   = 16 + ((4080+128)>>8) = 16 + (4208>>8) = 16 + 16 = 32
	// BT.709 Cb for (0,0,255): ((-0 - 0 + 112*255 + 128) >> 8) + 128
	//   = ((28560+128)>>8) + 128 = (28688>>8)+128 = 112+128 = 240
	// BT.709 Cr for (0,0,255): ((0 - 0 - 10*255 + 128) >> 8) + 128
	//   = ((-2550+128)>>8)+128 = (-2422>>8)+128 = -9+128 = 119

	y0, cb0, cr0 := testutil.YUVPixelAt(result, testWidth, testHeight, 0, 0)

	expectedY := byte(32)
	expectedCb := byte(240)
	expectedCr := byte(119)

	require.InDelta(t, float64(expectedY), float64(y0), 1.0,
		"blue layer on top Y: got %d, want %d", y0, expectedY)
	require.InDelta(t, float64(expectedCb), float64(cb0), 1.0,
		"blue layer on top Cb: got %d, want %d", cb0, expectedCb)
	require.InDelta(t, float64(expectedCr), float64(cr0), 1.0,
		"blue layer on top Cr: got %d, want %d", cr0, expectedCr)

	// Now swap z-orders: make red on top.
	require.NoError(t, c.SetLayerZOrder(id1, 1))
	require.NoError(t, c.SetLayerZOrder(id2, 0))

	program2 := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	result2 := c.ProcessYUV(program2, testWidth, testHeight, &blendScratch, &colLUTScratch)

	y1, _, cr1 := testutil.YUVPixelAt(result2, testWidth, testHeight, 0, 0)

	// Now red (R=255,G=0,B=0) is on top. Expected: Y=63, Cr=240.
	require.InDelta(t, 63.0, float64(y1), 1.0,
		"red layer on top Y: got %d, want 63", y1)
	require.InDelta(t, 240.0, float64(cr1), 1.0,
		"red layer on top Cr: got %d, want 240", cr1)
}

// ==========================================================================
// 5. TestFlyInAnimationEndpoints — configure a transition animation that
//    moves a layer from off-screen to on-screen. Verify initial and final
//    positions.
// ==========================================================================

func TestFlyInAnimationEndpoints(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Set overlay and activate layer.
	rgba := makeRGBA(testWidth, testHeight, 255, 255, 0, 255)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "yellow"))
	require.NoError(t, c.On(id))

	// Set initial rect to off-screen (above frame).
	startRect := image.Rect(0, -testHeight, testWidth, 0)
	require.NoError(t, c.SetLayerRect(id, startRect))

	// Define animation: fly from off-screen to on-screen position.
	endRect := image.Rect(0, 0, testWidth, testHeight)
	cfg := graphics.AnimationConfig{
		Mode:       "transition",
		ToRect:     &graphics.RectState{X: endRect.Min.X, Y: endRect.Min.Y, Width: endRect.Dx(), Height: endRect.Dy()},
		DurationMs: 100,
		Easing:     "linear",
	}
	require.NoError(t, c.Animate(id, cfg))

	// Wait for animation to complete.
	time.Sleep(250 * time.Millisecond)

	// Check final state: layer rect should be at the target position.
	status := c.Status()
	require.NotEmpty(t, status.Layers)

	var found bool
	for _, l := range status.Layers {
		if l.ID == id {
			found = true
			require.Equal(t, endRect.Min.X, l.Rect.X, "fly-in final X")
			require.Equal(t, endRect.Min.Y, l.Rect.Y, "fly-in final Y")
			require.Equal(t, endRect.Dx(), l.Rect.Width, "fly-in final Width")
			require.Equal(t, endRect.Dy(), l.Rect.Height, "fly-in final Height")
			break
		}
	}
	require.True(t, found, "animated layer not found in status")
}

// ==========================================================================
// 6. TestImageRGBAToYUVQuality — upload a known-color RGBA overlay via
//    SetImage (PNG path), composite onto black, verify PSNR > 40 dB vs
//    expected YUV values.
// ==========================================================================

func TestImageRGBAToYUVQuality(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Create a solid white RGBA overlay and set it directly.
	// White (R=255,G=255,B=255): overlayY = 16+((47*255+157*255+16*255+128)>>8)
	//   = 16 + ((11985+40035+4080+128)>>8) = 16 + (56228>>8) = 16 + 219 = 235
	rgba := makeRGBA(testWidth, testHeight, 255, 255, 255, 255)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "white"))
	require.NoError(t, c.On(id))

	// Program: black.
	program := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	var blendScratch []byte
	var colLUTScratch []int
	result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)

	// Expected output for white overlay on black program:
	// Y=235 (BT.709 white), Cb=128 (neutral), Cr=128 (neutral)
	expectedFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 235, 128, 128)

	psnr := testutil.YUVFramePSNR(result, expectedFrame, testWidth, testHeight)
	require.Greater(t, psnr, 40.0,
		"RGBA→YUV composite quality PSNR should be > 40 dB, got %.2f dB", psnr)

	// Spot-check actual pixel values.
	y0, cb0, cr0 := testutil.YUVPixelAt(result, testWidth, testHeight, 10, 10)
	require.InDelta(t, 235.0, float64(y0), 1.0,
		"white overlay Y: got %d, want 235", y0)
	require.InDelta(t, 128.0, float64(cb0), 1.0,
		"white overlay Cb: got %d, want 128", cb0)
	require.InDelta(t, 128.0, float64(cr0), 1.0,
		"white overlay Cr: got %d, want 128", cr0)
}

// ==========================================================================
// 7. TestTickerScrollRate — start a ticker, advance ProcessYUV frames,
//    verify the ticker state advances at the expected pixels-per-frame rate.
// ==========================================================================

func TestTickerScrollRate(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	renderer, err := textrender.NewRenderer()
	require.NoError(t, err)
	te := graphics.NewTickerEngine(c, renderer)
	defer te.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Start a looping ticker with known speed.
	tickerSpeed := 300.0 // pixels per second
	err = te.Start(id, graphics.TickerConfig{
		Text:     "BREAKING NEWS: This is a test ticker with enough text to create a meaningful strip for scroll testing purposes",
		FontSize: 24,
		Speed:    tickerSpeed,
		Loop:     true,
		Height:   48,
	})
	require.NoError(t, err)

	// Wait for ticker goroutine to install state on the layer.
	time.Sleep(100 * time.Millisecond)

	// Drive multiple frames through ProcessYUV and verify the overlay changes.
	// At 30fps with speed=300px/s, the ticker should advance 10 pixels per frame.
	frameSize := testWidth*testHeight + 2*(testWidth/2)*(testHeight/2)
	numFrames := 10
	overlays := make([][]byte, numFrames)

	for i := 0; i < numFrames; i++ {
		program := make([]byte, frameSize)
		// Fill Y plane with a constant so we can detect overlay changes.
		for j := 0; j < testWidth*testHeight; j++ {
			program[j] = 128
		}
		result := c.ProcessYUV(program, testWidth, testHeight, nil, nil)
		overlays[i] = copyFrame(result)
	}

	// Consecutive frames should differ (ticker is scrolling).
	framesDiffering := 0
	for i := 1; i < numFrames; i++ {
		if !testutil.YUVFrameEqual(overlays[i-1], overlays[i]) {
			framesDiffering++
		}
	}

	// At 300px/s / 30fps = 10 pixels/frame advancement, frames must differ.
	require.Greater(t, framesDiffering, numFrames/2,
		"ticker should produce differing frames as it scrolls: %d of %d frames differed",
		framesDiffering, numFrames-1)
}

// ==========================================================================
// 8. TestOutputInLegalRange — various overlay + program combinations should
//    produce output with all Y in [16,235] and all Cb/Cr in [16,240].
// ==========================================================================

func TestOutputInLegalRange(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		progY  byte
		progCb byte
		progCr byte
		overlR byte
		overlG byte
		overlB byte
		overlA byte
	}{
		{"red_on_black", 16, 128, 128, 255, 0, 0, 255},
		{"green_on_white", 235, 128, 128, 0, 255, 0, 255},
		{"blue_on_mid_gray", 128, 128, 128, 0, 0, 255, 255},
		{"white_on_black", 16, 128, 128, 255, 255, 255, 255},
		{"cyan_on_red_program", 63, 103, 240, 0, 255, 255, 128},
		{"magenta_on_green_program", 172, 43, 27, 255, 0, 255, 200},
		{"yellow_half_alpha_on_gray", 128, 128, 128, 255, 255, 0, 128},
		{"black_overlay_on_white", 235, 128, 128, 0, 0, 0, 255},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := setupCompositor(testWidth, testHeight)
			defer c.Close()

			id, err := c.AddLayer()
			require.NoError(t, err)

			rgba := makeRGBA(testWidth, testHeight, tc.overlR, tc.overlG, tc.overlB, tc.overlA)
			require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "test"))
			require.NoError(t, c.On(id))

			program := testutil.GenerateSolidYUV420(testWidth, testHeight, tc.progY, tc.progCb, tc.progCr)

			var blendScratch []byte
			var colLUTScratch []int
			result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)

			// Fast scan: check all pixels in legal range.
			assertYUVInBroadcastRange(t, result, testWidth, testHeight)
		})
	}
}

// ==========================================================================
// 9. TestDSKMultipleFramesConsistent — compositing the same overlay onto
//    identical program frames should produce identical output (determinism).
// ==========================================================================

func TestDSKMultipleFramesConsistent(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	rgba := makeRGBA(testWidth, testHeight, 100, 200, 50, 180)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "test"))
	require.NoError(t, c.On(id))

	var blendScratch []byte
	var colLUTScratch []int

	// Run 5 identical frames and verify all outputs are the same.
	var firstResult []byte
	for i := 0; i < 5; i++ {
		program := testutil.GenerateSolidYUV420(testWidth, testHeight, 100, 120, 140)
		result := c.ProcessYUV(program, testWidth, testHeight, &blendScratch, &colLUTScratch)
		if i == 0 {
			firstResult = copyFrame(result)
		} else {
			require.True(t, testutil.YUVFrameEqual(firstResult, result),
				"frame %d differs from frame 0 — compositing is non-deterministic", i)
		}
	}
}

// ==========================================================================
// 10. TestDSKLayerOnOff — toggling a layer on/off should correctly enable
//     and disable compositing.
// ==========================================================================

func TestDSKLayerOnOff(t *testing.T) {
	t.Parallel()

	c := setupCompositor(testWidth, testHeight)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	rgba := makeRGBA(testWidth, testHeight, 255, 0, 0, 255)
	require.NoError(t, c.SetOverlay(id, rgba, testWidth, testHeight, "red"))

	// Process with layer OFF (before On is called).
	programOff := testutil.GenerateSolidYUV420(testWidth, testHeight, 128, 128, 128)
	expectedOff := copyFrame(programOff)
	resultOff := c.ProcessYUV(programOff, testWidth, testHeight, nil, nil)
	require.True(t, testutil.YUVFrameEqual(expectedOff, resultOff),
		"inactive layer should produce passthrough")

	// Turn layer ON.
	require.NoError(t, c.On(id))
	programOn := testutil.GenerateSolidYUV420(testWidth, testHeight, 128, 128, 128)
	resultOn := c.ProcessYUV(programOn, testWidth, testHeight, nil, nil)
	require.False(t, testutil.YUVFrameEqual(expectedOff, resultOn),
		"active layer with opaque overlay must modify the program")

	// Turn layer OFF.
	require.NoError(t, c.Off(id))
	programOff2 := testutil.GenerateSolidYUV420(testWidth, testHeight, 128, 128, 128)
	expectedOff2 := copyFrame(programOff2)
	resultOff2 := c.ProcessYUV(programOff2, testWidth, testHeight, nil, nil)
	require.True(t, testutil.YUVFrameEqual(expectedOff2, resultOff2),
		"layer after Off() should produce passthrough again")
}

// ==========================================================================
// Determinism: TestCompositorDeterminism — process same overlay+program
//   20 times with alpha=137/255, verify byte-exact output every run.
// ==========================================================================

func TestCompositorDeterminism(t *testing.T) {
	t.Parallel()

	const w, h = 320, 240

	// Build reference result.
	c := setupCompositor(w, h)
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Overlay with alpha=137 (non-trivial, exercises more code paths than 128).
	rgba := makeRGBA(w, h, 180, 60, 220, 137)
	require.NoError(t, c.SetOverlay(id, rgba, w, h, "det-test"))
	require.NoError(t, c.On(id))

	program := testutil.GenerateSolidYUV420(w, h, 100, 120, 140)

	var blendScratch []byte
	var colLUTScratch []int
	reference := copyFrame(c.ProcessYUV(program, w, h, &blendScratch, &colLUTScratch))

	// 19 more runs — must be byte-exact identical.
	for i := 0; i < 19; i++ {
		prog := testutil.GenerateSolidYUV420(w, h, 100, 120, 140)
		result := c.ProcessYUV(prog, w, h, &blendScratch, &colLUTScratch)
		if !testutil.YUVFrameEqual(reference, result) {
			t.Fatalf("Run %d produced different output (non-determinism detected)", i+1)
		}
	}
}

// ==========================================================================
// 11. TestBlendFormulaAccuracy — verify the alpha blending formula matches
//     the documented integer fixed-point math across a range of alpha values.
// ==========================================================================

func TestBlendFormulaAccuracy(t *testing.T) {
	t.Parallel()

	// Use a small frame for efficiency.
	w, h := 32, 32

	alphaValues := []byte{1, 32, 64, 96, 128, 160, 192, 224, 254, 255}

	for _, alpha := range alphaValues {
		alpha := alpha
		t.Run("alpha_"+string(rune('0'+alpha/100))+string(rune('0'+(alpha/10)%10))+string(rune('0'+alpha%10)), func(t *testing.T) {
			t.Parallel()

			c := setupCompositor(w, h)
			defer c.Close()

			id, err := c.AddLayer()
			require.NoError(t, err)

			overlayR, overlayG, overlayB := byte(200), byte(100), byte(50)
			rgba := makeRGBA(w, h, overlayR, overlayG, overlayB, alpha)
			require.NoError(t, c.SetOverlay(id, rgba, w, h, "test"))
			require.NoError(t, c.On(id))

			progY := byte(100)
			program := testutil.GenerateSolidYUV420(w, h, progY, 128, 128)

			var blendScratch []byte
			var colLUTScratch []int
			result := c.ProcessYUV(program, w, h, &blendScratch, &colLUTScratch)

			// Compute expected Y using the documented formula.
			A := int(alpha)
			A += A >> 7
			a256 := (A * 256) >> 8 // alphaScale256=256 for fadePosition=1.0
			if a256 == 0 {
				// Should be passthrough.
				y0, _, _ := testutil.YUVPixelAt(result, w, h, 0, 0)
				require.Equal(t, progY, y0, "alpha effective 0 should passthrough")
				return
			}
			inv := 256 - a256
			R, G, B := int(overlayR), int(overlayG), int(overlayB)
			overlayY := 16 + ((47*R + 157*G + 16*B + 128) >> 8)
			expectedY := (int(progY)*inv + overlayY*a256 + 128) >> 8

			y0, _, _ := testutil.YUVPixelAt(result, w, h, 0, 0)

			diff := math.Abs(float64(expectedY) - float64(y0))
			require.LessOrEqual(t, diff, 1.0,
				"alpha=%d: Y got %d, expected %d (diff=%.0f)", alpha, y0, expectedY, diff)
		})
	}
}
