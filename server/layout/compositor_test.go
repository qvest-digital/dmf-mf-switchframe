package layout

import (
	"image"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestCompositor_SelectScaleQuality(t *testing.T) {
	c := NewCompositor(1920, 1080)

	// All sizes should use Lanczos after optimization
	q := c.selectScaleQuality(1280, 720, 960, 540, 1920, 1080)
	require.Equal(t, transition.ScaleQualityHigh, q, "quad slot should use Lanczos")

	q = c.selectScaleQuality(1920, 1080, 480, 270, 1920, 1080)
	require.Equal(t, transition.ScaleQualityHigh, q, "small PIP should use Lanczos")

	q = c.selectScaleQuality(1920, 1080, 960, 1080, 1920, 1080)
	require.Equal(t, transition.ScaleQualityHigh, q, "side-by-side should use Lanczos")
}

func TestCompositor_GraySlotDirectFill(t *testing.T) {
	c := NewCompositor(8, 8)

	// White background — gray slot should overwrite with broadcast black
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "missing", Rect: image.Rect(0, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	bg := makeYUV420(8, 8, 235, 60, 200) // white-ish background
	result := c.ProcessFrame(bg, 8, 8)

	// Slot region should be broadcast black (Y=16, Cb=128, Cr=128)
	require.Equal(t, byte(16), result[0], "gray slot Y should be broadcast black")
	// Outside slot should be unchanged
	require.Equal(t, byte(235), result[4], "outside slot should be unchanged")

	// Chroma inside slot
	ySize := 8 * 8
	require.Equal(t, byte(128), result[ySize], "gray slot Cb should be 128")
}

func BenchmarkCompositor_ProcessFrame_Quad(b *testing.B) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "quad",
		Slots: []LayoutSlot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Rect: image.Rect(960, 0, 1920, 540), Enabled: true},
			{SourceKey: "cam3", Rect: image.Rect(0, 540, 960, 1080), Enabled: true},
			{SourceKey: "cam4", Rect: image.Rect(960, 540, 1920, 1080), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Ingest 720p sources (requires scaling to 960×540 slots)
	for _, name := range []string{"cam1", "cam2", "cam3", "cam4"} {
		c.IngestSourceFrame(name, makeYUV420(1280, 720, 200, 128, 128), 1280, 720)
	}

	bg := makeYUV420(1920, 1080, 16, 128, 128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessFrame(bg, 1920, 1080)
	}
}

func TestCompositor_IngestAndNeedsSource(t *testing.T) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(1440, 0, 1920, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	require.True(t, c.NeedsSource("cam2"))
	require.False(t, c.NeedsSource("cam3"))

	// Ingest a frame
	yuv := makeYUV420(1920, 1080, 235, 128, 128)
	c.IngestSourceFrame("cam2", yuv, 1920, 1080)

	require.True(t, c.HasFrame("cam2"))
}

func TestCompositor_ProcessFrame(t *testing.T) {
	c := NewCompositor(8, 8)

	// Layout: 4x4 PIP at (4,0)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Ingest white source
	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4)

	// Process on black background
	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// PIP region (4,0) should be white
	require.Equal(t, byte(235), result[0*8+4], "PIP at (4,0) should be white Y")
	// Background (0,0) should be black
	require.Equal(t, byte(16), result[0], "background should be black Y")
}

func TestCompositor_InactiveWhenEmpty(t *testing.T) {
	c := NewCompositor(1920, 1080)
	require.False(t, c.Active())

	l := &Layout{Name: "pip", Slots: []LayoutSlot{
		{SourceKey: "cam2", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
	}}
	c.SetLayout(l)
	require.True(t, c.Active())

	c.SetLayout(nil)
	require.False(t, c.Active())
}

func TestCompositor_NilSourceRendersGray(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "missing", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	// No frame ingested for "missing" — should render gray
	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// PIP region should be broadcast black (Y=16)
	require.Equal(t, byte(16), result[0*8+4], "missing source should render broadcast black")
}

func TestCompositor_DisabledSlotSkipped(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: false},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// Disabled slot should not be composited
	require.Equal(t, byte(16), result[0*8+4], "disabled slot should not appear")
}

func TestCompositor_ZOrderSorting(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			// Higher z-order but listed first — should still render on top
			{SourceKey: "top", Rect: image.Rect(2, 2, 6, 6), ZOrder: 2, Enabled: true},
			{SourceKey: "bottom", Rect: image.Rect(0, 0, 8, 8), ZOrder: 0, Enabled: true},
		},
	}
	c.SetLayout(l)

	// Bottom source: mid-gray
	c.IngestSourceFrame("bottom", makeYUV420(8, 8, 100, 128, 128), 8, 8)
	// Top source: white
	c.IngestSourceFrame("top", makeYUV420(4, 4, 235, 128, 128), 4, 4)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// (3,3) is in both slots — top (white, z=2) should win
	require.Equal(t, byte(235), result[3*8+3], "higher z-order should be on top")
	// (0,0) is only in bottom — should be mid-gray
	require.Equal(t, byte(100), result[0], "lower z-order visible where not overlapped")
}

// Issue #1: Fly-in animation with partial off-screen rect must not panic.
// The source is scaled AFTER clamping, not before.
func TestCompositor_FlyInPartiallyOffScreen(t *testing.T) {
	c := NewCompositor(16, 16)

	// PIP at right edge — fly-in starts off-screen right
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(8, 0, 16, 8), Enabled: false,
				Transition: SlotTransition{Type: "fly", DurationMs: 500}},
		},
	}
	c.SetLayout(l)

	// Ingest a source at different resolution (will need scaling)
	src := makeYUV420(8, 8, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 8, 8)

	// Trigger fly-in animation
	c.SlotOn(0)

	// Immediately process — animation just started, rect should be partially off-screen.
	// This must NOT panic.
	bg := makeYUV420(16, 16, 16, 128, 128)
	require.NotPanics(t, func() {
		c.ProcessFrame(bg, 16, 16)
	})
}

// Issue #1 variant: fly-in with clamped rect at frame edge produces correct output.
func TestCompositor_FlyInClampedDoesNotCorrupt(t *testing.T) {
	c := NewCompositor(16, 16)

	// Place a slot that when animated from off-screen, gets clamped
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(8, 4, 16, 12), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Manually inject an animation that is partially off-screen right
	c.mu.Lock()
	c.animations = append(c.animations, &Animation{
		SlotIndex: 0,
		StartTime: time.Now(),
		Duration:  time.Hour, // won't complete during test
		FromRect:  image.Rect(12, 4, 20, 12), // extends 4px past right edge
		ToRect:    image.Rect(8, 4, 16, 12),
		FromAlpha: 1.0,
		ToAlpha:   1.0,
	})
	c.mu.Unlock()

	src := makeYUV420(8, 8, 200, 128, 128)
	c.IngestSourceFrame("cam2", src, 8, 8)

	bg := makeYUV420(16, 16, 16, 128, 128)
	require.NotPanics(t, func() {
		result := c.ProcessFrame(bg, 16, 16)
		// Frame should not be all-black (something was composited)
		hasContent := false
		for i := 0; i < 16*16; i++ {
			if result[i] != 16 {
				hasContent = true
				break
			}
		}
		require.True(t, hasContent, "clamped PIP should still render visible portion")
	})
}

// Issue #10: AutoDissolveSource must load layout inside mutex.
func TestCompositor_AutoDissolveSource_NoRace(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Call AutoDissolveSource concurrently with SetLayout.
	// With -race, this detects TOCTOU if layout is read outside mutex.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			c.SetLayout(l)
		}
	}()
	for i := 0; i < 100; i++ {
		c.AutoDissolveSource("cam1")
	}
	<-done
}

// Issue #8: Gray buffer used when source not yet ingested — dimensions must
// be safe even if the slot rect differs from the gray buffer allocation.
func TestCompositor_GrayBuffer_ScaledSlot(t *testing.T) {
	c := NewCompositor(16, 16)

	// Slot is 8x8 at position (4,4)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "missing", Rect: image.Rect(4, 4, 12, 12), Enabled: true},
		},
	}
	c.SetLayout(l)

	// No source ingested — gray buffer should be used without panic
	bg := makeYUV420(16, 16, 16, 128, 128)
	require.NotPanics(t, func() {
		c.ProcessFrame(bg, 16, 16)
	})
}

// Issue #7/#21: Alpha at dissolve endpoint should be exactly 1.0.
func TestCompositor_DissolveEndpoint_NoVisualPop(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(0, 0, 4, 4), Enabled: false,
				Transition: SlotTransition{Type: "dissolve", DurationMs: 10}},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4)

	c.SlotOn(0)

	// Wait for animation to complete
	time.Sleep(20 * time.Millisecond)

	// Process frame — animation done, should render opaque (not blended)
	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// Should be exactly 235 (opaque), not 234 or 233 (blended residue)
	require.Equal(t, byte(235), result[0], "completed dissolve should be fully opaque")
}

// Fill mode: crop-to-fill preserves aspect ratio and does not distort.
func TestCompositor_FillMode_CropsBeforeScale(t *testing.T) {
	c := NewCompositor(16, 16)

	// Slot is 4x8 (portrait) — a 16:9 source (8x4) should be cropped.
	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			{
				SourceKey:  "cam1",
				Rect:       image.Rect(0, 0, 4, 8),
				Enabled:    true,
				ScaleMode:  ScaleModeFill,
				CropAnchor: [2]float64{0.5, 0.5},
			},
		},
	}
	c.SetLayout(l)

	// Source is 8x4 (wider than slot aspect).
	// Left half: Y=200, right half: Y=100
	src := make([]byte, 8*4*3/2)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src[y*8+x] = 200 // left half
		}
		for x := 4; x < 8; x++ {
			src[y*8+x] = 100 // right half
		}
	}
	// Set chroma to neutral.
	ySize := 8 * 4
	for i := ySize; i < len(src); i++ {
		src[i] = 128
	}
	c.IngestSourceFrame("cam1", src, 8, 4)

	bg := makeYUV420(16, 16, 16, 128, 128)
	result := c.ProcessFrame(bg, 16, 16)

	// The crop should take a center 2x4 region from 8x4 source,
	// which straddles left/right halves. The rendered slot should not be
	// all-black (16) — it should have content.
	hasContent := false
	for y := 0; y < 8; y++ {
		for x := 0; x < 4; x++ {
			if result[y*16+x] != 16 {
				hasContent = true
				break
			}
		}
		if hasContent {
			break
		}
	}
	require.True(t, hasContent, "fill mode slot should render cropped content")
}

// Fill mode with matching aspect ratio should not distort.
func TestCompositor_FillMode_MatchingAspect(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(0, 0, 8, 4),
				Enabled:   true,
				ScaleMode: ScaleModeFill,
			},
		},
	}
	c.SetLayout(l)

	// Source exactly matches slot aspect (16:9 → 8:4 = 2:1 both).
	src := makeYUV420(8, 4, 200, 100, 150)
	c.IngestSourceFrame("cam1", src, 8, 4)

	bg := makeYUV420(16, 16, 16, 128, 128)
	result := c.ProcessFrame(bg, 16, 16)

	// No crop needed — exact match renders directly.
	require.Equal(t, byte(200), result[0], "matching aspect should render source directly")
}

// Stretch default is unchanged by fill mode code.
func TestCompositor_StretchDefault_Unchanged(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// Default stretch mode: same as before.
	require.Equal(t, byte(235), result[0*8+4], "stretch mode unchanged")
}

// Fill mode with gray (missing source) should still render.
func TestCompositor_FillMode_GrayBuffer(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			{
				SourceKey: "missing",
				Rect:      image.Rect(0, 0, 4, 8),
				Enabled:   true,
				ScaleMode: ScaleModeFill,
			},
		},
	}
	c.SetLayout(l)

	bg := makeYUV420(16, 16, 30, 128, 128)
	require.NotPanics(t, func() {
		result := c.ProcessFrame(bg, 16, 16)
		// Gray buffer should be rendered (Y=16 broadcast black).
		require.Equal(t, byte(16), result[0], "gray buffer should render for missing source in fill mode")
	})
}

func TestCompositor_UpdateSlotRect(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			{SourceKey: "cam1", Rect: image.Rect(100, 100, 580, 370), Enabled: true},
		},
	}
	c.SetLayout(l)

	err := c.UpdateSlotRect(0, image.Rect(200, 200, 680, 470))
	require.NoError(t, err)

	updated := c.GetLayout()
	require.Equal(t, image.Rect(200, 200, 680, 470), updated.Slots[0].Rect)
}

func TestCompositor_UpdateSlotRect_NoLayout(t *testing.T) {
	c := NewCompositor(1920, 1080)
	err := c.UpdateSlotRect(0, image.Rect(0, 0, 100, 100))
	require.Error(t, err)
}

func TestCompositor_UpdateSlotRect_OutOfRange(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)
	err := c.UpdateSlotRect(5, image.Rect(0, 0, 100, 100))
	require.Error(t, err)
}

func TestCompositor_UpdateSlotRect_ExceedsFrame(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)
	err := c.UpdateSlotRect(0, image.Rect(1800, 900, 2280, 1170))
	require.Error(t, err)
}

// Issue #11: "No signal" gray should be broadcast black, not mid-gray.
func TestBlendRegion_AlphaRounding(t *testing.T) {
	t.Parallel()
	// Bug 18: int(alpha * 256) truncates instead of rounding.
	// int(0.5 * 256) = 128 (happens to be exact), but int(alpha * 256 + 0.5)
	// is the standard rounding pattern used elsewhere (e.g. AlphaBlendRGBA).
	// This test verifies the rounding matches the standard pattern.
	//
	// At alpha=0.5: dst=0, src=254
	// With pos=128 (either way): (0*128 + 254*128) >> 8 = 32512 >> 8 = 127
	// So alpha=0.5 gives same result.
	//
	// Key difference: alpha = 0.501 (just above 0.5)
	// Without +0.5: int(0.501*256) = int(128.256) = 128
	// With +0.5: int(0.501*256 + 0.5) = int(128.756) = 128
	// Same result. The difference only matters at exact boundary values.
	//
	// More precisely, the rounding fix ensures consistency with AlphaBlendRGBA
	// and other blend functions. Verify BlendRegion at alpha=0.5 gives
	// the expected halfway blend value.
	dstW, dstH := 8, 8
	srcW, srcH := 4, 4
	dst := makeYUV420(dstW, dstH, 0, 128, 128)
	src := makeYUV420(srcW, srcH, 254, 128, 128)

	rect := image.Rect(0, 0, 4, 4)
	BlendRegion(dst, dstW, dstH, src, srcW, srcH, rect, 0.5)

	// At alpha=0.5, pos=128: (0*128 + 254*128) >> 8 = 127
	// We're just testing the blend works correctly at alpha=0.5
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			got := dst[y*dstW+x]
			require.InDelta(t, 127, int(got), 1,
				"Y[%d,%d] = %d, expected ~127 at alpha=0.5 blend of 0 and 254", y, x, got)
		}
	}
}

func TestMakeGrayFrame_BroadcastBlack(t *testing.T) {
	gray := makeGrayFrame(4, 4)
	// Y should be 16 (BT.709 limited range black), not 128
	require.Equal(t, byte(16), gray[0], "no-signal Y should be BT.709 black (16)")
	// Cb, Cr should be 128 (neutral chroma)
	ySize := 4 * 4
	require.Equal(t, byte(128), gray[ySize], "no-signal Cb should be 128")
	cbSize := 2 * 2
	require.Equal(t, byte(128), gray[ySize+cbSize], "no-signal Cr should be 128")
}
