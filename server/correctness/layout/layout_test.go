//go:build correctness

package layout

import (
	"image"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/layout"
)

// ==========================================================================
// Constants: well-known YUV colors (BT.709 limited range)
// ==========================================================================

// Broadcast black: Y=16, Cb=128, Cr=128
const blackY, blackCb, blackCr byte = 16, 128, 128

// White: Y=235, Cb=128, Cr=128
const whiteY, whiteCb, whiteCr byte = 235, 128, 128

// Distinct source colors for multi-source tests (legal range, clearly distinguishable).
// Red-ish: Y=81, Cb=90, Cr=240 (BT.709 limited range red)
const redY, redCb, redCr byte = 81, 90, 240

// Green-ish: Y=145, Cb=54, Cr=26 (BT.709 limited range green)
const greenY, greenCb, greenCr byte = 145, 54, 26

// Blue-ish: Y=41, Cb=240, Cr=110 (BT.709 limited range blue)
const blueY, blueCb, blueCr byte = 41, 240, 110

// Yellow-ish: Y=210, Cb=16, Cr=146 (BT.709 limited range yellow)
const yellowY, yellowCb, yellowCr byte = 210, 16, 146

// ==========================================================================
// Helper functions
// ==========================================================================

// yuvPixelY reads only the Y value at (x, y) from a YUV420 frame.
func yuvPixelY(yuv []byte, w, x, y int) byte {
	return yuv[y*w+x]
}

// assertPixelColor checks that the pixel at (x, y) has the expected Y, Cb, Cr values
// within the given tolerance.
func assertPixelColor(t *testing.T, yuv []byte, w, h, x, y int, expectY, expectCb, expectCr, tolerance byte, msg string) {
	t.Helper()
	gotY, gotCb, gotCr := testutil.YUVPixelAt(yuv, w, h, x, y)
	assert.InDelta(t, float64(expectY), float64(gotY), float64(tolerance),
		"%s: Y at (%d,%d) expected %d got %d", msg, x, y, expectY, gotY)
	assert.InDelta(t, float64(expectCb), float64(gotCb), float64(tolerance),
		"%s: Cb at (%d,%d) expected %d got %d", msg, x, y, expectCb, gotCb)
	assert.InDelta(t, float64(expectCr), float64(gotCr), float64(tolerance),
		"%s: Cr at (%d,%d) expected %d got %d", msg, x, y, expectCr, gotCr)
}

// ==========================================================================
// Test 1: PIP Position Accuracy
// ==========================================================================

func TestPIPPositionAccuracy(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// Place a PIP slot at position (100, 50) with size 200x100
	l := &layout.Layout{
		Name: "pip-test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(100, 50, 300, 150),
				ZOrder:    1,
				Enabled:   true,
			},
		},
	}
	comp.SetLayout(l)

	// Ingest a solid white source at slot dimensions (no scaling needed)
	src := testutil.GenerateSolidYUV420(200, 100, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 200, 100, 1000)

	// Process on broadcast black background
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// Verify: pixel inside the slot at (100, 50) should be white
	assertPixelColor(t, result, frameW, frameH, 100, 50, whiteY, whiteCb, whiteCr, 1,
		"top-left of PIP slot")

	// Verify: pixel at (299, 149) should be white (bottom-right of slot, exclusive boundary is 300,150)
	assertPixelColor(t, result, frameW, frameH, 299, 149, whiteY, whiteCb, whiteCr, 1,
		"bottom-right of PIP slot")

	// Verify: pixel at center of slot
	assertPixelColor(t, result, frameW, frameH, 200, 100, whiteY, whiteCb, whiteCr, 1,
		"center of PIP slot")

	// Verify: pixel just outside the slot on the left (98, 50) should be black
	assertPixelColor(t, result, frameW, frameH, 98, 50, blackY, blackCb, blackCr, 1,
		"just outside PIP slot left")

	// Verify: pixel just below the slot (100, 150) should be black
	assertPixelColor(t, result, frameW, frameH, 100, 152, blackY, blackCb, blackCr, 1,
		"just below PIP slot")

	// Verify: pixel at origin (0,0) should be black
	assertPixelColor(t, result, frameW, frameH, 0, 0, blackY, blackCb, blackCr, 1,
		"origin should be background")
}

// ==========================================================================
// Test 2: PIP Scale Accuracy
// ==========================================================================

func TestPIPScaleAccuracy(t *testing.T) {
	const frameW, frameH = 640, 480
	const slotW, slotH = 320, 180

	comp := layout.NewCompositor(frameW, frameH)

	// Place a slot at (0,0) with size 320x180
	l := &layout.Layout{
		Name: "scale-test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(0, 0, slotW, slotH),
				ZOrder:    1,
				Enabled:   true,
			},
		},
	}
	comp.SetLayout(l)

	// Ingest a full 1920x1080 white source (requires scaling down to 320x180)
	src := testutil.GenerateSolidYUV420(1920, 1080, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 1920, 1080, 1000)

	// Process on black background
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// The slot content should span exactly 320x180 pixels.
	// Check the last pixel inside the slot (319, 179) is white.
	gotY := yuvPixelY(result, frameW, slotW-1, slotH-1)
	assert.InDelta(t, float64(whiteY), float64(gotY), 2.0,
		"pixel at (%d,%d) should be white (scaled content)", slotW-1, slotH-1)

	// Check pixel just outside the slot boundary is black.
	// Right boundary: (320, 90) should be black.
	gotY = yuvPixelY(result, frameW, slotW, slotH/2)
	assert.Equal(t, blackY, gotY,
		"pixel at (%d,%d) should be black (outside slot right)", slotW, slotH/2)

	// Below boundary: (160, 180) should be black.
	gotY = yuvPixelY(result, frameW, slotW/2, slotH)
	assert.Equal(t, blackY, gotY,
		"pixel at (%d,%d) should be black (below slot)", slotW/2, slotH)

	// Verify interior is consistently white (sample several points)
	for _, pt := range [][2]int{{10, 10}, {160, 90}, {310, 170}} {
		gotY = yuvPixelY(result, frameW, pt[0], pt[1])
		assert.InDelta(t, float64(whiteY), float64(gotY), 2.0,
			"interior pixel at (%d,%d) should be white", pt[0], pt[1])
	}
}

// ==========================================================================
// Test 3: Even Alignment
// ==========================================================================

func TestEvenAlignment(t *testing.T) {
	// EvenAlign function
	t.Run("EvenAlignFunction", func(t *testing.T) {
		require.Equal(t, 0, layout.EvenAlign(0))
		require.Equal(t, 0, layout.EvenAlign(1))
		require.Equal(t, 2, layout.EvenAlign(2))
		require.Equal(t, 2, layout.EvenAlign(3))
		require.Equal(t, 100, layout.EvenAlign(100))
		require.Equal(t, 100, layout.EvenAlign(101))
	})

	// ValidateSlot rejects odd positions/dimensions
	t.Run("ValidateSlotRejectsOdd", func(t *testing.T) {
		const frameW, frameH = 640, 480

		// Odd X origin
		err := layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(101, 0, 201, 100),
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err, "odd X origin should be rejected")
		assert.Contains(t, err.Error(), "even-aligned")

		// Odd Y origin
		err = layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(100, 51, 200, 151),
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err, "odd Y origin should be rejected")

		// Odd width
		err = layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(100, 50, 301, 150),
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err, "odd width should be rejected")

		// Odd height
		err = layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(100, 50, 300, 151),
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err, "odd height should be rejected")

		// Even values should pass
		err = layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(100, 50, 300, 150),
			Enabled: true,
		}, frameW, frameH)
		require.NoError(t, err, "even-aligned slot should be valid")
	})

	// UpdateSlotRect forces even alignment
	t.Run("UpdateSlotRectForcesEven", func(t *testing.T) {
		const frameW, frameH = 640, 480
		comp := layout.NewCompositor(frameW, frameH)

		l := &layout.Layout{
			Name: "test",
			Slots: []layout.Slot{
				{
					SourceKey: "cam1",
					Rect:      image.Rect(100, 50, 300, 150),
					ZOrder:    1,
					Enabled:   true,
				},
			},
		}
		comp.SetLayout(l)

		// Try setting an odd-valued rect via UpdateSlotRect
		err := comp.UpdateSlotRect(0, image.Rect(101, 51, 301, 151))
		require.NoError(t, err, "UpdateSlotRect should not error (it auto-aligns)")

		// Verify the stored rect has even-aligned values
		got := comp.GetLayout().Slots[0].Rect
		require.Equal(t, 0, got.Min.X%2, "Min.X should be even, got %d", got.Min.X)
		require.Equal(t, 0, got.Min.Y%2, "Min.Y should be even, got %d", got.Min.Y)
		require.Equal(t, 0, got.Max.X%2, "Max.X should be even, got %d", got.Max.X)
		require.Equal(t, 0, got.Max.Y%2, "Max.Y should be even, got %d", got.Max.Y)
	})

	// Odd border width rejected
	t.Run("OddBorderWidthRejected", func(t *testing.T) {
		err := layout.ValidateSlot(layout.Slot{
			Rect:   image.Rect(100, 50, 300, 150),
			Border: layout.BorderConfig{Width: 3, ColorY: 235, ColorCb: 128, ColorCr: 128},
		}, 640, 480)
		require.Error(t, err, "odd border width should be rejected")
		assert.Contains(t, err.Error(), "border width")
	})
}

// ==========================================================================
// Test 4: Border Drawing
// ==========================================================================

func TestBorderDrawing(t *testing.T) {
	const frameW, frameH = 640, 480
	const borderWidth = 4

	comp := layout.NewCompositor(frameW, frameH)

	// Use red border color in YUV. BT.709 red: Y=81, Cb=90, Cr=240.
	borderY, borderCb, borderCr := redY, redCb, redCr

	l := &layout.Layout{
		Name: "border-test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(100, 100, 300, 200),
				ZOrder:    1,
				Enabled:   true,
				Border: layout.BorderConfig{
					Width:   borderWidth,
					ColorY:  borderY,
					ColorCb: borderCb,
					ColorCr: borderCr,
				},
			},
		},
	}
	comp.SetLayout(l)

	// Ingest a white source at slot dimensions
	src := testutil.GenerateSolidYUV420(200, 100, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 200, 100, 1000)

	// Process on black background
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// Border is drawn OUTSIDE the rectangle.
	// The slot rect is (100,100)-(300,200).
	// Border of 4px means the outer border region is (96,96)-(304,204).

	// Check top border strip: pixel at (200, 97) should be red-ish (Y ~81)
	// Note: chroma subsampling means Cb/Cr checks need to use even-aligned coordinates
	gotY := yuvPixelY(result, frameW, 200, 97)
	assert.InDelta(t, float64(borderY), float64(gotY), 1.0,
		"top border Y at (200,97)")

	// Check left border strip: pixel at (97, 150) should be red-ish
	gotY = yuvPixelY(result, frameW, 97, 150)
	assert.InDelta(t, float64(borderY), float64(gotY), 1.0,
		"left border Y at (97,150)")

	// Check bottom border strip: pixel at (200, 202) should be red-ish
	gotY = yuvPixelY(result, frameW, 200, 202)
	assert.InDelta(t, float64(borderY), float64(gotY), 1.0,
		"bottom border Y at (200,202)")

	// Check right border strip: pixel at (302, 150) should be red-ish
	gotY = yuvPixelY(result, frameW, 302, 150)
	assert.InDelta(t, float64(borderY), float64(gotY), 1.0,
		"right border Y at (302,150)")

	// Check chroma of border at an even-aligned border position: (98, 98)
	_, gotCb, gotCr := testutil.YUVPixelAt(result, frameW, frameH, 98, 98)
	assert.InDelta(t, float64(borderCb), float64(gotCb), 2.0,
		"border Cb at (98,98)")
	assert.InDelta(t, float64(borderCr), float64(gotCr), 2.0,
		"border Cr at (98,98)")

	// Verify interior of slot is still white (not overwritten by border)
	assertPixelColor(t, result, frameW, frameH, 200, 150, whiteY, whiteCb, whiteCr, 1,
		"slot interior should be white")

	// Verify outside the border is still black
	assertPixelColor(t, result, frameW, frameH, 90, 90, blackY, blackCb, blackCr, 1,
		"outside border should be black")
}

// ==========================================================================
// Test 5: Side-by-Side Preset
// ==========================================================================

func TestSideBySidePreset(t *testing.T) {
	const frameW, frameH = 640, 480

	// Use the SideBySidePreset factory with no gap
	l := layout.SideBySidePreset(frameW, frameH, "srcA", "srcB", 0)
	require.Len(t, l.Slots, 2, "side-by-side should have 2 slots")

	comp := layout.NewCompositor(frameW, frameH)
	comp.SetLayout(l)

	// Ingest distinct colors for each source at slot dimensions.
	// Side-by-side uses ScaleModeFill, which crops to match aspect ratio.
	// Source at 1920x1080 (16:9) into a ~320x480 slot (2:3) will crop.
	// To simplify verification, ingest frames at the exact slot dimensions.
	slotA := l.Slots[0].Rect
	slotB := l.Slots[1].Rect

	srcA := testutil.GenerateSolidYUV420(slotA.Dx(), slotA.Dy(), redY, redCb, redCr)
	srcB := testutil.GenerateSolidYUV420(slotB.Dx(), slotB.Dy(), greenY, greenCb, greenCr)

	comp.IngestSourceFrame("srcA", srcA, slotA.Dx(), slotA.Dy(), 1000)
	comp.IngestSourceFrame("srcB", srcB, slotB.Dx(), slotB.Dy(), 1000)

	// Process on broadcast black background
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// Left half should be red-ish (source A)
	leftCenterX := slotA.Dx() / 2
	leftCenterY := frameH / 2
	assertPixelColor(t, result, frameW, frameH, leftCenterX, leftCenterY, redY, redCb, redCr, 2,
		"left half center should be source A (red)")

	// Right half should be green-ish (source B)
	rightCenterX := slotB.Min.X + slotB.Dx()/2
	rightCenterY := frameH / 2
	assertPixelColor(t, result, frameW, frameH, rightCenterX, rightCenterY, greenY, greenCb, greenCr, 2,
		"right half center should be source B (green)")

	// Verify boundary: last pixel of left slot vs first pixel of right slot
	// With 0 gap, slotA.Max.X == slotB.Min.X
	require.Equal(t, slotA.Max.X, slotB.Min.X, "slots should be adjacent with no gap")

	// Just inside left slot (near boundary)
	gotY := yuvPixelY(result, frameW, slotA.Max.X-2, frameH/2)
	assert.InDelta(t, float64(redY), float64(gotY), 2.0,
		"pixel at left boundary should be source A")

	// Just inside right slot (near boundary)
	gotY = yuvPixelY(result, frameW, slotB.Min.X+2, frameH/2)
	assert.InDelta(t, float64(greenY), float64(gotY), 2.0,
		"pixel at right boundary should be source B")
}

// ==========================================================================
// Test 6: Quad Preset
// ==========================================================================

func TestQuadPreset(t *testing.T) {
	const frameW, frameH = 640, 480

	sources := [4]string{"cam1", "cam2", "cam3", "cam4"}
	l := layout.QuadPreset(frameW, frameH, sources, 0)
	require.Len(t, l.Slots, 4, "quad should have 4 slots")

	comp := layout.NewCompositor(frameW, frameH)
	comp.SetLayout(l)

	// Four distinct colors for the four quadrants.
	colors := [4][3]byte{
		{redY, redCb, redCr},          // cam1 = top-left
		{greenY, greenCb, greenCr},    // cam2 = top-right
		{blueY, blueCb, blueCr},       // cam3 = bottom-left
		{yellowY, yellowCb, yellowCr}, // cam4 = bottom-right
	}

	// Ingest each source at its exact slot dimensions to avoid scaling artifacts
	for i, src := range sources {
		slot := l.Slots[i]
		frame := testutil.GenerateSolidYUV420(slot.Rect.Dx(), slot.Rect.Dy(), colors[i][0], colors[i][1], colors[i][2])
		comp.IngestSourceFrame(src, frame, slot.Rect.Dx(), slot.Rect.Dy(), 1000)
	}

	// Process
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// Verify each quadrant center has the correct color.
	// Allow tolerance=3 for possible bilinear scaling at boundaries.
	tolerance := byte(3)

	for i, src := range sources {
		slot := l.Slots[i]
		cx := slot.Rect.Min.X + slot.Rect.Dx()/2
		cy := slot.Rect.Min.Y + slot.Rect.Dy()/2
		// Ensure center coordinates are even for clean chroma lookup
		cx = layout.EvenAlign(cx)
		cy = layout.EvenAlign(cy)

		assertPixelColor(t, result, frameW, frameH, cx, cy,
			colors[i][0], colors[i][1], colors[i][2], tolerance,
			"quadrant center for "+src)
	}

	// Verify quad slots tile the frame (with 0 gap, slots should cover entire frame).
	// Check that adjacent slot boundaries are contiguous.
	require.Equal(t, l.Slots[0].Rect.Max.X, l.Slots[1].Rect.Min.X,
		"top-left and top-right should be adjacent")
	require.Equal(t, l.Slots[0].Rect.Max.Y, l.Slots[2].Rect.Min.Y,
		"top-left and bottom-left should be adjacent")
}

// ==========================================================================
// Test 7: Background Fill
// ==========================================================================

func TestBackgroundFill(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// Layout with a small PIP slot in the center, leaving most of frame as background
	l := &layout.Layout{
		Name: "small-pip",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(200, 150, 440, 330),
				ZOrder:    1,
				Enabled:   true,
			},
		},
	}
	comp.SetLayout(l)

	// Ingest white source
	src := testutil.GenerateSolidYUV420(240, 180, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 240, 180, 1000)

	// Process on broadcast black background
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// Areas outside the slot should be broadcast black.
	// Check corners of the frame.
	corners := [][2]int{
		{0, 0},                   // top-left
		{frameW - 2, 0},          // top-right
		{0, frameH - 2},          // bottom-left
		{frameW - 2, frameH - 2}, // bottom-right
	}
	for _, pt := range corners {
		assertPixelColor(t, result, frameW, frameH, pt[0], pt[1],
			blackY, blackCb, blackCr, 0,
			"background corner")
	}

	// Check a point above the slot
	assertPixelColor(t, result, frameW, frameH, 320, 10, blackY, blackCb, blackCr, 0,
		"above slot should be black")

	// Check a point below the slot
	assertPixelColor(t, result, frameW, frameH, 320, 470, blackY, blackCb, blackCr, 0,
		"below slot should be black")

	// Also verify the slot area isn't black (sanity check)
	gotY := yuvPixelY(result, frameW, 320, 240)
	assert.Equal(t, whiteY, gotY, "slot center should be white, not black")
}

// ==========================================================================
// Test 8: Slot Animation (dissolve on/off)
// ==========================================================================

func TestSlotAnimation(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// Start with slot disabled, dissolve transition configured
	l := &layout.Layout{
		Name: "anim-test",
		Slots: []layout.Slot{
			{
				SourceKey:  "cam1",
				Rect:       image.Rect(200, 150, 440, 330),
				ZOrder:     1,
				Enabled:    false,
				Transition: layout.SlotTransition{Type: "dissolve", DurationMs: 300},
			},
		},
	}
	comp.SetLayout(l)

	// Ingest a white source
	src := testutil.GenerateSolidYUV420(240, 180, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 240, 180, 1000)

	// Before animation: slot is disabled, should render background only.
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)
	gotY := yuvPixelY(result, frameW, 320, 240)
	assert.Equal(t, blackY, gotY, "before SlotOn: slot area should be black")

	// Trigger dissolve-in animation
	comp.SlotOn(0)

	// Immediately after SlotOn, the slot is enabled but animation is still in progress.
	// Process a frame — it should be partially blended (between black and white).
	bg = testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result = comp.ProcessFrame(bg, frameW, frameH)
	gotY = yuvPixelY(result, frameW, 320, 240)
	// During dissolve: Y should be somewhere between 16 (black) and 235 (white)
	// At the very start of a 300ms dissolve, alpha is very small, so Y is close to black.
	// Just verify it's in a valid range.
	assert.GreaterOrEqual(t, gotY, blackY, "during dissolve: Y should be >= black")

	// Wait for animation to complete
	time.Sleep(350 * time.Millisecond)

	// After animation completes: should be fully white
	bg = testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result = comp.ProcessFrame(bg, frameW, frameH)
	gotY = yuvPixelY(result, frameW, 320, 240)
	assert.InDelta(t, float64(whiteY), float64(gotY), 1.0,
		"after dissolve complete: slot should be fully white")

	// Now trigger dissolve-off
	comp.SlotOff(0)

	// Immediately after: should still be partially visible
	bg = testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result = comp.ProcessFrame(bg, frameW, frameH)
	gotY = yuvPixelY(result, frameW, 320, 240)
	// During dissolve-out: Y should be between black and white
	assert.GreaterOrEqual(t, gotY, blackY, "during dissolve-off: Y should be >= black")

	// Wait for animation to complete
	time.Sleep(350 * time.Millisecond)

	// After dissolve-off completes, the OnComplete callback fires during the
	// next ProcessFrame's tickAnimations(). That updates the layout pointer,
	// but the local layout snapshot for *that* ProcessFrame was already read.
	// So we need two ProcessFrame calls: one to trigger OnComplete, and
	// another to see the disabled slot.
	bg = testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	_ = comp.ProcessFrame(bg, frameW, frameH) // triggers OnComplete, disables slot

	bg = testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result = comp.ProcessFrame(bg, frameW, frameH)
	gotY = yuvPixelY(result, frameW, 320, 240)
	assert.Equal(t, blackY, gotY, "after dissolve-off complete: slot area should be black")
}

// ==========================================================================
// Test 9: Crop Region
// ==========================================================================

func TestCropRegion(t *testing.T) {
	// Test ComputeCropRect for various aspect ratio mismatches.

	t.Run("WiderSourceCropsHorizontally", func(t *testing.T) {
		// Source is 1920x1080 (16:9), slot is 400x400 (1:1)
		// Source is wider than slot, should crop horizontally.
		cropX, cropY, cropW, cropH := layout.ComputeCropRect(1920, 1080, 400, 400, 0.5, 0.5)

		// Crop should be 1080x1080 (matching slot 1:1 aspect) centered.
		require.Equal(t, 0, cropW%2, "cropW should be even")
		require.Equal(t, 0, cropH%2, "cropH should be even")
		require.Equal(t, 0, cropX%2, "cropX should be even")
		require.Equal(t, 0, cropY%2, "cropY should be even")

		// Aspect ratio of crop should match slot (1:1)
		aspectRatio := float64(cropW) / float64(cropH)
		assert.InDelta(t, 1.0, aspectRatio, 0.02,
			"crop aspect ratio should match slot (1:1)")

		// Crop height should be full source height (no vertical crop needed)
		assert.Equal(t, layout.EvenAlign(1080), cropH, "should use full source height")

		// Centered horizontally
		expectedX := layout.EvenAlign((1920 - cropW) / 2)
		assert.Equal(t, expectedX, cropX, "crop should be centered horizontally")
		assert.Equal(t, 0, cropY, "crop should start at top")
	})

	t.Run("TallerSourceCropsVertically", func(t *testing.T) {
		// Source is 1080x1920 (9:16), slot is 400x400 (1:1)
		// Source is taller than slot, should crop vertically.
		cropX, cropY, cropW, cropH := layout.ComputeCropRect(1080, 1920, 400, 400, 0.5, 0.5)

		require.Equal(t, 0, cropW%2, "cropW should be even")
		require.Equal(t, 0, cropH%2, "cropH should be even")

		// Aspect ratio should match slot (1:1)
		aspectRatio := float64(cropW) / float64(cropH)
		assert.InDelta(t, 1.0, aspectRatio, 0.02,
			"crop aspect ratio should match slot (1:1)")

		// Crop width should be full source width
		assert.Equal(t, layout.EvenAlign(1080), cropW, "should use full source width")

		// No horizontal crop needed since we use the full width
		assert.Equal(t, 0, cropX, "no horizontal crop offset for taller source")

		// Centered vertically
		expectedY := layout.EvenAlign((1920 - cropH) / 2)
		assert.Equal(t, expectedY, cropY, "crop should be centered vertically")
	})

	t.Run("MatchingAspectNoCrop", func(t *testing.T) {
		// Source and slot have same 16:9 aspect ratio
		cropX, cropY, cropW, cropH := layout.ComputeCropRect(1920, 1080, 320, 180, 0.5, 0.5)

		assert.Equal(t, 0, cropX, "no horizontal crop needed")
		assert.Equal(t, 0, cropY, "no vertical crop needed")
		assert.Equal(t, layout.EvenAlign(1920), cropW, "full width")
		assert.Equal(t, layout.EvenAlign(1080), cropH, "full height")
	})

	t.Run("AnchorTopLeft", func(t *testing.T) {
		// 1920x1080 source, 1:1 slot, anchor at top-left
		cropX, cropY, _, _ := layout.ComputeCropRect(1920, 1080, 400, 400, 0.0, 0.0)
		assert.Equal(t, 0, cropX, "top-left anchor: cropX should be 0")
		assert.Equal(t, 0, cropY, "top-left anchor: cropY should be 0")
	})

	t.Run("AnchorBottomRight", func(t *testing.T) {
		// 1920x1080 source, 1:1 slot, anchor at bottom-right
		cropX, _, cropW, cropH := layout.ComputeCropRect(1920, 1080, 400, 400, 1.0, 1.0)

		// For bottom-right anchor, crop should be at the right edge
		// cropX should be maxX = srcW - cropW
		expectedX := layout.EvenAlign(1920 - cropW)
		assert.Equal(t, expectedX, cropX, "bottom-right anchor: crop should be at right edge")
		// cropH should use full height, so cropY = 0
		assert.Equal(t, layout.EvenAlign(1080), cropH, "should use full source height")
	})

	t.Run("AllOutputsEvenAligned", func(t *testing.T) {
		// Sweep various dimensions and verify all outputs are even
		cases := [][4]int{
			{1920, 1080, 320, 240},
			{1280, 720, 400, 300},
			{800, 600, 160, 90},
			{640, 360, 200, 200},
			{1920, 1080, 1, 1}, // extreme case
		}
		for _, c := range cases {
			cropX, cropY, cropW, cropH := layout.ComputeCropRect(c[0], c[1], c[2], c[3], 0.5, 0.5)
			assert.Equal(t, 0, cropX%2, "cropX even for %v", c)
			assert.Equal(t, 0, cropY%2, "cropY even for %v", c)
			assert.Equal(t, 0, cropW%2, "cropW even for %v", c)
			assert.Equal(t, 0, cropH%2, "cropH even for %v", c)
		}
	})
}

// ==========================================================================
// Test: No-signal slots render broadcast black
// ==========================================================================

func TestNoSignalSlotsRenderBlack(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// Layout with a slot assigned to a source that never sends frames
	l := &layout.Layout{
		Name: "no-signal-test",
		Slots: []layout.Slot{
			{
				SourceKey: "missing-cam",
				Rect:      image.Rect(100, 100, 300, 300),
				ZOrder:    1,
				Enabled:   true,
			},
		},
	}
	comp.SetLayout(l)

	// Process on a white background so we can verify the slot area is overwritten
	bg := testutil.GenerateSolidYUV420(frameW, frameH, whiteY, whiteCb, whiteCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// The slot region should be broadcast black (Y=16, Cb=128, Cr=128)
	assertPixelColor(t, result, frameW, frameH, 200, 200, blackY, blackCb, blackCr, 0,
		"no-signal slot center should be broadcast black")

	// Outside the slot should remain white (the original background)
	assertPixelColor(t, result, frameW, frameH, 50, 50, whiteY, whiteCb, whiteCr, 0,
		"outside no-signal slot should be unchanged")
}

// ==========================================================================
// Test: Preset factories produce valid layouts
// ==========================================================================

func TestPresetFactoriesValidity(t *testing.T) {
	const frameW, frameH = 1920, 1080

	t.Run("PIPPreset", func(t *testing.T) {
		for _, pos := range []string{"top-left", "top-right", "bottom-left", "bottom-right"} {
			l := layout.PIPPreset(frameW, frameH, "cam1", pos, 0.25)
			require.NotNil(t, l)
			require.Len(t, l.Slots, 1)

			err := layout.ValidateLayout(l, frameW, frameH)
			require.NoError(t, err, "PIP preset %q should be valid", pos)

			// Verify slot dimensions are approximately 25% of frame width
			slot := l.Slots[0]
			expectedW := layout.EvenAlign(int(float64(frameW) * 0.25))
			assert.Equal(t, expectedW, slot.Rect.Dx(), "PIP width for %q", pos)
		}
	})

	t.Run("SideBySidePreset", func(t *testing.T) {
		for _, gap := range []int{0, 4, 8} {
			l := layout.SideBySidePreset(frameW, frameH, "a", "b", gap)
			require.NotNil(t, l)
			require.Len(t, l.Slots, 2)

			err := layout.ValidateLayout(l, frameW, frameH)
			require.NoError(t, err, "SideBySide with gap=%d should be valid", gap)

			// Both slots should span the full frame height
			assert.Equal(t, frameH, l.Slots[0].Rect.Dy(), "left slot height")
			assert.Equal(t, frameH, l.Slots[1].Rect.Dy(), "right slot height")
		}
	})

	t.Run("QuadPreset", func(t *testing.T) {
		for _, gap := range []int{0, 2, 6} {
			l := layout.QuadPreset(frameW, frameH, [4]string{"a", "b", "c", "d"}, gap)
			require.NotNil(t, l)
			require.Len(t, l.Slots, 4)

			err := layout.ValidateLayout(l, frameW, frameH)
			require.NoError(t, err, "Quad with gap=%d should be valid", gap)
		}
	})

	t.Run("BuiltinPresets", func(t *testing.T) {
		names := layout.BuiltinPresets()
		require.NotEmpty(t, names)

		for _, name := range names {
			l := layout.ResolveBuiltinPreset(name, frameW, frameH)
			require.NotNil(t, l, "preset %q should resolve", name)

			err := layout.ValidateLayout(l, frameW, frameH)
			require.NoError(t, err, "preset %q should be valid", name)
		}
	})
}

// ==========================================================================
// Test: ZOrder compositing order
// ==========================================================================

func TestZOrderCompositing(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// Two overlapping slots at the same position, different z-orders.
	// Slot with higher z-order should be on top (painted last).
	l := &layout.Layout{
		Name: "zorder-test",
		Slots: []layout.Slot{
			{
				SourceKey: "bottom-src",
				Rect:      image.Rect(100, 100, 300, 300),
				ZOrder:    0, // lower z-order = painted first
				Enabled:   true,
			},
			{
				SourceKey: "top-src",
				Rect:      image.Rect(100, 100, 300, 300),
				ZOrder:    1, // higher z-order = painted last (on top)
				Enabled:   true,
			},
		},
	}
	comp.SetLayout(l)

	// Bottom source: red
	srcBot := testutil.GenerateSolidYUV420(200, 200, redY, redCb, redCr)
	comp.IngestSourceFrame("bottom-src", srcBot, 200, 200, 1000)

	// Top source: green (should be visible due to higher z-order)
	srcTop := testutil.GenerateSolidYUV420(200, 200, greenY, greenCb, greenCr)
	comp.IngestSourceFrame("top-src", srcTop, 200, 200, 1000)

	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// The overlapping area should show the top source (green), not the bottom (red)
	assertPixelColor(t, result, frameW, frameH, 200, 200, greenY, greenCb, greenCr, 2,
		"overlapping area should show higher z-order source (green)")
}

// ==========================================================================
// Test: Layout validation edge cases
// ==========================================================================

func TestLayoutValidation(t *testing.T) {
	const frameW, frameH = 640, 480

	t.Run("ExceedsFrameBounds", func(t *testing.T) {
		err := layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(600, 0, 700, 100), // extends past frame width
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds frame bounds")
	})

	t.Run("NegativePosition", func(t *testing.T) {
		err := layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(-10, 0, 100, 100),
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err)
	})

	t.Run("ZeroDimensions", func(t *testing.T) {
		err := layout.ValidateSlot(layout.Slot{
			Rect:    image.Rect(100, 100, 100, 100), // zero width and height
			Enabled: true,
		}, frameW, frameH)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zero or negative")
	})

	t.Run("TooManySlots", func(t *testing.T) {
		l := &layout.Layout{
			Name:  "too-many",
			Slots: make([]layout.Slot, layout.MaxSlots+1),
		}
		// Fill with valid slots
		for i := range l.Slots {
			l.Slots[i] = layout.Slot{
				Rect:    image.Rect(0, 0, 100, 100),
				Enabled: true,
			}
		}
		err := layout.ValidateLayout(l, frameW, frameH)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max")
	})

	t.Run("UnknownScaleMode", func(t *testing.T) {
		err := layout.ValidateSlot(layout.Slot{
			Rect:      image.Rect(0, 0, 100, 100),
			ScaleMode: "invalid",
			Enabled:   true,
		}, frameW, frameH)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scaleMode")
	})

	t.Run("CropAnchorOutOfRange", func(t *testing.T) {
		err := layout.ValidateSlot(layout.Slot{
			Rect:       image.Rect(0, 0, 100, 100),
			CropAnchor: [2]float64{1.5, 0.5},
			Enabled:    true,
		}, frameW, frameH)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cropAnchor")
	})
}

// ==========================================================================
// Test: CropYUV420Region correctness
// ==========================================================================

func TestCropYUV420RegionCorrectness(t *testing.T) {
	const srcW, srcH = 640, 480

	// Create a source with two halves: left=red, right=green
	src := make([]byte, srcW*srcH*3/2)
	ySize := srcW * srcH
	chromaW := srcW / 2
	chromaH := srcH / 2
	halfW := srcW / 2

	// Y plane: left half = redY, right half = greenY
	for row := 0; row < srcH; row++ {
		for col := 0; col < srcW; col++ {
			if col < halfW {
				src[row*srcW+col] = redY
			} else {
				src[row*srcW+col] = greenY
			}
		}
	}
	// Cb plane
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			if col < chromaW/2 {
				src[ySize+row*chromaW+col] = redCb
			} else {
				src[ySize+row*chromaW+col] = greenCb
			}
		}
	}
	// Cr plane
	crStart := ySize + chromaW*chromaH
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			if col < chromaW/2 {
				src[crStart+row*chromaW+col] = redCr
			} else {
				src[crStart+row*chromaW+col] = greenCr
			}
		}
	}

	// Crop the left half: (0, 0) to (320, 480)
	cropW, cropH := 320, 480
	dst := make([]byte, cropW*cropH*3/2)
	layout.CropYUV420Region(dst, src, srcW, srcH, 0, 0, cropW, cropH)

	// All pixels in cropped region should be red
	for row := 0; row < cropH; row += 20 { // sample every 20 rows
		for col := 0; col < cropW; col += 20 {
			gotY, gotCb, gotCr := testutil.YUVPixelAt(dst, cropW, cropH, col, row)
			assert.Equal(t, redY, gotY, "Y at (%d,%d) in left crop", col, row)
			assert.Equal(t, redCb, gotCb, "Cb at (%d,%d) in left crop", col, row)
			assert.Equal(t, redCr, gotCr, "Cr at (%d,%d) in left crop", col, row)
		}
	}

	// Crop the right half: (320, 0) to (320, 480)
	dst2 := make([]byte, cropW*cropH*3/2)
	layout.CropYUV420Region(dst2, src, srcW, srcH, 320, 0, cropW, cropH)

	// All pixels should be green
	for row := 0; row < cropH; row += 20 {
		for col := 0; col < cropW; col += 20 {
			gotY, gotCb, gotCr := testutil.YUVPixelAt(dst2, cropW, cropH, col, row)
			assert.Equal(t, greenY, gotY, "Y at (%d,%d) in right crop", col, row)
			assert.Equal(t, greenCb, gotCb, "Cb at (%d,%d) in right crop", col, row)
			assert.Equal(t, greenCr, gotCr, "Cr at (%d,%d) in right crop", col, row)
		}
	}
}

// ==========================================================================
// Test: FillMode compositor integration
// ==========================================================================

func TestFillModeCompositor(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// Slot with fill mode: 200x200 square in a 16:9 source should crop source
	l := &layout.Layout{
		Name: "fill-test",
		Slots: []layout.Slot{
			{
				SourceKey:  "cam1",
				Rect:       image.Rect(100, 100, 300, 300),
				ZOrder:     1,
				Enabled:    true,
				ScaleMode:  layout.ScaleModeFill,
				CropAnchor: [2]float64{0.5, 0.5},
			},
		},
	}
	comp.SetLayout(l)

	// Ingest a 1920x1080 white source
	src := testutil.GenerateSolidYUV420(1920, 1080, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 1920, 1080, 1000)

	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	result := comp.ProcessFrame(bg, frameW, frameH)

	// The fill-mode slot should contain white (source) content
	gotY := yuvPixelY(result, frameW, 200, 200)
	assert.InDelta(t, float64(whiteY), float64(gotY), 2.0,
		"fill-mode slot center should be white")

	// Outside slot should be black
	assertPixelColor(t, result, frameW, frameH, 50, 50, blackY, blackCb, blackCr, 0,
		"outside fill-mode slot should be black")
}

// ==========================================================================
// Test: Active state transitions
// ==========================================================================

func TestActiveStateTransitions(t *testing.T) {
	comp := layout.NewCompositor(640, 480)

	// Initially no layout = not active
	require.False(t, comp.Active(), "no layout = not active")

	// Set layout with no enabled slots = not active
	l := &layout.Layout{
		Name: "test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(0, 0, 100, 100),
				Enabled:   false,
			},
		},
	}
	comp.SetLayout(l)
	require.False(t, comp.Active(), "no enabled slots = not active")

	// Enable slot via SlotOn
	comp.SlotOn(0)
	require.True(t, comp.Active(), "one enabled slot = active")

	// Disable via cut SlotOff
	// Need to set transition to "cut" first
	comp.UpdateSlot(0, func(s *layout.Slot) {
		s.Transition = layout.SlotTransition{Type: "cut"}
	})
	comp.SlotOff(0)
	require.False(t, comp.Active(), "slot off (cut) = not active")

	// Clear layout
	comp.SetLayout(nil)
	require.False(t, comp.Active(), "nil layout = not active")
}

// ==========================================================================
// Determinism: TestLayoutDeterminism — same PIP slot + source frame
//   processed 20 times → byte-exact output every run.
// ==========================================================================

func TestLayoutDeterminism(t *testing.T) {
	const frameW, frameH = 640, 480

	comp := layout.NewCompositor(frameW, frameH)

	// PIP slot at (100, 50) with size 200x100
	l := &layout.Layout{
		Name: "det-test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(100, 50, 300, 150),
				ZOrder:    1,
				Enabled:   true,
			},
		},
	}
	comp.SetLayout(l)

	// Ingest a solid white source at slot dimensions (no scaling needed).
	src := testutil.GenerateSolidYUV420(200, 100, whiteY, whiteCb, whiteCr)
	comp.IngestSourceFrame("cam1", src, 200, 100, 1000)

	// First run — reference result.
	bg := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
	reference := make([]byte, len(bg))
	result := comp.ProcessFrame(bg, frameW, frameH)
	copy(reference, result)

	// 19 more runs — must be byte-exact identical.
	for i := 0; i < 19; i++ {
		bgI := testutil.GenerateSolidYUV420(frameW, frameH, blackY, blackCb, blackCr)
		resultI := comp.ProcessFrame(bgI, frameW, frameH)
		if !testutil.YUVFrameEqual(reference, resultI) {
			t.Fatalf("Run %d produced different layout composite output (non-determinism detected)", i+1)
		}
	}
}

// ==========================================================================
// Test: ProcessFrame returns input unchanged when no layout
// ==========================================================================

func TestProcessFrameNoLayout(t *testing.T) {
	comp := layout.NewCompositor(640, 480)

	bg := testutil.GenerateSolidYUV420(640, 480, 100, 128, 128)
	original := make([]byte, len(bg))
	copy(original, bg)

	result := comp.ProcessFrame(bg, 640, 480)

	require.True(t, testutil.YUVFrameEqual(original, result),
		"ProcessFrame with no layout should return frame unchanged")
}
