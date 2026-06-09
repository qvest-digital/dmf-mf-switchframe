package stmap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// TestProcessYUV_FrameLargerThanMap_NoOp verifies the dimension guard: when the
// frame is larger than the ST map the LUTs were built for, ProcessYUV must
// no-op instead of reading past the end of the LUT arrays. Before the guard the
// warp kernel read frameW*frameH int32 entries from a LUT sized mapW*mapH,
// over-reading the backing array (heap over-read / segfault for mmap LUTs).
func TestProcessYUV_FrameLargerThanMap_NoOp(t *testing.T) {
	const mapW, mapH = 8, 8
	p := NewProcessor(Identity(mapW, mapH))
	require.True(t, p.Active())

	// Frame is larger than the map.
	const frameW, frameH = 64, 64
	frameSize := video.YUV420FrameSize(frameW, frameH)
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)
	for i := range src {
		src[i] = byte(i)
	}
	const sentinel = 0xAB
	for i := range dst {
		dst[i] = sentinel
	}

	p.ProcessYUV(dst, src, frameW, frameH)

	// dst must be untouched (no-op) — proving no out-of-bounds warp occurred.
	for i := range dst {
		require.Equalf(t, byte(sentinel), dst[i], "dst[%d] modified; warp ran on mismatched dims", i)
	}
}

// TestProcessYUV_MatchingDims_StillWarps confirms the guard does not break the
// normal equal-dimension path.
func TestProcessYUV_MatchingDims_StillWarps(t *testing.T) {
	const w, h = 8, 8
	p := NewProcessor(Identity(w, h))

	frameSize := video.YUV420FrameSize(w, h)
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src[y*w+x] = byte(y*w + x)
		}
	}

	p.ProcessYUV(dst, src, w, h)

	// Identity map reproduces the Y plane.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			require.Equal(t, src[y*w+x], dst[y*w+x])
		}
	}
}

// TestWarp10_FrameLargerThanMap_Error verifies the 10-bit path rejects a frame
// larger than the map with ErrDimensionMismatch rather than over-reading the
// luma LUT (both in ensure422ChromaLUTs and in the Y warp).
func TestWarp10_FrameLargerThanMap_Error(t *testing.T) {
	const mapW, mapH = 8, 8
	p := NewProcessor(Identity(mapW, mapH))
	require.True(t, p.Active())

	const frameW, frameH = 64, 64
	dst := make([]byte, yuv422p10Size(frameW, frameH))
	src := make([]byte, yuv422p10Size(frameW, frameH))

	err := p.Warp10(dst, src, frameW, frameH)
	require.ErrorIs(t, err, ErrDimensionMismatch)
}
