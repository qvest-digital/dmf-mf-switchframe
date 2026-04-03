//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

func TestFillColor(t *testing.T) {
	ctx := testContext(t)
	pool, err := NewFramePool(ctx, 64, 64, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Fill with a distinctive color (BT.709 limited-range green-ish)
	green := YUVColor{Y: 145, Cb: 54, Cr: 34}
	err = FillColor(ctx, frame, green)
	require.NoError(t, err)

	// Download and verify
	yuv := make([]byte, video.YUV420FrameSize(64, 64))
	require.NoError(t, Download(ctx, yuv, frame, 64, 64))

	// Check Y plane
	require.Equal(t, byte(145), yuv[0], "Y[0] should be 145")
	require.Equal(t, byte(145), yuv[64*32+16], "Y[mid] should be 145")

	// Check Cb and Cr planes (Download outputs YUV420p: Y, then Cb, then Cr)
	cbOff := 64 * 64
	crOff := cbOff + 32*32
	require.Equal(t, byte(54), yuv[cbOff], "Cb should be 54")
	require.Equal(t, byte(34), yuv[crOff], "Cr should be 34")
}

func TestFillColorMatchesFillBlack(t *testing.T) {
	ctx := testContext(t)
	pool, err := NewFramePool(ctx, 64, 64, 3)
	require.NoError(t, err)
	defer pool.Close()

	f1, _ := pool.Acquire()
	f2, _ := pool.Acquire()
	defer f1.Release()
	defer f2.Release()

	// FillBlack and FillColor with black should produce identical results
	require.NoError(t, FillBlack(ctx, f1))
	require.NoError(t, FillColor(ctx, f2, ColorBlack))

	yuv1 := make([]byte, video.YUV420FrameSize(64, 64))
	yuv2 := make([]byte, video.YUV420FrameSize(64, 64))
	require.NoError(t, Download(ctx, yuv1, f1, 64, 64))
	require.NoError(t, Download(ctx, yuv2, f2, 64, 64))
	require.Equal(t, yuv1, yuv2, "FillColor(black) should match FillBlack")
}
