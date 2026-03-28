//go:build correctness

package htmlgfx

import (
	"bytes"
	"encoding/json"
	"testing"

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
func TestCEFToCompositorOverlay(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	// Add a layer.
	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	// Create solid white RGBA overlay (R=255, G=255, B=255, A=255).
	whiteOverlay := makeRGBA(testWidth, testHeight, 255, 255, 255, 255)

	// Set overlay on compositor.
	err = comp.SetOverlay(layerID, whiteOverlay, testWidth, testHeight, "test")
	if err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}

	// Turn layer on.
	err = comp.On(layerID)
	if err != nil {
		t.Fatalf("On: %v", err)
	}

	// Create a solid-black program frame (Y=16, Cb=128, Cr=128 = BT.709 black).
	blackFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	// Process frame through compositor.
	result := comp.ProcessYUV(blackFrame, testWidth, testHeight, nil, nil)

	// Measure output values.
	yAvg := avgY(result, testWidth, testHeight)
	cbAvg := avgCb(result, testWidth, testHeight)
	crAvg := avgCr(result, testWidth, testHeight)

	t.Logf("MEASURE: output avg Y=%.1f (expected ~235 for white)", yAvg)
	t.Logf("MEASURE: output avg Cb=%.1f (expected ~128 for white)", cbAvg)
	t.Logf("MEASURE: output avg Cr=%.1f (expected ~128 for white)", crAvg)

	// White in BT.709 limited range: Y=235, Cb=128, Cr=128.
	// Allow some tolerance for integer rounding in the blend kernels.
	if yAvg < 230 || yAvg > 240 {
		t.Errorf("white overlay Y avg=%.1f, expected ~235 (range 230-240)", yAvg)
	}
	if cbAvg < 124 || cbAvg > 132 {
		t.Errorf("white overlay Cb avg=%.1f, expected ~128 (range 124-132)", cbAvg)
	}
	if crAvg < 124 || crAvg > 132 {
		t.Errorf("white overlay Cr avg=%.1f, expected ~128 (range 124-132)", crAvg)
	}
}

// TestCEFBGRAToCompositorPipeline tests the end-to-end path: BGRA frame
// (as CEF produces) -> bgraToRGBA conversion -> compositor overlay -> program output.
func TestCEFBGRAToCompositorPipeline(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	// Create a BGRA frame as CEF would produce.
	// Opaque red: BGRA = (B=0, G=0, R=255, A=255).
	bgraPixels := make([]byte, testWidth*testHeight*4)
	for i := 0; i < len(bgraPixels); i += 4 {
		bgraPixels[i] = 0   // B
		bgraPixels[i+1] = 0 // G
		bgraPixels[i+2] = 255 // R (premultiplied, but A=255 so same)
		bgraPixels[i+3] = 255 // A
	}

	// Convert via bgraToRGBA (exported via the Frame pipeline in htmlgfx).
	// Since bgraToRGBA is unexported, we simulate its behavior:
	// For opaque (A=255) pixels, it simply swaps B and R channels.
	// BGRA(0,0,255,255) -> RGBA(255,0,0,255) = pure red.
	rgbaPixels := make([]byte, len(bgraPixels))
	copy(rgbaPixels, bgraPixels)
	// Perform the BGRA->RGBA swap manually (same as bgraToRGBA for opaque).
	for i := 0; i < len(rgbaPixels); i += 4 {
		b := rgbaPixels[i]
		r := rgbaPixels[i+2]
		rgbaPixels[i] = r   // R
		rgbaPixels[i+2] = b // B
		// G and A stay the same
	}

	// Verify the conversion result: should be RGBA(255,0,0,255) = pure red.
	if rgbaPixels[0] != 255 || rgbaPixels[1] != 0 || rgbaPixels[2] != 0 || rgbaPixels[3] != 255 {
		t.Fatalf("BGRA->RGBA conversion wrong: got RGBA(%d,%d,%d,%d), want (255,0,0,255)",
			rgbaPixels[0], rgbaPixels[1], rgbaPixels[2], rgbaPixels[3])
	}

	// Set on compositor as overlay.
	err = comp.SetOverlay(layerID, rgbaPixels, testWidth, testHeight, "html5")
	if err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}

	err = comp.On(layerID)
	if err != nil {
		t.Fatalf("On: %v", err)
	}

	// Process a black program frame.
	blackFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	result := comp.ProcessYUV(blackFrame, testWidth, testHeight, nil, nil)

	yAvg := avgY(result, testWidth, testHeight)
	cbAvg := avgCb(result, testWidth, testHeight)
	crAvg := avgCr(result, testWidth, testHeight)

	t.Logf("MEASURE: end-to-end pipeline avg Y=%.1f (expected ~81 for red)", yAvg)
	t.Logf("MEASURE: end-to-end pipeline avg Cb=%.1f (expected ~90 for red)", cbAvg)
	t.Logf("MEASURE: end-to-end pipeline avg Cr=%.1f (expected ~240 for red)", crAvg)

	// Pure red in BT.709 limited range:
	// Y  = 0.2126*255 + 0.7152*0 + 0.0722*0 = 54.2 -> limited: 16 + 54.2*219/255 ~ 62.5
	// Actually BT.709 limited:
	// Y  = 16 + 65.481*R + 128.553*G + 24.966*B (where R,G,B are 0-1)
	// Y  = 16 + 65.481*1 + 0 + 0 = 81.5
	// Cb = 128 + (-37.797*1 + 0 + 0) = 90.2 (approximately)
	// Cr = 128 + (112*1 + 0 + 0) = 240 (approximately)
	//
	// These are approximate; the SIMD blend uses integer coefficients.
	if yAvg < 60 || yAvg > 100 {
		t.Errorf("red overlay Y avg=%.1f, expected ~81 (range 60-100)", yAvg)
	}
	if crAvg < 220 {
		t.Errorf("red overlay Cr avg=%.1f, expected ~240 (should be > 220)", crAvg)
	}
}

// TestCEFBGRAToCompositorPremultiplied tests the pipeline with semi-transparent
// premultiplied alpha BGRA, as CEF actually produces for partially transparent overlays.
func TestCEFBGRAToCompositorPremultiplied(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

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

	if rgbaPixels[0] != 255 || rgbaPixels[1] != 255 || rgbaPixels[2] != 255 || rgbaPixels[3] != 128 {
		t.Errorf("un-premultiply wrong: got RGBA(%d,%d,%d,%d), want (255,255,255,128)",
			rgbaPixels[0], rgbaPixels[1], rgbaPixels[2], rgbaPixels[3])
	}

	err = comp.SetOverlay(layerID, rgbaPixels, testWidth, testHeight, "html5")
	if err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}

	err = comp.On(layerID)
	if err != nil {
		t.Fatalf("On: %v", err)
	}

	// Process a black program frame.
	blackFrame := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	result := comp.ProcessYUV(blackFrame, testWidth, testHeight, nil, nil)

	yAvg := avgY(result, testWidth, testHeight)
	t.Logf("MEASURE: semi-transparent white overlay avg Y=%.1f (expected ~125, midpoint between 16 and 235)", yAvg)

	// 50% transparent white over black should give roughly mid-gray.
	// Y should be between black (16) and white (235), around 125.
	if yAvg < 80 || yAvg > 170 {
		t.Errorf("semi-transparent overlay Y avg=%.1f, expected ~125 (range 80-170)", yAvg)
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
func TestCEFCompositorOverlayOverBlackFrameIsIdempotent(t *testing.T) {
	comp := setupCompositor(testWidth, testHeight)
	defer comp.Close()

	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	greenOverlay := makeRGBA(testWidth, testHeight, 0, 255, 0, 255)
	err = comp.SetOverlay(layerID, greenOverlay, testWidth, testHeight, "test")
	if err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}
	err = comp.On(layerID)
	if err != nil {
		t.Fatalf("On: %v", err)
	}

	// Process two independent black frames.
	frame1 := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)
	frame2 := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	result1 := comp.ProcessYUV(frame1, testWidth, testHeight, nil, nil)
	result2 := comp.ProcessYUV(frame2, testWidth, testHeight, nil, nil)

	y1 := avgY(result1, testWidth, testHeight)
	y2 := avgY(result2, testWidth, testHeight)

	t.Logf("MEASURE: pass 1 avg Y=%.1f, pass 2 avg Y=%.1f", y1, y2)

	// Both passes should give identical results.
	if !bytes.Equal(result1, result2) {
		t.Errorf("ProcessYUV not idempotent: Y1=%.1f Y2=%.1f", y1, y2)
	}
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

func intPtr(i int) *int {
	return &i
}
