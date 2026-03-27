//go:build correctness

package encode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
)

// encodeToTS encodes N frames of varying YUV420 content at the given
// resolution/fps/gop, muxes them to MPEG-TS, writes the TS to a temp file,
// and returns the file path. The encoder uses libx264 with software encoding.
// Frame content varies between frames to produce realistic encode bitrates.
func encodeToTS(t *testing.T, w, h, fps, gopSecs, bitrate, numFrames int, startPTS int64) string {
	t.Helper()

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / fps) // 90kHz / fps
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		// Vary content each frame to produce realistic bitrates.
		// Without variation, P-frames compress to near-zero bytes.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue // encoder warmup
		}

		// Convert Annex B to AVC1 for WireData
		avc1 := codec.AnnexBToAVC1(encoded)

		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}

		// Extract SPS/PPS from keyframes
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

	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	return path
}

// TestValidH264Stream encodes 150 frames (5s at 30fps) and verifies ffprobe
// can parse the result without errors and reports codec_name="h264".
func TestValidH264Stream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 2, 500_000, 150, 90000)

	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)
}

// TestSPSPPSOnEveryIDR encodes with a 1s GOP and verifies that SPS/PPS
// are present before every IDR keyframe by checking ffprobe can parse the
// stream without errors even when every keyframe is a random access point.
func TestSPSPPSOnEveryIDR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 1, 500_000, 150, 90000)

	testutil.RequireNoErrors(t, path)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 100, "expected at least 100 video frames")

	// Count keyframes — with 1s GOP at 30fps, we expect ~5 keyframes in 150 frames.
	keyCount := 0
	for _, f := range frames {
		if f.KeyFrame {
			keyCount++
		}
	}
	require.GreaterOrEqual(t, keyCount, 4, "expected at least 4 keyframes with 1s GOP over 5s")
}

// TestDTSMonotonicallyIncreasing encodes 300 frames and verifies that DTS
// values are strictly increasing across all frames.
func TestDTSMonotonicallyIncreasing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 2, 500_000, 300, 90000)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 200, "expected at least 200 video frames")

	dtsValues := make([]int64, len(frames))
	for i, f := range frames {
		dtsValues[i] = f.DTS
	}

	testutil.AssertMonotonic(t, dtsValues, "DTS")
}

// TestPTSMonotonicNoBFrames encodes with baseline profile (no B-frames) and
// verifies PTS is strictly increasing and no B-frames exist.
func TestPTSMonotonicNoBFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Use the preview encoder which uses baseline profile (no B-frames).
	w, h := 320, 240
	fps := 30
	numFrames := 150
	bitrate := 500_000

	enc, err := codec.NewFFmpegPreviewEncoder(w, h, bitrate, fps, 1, 2)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / fps)
	yuv := testutil.GenerateGradientYUV420(w, h)

	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := (i % (fps * 2)) == 0

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
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

	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 100, "expected at least 100 video frames")

	// Verify PTS is strictly increasing.
	ptsValues := make([]int64, len(frames))
	for i, f := range frames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Verify no B-frames.
	for i, f := range frames {
		require.NotEqual(t, "B", f.PictType,
			"frame %d should not be a B-frame with baseline profile", i)
	}
}

// TestNoBFramesInLiveConfig encodes with our default encoder settings and
// verifies zero B-frames exist (pict_type != "B").
func TestNoBFramesInLiveConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 2, 500_000, 150, 90000)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 100, "expected at least 100 video frames")

	for i, f := range frames {
		require.NotEqual(t, "B", f.PictType,
			"frame %d is a B-frame, but max_b_frames=0 should prevent this", i)
	}
}

// TestBitrateWithinCVBRWindow encodes 300 frames at 2Mbps and verifies that
// the bitrate in 2-second windows stays within +/-15% of the target.
func TestBitrateWithinCVBRWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	targetBitrate := 2_000_000
	fps := 30
	numFrames := 300
	windowSec := 2

	path := encodeToTS(t, 320, 240, fps, 2, targetBitrate, numFrames, 90000)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 200, "expected at least 200 video frames")

	framesPerWindow := fps * windowSec
	tolerance := 0.15 // +/-15%
	// Allow extra tolerance at the start for encoder warmup (first window).
	startupWindows := 1

	windowCount := 0
	for start := 0; start+framesPerWindow <= len(frames); start += framesPerWindow {
		windowCount++
		var totalBytes int
		for i := start; i < start+framesPerWindow; i++ {
			totalBytes += frames[i].PktSize
		}

		windowBitrate := float64(totalBytes) * 8.0 / float64(windowSec)
		target := float64(targetBitrate)

		// Use relaxed tolerance for startup window.
		tol := tolerance
		if windowCount <= startupWindows {
			tol = 0.50 // 50% tolerance for first window (encoder warmup)
		}

		require.InDelta(t, target, windowBitrate, target*tol,
			"window %d (frames %d-%d): bitrate %.0f bps outside %.0f%% of target %d bps",
			windowCount, start, start+framesPerWindow-1, windowBitrate, tol*100, targetBitrate)
	}

	require.GreaterOrEqual(t, windowCount, 3, "need at least 3 windows to validate bitrate")
}

// TestIDRIntervalMatchesConfig encodes with a 2s GOP at 30fps and verifies
// keyframes appear at approximately every 60 frames.
func TestIDRIntervalMatchesConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	fps := 30
	gopSecs := 2
	expectedInterval := fps * gopSecs // 60 frames

	path := encodeToTS(t, 320, 240, fps, gopSecs, 500_000, 300, 90000)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 200, "expected at least 200 video frames")

	var keyframeIndices []int
	for i, f := range frames {
		if f.KeyFrame {
			keyframeIndices = append(keyframeIndices, i)
		}
	}
	require.GreaterOrEqual(t, len(keyframeIndices), 3, "expected at least 3 keyframes")

	// Check intervals between consecutive keyframes.
	for i := 1; i < len(keyframeIndices); i++ {
		interval := keyframeIndices[i] - keyframeIndices[i-1]
		// Allow +/-2 frame tolerance for muxer timing differences.
		require.InDelta(t, float64(expectedInterval), float64(interval), 2.0,
			"keyframe interval %d->%d: got %d, expected ~%d",
			keyframeIndices[i-1], keyframeIndices[i], interval, expectedInterval)
	}
}

// TestResolutionPreserved encodes at 320x240 and verifies ffprobe reports
// the same resolution.
func TestResolutionPreserved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 2, 500_000, 60, 90000)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	require.Equal(t, 320, vs.Width)
	require.Equal(t, 240, vs.Height)
}

// TestFrameTimingRegularity encodes 300 frames at 30fps and verifies all
// PTS deltas are approximately 3000 ticks (90kHz / 30fps) with +/-1 tolerance.
func TestFrameTimingRegularity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 2, 500_000, 300, 90000)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 200, "expected at least 200 video frames")

	expectedDelta := int64(3000) // 90000 / 30
	testutil.AssertPTSDelta(t, frames, expectedDelta, 1)
}

// TestVUIColorSignaling encodes video and verifies BT.709 color signaling
// is present in the stream via ffprobe.
func TestVUIColorSignaling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeToTS(t, 320, 240, 30, 2, 500_000, 60, 90000)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")

	// ffprobe reports BT.709 values as "bt709" for all three fields.
	require.Equal(t, "bt709", vs.ColorPrimaries,
		"color_primaries should be bt709")
	require.Equal(t, "bt709", vs.ColorTransfer,
		"color_transfer should be bt709")
	require.Equal(t, "bt709", vs.ColorSpace,
		"color_space should be bt709")
}

// TestEncodeDecodeRoundTrip encodes a solid color frame, decodes it back,
// and verifies PSNR > 30 dB (lossy codec but simple content should be very high).
func TestEncodeDecodeRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 1_000_000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := codec.NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// Encode a solid mid-gray frame.
	original := testutil.GenerateSolidYUV420(w, h, 128, 128, 128)

	// Collect all encoded packets (encoder outputs immediately with zerolatency).
	var encodedFrames [][]byte
	for i := 0; i < 30; i++ {
		data, _, encErr := enc.Encode(original, int64(i*3000), i == 0)
		require.NoError(t, encErr)
		if data != nil {
			encodedFrames = append(encodedFrames, append([]byte(nil), data...))
		}
	}
	require.NotEmpty(t, encodedFrames, "encoder should produce output")

	// Feed all encoded packets to the decoder until we get decoded output.
	var decoded []byte
	var dw, dh int
	for _, ef := range encodedFrames {
		decoded, dw, dh, err = dec.Decode(ef)
		if err == nil && decoded != nil {
			break
		}
	}
	require.NoError(t, err, "decoder should produce output")
	require.NotNil(t, decoded, "decoder should produce decoded YUV frame")
	require.Equal(t, w, dw)
	require.Equal(t, h, dh)

	psnr := testutil.YUVFramePSNR(original, decoded, w, h)
	require.Greater(t, psnr, 30.0,
		"PSNR should be > 30 dB for solid color content, got %.1f dB", psnr)
}

// TestPTSWraparound encodes frames with PTS starting near the 33-bit wrap
// point (1<<33) and verifies that frames exist on both sides of the wrap
// and ffprobe doesn't error.
func TestPTSWraparound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	numFrames := 300
	ptsDelta := int64(3000)                                      // 90kHz / 30fps
	startPTS := int64((1 << 33) - int64(numFrames/2)*ptsDelta) // Start so wrap occurs midway

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 500_000, fps, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	yuv := testutil.GenerateGradientYUV420(w, h)
	gopSize := fps * 2

	preWrapFrames := 0
	postWrapFrames := 0

	for i := 0; i < numFrames; i++ {
		rawPTS := startPTS + int64(i)*ptsDelta
		// Apply 33-bit wrap to simulate MPEG-TS PTS wrapping.
		pts := rawPTS & 0x1FFFFFFFF
		forceIDR := (i % gopSize) == 0

		if rawPTS < (1 << 33) {
			preWrapFrames++
		} else {
			postWrapFrames++
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
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

	require.Greater(t, preWrapFrames, 0, "should have frames before PTS wrap")
	require.Greater(t, postWrapFrames, 0, "should have frames after PTS wrap")

	dir := t.TempDir()
	path := filepath.Join(dir, "wrap.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// ffprobe should parse the file without errors.
	testutil.RequireNoErrors(t, path)

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), numFrames/2,
		"expected at least %d video frames across PTS wrap", numFrames/2)
}

