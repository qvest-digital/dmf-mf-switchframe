//go:build cgo && !noffmpeg

package codec

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/video"
)

// sliceCount is the number of coded slices in one encoded picture. With
// the zerolatency tune x264 threads by slice, one slice per thread.
func sliceCount(t *testing.T, au []byte) int {
	t.Helper()
	n := 0
	for _, nalu := range splitAnnexBNALUs(au) {
		if typ := nalu[0] & 0x1f; typ == 1 || typ == 5 {
			n++
		}
	}
	return n
}

func encodeOneFrame(t *testing.T, opts *EncoderOptions) []byte {
	t.Helper()
	const w, h = 1920, 1080
	enc, err := NewFFmpegEncoder("libx264", w, h, 10_000_000, 50, 1, 2, nil, opts)
	require.NoError(t, err)
	defer enc.Close()
	yuv := make([]byte, video.YUV420FrameSize(w, h))
	for i := range yuv {
		yuv[i] = byte(i * 7)
	}
	au, _, err := enc.Encode(yuv, 0, true)
	require.NoError(t, err)
	require.NotEmpty(t, au)
	return au
}

func TestFFmpegEncoderUsesConfiguredX264Threads(t *testing.T) {
	for _, n := range []int{4, 12, 16} {
		require.Equal(t, n, sliceCount(t, encodeOneFrame(t, &EncoderOptions{Threads: n})), "threads=%d", n)
	}
	// x264 allows at most one slice per four macroblock rows: 17 at 1080p.
	require.Equal(t, 17, sliceCount(t, encodeOneFrame(t, &EncoderOptions{Threads: 24})))
}

// Every encoder but the program one passes no thread count and keeps the
// host CPU count clamped to 2..8.
func TestFFmpegEncoderDefaultX264Threads(t *testing.T) {
	want := min(max(runtime.NumCPU(), 2), 8)
	require.Equal(t, want, sliceCount(t, encodeOneFrame(t, nil)))
	require.Equal(t, want, sliceCount(t, encodeOneFrame(t, &EncoderOptions{Preset: OutputPresetFastest})))
}
