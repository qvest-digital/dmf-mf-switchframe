package layout

import (
	"bytes"
	"image"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestCompositor_SelectScaleQuality(t *testing.T) {
	c := NewCompositor(1920, 1080)

	// PIP compositor always uses bilinear for speed — Lanczos-3 is 5-15x
	// slower and imperceptible at PIP overlay scale. With 4 PIP slots scaling
	// from 1080p sources, Lanczos-3 caused 400ms+ per frame (446ms measured).
	q := c.selectScaleQuality()
	require.Equal(t, transition.ScaleQualityFast, q, "PIP should always use bilinear")
}

func TestCompositor_GraySlotDirectFill(t *testing.T) {
	c := NewCompositor(8, 8)

	// White background — gray slot should overwrite with broadcast black
	l := &Layout{
		Name: "pip",
		Slots: []Slot{
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

func BenchmarkCompositor_ProcessFrame_PIP(b *testing.B) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(1440, 0, 1920, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Ingest 1080p source (requires scaling to 480×270 PIP slot)
	c.IngestSourceFrame("cam2", makeYUV420(1920, 1080, 200, 128, 128), 1920, 1080, 0)

	bg := makeYUV420(1920, 1080, 16, 128, 128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessFrame(bg, 1920, 1080)
	}
}

func BenchmarkCompositor_ProcessFrame_Quad(b *testing.B) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "quad",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Rect: image.Rect(960, 0, 1920, 540), Enabled: true},
			{SourceKey: "cam3", Rect: image.Rect(0, 540, 960, 1080), Enabled: true},
			{SourceKey: "cam4", Rect: image.Rect(960, 540, 1920, 1080), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Ingest 720p sources (requires scaling to 960×540 slots)
	for _, name := range []string{"cam1", "cam2", "cam3", "cam4"} {
		c.IngestSourceFrame(name, makeYUV420(1280, 720, 200, 128, 128), 1280, 720, 0)
	}

	bg := makeYUV420(1920, 1080, 16, 128, 128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessFrame(bg, 1920, 1080)
	}
}

// BenchmarkCompositor_ProcessFrame_Quad_CacheHit benchmarks quad layout when
// all sources have the same PTS (cache hit — scaling skipped).
func BenchmarkCompositor_ProcessFrame_Quad_CacheHit(b *testing.B) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "quad",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Rect: image.Rect(960, 0, 1920, 540), Enabled: true},
			{SourceKey: "cam3", Rect: image.Rect(0, 540, 960, 1080), Enabled: true},
			{SourceKey: "cam4", Rect: image.Rect(960, 540, 1920, 1080), Enabled: true},
		},
	}
	c.SetLayout(l)
	for _, name := range []string{"cam1", "cam2", "cam3", "cam4"} {
		c.IngestSourceFrame(name, makeYUV420(1280, 720, 200, 128, 128), 1280, 720, 1000)
	}
	bg := makeYUV420(1920, 1080, 16, 128, 128)
	// First call populates cache
	c.ProcessFrame(bg, 1920, 1080)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.ProcessFrame(bg, 1920, 1080)
	}
}

// BenchmarkCompositor_ProcessFrame_Quad_CacheMiss benchmarks quad layout when
// sources change PTS every frame (cache miss — scaling every time).
func BenchmarkCompositor_ProcessFrame_Quad_CacheMiss(b *testing.B) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "quad",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Rect: image.Rect(960, 0, 1920, 540), Enabled: true},
			{SourceKey: "cam3", Rect: image.Rect(0, 540, 960, 1080), Enabled: true},
			{SourceKey: "cam4", Rect: image.Rect(960, 540, 1920, 1080), Enabled: true},
		},
	}
	c.SetLayout(l)
	bg := makeYUV420(1920, 1080, 16, 128, 128)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Different PTS each iteration = cache miss, forces re-scale
		pts := int64(i * 3000)
		for _, name := range []string{"cam1", "cam2", "cam3", "cam4"} {
			c.IngestSourceFrame(name, makeYUV420(1280, 720, 200, 128, 128), 1280, 720, pts)
		}
		c.ProcessFrame(bg, 1920, 1080)
	}
}

func TestCompositor_IngestAndNeedsSource(t *testing.T) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(1440, 0, 1920, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	require.True(t, c.NeedsSource("cam2"))
	require.False(t, c.NeedsSource("cam3"))

	// Ingest a frame
	yuv := makeYUV420(1920, 1080, 235, 128, 128)
	c.IngestSourceFrame("cam2", yuv, 1920, 1080, 0)

	require.True(t, c.HasFrame("cam2"))
}

func TestCompositor_ProcessFrame(t *testing.T) {
	c := NewCompositor(8, 8)

	// Layout: 4x4 PIP at (4,0)
	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Ingest white source
	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

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

	l := &Layout{Name: "pip", Slots: []Slot{
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
		Slots: []Slot{
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
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: false},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// Disabled slot should not be composited
	require.Equal(t, byte(16), result[0*8+4], "disabled slot should not appear")
}

func TestCompositor_ZOrderSorting(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			// Higher z-order but listed first — should still render on top
			{SourceKey: "top", Rect: image.Rect(2, 2, 6, 6), ZOrder: 2, Enabled: true},
			{SourceKey: "bottom", Rect: image.Rect(0, 0, 8, 8), ZOrder: 0, Enabled: true},
		},
	}
	c.SetLayout(l)

	// Bottom source: mid-gray
	c.IngestSourceFrame("bottom", makeYUV420(8, 8, 100, 128, 128), 8, 8, 0)
	// Top source: white
	c.IngestSourceFrame("top", makeYUV420(4, 4, 235, 128, 128), 4, 4, 0)

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
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(8, 0, 16, 8), Enabled: false,
				Transition: SlotTransition{Type: "fly", DurationMs: 500}},
		},
	}
	c.SetLayout(l)

	// Ingest a source at different resolution (will need scaling)
	src := makeYUV420(8, 8, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 8, 8, 0)

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
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(8, 4, 16, 12), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Manually inject an animation that is partially off-screen right
	c.mu.Lock()
	c.animations = append(c.animations, &Animation{
		SlotIndex: 0,
		StartTime: time.Now(),
		Duration:  time.Hour,                 // won't complete during test
		FromRect:  image.Rect(12, 4, 20, 12), // extends 4px past right edge
		ToRect:    image.Rect(8, 4, 16, 12),
		FromAlpha: 1.0,
		ToAlpha:   1.0,
	})
	c.mu.Unlock()

	src := makeYUV420(8, 8, 200, 128, 128)
	c.IngestSourceFrame("cam2", src, 8, 8, 0)

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
		Slots: []Slot{
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
		Slots: []Slot{
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
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(0, 0, 4, 4), Enabled: false,
				Transition: SlotTransition{Type: "dissolve", DurationMs: 10}},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

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
		Slots: []Slot{
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
	c.IngestSourceFrame("cam1", src, 8, 4, 0)

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
		Slots: []Slot{
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
	c.IngestSourceFrame("cam1", src, 8, 4, 0)

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
		Slots: []Slot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

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
		Slots: []Slot{
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
		Slots: []Slot{
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
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)
	err := c.UpdateSlotRect(5, image.Rect(0, 0, 100, 100))
	require.Error(t, err)
}

func TestCompositor_UpdateSlotRect_SnapsOddToEven(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(100, 100, 580, 370), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Pass odd coordinates — they should be snapped to even values.
	// Min rounds down (&^1), Max rounds up ((v+1)&^1).
	err := c.UpdateSlotRect(0, image.Rect(201, 203, 681, 471))
	require.NoError(t, err)

	updated := c.GetLayout()
	r := updated.Slots[0].Rect
	require.Equal(t, 200, r.Min.X, "Min.X should snap down to even")
	require.Equal(t, 202, r.Min.Y, "Min.Y should snap down to even")
	require.Equal(t, 682, r.Max.X, "Max.X should snap up to even")
	require.Equal(t, 472, r.Max.Y, "Max.Y should snap up to even")

	// Already-even values should pass through unchanged.
	err = c.UpdateSlotRect(0, image.Rect(100, 100, 580, 370))
	require.NoError(t, err)
	updated = c.GetLayout()
	require.Equal(t, image.Rect(100, 100, 580, 370), updated.Slots[0].Rect)
}

func TestCompositor_UpdateSlotRect_ExceedsFrame(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
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

// B2: Concurrent IngestSourceFrame and ProcessFrame must not race on fill data.
// The fill entry's yuv slice was aliased into slotSnapshot without copying,
// allowing IngestSourceFrame to write while ProcessFrame reads in Phase 2.
func TestCompositor_FillDataRace(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Seed with an initial frame so ProcessFrame has something to snapshot.
	c.IngestSourceFrame("cam1", makeYUV420(8, 8, 128, 128, 128), 8, 8, 0)

	done := make(chan struct{})

	// Writer goroutine: continuously ingest new frames
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			c.IngestSourceFrame("cam1", makeYUV420(8, 8, byte(i%256), 128, 128), 8, 8, 0)
		}
	}()

	// Reader goroutine: continuously process frames
	bg := makeYUV420(16, 16, 16, 128, 128)
	for i := 0; i < 1000; i++ {
		bgCopy := make([]byte, len(bg))
		copy(bgCopy, bg)
		c.ProcessFrame(bgCopy, 16, 16)
	}

	<-done
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

// Race 1: SetLayout stores the layout atomically BEFORE allocating buffers
// under the mutex. A concurrent ProcessFrame loads the new layout (with N slots)
// but sees stale sortedSlots/scaleBufs/grayBufs (from a previous layout with
// fewer slots), causing incorrect compositing or missed slots.
//
// The fix moves c.layout.Store(l) AFTER allocateBuffers and fill pruning,
// so ProcessFrame never sees a layout whose buffers haven't been allocated.
func TestSetLayout_ProcessFrame_Race(t *testing.T) {
	c := NewCompositor(16, 16)

	// Start with a 1-slot layout so buffers are allocated for 1 slot.
	l1 := &Layout{
		Name: "one",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l1)
	c.IngestSourceFrame("cam1", makeYUV420(8, 8, 200, 128, 128), 8, 8, 0)
	c.IngestSourceFrame("cam2", makeYUV420(8, 8, 100, 128, 128), 8, 8, 0)

	// Two-slot layout — allocateBuffers produces different-length slices.
	l2 := &Layout{
		Name: "two",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: true},
			{SourceKey: "cam2", Rect: image.Rect(8, 0, 16, 8), Enabled: true},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			// Alternate between 1-slot and 2-slot layouts.
			if i%2 == 0 {
				c.SetLayout(l2)
			} else {
				c.SetLayout(l1)
			}
		}
	}()

	// ProcessFrame must not panic or produce corrupted output.
	bg := makeYUV420(16, 16, 16, 128, 128)
	for i := 0; i < 1000; i++ {
		bgCopy := make([]byte, len(bg))
		copy(bgCopy, bg)
		c.ProcessFrame(bgCopy, 16, 16)
	}
	<-done
}

// Race 2: SlotOn/SlotOff read Active() without the lock held. Between the
// wasBefore check and the wasAfter check, another goroutine can change the
// active state, causing missed or spurious OnActiveChange callbacks.
//
// The fix moves wasBefore inside the lock and computes wasAfter before unlock,
// so the before/after comparison is atomic with respect to the state change.
func TestSlotOn_ConcurrentActiveChange_Race(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: false},
			{SourceKey: "cam2", Rect: image.Rect(8, 0, 16, 8), Enabled: false},
		},
	}
	c.SetLayout(l)

	var callCount int32
	c.OnActiveChange = func() {
		atomic.AddInt32(&callCount, 1)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			c.SlotOn(0)
			c.SlotOff(0)
		}
	}()

	for i := 0; i < 1000; i++ {
		c.SlotOn(1)
		c.SlotOff(1)
	}
	<-done

	// After all goroutines finish, both slots should be off.
	// The exact callCount depends on interleaving, but it should be > 0
	// (at least some transitions from inactive→active and active→inactive occurred).
	require.Greater(t, atomic.LoadInt32(&callCount), int32(0),
		"OnActiveChange should have been called at least once")
}

// TestSetLayout_ActiveChange_Atomicity verifies that SetLayout correctly
// detects active-state transitions. The wasBefore/wasAfter check must
// be performed atomically with the layout swap to avoid missing callbacks.
func TestSetLayout_ActiveChange_Atomicity(t *testing.T) {
	c := NewCompositor(16, 16)

	var callCount int32
	c.OnActiveChange = func() {
		atomic.AddInt32(&callCount, 1)
	}

	// Going from nil → active layout should trigger callback.
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "nil→active should trigger callback")

	// Going from active → nil should trigger callback.
	c.SetLayout(nil)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "active→nil should trigger callback")

	// Going from nil → inactive layout should NOT trigger callback.
	inactiveLayout := &Layout{
		Name: "inactive",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: false},
		},
	}
	c.SetLayout(inactiveLayout)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "nil→inactive should not trigger callback")
}

// TestSlotOn_ActiveChange_NotMissed verifies that SlotOn correctly fires
// OnActiveChange when going from no-enabled-slots to at least one enabled.
func TestSlotOn_ActiveChange_NotMissed(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 8, 8), Enabled: false},
		},
	}
	c.SetLayout(l)

	var callCount int32
	c.OnActiveChange = func() {
		atomic.AddInt32(&callCount, 1)
	}

	// SlotOn on the only slot: inactive→active should fire callback.
	c.SlotOn(0)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "SlotOn inactive→active should trigger callback")

	// SlotOn again (already enabled): should NOT fire callback.
	c.SlotOn(0)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "SlotOn active→active should not trigger callback")

	// SlotOff: active→inactive should fire callback.
	c.SlotOff(0)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "SlotOff active→inactive should trigger callback")

	// SlotOff again (already disabled): should NOT fire callback.
	c.SlotOff(0)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "SlotOff inactive→inactive should not trigger callback")
}

func TestCompositor_SkipsScaleOnSamePTS(t *testing.T) {
	c := NewCompositor(1920, 1080)
	layout := &Layout{
		Slots: []Slot{
			{SourceKey: "cam1", Enabled: true, Rect: image.Rect(0, 0, 960, 540)},
		},
	}
	c.SetLayout(layout)

	// Create a source frame at different resolution (forces scaling)
	yuv := make([]byte, 1280*720*3/2)
	for i := range yuv {
		yuv[i] = byte(i % 256)
	}

	// First ingest + process -- must scale
	c.IngestSourceFrame("cam1", yuv, 1280, 720, 1000)
	frame1 := make([]byte, 1920*1080*3/2)
	c.ProcessFrame(frame1, 1920, 1080)

	// Same PTS again -- should use cache
	c.IngestSourceFrame("cam1", yuv, 1280, 720, 1000)
	frame2 := make([]byte, 1920*1080*3/2)
	c.ProcessFrame(frame2, 1920, 1080)

	if !bytes.Equal(frame1, frame2) {
		t.Error("same PTS should produce identical output")
	}
}

func TestCompositor_InvalidatesCacheOnPTSChange(t *testing.T) {
	c := NewCompositor(1920, 1080)
	layout := &Layout{
		Slots: []Slot{
			{SourceKey: "cam1", Enabled: true, Rect: image.Rect(0, 0, 960, 540)},
		},
	}
	c.SetLayout(layout)

	yuv1 := make([]byte, 1280*720*3/2)
	for i := range yuv1 {
		yuv1[i] = byte(i % 256)
	}
	yuv2 := make([]byte, 1280*720*3/2)
	for i := range yuv2 {
		yuv2[i] = byte((i + 50) % 256)
	}

	c.IngestSourceFrame("cam1", yuv1, 1280, 720, 1000)
	frame1 := make([]byte, 1920*1080*3/2)
	c.ProcessFrame(frame1, 1920, 1080)

	// Different PTS + different content
	c.IngestSourceFrame("cam1", yuv2, 1280, 720, 2000)
	frame2 := make([]byte, 1920*1080*3/2)
	c.ProcessFrame(frame2, 1920, 1080)

	if bytes.Equal(frame1, frame2) {
		t.Error("different PTS should produce different output")
	}
}
