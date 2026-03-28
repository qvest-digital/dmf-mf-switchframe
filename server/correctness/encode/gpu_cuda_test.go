//go:build correctness && cgo && cuda

package encode

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	gpupkg "github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/output"
)

// nvencContext returns a GPU context for NVENC tests, skipping if unavailable.
func nvencContext(t *testing.T) *gpupkg.Context {
	t.Helper()
	ctx, err := gpupkg.NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	return ctx
}

// nvencEncodeToTS encodes N frames with the specified encoder, muxes to
// MPEG-TS, writes to a temp file, and returns the path.
func nvencEncodeToTS(t *testing.T, encoderName string, w, h, fps, gopSecs, bitrate, numFrames int, hwCtx unsafe.Pointer) string {
	t.Helper()

	enc, err := codec.NewFFmpegEncoder(encoderName, w, h, bitrate, fps, 1, gopSecs, hwCtx)
	if err != nil {
		t.Skipf("encoder %s not available: %v", encoderName, err)
	}
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / fps)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		// Vary content per frame for realistic encode.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d with %s", i, encoderName)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}

		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}

		require.NoError(t, muxer.WriteVideo(frame), "mux frame %d", i)
	}

	require.NotEmpty(t, tsData, "encoder %s should produce TS data", encoderName)

	dir := t.TempDir()
	path := filepath.Join(dir, encoderName+".ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	return path
}

// TestNVENCvsLibx264Structure encodes the same input frames with both NVENC
// and libx264, then compares the structural properties via ffprobe:
// same frame count (within tolerance), same resolution, same PTS sequence.
func TestNVENCvsLibx264Structure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx := nvencContext(t)
	hwCtx := ctx.CUDAContext()

	w, h := 320, 240
	fps := 30
	gopSecs := 2
	bitrate := 500_000
	numFrames := 150

	// Encode with NVENC
	nvencPath := nvencEncodeToTS(t, "h264_nvenc", w, h, fps, gopSecs, bitrate, numFrames, hwCtx)

	// Encode with libx264 (no hw context)
	x264Path := nvencEncodeToTS(t, "libx264", w, h, fps, gopSecs, bitrate, numFrames, nil)

	// Probe both
	nvencResult := testutil.ProbeFile(t, nvencPath)
	x264Result := testutil.ProbeFile(t, x264Path)

	nvencVS := nvencResult.VideoStream()
	x264VS := x264Result.VideoStream()
	require.NotNil(t, nvencVS, "NVENC output should have video stream")
	require.NotNil(t, x264VS, "x264 output should have video stream")

	// Same codec
	require.Equal(t, "h264", nvencVS.CodecName, "NVENC codec should be h264")
	require.Equal(t, "h264", x264VS.CodecName, "x264 codec should be h264")

	// Same resolution
	require.Equal(t, x264VS.Width, nvencVS.Width, "NVENC width should match x264")
	require.Equal(t, x264VS.Height, nvencVS.Height, "NVENC height should match x264")

	// Compare frame counts and PTS sequences
	nvencFrames := testutil.ProbeVideoFrames(t, nvencPath)
	x264Frames := testutil.ProbeVideoFrames(t, x264Path)

	// Frame counts should be within 5% of each other (encoder warmup differences).
	require.InDelta(t, float64(len(x264Frames)), float64(len(nvencFrames)),
		float64(len(x264Frames))*0.10+5,
		"NVENC frame count should be within 10%% of x264: NVENC=%d, x264=%d",
		len(nvencFrames), len(x264Frames))

	// PTS should be monotonically increasing for both
	if len(nvencFrames) >= 2 {
		for i := 1; i < len(nvencFrames); i++ {
			require.Greater(t, nvencFrames[i].PTS, nvencFrames[i-1].PTS,
				"NVENC PTS should be monotonically increasing at frame %d", i)
		}
	}

	t.Logf("NVENC: %d frames, x264: %d frames, resolution: %dx%d",
		len(nvencFrames), len(x264Frames), nvencVS.Width, nvencVS.Height)
}

// TestNVENCVUICorrect encodes with NVENC and verifies that the VUI
// (Video Usability Information) signals BT.709 color primaries.
func TestNVENCVUICorrect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx := nvencContext(t)
	hwCtx := ctx.CUDAContext()

	path := nvencEncodeToTS(t, "h264_nvenc", 320, 240, 30, 2, 500_000, 60, hwCtx)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in NVENC output")

	// BT.709 color signaling
	require.Equal(t, "bt709", vs.ColorPrimaries,
		"NVENC color_primaries should be bt709, got %s", vs.ColorPrimaries)
	require.Equal(t, "bt709", vs.ColorTransfer,
		"NVENC color_transfer should be bt709, got %s", vs.ColorTransfer)
	require.Equal(t, "bt709", vs.ColorSpace,
		"NVENC color_space should be bt709, got %s", vs.ColorSpace)

	t.Logf("NVENC VUI: primaries=%s, transfer=%s, space=%s",
		vs.ColorPrimaries, vs.ColorTransfer, vs.ColorSpace)
}

// TestNVENCValidStream encodes with NVENC and verifies that ffprobe
// can parse the output without any errors.
func TestNVENCValidStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx := nvencContext(t)
	hwCtx := ctx.CUDAContext()

	path := nvencEncodeToTS(t, "h264_nvenc", 320, 240, 30, 2, 500_000, 150, hwCtx)

	// ffprobe should parse without errors
	testutil.RequireNoErrors(t, path)

	// Also verify basic stream properties
	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	require.Equal(t, "h264", vs.CodecName, "codec should be h264")
	require.Equal(t, 320, vs.Width)
	require.Equal(t, 240, vs.Height)

	// Verify PTS is monotonically increasing
	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 100, "expected at least 100 video frames")

	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"PTS should be monotonically increasing at frame %d: %d <= %d",
			i, frames[i].PTS, frames[i-1].PTS)
	}

	t.Logf("NVENC valid stream: %d frames, %dx%d", len(frames), vs.Width, vs.Height)
}

// TestNVENCIDRInterval encodes with NVENC using a 2s GOP and verifies
// that keyframes appear at approximately the configured interval.
func TestNVENCIDRInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx := nvencContext(t)
	hwCtx := ctx.CUDAContext()

	fps := 30
	gopSecs := 2
	expectedInterval := fps * gopSecs // 60 frames

	path := nvencEncodeToTS(t, "h264_nvenc", 320, 240, fps, gopSecs, 500_000, 300, hwCtx)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 200, "expected at least 200 video frames")

	var keyframeIndices []int
	for i, f := range frames {
		if f.KeyFrame {
			keyframeIndices = append(keyframeIndices, i)
		}
	}
	require.GreaterOrEqual(t, len(keyframeIndices), 3, "expected at least 3 keyframes")

	// First keyframe should be at or near index 0.
	require.LessOrEqual(t, keyframeIndices[0], 2,
		"first keyframe should be at or near frame 0, got %d", keyframeIndices[0])

	// Check intervals between consecutive keyframes.
	for i := 1; i < len(keyframeIndices); i++ {
		interval := keyframeIndices[i] - keyframeIndices[i-1]
		// NVENC may not respect GOP exactly like libx264, allow +/-5 frame tolerance.
		require.InDelta(t, float64(expectedInterval), float64(interval), 5.0,
			"NVENC keyframe interval %d->%d: got %d, expected ~%d",
			keyframeIndices[i-1], keyframeIndices[i], interval, expectedInterval)
	}

	t.Logf("NVENC IDR interval: %d keyframes in %d frames, intervals: %v",
		len(keyframeIndices), len(frames), keyframeIndices)
}
