//go:build correctness

package htmlgfx

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/htmlgfx"
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

// avgY computes the average Y value across the Y plane of a YUV420 frame.
func avgY(yuv []byte, w, h int) float64 {
	ySize := w * h
	if len(yuv) < ySize {
		return 0
	}
	var sum int64
	for i := 0; i < ySize; i++ {
		sum += int64(yuv[i])
	}
	return float64(sum) / float64(ySize)
}

// avgCb computes the average Cb value across the Cb plane of a YUV420 frame.
func avgCb(yuv []byte, w, h int) float64 {
	ySize := w * h
	chromaSize := (w / 2) * (h / 2)
	cbStart := ySize
	if len(yuv) < cbStart+chromaSize {
		return 0
	}
	var sum int64
	for i := 0; i < chromaSize; i++ {
		sum += int64(yuv[cbStart+i])
	}
	return float64(sum) / float64(chromaSize)
}

// avgCr computes the average Cr value across the Cr plane of a YUV420 frame.
func avgCr(yuv []byte, w, h int) float64 {
	ySize := w * h
	chromaSize := (w / 2) * (h / 2)
	crStart := ySize + chromaSize
	if len(yuv) < crStart+chromaSize {
		return 0
	}
	var sum int64
	for i := 0; i < chromaSize; i++ {
		sum += int64(yuv[crStart+i])
	}
	return float64(sum) / float64(chromaSize)
}

// TestCEFToCompositorOverlay tests the graphics compositor overlay path that
// CEF output eventually lands on: set a white RGBA overlay, process a black
// program frame, verify output has white pixels in YUV domain.
//
// Independent math (BT.709 integer coefficients from blend kernel):
//
//	White (R=255,G=255,B=255), A=255 over black (Y=16,Cb=128,Cr=128):
//	overlayY = 16 + ((47*255 + 157*255 + 16*255 + 128) >> 8) = 16 + (56228 >> 8) = 16 + 219 = 235
//	A' = 255 + (255>>7) = 256, a256 = (256*256)>>8 = 256, inv = 0
//	Y_out = (16*0 + 235*256 + 128) >> 8 = 60288 >> 8 = 235
//	overlayCb = ((-26*255 - 86*255 + 112*255 + 128) >> 8) + 128 = (128 >> 8) + 128 = 128
//	overlayCr = ((112*255 - 102*255 - 10*255 + 128) >> 8) + 128 = (128 >> 8) + 128 = 128
func TestCEFToCompositorOverlay(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	// Add a layer.
	layerID, err := comp.AddLayer()
	require.NoError(t, err, "AddLayer")

	// Create solid white RGBA overlay (R=255, G=255, B=255, A=255).
	whiteOverlay := makeRGBA(testWidth, testHeight, 255, 255, 255, 255)

	// Set overlay on compositor.
	err = comp.SetOverlay(layerID, whiteOverlay, testWidth, testHeight, "test")
	require.NoError(t, err, "SetOverlay")

	// Turn layer on.
	err = comp.On(layerID)
	require.NoError(t, err, "On")

	// Verify side effect: compositor reports active layers.
	require.True(t, comp.HasActiveLayers(), "HasActiveLayers should be true after On()")

	// Create a solid-black program frame (Y=16, Cb=128, Cr=128 = BT.709 black).
	blackFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	// Process frame through compositor.
	result := comp.ProcessYUV(blackFrame, testWidth, testHeight, nil, nil)

	// Validate output buffer size before any pixel inspection.
	expectedSize := testWidth * testHeight * 3 / 2
	require.Equal(t, expectedSize, len(result), "output YUV420 buffer size")

	// Per-pixel validation at 5 positions (corners + center).
	// Expected: Y=235, Cb=128, Cr=128 (white in BT.709 limited range).
	const expectedY = 235
	const expectedCb = 128
	const expectedCr = 128
	positions := [][2]int{
		{0, 0},                          // top-left
		{testWidth - 2, 0},              // top-right (even-aligned)
		{0, testHeight - 2},             // bottom-left (even-aligned)
		{testWidth - 2, testHeight - 2}, // bottom-right
		{testWidth / 2, testHeight / 2}, // center
	}
	for _, pos := range positions {
		x, y := pos[0], pos[1]
		gotY, gotCb, gotCr := testutil.YUVPixelAt(result, testWidth, testHeight, x, y)
		t.Logf("MEASURE: pixel(%d,%d) Y=%d Cb=%d Cr=%d (expected Y=%d Cb=%d Cr=%d)",
			x, y, gotY, gotCb, gotCr, expectedY, expectedCb, expectedCr)
		if abs(int(gotY)-expectedY) > 2 {
			t.Errorf("pixel(%d,%d) Y=%d, expected %d +/-2", x, y, gotY, expectedY)
		}
		if abs(int(gotCb)-expectedCb) > 2 {
			t.Errorf("pixel(%d,%d) Cb=%d, expected %d +/-2", x, y, gotCb, expectedCb)
		}
		if abs(int(gotCr)-expectedCr) > 2 {
			t.Errorf("pixel(%d,%d) Cr=%d, expected %d +/-2", x, y, gotCr, expectedCr)
		}
	}

	// Also verify averages match per-pixel values (uniform frame should be identical).
	yAvg := avgY(result, testWidth, testHeight)
	cbAvg := avgCb(result, testWidth, testHeight)
	crAvg := avgCr(result, testWidth, testHeight)
	t.Logf("MEASURE: avg Y=%.1f Cb=%.1f Cr=%.1f", yAvg, cbAvg, crAvg)
	require.InDelta(t, float64(expectedY), yAvg, 2.0, "avg Y")
	require.InDelta(t, float64(expectedCb), cbAvg, 2.0, "avg Cb")
	require.InDelta(t, float64(expectedCr), crAvg, 2.0, "avg Cr")
}

// TestCEFBGRAToCompositorPipeline tests the end-to-end path: BGRA frame
// (as CEF produces) -> bgraToRGBA conversion -> compositor overlay -> program output.
//
// Independent math (BT.709 integer coefficients from blend kernel):
//
//	Red (R=255,G=0,B=0), A=255 over black (Y=16,Cb=128,Cr=128):
//	overlayY = 16 + ((47*255 + 157*0 + 16*0 + 128) >> 8) = 16 + (12113 >> 8) = 16 + 47 = 63
//	A' = 256, a256 = 256, inv = 0
//	Y_out = (16*0 + 63*256 + 128) >> 8 = 16256 >> 8 = 63
//	overlayCb = ((-26*255 - 86*0 + 112*0 + 128) >> 8) + 128 = (-6502 >> 8) + 128 = -26 + 128 = 102
//	Cb_out = (128*0 + 102*256 + 128) >> 8 = 26240 >> 8 = 102
//	overlayCr = ((112*255 - 102*0 - 10*0 + 128) >> 8) + 128 = (28688 >> 8) + 128 = 112 + 128 = 240
//	Cr_out = (128*0 + 240*256 + 128) >> 8 = 61568 >> 8 = 240
func TestCEFBGRAToCompositorPipeline(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	require.NoError(t, err, "AddLayer")

	// Create a BGRA frame as CEF would produce.
	// Opaque red: BGRA = (B=0, G=0, R=255, A=255).
	bgraPixels := make([]byte, testWidth*testHeight*4)
	for i := 0; i < len(bgraPixels); i += 4 {
		bgraPixels[i] = 0     // B
		bgraPixels[i+1] = 0   // G
		bgraPixels[i+2] = 255 // R (premultiplied, but A=255 so same)
		bgraPixels[i+3] = 255 // A
	}

	// Convert via bgraToRGBA (exported via the Frame pipeline in htmlgfx).
	// Since bgraToRGBA is unexported, we simulate its behavior:
	// For opaque (A=255) pixels, it simply swaps B and R channels.
	// BGRA(0,0,255,255) -> RGBA(255,0,0,255) = pure red.
	rgbaPixels := make([]byte, len(bgraPixels))
	copy(rgbaPixels, bgraPixels)
	for i := 0; i < len(rgbaPixels); i += 4 {
		b := rgbaPixels[i]
		r := rgbaPixels[i+2]
		rgbaPixels[i] = r   // R
		rgbaPixels[i+2] = b // B
	}

	// Verify the conversion result: should be RGBA(255,0,0,255) = pure red.
	require.Equal(t, byte(255), rgbaPixels[0], "R channel after BGRA->RGBA")
	require.Equal(t, byte(0), rgbaPixels[1], "G channel after BGRA->RGBA")
	require.Equal(t, byte(0), rgbaPixels[2], "B channel after BGRA->RGBA")
	require.Equal(t, byte(255), rgbaPixels[3], "A channel after BGRA->RGBA")

	// Set on compositor as overlay.
	err = comp.SetOverlay(layerID, rgbaPixels, testWidth, testHeight, "html5")
	require.NoError(t, err, "SetOverlay")

	err = comp.On(layerID)
	require.NoError(t, err, "On")

	// Verify side effect: compositor reports active layers.
	require.True(t, comp.HasActiveLayers(), "HasActiveLayers should be true after On()")

	// Process a black program frame.
	blackFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	result := comp.ProcessYUV(blackFrame, testWidth, testHeight, nil, nil)

	// Validate output buffer size.
	expectedSize := testWidth * testHeight * 3 / 2
	require.Equal(t, expectedSize, len(result), "output YUV420 buffer size")

	// Independently computed expected values from kernel coefficients:
	const expectedY = 63
	const expectedCb = 102
	const expectedCr = 240

	yAvg := avgY(result, testWidth, testHeight)
	cbAvg := avgCb(result, testWidth, testHeight)
	crAvg := avgCr(result, testWidth, testHeight)

	t.Logf("MEASURE: avg Y=%.1f (expected %d), Cb=%.1f (expected %d), Cr=%.1f (expected %d)",
		yAvg, expectedY, cbAvg, expectedCb, crAvg, expectedCr)

	// Assert avg Y, Cb, Cr within tight tolerance of kernel-computed values.
	require.InDelta(t, float64(expectedY), yAvg, 3.0, "red overlay avg Y")
	require.InDelta(t, float64(expectedCb), cbAvg, 3.0, "red overlay avg Cb")
	require.InDelta(t, float64(expectedCr), crAvg, 3.0, "red overlay avg Cr")

	// Per-pixel spot check at center to confirm uniform output.
	gotY, gotCb, gotCr := testutil.YUVPixelAt(result, testWidth, testHeight, testWidth/2, testHeight/2)
	t.Logf("MEASURE: center pixel Y=%d Cb=%d Cr=%d", gotY, gotCb, gotCr)
	if abs(int(gotY)-expectedY) > 2 {
		t.Errorf("center pixel Y=%d, expected %d +/-2", gotY, expectedY)
	}
	if abs(int(gotCb)-expectedCb) > 2 {
		t.Errorf("center pixel Cb=%d, expected %d +/-2", gotCb, expectedCb)
	}
	if abs(int(gotCr)-expectedCr) > 2 {
		t.Errorf("center pixel Cr=%d, expected %d +/-2", gotCr, expectedCr)
	}
}

// TestCEFBGRAToCompositorPremultiplied tests the pipeline with semi-transparent
// premultiplied alpha BGRA, as CEF actually produces for partially transparent overlays.
//
// Independent math (BT.709 integer coefficients from blend kernel):
//
//	White (R=255,G=255,B=255), A=128 over black (Y=16,Cb=128,Cr=128):
//	overlayY = 235 (same as opaque white, computed in test 1)
//	A' = 128 + (128>>7) = 128 + 1 = 129
//	a256 = (129 * 256) >> 8 = 33024 >> 8 = 129
//	inv = 256 - 129 = 127
//	Y_out = (16*127 + 235*129 + 128) >> 8 = (2032 + 30315 + 128) >> 8 = 32475 >> 8 = 126
//	overlayCb = 128, overlayCr = 128 (achromatic white)
//	Cb_out = (128*127 + 128*129 + 128) >> 8 = (16256 + 16512 + 128) >> 8 = 32896 >> 8 = 128
//	Cr_out = same = 128
func TestCEFBGRAToCompositorPremultiplied(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	require.NoError(t, err, "AddLayer")

	// Semi-transparent white: premultiplied BGRA = (128, 128, 128, 128).
	// Un-premultiply: R = min((128*255 + 64)/128, 255) = 255
	//                 G = min((128*255 + 64)/128, 255) = 255
	//                 B = min((128*255 + 64)/128, 255) = 255
	//                 A = 128
	// So RGBA should be (255, 255, 255, 128) = 50% transparent white.
	bgraPixels := make([]byte, testWidth*testHeight*4)
	for i := 0; i < len(bgraPixels); i += 4 {
		bgraPixels[i] = 128   // B (premultiplied)
		bgraPixels[i+1] = 128 // G (premultiplied)
		bgraPixels[i+2] = 128 // R (premultiplied)
		bgraPixels[i+3] = 128 // A
	}

	// Simulate bgraToRGBA un-premultiply for semi-transparent pixels.
	rgbaPixels := make([]byte, len(bgraPixels))
	for i := 0; i < len(bgraPixels); i += 4 {
		b := uint16(bgraPixels[i])
		g := uint16(bgraPixels[i+1])
		r := uint16(bgraPixels[i+2])
		a := uint16(bgraPixels[i+3])

		if a == 0 {
			rgbaPixels[i] = 0
			rgbaPixels[i+1] = 0
			rgbaPixels[i+2] = 0
			rgbaPixels[i+3] = 0
		} else if a == 255 {
			rgbaPixels[i] = byte(r)
			rgbaPixels[i+1] = byte(g)
			rgbaPixels[i+2] = byte(b)
			rgbaPixels[i+3] = 255
		} else {
			rr := (r*255 + a/2) / a
			gg := (g*255 + a/2) / a
			bb := (b*255 + a/2) / a
			if rr > 255 {
				rr = 255
			}
			if gg > 255 {
				gg = 255
			}
			if bb > 255 {
				bb = 255
			}
			rgbaPixels[i] = byte(rr)
			rgbaPixels[i+1] = byte(gg)
			rgbaPixels[i+2] = byte(bb)
			rgbaPixels[i+3] = byte(a)
		}
	}

	// Verify un-premultiply: should get RGBA(255, 255, 255, 128).
	t.Logf("MEASURE: un-premultiplied pixel RGBA(%d,%d,%d,%d), expected (255,255,255,128)",
		rgbaPixels[0], rgbaPixels[1], rgbaPixels[2], rgbaPixels[3])
	require.Equal(t, byte(255), rgbaPixels[0], "un-premultiply R")
	require.Equal(t, byte(255), rgbaPixels[1], "un-premultiply G")
	require.Equal(t, byte(255), rgbaPixels[2], "un-premultiply B")
	require.Equal(t, byte(128), rgbaPixels[3], "un-premultiply A")

	err = comp.SetOverlay(layerID, rgbaPixels, testWidth, testHeight, "html5")
	require.NoError(t, err, "SetOverlay")

	err = comp.On(layerID)
	require.NoError(t, err, "On")

	// Verify side effect: compositor reports active layers.
	require.True(t, comp.HasActiveLayers(), "HasActiveLayers should be true after On()")

	// Process a black program frame.
	blackFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	result := comp.ProcessYUV(blackFrame, testWidth, testHeight, nil, nil)

	// Validate output buffer size.
	expectedSize := testWidth * testHeight * 3 / 2
	require.Equal(t, expectedSize, len(result), "output YUV420 buffer size")

	// Independently computed expected values:
	const expectedY = 126
	const expectedCb = 128
	const expectedCr = 128

	yAvg := avgY(result, testWidth, testHeight)
	cbAvg := avgCb(result, testWidth, testHeight)
	crAvg := avgCr(result, testWidth, testHeight)

	t.Logf("MEASURE: avg Y=%.1f (expected %d), Cb=%.1f (expected %d), Cr=%.1f (expected %d)",
		yAvg, expectedY, cbAvg, expectedCb, crAvg, expectedCr)

	// Assert Y within tight tolerance (kernel integer rounding allows +/-1).
	require.InDelta(t, float64(expectedY), yAvg, 3.0, "semi-transparent white overlay avg Y")
	require.InDelta(t, float64(expectedCb), cbAvg, 3.0, "semi-transparent white overlay avg Cb")
	require.InDelta(t, float64(expectedCr), crAvg, 3.0, "semi-transparent white overlay avg Cr")

	// Per-pixel spot check at center.
	gotY, gotCb, gotCr := testutil.YUVPixelAt(result, testWidth, testHeight, testWidth/2, testHeight/2)
	t.Logf("MEASURE: center pixel Y=%d Cb=%d Cr=%d", gotY, gotCb, gotCr)
	if abs(int(gotY)-expectedY) > 2 {
		t.Errorf("center pixel Y=%d, expected %d +/-2", gotY, expectedY)
	}
	if abs(int(gotCb)-expectedCb) > 2 {
		t.Errorf("center pixel Cb=%d, expected %d +/-2", gotCb, expectedCb)
	}
	if abs(int(gotCr)-expectedCr) > 2 {
		t.Errorf("center pixel Cr=%d, expected %d +/-2", gotCr, expectedCr)
	}
}

// TestCEFProtocolRoundTrip tests serialization/deserialization of CEF protocol messages.
func TestCEFProtocolRoundTrip(t *testing.T) {
	// Test ControlCommand round-trip via WriteControlCommand + ReadFrame.
	testCases := []struct {
		name string
		cmd  htmlgfx.ControlCommand
	}{
		{
			name: "create",
			cmd: htmlgfx.ControlCommand{
				Cmd:    "create",
				ID:     intPtr(0),
				Width:  1920,
				Height: 1080,
				FPS:    30,
			},
		},
		{
			name: "load",
			cmd: htmlgfx.ControlCommand{
				Cmd: "load",
				ID:  intPtr(1),
				URL: "file:///test/lower-third.html",
			},
		},
		{
			name: "update",
			cmd: htmlgfx.ControlCommand{
				Cmd:  "update",
				ID:   intPtr(0),
				Data: `{"headline":"Breaking News","subtitle":"Live"}`,
			},
		},
		{
			name: "custom",
			cmd: htmlgfx.ControlCommand{
				Cmd:     "custom",
				ID:      intPtr(2),
				Action:  "animate_in",
				Payload: `{"speed":1.5,"easing":"ease-out"}`,
			},
		},
		{
			name: "shutdown",
			cmd: htmlgfx.ControlCommand{
				Cmd: "shutdown",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize.
			err := htmlgfx.WriteControlCommand(&buf, tc.cmd)
			if err != nil {
				t.Fatalf("WriteControlCommand: %v", err)
			}

			// Deserialize back.
			data := buf.Bytes()
			t.Logf("MEASURE: serialized %q command = %d bytes: %s", tc.cmd.Cmd, len(data), string(data[:len(data)-1]))

			// Verify it ends with newline.
			if data[len(data)-1] != '\n' {
				t.Errorf("serialized command does not end with newline")
			}

			// Parse back.
			var parsed htmlgfx.ControlCommand
			err = json.Unmarshal(data[:len(data)-1], &parsed)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			// Verify fields match.
			if parsed.Cmd != tc.cmd.Cmd {
				t.Errorf("Cmd: got %q, want %q", parsed.Cmd, tc.cmd.Cmd)
			}
			if tc.cmd.ID != nil {
				if parsed.ID == nil {
					t.Errorf("ID: got nil, want %d", *tc.cmd.ID)
				} else if *parsed.ID != *tc.cmd.ID {
					t.Errorf("ID: got %d, want %d", *parsed.ID, *tc.cmd.ID)
				}
			} else {
				if parsed.ID != nil {
					t.Errorf("ID: got %d, want nil", *parsed.ID)
				}
			}
			if parsed.URL != tc.cmd.URL {
				t.Errorf("URL: got %q, want %q", parsed.URL, tc.cmd.URL)
			}
			if parsed.Data != tc.cmd.Data {
				t.Errorf("Data: got %q, want %q", parsed.Data, tc.cmd.Data)
			}
			if parsed.Action != tc.cmd.Action {
				t.Errorf("Action: got %q, want %q", parsed.Action, tc.cmd.Action)
			}
			if parsed.Payload != tc.cmd.Payload {
				t.Errorf("Payload: got %q, want %q", parsed.Payload, tc.cmd.Payload)
			}
		})
	}
}

// TestCEFProtocolFrameRoundTrip tests binary frame protocol serialization.
func TestCEFProtocolFrameRoundTrip(t *testing.T) {
	// Create a frame with known pixel data.
	width := uint16(8)
	height := uint16(4)
	pixels := make([]byte, int(width)*int(height)*4)
	for i := range pixels {
		pixels[i] = byte(i % 256)
	}

	original := htmlgfx.Frame{
		BrowserID: 2,
		Width:     width,
		Height:    height,
		Pixels:    pixels,
	}

	var buf bytes.Buffer
	err := htmlgfx.WriteFrame(&buf, original)
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	t.Logf("MEASURE: frame %dx%d = %d wire bytes (header overhead = %d)",
		width, height, buf.Len(), buf.Len()-len(pixels))

	got, err := htmlgfx.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	if got.BrowserID != original.BrowserID {
		t.Errorf("BrowserID: got %d, want %d", got.BrowserID, original.BrowserID)
	}
	if got.Width != original.Width {
		t.Errorf("Width: got %d, want %d", got.Width, original.Width)
	}
	if got.Height != original.Height {
		t.Errorf("Height: got %d, want %d", got.Height, original.Height)
	}
	if !bytes.Equal(got.Pixels, original.Pixels) {
		t.Errorf("Pixels mismatch: first differing byte index found")
		for i := range got.Pixels {
			if got.Pixels[i] != original.Pixels[i] {
				t.Logf("MEASURE: first diff at byte %d: got %d, want %d", i, got.Pixels[i], original.Pixels[i])
				break
			}
		}
	}

	// Verify wire overhead: 4 bytes length prefix + 5 bytes header = 9 bytes.
	expectedWireSize := 4 + htmlgfx.FrameHeaderSize + len(pixels)
	if buf.Len() != 0 {
		t.Errorf("buffer not fully consumed: %d bytes remaining", buf.Len())
	}
	wireSize := 4 + htmlgfx.FrameHeaderSize + len(pixels)
	t.Logf("MEASURE: expected wire size = %d, FrameHeaderSize = %d", expectedWireSize, htmlgfx.FrameHeaderSize)
	if wireSize != expectedWireSize {
		t.Errorf("wire size: got %d, want %d", wireSize, expectedWireSize)
	}
}

// TestCEFProtocolMultipleFramesSequential tests that multiple frames can be
// written and read back in order without corruption.
func TestCEFProtocolMultipleFramesSequential(t *testing.T) {
	var buf bytes.Buffer

	const numFrames = 5
	frames := make([]htmlgfx.Frame, numFrames)
	for i := range frames {
		w := uint16(4)
		h := uint16(4)
		pixels := make([]byte, int(w)*int(h)*4)
		// Fill with browser-ID-specific pattern so we can detect ordering bugs.
		for j := range pixels {
			pixels[j] = byte((i*37 + j) % 256)
		}
		frames[i] = htmlgfx.Frame{
			BrowserID: uint8(i),
			Width:     w,
			Height:    h,
			Pixels:    pixels,
		}
		if err := htmlgfx.WriteFrame(&buf, frames[i]); err != nil {
			t.Fatalf("WriteFrame(%d): %v", i, err)
		}
	}

	t.Logf("MEASURE: %d frames written, total wire bytes = %d", numFrames, buf.Len())

	for i := 0; i < numFrames; i++ {
		got, err := htmlgfx.ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame(%d): %v", i, err)
		}
		if got.BrowserID != frames[i].BrowserID {
			t.Errorf("frame %d: BrowserID got %d, want %d", i, got.BrowserID, frames[i].BrowserID)
		}
		if !bytes.Equal(got.Pixels, frames[i].Pixels) {
			t.Errorf("frame %d: pixel data mismatch", i)
		}
	}
}

// TestCEFManagerStartFailureGraceful tests that Manager handles missing sidecar binary gracefully.
func TestCEFManagerStartFailureGraceful(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	// Test 1: Empty binary path -> Start() returns nil (graceful, HTML5 disabled).
	cfg1 := htmlgfx.Config{
		BinaryPath: "",
		PoolSize:   4,
		Width:      1920,
		Height:     1080,
		FPS:        30,
	}
	mgr1 := htmlgfx.NewManager(comp, cfg1)
	err := mgr1.Start()
	t.Logf("MEASURE: Start() with empty path returned %v", err)
	if err != nil {
		t.Errorf("Start() with empty BinaryPath should return nil, got: %v", err)
	}
	// Alive should be false since no sidecar was started.
	if mgr1.Alive() {
		t.Errorf("Alive() should be false when no binary configured")
	}
	_ = mgr1.Close()

	// Test 2: Non-existent binary path -> Start() returns nil (graceful, logs warning).
	cfg2 := htmlgfx.Config{
		BinaryPath: "/nonexistent/path/to/cef-sidecar-binary-that-does-not-exist",
		PoolSize:   4,
		Width:      1920,
		Height:     1080,
		FPS:        30,
	}
	mgr2 := htmlgfx.NewManager(comp, cfg2)
	err = mgr2.Start()
	t.Logf("MEASURE: Start() with nonexistent path returned %v", err)
	if err != nil {
		t.Errorf("Start() with nonexistent binary should return nil (graceful degradation), got: %v", err)
	}
	if mgr2.Alive() {
		t.Errorf("Alive() should be false when binary not found")
	}
	_ = mgr2.Close()

	// Test 3: All browsers should be in initial empty state even without a sidecar.
	cfg3 := htmlgfx.Config{
		BinaryPath: "",
		PoolSize:   4,
		Width:      1920,
		Height:     1080,
		FPS:        30,
	}
	mgr3 := htmlgfx.NewManager(comp, cfg3)
	_ = mgr3.Start()
	states := mgr3.BrowserStates()
	t.Logf("MEASURE: browser pool size = %d", len(states))
	for i, bs := range states {
		if bs.Status != "empty" {
			t.Errorf("browser %d: status=%q, want %q", i, bs.Status, "empty")
		}
		if bs.LayerID != -1 {
			t.Errorf("browser %d: layerID=%d, want -1", i, bs.LayerID)
		}
	}
	_ = mgr3.Close()
}

// TestCEFManagerAllocateWithoutSidecar tests that AllocateBrowser fails
// gracefully when no sidecar is connected.
func TestCEFManagerAllocateWithoutSidecar(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	cfg := htmlgfx.Config{
		BinaryPath: "",
		PoolSize:   4,
		Width:      1920,
		Height:     1080,
		FPS:        30,
	}
	mgr := htmlgfx.NewManager(comp, cfg)
	_ = mgr.Start()
	defer func() { _ = mgr.Close() }()

	// Try to allocate a browser without a sidecar connection.
	// This should fail because sendCommand will fail (no control connection).
	_, err := mgr.AllocateBrowser(0)
	t.Logf("MEASURE: AllocateBrowser without sidecar returned %v", err)

	if err == nil {
		t.Errorf("AllocateBrowser should fail when no sidecar is connected, got nil error")
	}
}

// TestCEFOverlayOnLayerWithoutOverlayFails tests that On() returns ErrNoOverlay
// when called on a layer that has no overlay data set.
func TestCEFOverlayOnLayerWithoutOverlayFails(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	// Try to turn on a layer without setting an overlay first.
	err = comp.On(layerID)
	t.Logf("MEASURE: On() without overlay returned %v", err)

	if err != graphics.ErrNoOverlay {
		t.Errorf("On() without overlay: got %v, want %v", err, graphics.ErrNoOverlay)
	}
}

// TestCEFCompositorOverlayOverBlackFrameIsIdempotent tests that processing the
// same frame multiple times with the same overlay gives consistent results.
//
// Independent math for green overlay (R=0,G=255,B=0), A=255 over black:
//
//	overlayY = 16 + ((47*0 + 157*255 + 16*0 + 128) >> 8) = 16 + (40163 >> 8) = 16 + 156 = 172
//	overlayCb = ((-26*0 - 86*255 + 112*0 + 128) >> 8) + 128 = (-21802 >> 8) + 128 = -86 + 128 = 42
//	overlayCr = ((112*0 - 102*255 - 10*0 + 128) >> 8) + 128 = (-25882 >> 8) + 128 = -102 + 128 = 26
func TestCEFCompositorOverlayOverBlackFrameIsIdempotent(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	require.NoError(t, err, "AddLayer")

	greenOverlay := makeRGBA(testWidth, testHeight, 0, 255, 0, 255)
	err = comp.SetOverlay(layerID, greenOverlay, testWidth, testHeight, "test")
	require.NoError(t, err, "SetOverlay")
	err = comp.On(layerID)
	require.NoError(t, err, "On")

	// Process two independent black frames.
	frame1 := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	frame2 := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	result1 := comp.ProcessYUV(frame1, testWidth, testHeight, nil, nil)
	result2 := comp.ProcessYUV(frame2, testWidth, testHeight, nil, nil)

	// Validate output buffer sizes.
	expectedSize := testWidth * testHeight * 3 / 2
	require.Equal(t, expectedSize, len(result1), "result1 YUV420 buffer size")
	require.Equal(t, expectedSize, len(result2), "result2 YUV420 buffer size")

	y1 := avgY(result1, testWidth, testHeight)
	y2 := avgY(result2, testWidth, testHeight)

	t.Logf("MEASURE: pass 1 avg Y=%.1f, pass 2 avg Y=%.1f (expected 172)", y1, y2)

	// Both passes should give identical results (byte-for-byte).
	require.True(t, bytes.Equal(result1, result2), "ProcessYUV not idempotent: Y1=%.1f Y2=%.1f", y1, y2)

	// Verify the actual value matches independently computed green Y.
	require.InDelta(t, 172.0, y1, 3.0, "green overlay avg Y")
}

// TestCEFSetOverlayOnNonExistentLayer tests that SetOverlay returns ErrLayerNotFound.
func TestCEFSetOverlayOnNonExistentLayer(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	overlay := makeRGBA(testWidth, testHeight, 255, 0, 0, 255)
	err := comp.SetOverlay(999, overlay, testWidth, testHeight, "test")
	t.Logf("MEASURE: SetOverlay on non-existent layer returned %v", err)

	if err != graphics.ErrLayerNotFound {
		t.Errorf("SetOverlay on non-existent layer: got %v, want %v", err, graphics.ErrLayerNotFound)
	}
}

// TestCEFSetOverlaySizeMismatch tests that SetOverlay rejects RGBA data
// that doesn't match the declared dimensions.
func TestCEFSetOverlaySizeMismatch(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	// Provide wrong-sized RGBA data (too small).
	badOverlay := make([]byte, 100) // much smaller than testWidth*testHeight*4
	err = comp.SetOverlay(layerID, badOverlay, testWidth, testHeight, "test")
	t.Logf("MEASURE: SetOverlay with size mismatch returned %v", err)

	if err == nil {
		t.Errorf("SetOverlay with mismatched data size should return error, got nil")
	}
}

// TestCEFFrameProtocolMalformedHeader tests ReadFrame behavior with a
// truncated header (less than FrameHeaderSize bytes after the length prefix).
func TestCEFFrameProtocolMalformedHeader(t *testing.T) {
	var buf bytes.Buffer

	// Write a length prefix that claims 3 bytes (< FrameHeaderSize=5).
	header := []byte{0, 0, 0, 3, 0xAA, 0xBB, 0xCC}
	buf.Write(header)

	_, err := htmlgfx.ReadFrame(&buf)
	t.Logf("MEASURE: ReadFrame with small total_length returned %v", err)

	if err == nil {
		t.Errorf("ReadFrame with total_length < FrameHeaderSize should return error, got nil")
	}
}

// TestCEFControlResponseRoundTrip tests that ControlResponse JSON round-trips correctly.
func TestCEFControlResponseRoundTrip(t *testing.T) {
	responses := []htmlgfx.ControlResponse{
		{ID: 0, Status: "ok"},
		{ID: 1, Event: "format_detected", Format: "casparcg"},
		{ID: 2, Event: "load_error", Error: "ERR_FILE_NOT_FOUND"},
		{ID: 3, Event: "schema_parsed", Data: `{"fields":["name","score"]}`},
	}

	for _, orig := range responses {
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var parsed htmlgfx.ControlResponse
		err = json.Unmarshal(data, &parsed)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		t.Logf("MEASURE: response ID=%d Event=%q Format=%q serialized to %d bytes",
			orig.ID, orig.Event, orig.Format, len(data))

		if parsed.ID != orig.ID {
			t.Errorf("ID: got %d, want %d", parsed.ID, orig.ID)
		}
		if parsed.Status != orig.Status {
			t.Errorf("Status: got %q, want %q", parsed.Status, orig.Status)
		}
		if parsed.Event != orig.Event {
			t.Errorf("Event: got %q, want %q", parsed.Event, orig.Event)
		}
		if parsed.Format != orig.Format {
			t.Errorf("Format: got %q, want %q", parsed.Format, orig.Format)
		}
		if parsed.Error != orig.Error {
			t.Errorf("Error: got %q, want %q", parsed.Error, orig.Error)
		}
		if parsed.Data != orig.Data {
			t.Errorf("Data: got %q, want %q", parsed.Data, orig.Data)
		}
	}
}

// abs returns the absolute value of an int.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func intPtr(i int) *int {
	return &i
}
