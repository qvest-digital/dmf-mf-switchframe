package layout

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/internal/video"
)

func TestSetDVELayout(t *testing.T) {
	c := NewCompositor(1920, 1080)

	dl := &dve.Layout{
		Name: "dve-test",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: dve.DefaultTransform(100, 100, 480, 270),
				ZOrder:    0,
				Enabled:   true,
			},
			{
				SourceKey: "cam2",
				Transform: dve.DefaultTransform(600, 100, 480, 270),
				ZOrder:    1,
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dl)

	// SnapshotSlots should return 2 slots with DVE transforms.
	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 2)

	// Verify first slot has expected position.
	require.Equal(t, "cam1", snaps[0].SourceKey)
	require.Equal(t, 100, snaps[0].RectX)
	require.Equal(t, 100, snaps[0].RectY)
	require.Equal(t, 480, snaps[0].RectW)
	require.Equal(t, 270, snaps[0].RectH)

	// Second slot.
	require.Equal(t, "cam2", snaps[1].SourceKey)
	require.Equal(t, 600, snaps[1].RectX)

	// DVE transforms should be populated.
	require.NotNil(t, snaps[0].DVETransform)
	require.NotNil(t, snaps[1].DVETransform)
}

func TestDVEIdentityFastPath(t *testing.T) {
	c := NewCompositor(1920, 1080)

	// DVE layout with identity-like transforms (no rotation, no perspective).
	dl := &dve.Layout{
		Name: "identity",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: dve.DefaultTransform(100, 100, 480, 270),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dl)

	// Ingest a source frame to get fill data in snapshot.
	c.IngestSourceFrame("cam1", makeYUV420(1920, 1080, 200, 128, 128), 1920, 1080, 0)

	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.True(t, snaps[0].DVEIsIdentity, "no rotation/perspective should be identity")
}

func TestDVEWithRotation(t *testing.T) {
	c := NewCompositor(1920, 1080)

	tr := dve.DefaultTransform(100, 100, 480, 270)
	tr.RotationDeg = 15.0

	dl := &dve.Layout{
		Name: "rotated",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: tr,
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dl)

	c.IngestSourceFrame("cam1", makeYUV420(1920, 1080, 200, 128, 128), 1920, 1080, 0)

	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.False(t, snaps[0].DVEIsIdentity, "rotation should not be identity")

	// Inverse matrix should be non-identity.
	inv := snaps[0].DVEInvMatrix
	require.NotEqual(t, dve.Identity(), inv, "inverse matrix should be non-identity for rotation")
}

func TestMaxSlots8(t *testing.T) {
	require.Equal(t, 8, MaxSlots)

	// Verify a layout with 8 slots is accepted.
	slots := make([]Slot, 8)
	for i := range slots {
		x := (i % 4) * 480
		y := (i / 4) * 270
		slots[i] = Slot{
			SourceKey: "",
			Rect:      image.Rect(x, y, x+480, y+270),
			ZOrder:    i,
			Enabled:   true,
		}
	}
	l := &Layout{Name: "octo", Slots: slots}
	require.NoError(t, ValidateLayout(l, 1920, 1080))

	// 9 slots should fail.
	l9 := &Layout{Name: "too-many", Slots: make([]Slot, 9)}
	require.Error(t, ValidateLayout(l9, 1920, 1080))
}

func TestProcessFrameDVEIdentity(t *testing.T) {
	// DVE identity transform should produce the same output as legacy rect-based compositing.
	frameW, frameH := 16, 16

	// Setup legacy compositor.
	cLegacy := NewCompositor(frameW, frameH)
	legacyLayout := &Layout{
		Name: "legacy",
		Slots: []Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(4, 4, 12, 12),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	cLegacy.SetLayout(legacyLayout)

	// Setup DVE compositor.
	cDVE := NewCompositor(frameW, frameH)
	dveLayout := &dve.Layout{
		Name: "dve",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: dve.DefaultTransform(4, 4, 8, 8),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	cDVE.SetDVELayout(dveLayout)

	// Same source for both.
	src := makeYUV420(8, 8, 200, 100, 150)
	cLegacy.IngestSourceFrame("cam1", src, 8, 8, 0)
	cDVE.IngestSourceFrame("cam1", src, 8, 8, 0)

	// Process both.
	bgLegacy := makeYUV420(frameW, frameH, 16, 128, 128)
	bgDVE := makeYUV420(frameW, frameH, 16, 128, 128)

	resultLegacy := cLegacy.ProcessFrame(bgLegacy, frameW, frameH)
	resultDVE := cDVE.ProcessFrame(bgDVE, frameW, frameH)

	// They should produce identical output.
	require.Equal(t, resultLegacy, resultDVE, "DVE identity should produce same output as legacy rect")
}

func TestProcessFrameDVEWithRotation(t *testing.T) {
	// DVE with rotation should produce different output than identity (pixels are warped).
	frameW, frameH := 32, 32

	c := NewCompositor(frameW, frameH)

	tr := dve.DefaultTransform(4, 4, 24, 24)
	tr.RotationDeg = 30.0

	dl := &dve.Layout{
		Name: "rotated",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: tr,
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dl)

	// Source with a gradient to make rotation visible.
	src := make([]byte, video.YUV420FrameSize(24, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			src[y*24+x] = byte(16 + x*9) // horizontal gradient
		}
	}
	ySize := 24 * 24
	for i := ySize; i < len(src); i++ {
		src[i] = 128
	}
	c.IngestSourceFrame("cam1", src, 24, 24, 0)

	bg := makeYUV420(frameW, frameH, 16, 128, 128)
	result := c.ProcessFrame(bg, frameW, frameH)

	// With rotation, some pixels outside the original rect should have content,
	// and the rotation should have moved content around.
	hasContent := false
	for i := 0; i < frameW*frameH; i++ {
		if result[i] != 16 {
			hasContent = true
			break
		}
	}
	require.True(t, hasContent, "rotated DVE slot should render visible content")
}

func TestSetDVELayout_ClearsLegacy(t *testing.T) {
	c := NewCompositor(1920, 1080)

	// Set a legacy layout first.
	legacyLayout := &Layout{
		Name: "legacy",
		Slots: []Slot{
			{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
		},
	}
	c.SetLayout(legacyLayout)
	require.True(t, c.Active())

	// Now set a DVE layout — should replace the legacy layout.
	dveLayout := &dve.Layout{
		Name: "dve",
		Slots: []dve.Slot{
			{
				SourceKey: "cam2",
				Transform: dve.DefaultTransform(100, 100, 480, 270),
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dveLayout)
	require.True(t, c.Active())

	// Only cam2 should be needed now.
	require.False(t, c.NeedsSource("cam1"))
	require.True(t, c.NeedsSource("cam2"))
}

func TestSetDVELayout_Nil(t *testing.T) {
	c := NewCompositor(1920, 1080)

	dveLayout := &dve.Layout{
		Name: "dve",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: dve.DefaultTransform(100, 100, 480, 270),
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dveLayout)
	require.True(t, c.Active())

	// Nil clears it.
	c.SetDVELayout(nil)
	require.False(t, c.Active())
}

func TestDVELayout_OpacityPassthrough(t *testing.T) {
	c := NewCompositor(16, 16)

	tr := dve.DefaultTransform(0, 0, 8, 8)
	tr.Opacity = 0.5

	dl := &dve.Layout{
		Name: "translucent",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: tr,
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	c.SetDVELayout(dl)

	src := makeYUV420(8, 8, 235, 128, 128)
	c.IngestSourceFrame("cam1", src, 8, 8, 0)

	bg := makeYUV420(16, 16, 16, 128, 128)
	result := c.ProcessFrame(bg, 16, 16)

	// At 0.5 opacity, blending 16 and 235 should give ~125.
	got := result[0]
	require.InDelta(t, 125, int(got), 3, "0.5 opacity should blend source with background")
}

func TestSlotFromLegacy(t *testing.T) {
	legacy := Slot{
		SourceKey: "cam1",
		Rect:      image.Rect(100, 200, 740, 680),
		ZOrder:    2,
		Border: BorderConfig{
			Width:   4,
			ColorY:  235,
			ColorCb: 128,
			ColorCr: 128,
		},
		Enabled:    true,
		ScaleMode:  "fill",
		CropAnchor: [2]float64{0.3, 0.7},
	}

	slot := SlotFromLegacy(legacy)

	require.Equal(t, "cam1", slot.SourceKey)
	require.Equal(t, float64(100), slot.Transform.PositionX)
	require.Equal(t, float64(200), slot.Transform.PositionY)
	require.Equal(t, float64(640), slot.Transform.Width)
	require.Equal(t, float64(480), slot.Transform.Height)
	require.Equal(t, 2, slot.ZOrder)
	require.Equal(t, float64(4), slot.Transform.Border.Width)
	require.Equal(t, byte(235), slot.Transform.Border.ColorY)
	require.Equal(t, byte(128), slot.Transform.Border.ColorCb)
	require.Equal(t, byte(128), slot.Transform.Border.ColorCr)
	require.True(t, slot.Enabled)
	require.Equal(t, 1.0, slot.Transform.Opacity)
	require.Equal(t, 0.5, slot.Transform.AnchorX)
	require.Equal(t, 0.5, slot.Transform.AnchorY)
	require.Equal(t, "fill", slot.Transform.ScaleMode)
	require.Equal(t, [2]float64{0.3, 0.7}, slot.Transform.CropAnchor)
}

func TestDVELayout_8Slots(t *testing.T) {
	c := NewCompositor(1920, 1080)

	slots := make([]dve.Slot, 8)
	for i := range slots {
		x := float64((i % 4) * 480)
		y := float64((i / 4) * 270)
		slots[i] = dve.Slot{
			SourceKey: "",
			Transform: dve.DefaultTransform(x, y, 480, 270),
			ZOrder:    i,
			Enabled:   true,
		}
	}
	dl := &dve.Layout{Name: "octo-dve", Slots: slots}
	c.SetDVELayout(dl)

	require.True(t, c.Active())
	snaps := c.SnapshotSlots()
	require.Len(t, snaps, 8)
}
