package dve

import (
	"bytes"
	"image"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// makeYUV420 creates a YUV420 buffer filled with constant Y, Cb, Cr values.
func makeYUV420(w, h int, y, cb, cr byte) []byte {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+cbSize*2)
	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := 0; i < cbSize; i++ {
		buf[ySize+i] = cb
		buf[ySize+cbSize+i] = cr
	}
	return buf
}

// --- Compositor tests ---

func TestCompositor_SelectScaleQuality(t *testing.T) {
	c := NewCompositor(1920, 1080)
	q := c.selectScaleQuality()
	require.Equal(t, transition.ScaleQualityFast, q, "PIP should always use bilinear")
}

func TestCompositor_GraySlotDirectFill(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "missing", Transform: DefaultTransform(0, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	bg := makeYUV420(8, 8, 235, 60, 200)
	result := c.ProcessFrame(bg, 8, 8)

	// Slot region should be broadcast black (Y=16, Cb=128, Cr=128)
	require.Equal(t, byte(16), result[0], "gray slot Y should be broadcast black")
	// Outside slot should be unchanged
	require.Equal(t, byte(235), result[4], "outside slot should be unchanged")

	// Chroma inside slot
	ySize := 8 * 8
	require.Equal(t, byte(128), result[ySize], "gray slot Cb should be 128")
}

func TestCompositor_IngestAndNeedsSource(t *testing.T) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(1440, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	require.True(t, c.NeedsSource("cam2"))
	require.False(t, c.NeedsSource("cam3"))

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
			{SourceKey: "cam2", Transform: DefaultTransform(4, 0, 4, 4), Enabled: true},
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
		{SourceKey: "cam2", Transform: DefaultTransform(0, 0, 480, 270), Enabled: true},
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
			{SourceKey: "missing", Transform: DefaultTransform(4, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	require.Equal(t, byte(16), result[0*8+4], "missing source should render broadcast black")
}

func TestCompositor_DisabledSlotSkipped(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(4, 0, 4, 4), Enabled: false},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	require.Equal(t, byte(16), result[0*8+4], "disabled slot should not appear")
}

func TestCompositor_ZOrderSorting(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			// Higher z-order but listed first -- should still render on top
			{SourceKey: "top", Transform: DefaultTransform(2, 2, 4, 4), ZOrder: 2, Enabled: true},
			{SourceKey: "bottom", Transform: DefaultTransform(0, 0, 8, 8), ZOrder: 0, Enabled: true},
		},
	}
	c.SetLayout(l)

	c.IngestSourceFrame("bottom", makeYUV420(8, 8, 100, 128, 128), 8, 8, 0)
	c.IngestSourceFrame("top", makeYUV420(4, 4, 235, 128, 128), 4, 4, 0)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// (3,3) is in both slots -- top (white, z=2) should win
	require.Equal(t, byte(235), result[3*8+3], "higher z-order should be on top")
	// (0,0) is only in bottom -- should be mid-gray
	require.Equal(t, byte(100), result[0], "lower z-order visible where not overlapped")
}

func TestCompositor_FlyInPartiallyOffScreen(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(8, 0, 8, 8), Enabled: false},
		},
	}
	c.SetLayout(l)
	c.SetSlotTransition(0, SlotTransition{Type: "fly", DurationMs: 500})

	src := makeYUV420(8, 8, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 8, 8, 0)

	c.SlotOn(0)

	bg := makeYUV420(16, 16, 16, 128, 128)
	require.NotPanics(t, func() {
		c.ProcessFrame(bg, 16, 16)
	})
}

func TestCompositor_FlyInClampedDoesNotCorrupt(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(8, 4, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Manually inject an animation that is partially off-screen right
	c.mu.Lock()
	c.animations = append(c.animations, &slotAnimation{
		SlotIndex: 0,
		StartTime: time.Now(),
		Duration:  time.Hour,
		FromRect:  image.Rect(12, 4, 20, 12),
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

func TestCompositor_AutoDissolveSource_NoRace(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

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

func TestCompositor_GrayBuffer_ScaledSlot(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "missing", Transform: DefaultTransform(4, 4, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l)

	bg := makeYUV420(16, 16, 16, 128, 128)
	require.NotPanics(t, func() {
		c.ProcessFrame(bg, 16, 16)
	})
}

func TestCompositor_DissolveEndpoint_NoVisualPop(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(0, 0, 4, 4), Enabled: false},
		},
	}
	c.SetLayout(l)
	c.SetSlotTransition(0, SlotTransition{Type: "dissolve", DurationMs: 10})

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

	c.SlotOn(0)

	time.Sleep(20 * time.Millisecond)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	require.Equal(t, byte(235), result[0], "completed dissolve should be fully opaque")
}

func TestCompositor_FillMode_CropsBeforeScale(t *testing.T) {
	c := NewCompositor(16, 16)

	tr := DefaultTransform(0, 0, 4, 8)
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.5, 0.5}

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: tr, Enabled: true},
		},
	}
	c.SetLayout(l)

	// Source is 8x4 (wider than slot aspect).
	src := make([]byte, video.YUV420FrameSize(8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src[y*8+x] = 200
		}
		for x := 4; x < 8; x++ {
			src[y*8+x] = 100
		}
	}
	ySize := 8 * 4
	for i := ySize; i < len(src); i++ {
		src[i] = 128
	}
	c.IngestSourceFrame("cam1", src, 8, 4, 0)

	bg := makeYUV420(16, 16, 16, 128, 128)
	result := c.ProcessFrame(bg, 16, 16)

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

func TestCompositor_StretchDefault_Unchanged(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(4, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4, 0)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	require.Equal(t, byte(235), result[0*8+4], "stretch mode unchanged")
}

func TestCompositor_FillMode_GrayBuffer(t *testing.T) {
	c := NewCompositor(16, 16)

	tr := DefaultTransform(0, 0, 4, 8)
	tr.ScaleMode = ScaleModeFill

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "missing", Transform: tr, Enabled: true},
		},
	}
	c.SetLayout(l)

	bg := makeYUV420(16, 16, 30, 128, 128)
	require.NotPanics(t, func() {
		result := c.ProcessFrame(bg, 16, 16)
		require.Equal(t, byte(16), result[0], "gray buffer should render for missing source in fill mode")
	})
}

func TestCompositor_UpdateSlotRect(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(100, 100, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	err := c.UpdateSlotRect(0, image.Rect(200, 200, 680, 470))
	require.NoError(t, err)

	updated := c.GetLayout()
	rect := updated.Slots[0].Transform.ToRect()
	require.Equal(t, image.Rect(200, 200, 680, 470), rect)
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
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 480, 270), Enabled: true},
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
			{SourceKey: "cam1", Transform: DefaultTransform(100, 100, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	err := c.UpdateSlotRect(0, image.Rect(201, 203, 681, 471))
	require.NoError(t, err)

	updated := c.GetLayout()
	r := updated.Slots[0].Transform.ToRect()
	require.Equal(t, 200, r.Min.X, "Min.X should snap down to even")
	require.Equal(t, 202, r.Min.Y, "Min.Y should snap down to even")
	require.Equal(t, 682, r.Max.X, "Max.X should snap up to even")
	require.Equal(t, 472, r.Max.Y, "Max.Y should snap up to even")

	err = c.UpdateSlotRect(0, image.Rect(100, 100, 580, 370))
	require.NoError(t, err)
	updated = c.GetLayout()
	r2 := updated.Slots[0].Transform.ToRect()
	require.Equal(t, image.Rect(100, 100, 580, 370), r2)
}

func TestCompositor_UpdateSlotRect_ExceedsFrame(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)
	err := c.UpdateSlotRect(0, image.Rect(1800, 900, 2280, 1170))
	require.Error(t, err)
}

func TestCompositor_FillDataRace(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l)

	c.IngestSourceFrame("cam1", makeYUV420(8, 8, 128, 128, 128), 8, 8, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			c.IngestSourceFrame("cam1", makeYUV420(8, 8, byte(i%256), 128, 128), 8, 8, 0)
		}
	}()

	bg := makeYUV420(16, 16, 16, 128, 128)
	for i := 0; i < 1000; i++ {
		bgCopy := make([]byte, len(bg))
		copy(bgCopy, bg)
		c.ProcessFrame(bgCopy, 16, 16)
	}
	<-done
}

func TestSetLayout_ProcessFrame_Race(t *testing.T) {
	c := NewCompositor(16, 16)

	l1 := &Layout{
		Name: "one",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l1)
	c.IngestSourceFrame("cam1", makeYUV420(8, 8, 200, 128, 128), 8, 8, 0)
	c.IngestSourceFrame("cam2", makeYUV420(8, 8, 100, 128, 128), 8, 8, 0)

	l2 := &Layout{
		Name: "two",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: true},
			{SourceKey: "cam2", Transform: DefaultTransform(8, 0, 8, 8), Enabled: true},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			if i%2 == 0 {
				c.SetLayout(l2)
			} else {
				c.SetLayout(l1)
			}
		}
	}()

	bg := makeYUV420(16, 16, 16, 128, 128)
	for i := 0; i < 1000; i++ {
		bgCopy := make([]byte, len(bg))
		copy(bgCopy, bg)
		c.ProcessFrame(bgCopy, 16, 16)
	}
	<-done
}

func TestSlotOn_ConcurrentActiveChange_Race(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: false},
			{SourceKey: "cam2", Transform: DefaultTransform(8, 0, 8, 8), Enabled: false},
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

	require.Greater(t, atomic.LoadInt32(&callCount), int32(0),
		"OnActiveChange should have been called at least once")
}

func TestSetLayout_ActiveChange_Atomicity(t *testing.T) {
	c := NewCompositor(16, 16)

	var callCount int32
	c.OnActiveChange = func() {
		atomic.AddInt32(&callCount, 1)
	}

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: true},
		},
	}
	c.SetLayout(l)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "nil->active should trigger callback")

	c.SetLayout(nil)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "active->nil should trigger callback")

	inactiveLayout := &Layout{
		Name: "inactive",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: false},
		},
	}
	c.SetLayout(inactiveLayout)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "nil->inactive should not trigger callback")
}

func TestSlotOn_ActiveChange_NotMissed(t *testing.T) {
	c := NewCompositor(16, 16)

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 8, 8), Enabled: false},
		},
	}
	c.SetLayout(l)

	var callCount int32
	c.OnActiveChange = func() {
		atomic.AddInt32(&callCount, 1)
	}

	c.SlotOn(0)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "SlotOn inactive->active should trigger callback")

	c.SlotOn(0)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "SlotOn active->active should not trigger callback")

	c.SlotOff(0)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "SlotOff active->inactive should trigger callback")

	c.SlotOff(0)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "SlotOff inactive->inactive should not trigger callback")
}

// TestSlotOff_AnimatedDeactivation_FiresActiveChange verifies that when the
// last enabled slot is taken off-air with a dissolve or fly transition, the
// OnActiveChange callback fires when the animation completes (not at SlotOff
// time, since the slot stays Enabled during the animation). Without this the
// switcher is never told the DVE node went idle.
func TestSlotOff_AnimatedDeactivation_FiresActiveChange(t *testing.T) {
	for _, transType := range []string{"dissolve", "fly"} {
		t.Run(transType, func(t *testing.T) {
			c := NewCompositor(8, 8)
			l := &Layout{
				Name: "pip",
				Slots: []Slot{
					{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 4, 4), Enabled: true},
				},
			}
			c.SetLayout(l)
			c.SetSlotTransition(0, SlotTransition{Type: transType, DurationMs: 10})

			src := makeYUV420(4, 4, 235, 128, 128)
			c.IngestSourceFrame("cam1", src, 4, 4, 0)

			var callCount int32
			c.OnActiveChange = func() {
				atomic.AddInt32(&callCount, 1)
			}

			c.SlotOff(0)
			// The slot stays enabled during the transition, so no callback yet.
			require.Equal(t, int32(0), atomic.LoadInt32(&callCount),
				"animated SlotOff should not fire OnActiveChange before completion")
			require.True(t, c.Active(), "compositor still active during animation")

			time.Sleep(20 * time.Millisecond)

			bg := makeYUV420(8, 8, 16, 128, 128)
			c.ProcessFrame(bg, 8, 8)

			require.Equal(t, int32(1), atomic.LoadInt32(&callCount),
				"completed animated SlotOff of last slot should fire OnActiveChange once")
			require.False(t, c.Active(), "compositor inactive after last slot animated off")

			// A subsequent frame must not re-fire the callback.
			c.ProcessFrame(bg, 8, 8)
			require.Equal(t, int32(1), atomic.LoadInt32(&callCount),
				"OnActiveChange should fire only on the active->inactive transition")
		})
	}
}

// TestSlotOff_AnimatedDeactivation_OtherSlotEnabled verifies that completing a
// dissolve-off of one slot does NOT fire OnActiveChange while another slot
// remains enabled (the compositor is still active).
func TestSlotOff_AnimatedDeactivation_OtherSlotEnabled(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 4, 4), Enabled: true},
			{SourceKey: "cam2", Transform: DefaultTransform(4, 0, 4, 4), Enabled: true},
		},
	}
	c.SetLayout(l)
	c.SetSlotTransition(0, SlotTransition{Type: "dissolve", DurationMs: 10})
	c.IngestSourceFrame("cam1", makeYUV420(4, 4, 235, 128, 128), 4, 4, 0)
	c.IngestSourceFrame("cam2", makeYUV420(4, 4, 100, 128, 128), 4, 4, 0)

	var callCount int32
	c.OnActiveChange = func() {
		atomic.AddInt32(&callCount, 1)
	}

	c.SlotOff(0)
	time.Sleep(20 * time.Millisecond)

	bg := makeYUV420(8, 8, 16, 128, 128)
	c.ProcessFrame(bg, 8, 8)

	require.Equal(t, int32(0), atomic.LoadInt32(&callCount),
		"completing dissolve-off while another slot is enabled must not fire OnActiveChange")
	require.True(t, c.Active(), "compositor still active with second slot enabled")
}

func TestCompositor_SkipsScaleOnSamePTS(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Slots: []Slot{
			{SourceKey: "cam1", Enabled: true, Transform: DefaultTransform(0, 0, 960, 540)},
		},
	}
	c.SetLayout(l)

	yuv := make([]byte, video.YUV420FrameSize(1280, 720))
	for i := range yuv {
		yuv[i] = byte(i % 256)
	}

	c.IngestSourceFrame("cam1", yuv, 1280, 720, 1000)
	frame1 := make([]byte, video.YUV420FrameSize(1920, 1080))
	c.ProcessFrame(frame1, 1920, 1080)

	c.IngestSourceFrame("cam1", yuv, 1280, 720, 1000)
	frame2 := make([]byte, video.YUV420FrameSize(1920, 1080))
	c.ProcessFrame(frame2, 1920, 1080)

	if !bytes.Equal(frame1, frame2) {
		t.Error("same PTS should produce identical output")
	}
}

func TestCompositor_InvalidatesCacheOnPTSChange(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Slots: []Slot{
			{SourceKey: "cam1", Enabled: true, Transform: DefaultTransform(0, 0, 960, 540)},
		},
	}
	c.SetLayout(l)

	yuv1 := make([]byte, video.YUV420FrameSize(1280, 720))
	for i := range yuv1 {
		yuv1[i] = byte(i % 256)
	}
	yuv2 := make([]byte, video.YUV420FrameSize(1280, 720))
	for i := range yuv2 {
		yuv2[i] = byte((i + 50) % 256)
	}

	c.IngestSourceFrame("cam1", yuv1, 1280, 720, 1000)
	frame1 := make([]byte, video.YUV420FrameSize(1920, 1080))
	c.ProcessFrame(frame1, 1920, 1080)

	c.IngestSourceFrame("cam1", yuv2, 1280, 720, 2000)
	frame2 := make([]byte, video.YUV420FrameSize(1920, 1080))
	c.ProcessFrame(frame2, 1920, 1080)

	if bytes.Equal(frame1, frame2) {
		t.Error("different PTS should produce different output")
	}
}

func TestCompositor_InvalidatesCacheOnDimensionChange(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Slots: []Slot{
			{SourceKey: "cam1", Enabled: true, Transform: DefaultTransform(0, 0, 960, 540)},
		},
	}
	c.SetLayout(l)

	yuv720 := make([]byte, video.YUV420FrameSize(1280, 720))
	for i := range yuv720 {
		yuv720[i] = byte(i % 256)
	}
	c.IngestSourceFrame("cam1", yuv720, 1280, 720, 1000)
	frame1 := make([]byte, video.YUV420FrameSize(1920, 1080))
	c.ProcessFrame(frame1, 1920, 1080)

	yuv480 := make([]byte, video.YUV420FrameSize(640, 480))
	for i := range yuv480 {
		yuv480[i] = byte((i + 30) % 256)
	}
	c.IngestSourceFrame("cam1", yuv480, 640, 480, 1000)
	frame2 := make([]byte, video.YUV420FrameSize(1920, 1080))
	c.ProcessFrame(frame2, 1920, 1080)

	if bytes.Equal(frame1, frame2) {
		t.Error("dimension change at same PTS should invalidate cache and produce different output")
	}
}

func TestCompositor_UpdateSlotTransform(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(100, 100, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	c.UpdateSlotTransform(0, func(t *Transform) {
		t.PositionX = 200
		t.PositionY = 200
	})

	updated := c.GetLayout()
	require.Equal(t, 200.0, updated.Slots[0].Transform.PositionX)
	require.Equal(t, 200.0, updated.Slots[0].Transform.PositionY)
}

func TestCompositor_SnapshotSlots(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Transform: DefaultTransform(960, 0, 960, 540), Enabled: true},
		},
	}
	c.SetLayout(l)

	c.IngestSourceFrame("cam1", makeYUV420(1920, 1080, 200, 128, 128), 1920, 1080, 1000)
	c.IngestSourceFrame("cam2", makeYUV420(1280, 720, 100, 128, 128), 1280, 720, 2000)

	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 2)

	require.Equal(t, "cam1", snaps[0].SourceKey)
	require.Equal(t, 960, snaps[0].RectW)
	require.Equal(t, 540, snaps[0].RectH)
	require.NotNil(t, snaps[0].FillYUV)
	require.Equal(t, 1920, snaps[0].FillW)
	require.Equal(t, 1080, snaps[0].FillH)
	require.NotNil(t, snaps[0].DVETransform)
	require.True(t, snaps[0].DVEIsIdentity)

	require.Equal(t, "cam2", snaps[1].SourceKey)
	require.NotNil(t, snaps[1].FillYUV)
	require.Equal(t, 1280, snaps[1].FillW)
	require.Equal(t, 720, snaps[1].FillH)
}

func TestCompositor_SnapshotSlots_DisabledSkipped(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Transform: DefaultTransform(960, 0, 960, 540), Enabled: false},
		},
	}
	c.SetLayout(l)

	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.Equal(t, "cam1", snaps[0].SourceKey)
}

func TestCompositor_SnapshotSlots_RectMode(t *testing.T) {
	c := NewCompositor(1920, 1080)

	tr := DefaultTransform(0, 0, 960, 540)
	tr.RectMode = true
	tr.OutputRect = Rect{X: 100, Y: 100, W: 800, H: 450}
	tr.ScaleMode = ScaleModeStretch

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: tr, Enabled: true},
		},
	}
	c.SetLayout(l)

	c.IngestSourceFrame("cam1", makeYUV420(1920, 1080, 200, 128, 128), 1920, 1080, 1000)

	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 1)
	// Rect should be resolved from OutputRect
	require.Equal(t, 100, snaps[0].RectX)
	require.Equal(t, 100, snaps[0].RectY)
	require.Equal(t, 800, snaps[0].RectW)
	require.Equal(t, 450, snaps[0].RectH)
}

func TestCompositor_ProcessFrame_RectMode(t *testing.T) {
	c := NewCompositor(16, 16)

	tr := DefaultTransform(0, 0, 16, 16)
	tr.RectMode = true
	tr.OutputRect = Rect{X: 4, Y: 0, W: 4, H: 4}
	tr.ScaleMode = ScaleModeStretch

	l := &Layout{
		Name: "test",
		Slots: []Slot{
			{SourceKey: "cam1", Transform: tr, Enabled: true},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam1", src, 4, 4, 0)

	bg := makeYUV420(16, 16, 16, 128, 128)
	result := c.ProcessFrame(bg, 16, 16)

	// PIP region should be white at (4,0)
	require.Equal(t, byte(235), result[0*16+4], "RectMode PIP should render at resolved position")
	// Background should be black at (0,0)
	require.Equal(t, byte(16), result[0], "background should be black")
}

func TestMakeGrayFrame_BroadcastBlack(t *testing.T) {
	gray := makeGrayFrame(4, 4)
	require.Equal(t, byte(16), gray[0], "no-signal Y should be BT.709 black (16)")
	ySize := 4 * 4
	require.Equal(t, byte(128), gray[ySize], "no-signal Cb should be 128")
	cbSize := 2 * 2
	require.Equal(t, byte(128), gray[ySize+cbSize], "no-signal Cr should be 128")
}

// --- Benchmark ---

func BenchmarkCompositor_ProcessFrame_PIP(b *testing.B) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "pip",
		Slots: []Slot{
			{SourceKey: "cam2", Transform: DefaultTransform(1440, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

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
			{SourceKey: "cam1", Transform: DefaultTransform(0, 0, 960, 540), Enabled: true},
			{SourceKey: "cam2", Transform: DefaultTransform(960, 0, 960, 540), Enabled: true},
			{SourceKey: "cam3", Transform: DefaultTransform(0, 540, 960, 540), Enabled: true},
			{SourceKey: "cam4", Transform: DefaultTransform(960, 540, 960, 540), Enabled: true},
		},
	}
	c.SetLayout(l)

	for _, name := range []string{"cam1", "cam2", "cam3", "cam4"} {
		c.IngestSourceFrame(name, makeYUV420(1280, 720, 200, 128, 128), 1280, 720, 0)
	}

	bg := makeYUV420(1920, 1080, 16, 128, 128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessFrame(bg, 1920, 1080)
	}
}

// --- Composite helper tests ---

func TestBlendRegion_AlphaRounding(t *testing.T) {
	t.Parallel()
	dstW, dstH := 8, 8
	srcW, srcH := 4, 4
	dst := makeYUV420(dstW, dstH, 0, 128, 128)
	src := makeYUV420(srcW, srcH, 254, 128, 128)

	rect := image.Rect(0, 0, 4, 4)
	BlendRegion(dst, dstW, dstH, src, srcW, srcH, rect, 0.5)

	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			got := dst[y*dstW+x]
			require.InDelta(t, 127, int(got), 1,
				"Y[%d,%d] = %d, expected ~127 at alpha=0.5 blend of 0 and 254", y, x, got)
		}
	}
}

func TestComposePIPOpaque(t *testing.T) {
	dstW, dstH := 8, 8
	srcW, srcH := 4, 4
	dst := makeYUV420(dstW, dstH, 0, 128, 128)
	src := makeYUV420(srcW, srcH, 200, 100, 150)

	rect := image.Rect(2, 2, 6, 6)
	ComposePIPOpaque(dst, dstW, dstH, src, srcW, srcH, rect)

	// Inside PIP region
	require.Equal(t, byte(200), dst[2*dstW+2], "PIP region should have source Y")
	// Outside PIP region
	require.Equal(t, byte(0), dst[0], "outside PIP should be unchanged")
}

func TestFillRectBlack(t *testing.T) {
	dstW, dstH := 8, 8
	dst := makeYUV420(dstW, dstH, 200, 100, 150)

	rect := image.Rect(0, 0, 4, 4)
	FillRectBlack(dst, dstW, dstH, rect)

	require.Equal(t, byte(16), dst[0], "filled region Y should be 16")
	require.Equal(t, byte(200), dst[4], "outside region should be unchanged")
}

// --- Crop tests ---

func TestComputeCropRect_WiderSource(t *testing.T) {
	// 8x4 source into 4x4 slot -- crop horizontally
	cropX, cropY, cropW, cropH := ComputeCropRect(8, 4, 4, 4, 0.5, 0.5)
	require.Equal(t, 4, cropW, "crop width should match slot aspect")
	require.Equal(t, 4, cropH, "crop height should be full source height")
	require.Equal(t, 2, cropX, "crop X should be centered")
	require.Equal(t, 0, cropY, "crop Y should be 0")
}

func TestComputeCropRect_TallerSource(t *testing.T) {
	// 4x8 source into 4x4 slot -- crop vertically
	cropX, cropY, cropW, cropH := ComputeCropRect(4, 8, 4, 4, 0.5, 0.5)
	require.Equal(t, 4, cropW, "crop width should be full source width")
	require.Equal(t, 4, cropH, "crop height should match slot aspect")
	require.Equal(t, 0, cropX, "crop X should be 0")
	require.Equal(t, 2, cropY, "crop Y should be centered")
}

func TestComputeCropRect_MatchingAspect(t *testing.T) {
	// Same aspect: no crop
	cropX, cropY, cropW, cropH := ComputeCropRect(8, 4, 8, 4, 0.5, 0.5)
	require.Equal(t, 0, cropX)
	require.Equal(t, 0, cropY)
	require.Equal(t, 8, cropW)
	require.Equal(t, 4, cropH)
}
