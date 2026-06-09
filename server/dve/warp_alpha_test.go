package dve

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProcessSlotDVEWarp_TranslucentRespectsCoverageMask reproduces the bug
// where a rotated slot at effective opacity < 1 blended the black AABB
// padding (pixels never covered by the rotated quad) over the underlying
// program, darkening the whole bounding box during dissolves.
func TestProcessSlotDVEWarp_TranslucentRespectsCoverageMask(t *testing.T) {
	c := NewCompositor(32, 32)

	// 16x16 source rotated 45 degrees inside a 16x16 slot at (8,8).
	// The slot rect corners are NOT covered by the rotated quad.
	src := makeYUV420(16, 16, 60, 90, 160)
	bg := makeYUV420(32, 32, 200, 100, 60)

	tr := DefaultTransform(8, 8, 16, 16)
	tr.RotationDeg = 45

	snap := &slotSnapshot{
		slot:   Slot{SourceKey: "cam1", Transform: tr, Enabled: true},
		srcYUV: src,
		srcW:   16,
		srcH:   16,
		rect:   image.Rect(8, 8, 24, 24),
		alpha:  0.5,
	}

	c.processSlotDVEWarp(bg, 32, 32, snap)

	ySize := 32 * 32
	chromaW := 16

	// Rect corner (8,8) is outside the rotated quad: the program must show
	// through unchanged instead of being blended with black padding.
	require.Equal(t, byte(200), bg[8*32+8],
		"uncovered AABB corner Y must remain program background")
	require.Equal(t, byte(100), bg[ySize+4*chromaW+4],
		"uncovered AABB corner Cb must remain program background")
	require.Equal(t, byte(60), bg[ySize+chromaW*16+4*chromaW+4],
		"uncovered AABB corner Cr must remain program background")

	// Slot center (16,16) IS covered: it must be a 50/50 blend of source
	// and program.
	require.InDelta(t, 130, int(bg[16*32+16]), 2,
		"covered center Y should be 50/50 blend of src(60) and bg(200)")
	require.InDelta(t, 95, int(bg[ySize+8*chromaW+8]), 2,
		"covered center Cb should be 50/50 blend of src(90) and bg(100)")
	require.InDelta(t, 110, int(bg[ySize+chromaW*16+8*chromaW+8]), 2,
		"covered center Cr should be 50/50 blend of src(160) and bg(60)")
}
