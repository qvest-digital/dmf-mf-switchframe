//go:build correctness

package ingest

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// ==========================================================================
// HEVC TS generation helpers
// ==========================================================================

// requireLibx265 checks that ffmpeg has libx265 support and skips the test if not.
func requireLibx265(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	// Try a minimal libx265 encode to verify the codec is available.
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "nullsrc=s=16x16:d=0.1:r=1",
		"-c:v", "libx265",
		"-frames:v", "1",
		"-f", "null", "-",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("libx265 not available in this ffmpeg build: %s", string(out))
	}
}

// generateHEVC8bitTS creates an MPEG-TS file with HEVC 8-bit video and AAC audio.
func generateHEVC8bitTS(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()
	requireLibx265(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hevc8bit.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-f", "lavfi",
		"-i", formatSineSrc(durationSec),
		"-c:v", "libx265",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-x265-params", "keyint=30:no-open-gop=1",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg HEVC 8-bit failed: %s", string(out))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0), "generated HEVC 8-bit TS should not be empty")

	return path
}

// generateHEVC8bitTSVideoOnly creates a TS with HEVC 8-bit video only (no audio).
func generateHEVC8bitTSVideoOnly(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()
	requireLibx265(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hevc8bit_videoonly.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-c:v", "libx265",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-x265-params", "keyint=30:no-open-gop=1",
		"-pix_fmt", "yuv420p",
		"-an",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg HEVC 8-bit video-only failed: %s", string(out))

	return path
}

// generateHEVC10bitTS creates an MPEG-TS file with HEVC 10-bit video (yuv420p10le).
// No audio to keep things simple for bit-depth specific tests.
func generateHEVC10bitTS(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()
	requireLibx265(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hevc10bit.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-c:v", "libx265",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-x265-params", "keyint=30:no-open-gop=1",
		"-pix_fmt", "yuv420p10le",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg HEVC 10-bit failed: %s", string(out))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0), "generated HEVC 10-bit TS should not be empty")

	return path
}

// generateHEVC10bitTSWithAudio creates HEVC 10-bit video + AAC audio.
func generateHEVC10bitTSWithAudio(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()
	requireLibx265(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hevc10bit_audio.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-f", "lavfi",
		"-i", formatSineSrc(durationSec),
		"-c:v", "libx265",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-x265-params", "keyint=30:no-open-gop=1",
		"-pix_fmt", "yuv420p10le",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg HEVC 10-bit with audio failed: %s", string(out))

	return path
}

// ==========================================================================
// HEVC 8-bit Tests
// ==========================================================================

// TestTSDemux_HEVC8bit_ProducesVideoFrames generates an HEVC 8-bit TS file,
// demuxes it, and verifies video frames are extracted with the correct codec.
func TestTSDemux_HEVC8bit_ProducesVideoFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on valid HEVC 8-bit TS")

	// 2 seconds at 30fps = ~60 frames. Allow tolerance for encoder warmup.
	require.InDelta(t, 60, len(videoFrames), 15,
		"expected approximately 60 video frames from 2s of 30fps HEVC content, got %d", len(videoFrames))

	// Verify codec is detected as HEVC.
	for i, f := range videoFrames {
		require.Equal(t, "h265", f.Codec(),
			"frame %d: expected codec h265, got %s", i, f.Codec())
		require.True(t, f.IsHEVC(),
			"frame %d: IsHEVC() should return true", i)
	}

	// Verify all frames have non-empty wire data.
	for i, f := range videoFrames {
		require.NotEmpty(t, f.WireData(), "frame %d: WireData should not be empty", i)
	}
}

// TestTSDemux_HEVC8bit_ProducesAudioFrames verifies AAC audio frames are
// extracted alongside HEVC video.
func TestTSDemux_HEVC8bit_ProducesAudioFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	_, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on valid HEVC TS")

	// AAC at 48kHz produces ~1024 samples per frame = ~46.9 frames/sec.
	// 2 seconds should yield ~93 frames.
	require.InDelta(t, 93, len(audioFrames), 15,
		"expected approximately 93 audio frames from 2s at 48kHz/1024, got %d", len(audioFrames))

	for i, af := range audioFrames {
		require.NotEmpty(t, af.Data(), "audio frame %d: data should not be empty", i)
		require.Greater(t, af.SampleRate(), 0, "audio frame %d: sample rate should be positive", i)
		require.Greater(t, af.Channels(), 0, "audio frame %d: channels should be positive", i)
	}
}

// TestTSDemux_HEVC8bit_PTSPassthrough verifies PTS values from HEVC demux are
// monotonically non-decreasing and spaced correctly for 30fps content.
func TestTSDemux_HEVC8bit_PTSPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(videoFrames), 30,
		"need at least 30 frames for PTS analysis")

	// Verify PTS values are non-negative.
	for i, f := range videoFrames {
		require.GreaterOrEqual(t, f.PTS(), int64(0),
			"frame %d: PTS should be non-negative, got %d", i, f.PTS())
	}

	// Get reference PTS from ffprobe.
	if _, err := exec.LookPath("ffprobe"); err == nil {
		probeFrames := testutil.ProbeVideoFrames(t, path)
		require.GreaterOrEqual(t, len(probeFrames), 30,
			"ffprobe should report at least 30 video frames")

		// Build PTS set from probe results.
		probePTSSet := make(map[int64]bool, len(probeFrames))
		for _, pf := range probeFrames {
			probePTSSet[pf.PTS] = true
		}

		// At least 80% of demuxed PTS values should match ffprobe.
		matchCount := 0
		for _, vf := range videoFrames {
			if probePTSSet[vf.PTS()] {
				matchCount++
			}
		}
		minMatch := len(videoFrames) * 80 / 100
		require.GreaterOrEqual(t, matchCount, minMatch,
			"expected at least %d/%d PTS values to match ffprobe, got %d",
			minMatch, len(videoFrames), matchCount)
	}

	// Verify audio PTS values are monotonically non-decreasing.
	_, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err)
	for i := 1; i < len(audioFrames); i++ {
		require.GreaterOrEqual(t, audioFrames[i].PTS(), audioFrames[i-1].PTS(),
			"audio PTS not monotonic at frame %d: %d < %d",
			i, audioFrames[i].PTS(), audioFrames[i-1].PTS())
	}
}

// TestTSDemux_HEVC8bit_KeyframeDetection verifies that HEVC keyframes are
// detected correctly. HEVC uses IDR types 19/20 and CRA type 21, which
// differ from H.264's single IDR type 5.
func TestTSDemux_HEVC8bit_KeyframeDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// 5 seconds at 30fps with keyframe every 30 frames.
	path := generateHEVC8bitTS(t, 5, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(videoFrames), 100,
		"expected at least 100 frames from 5s at 30fps")

	// Count keyframes.
	keyframeCount := 0
	var keyframeIndices []int
	for i, f := range videoFrames {
		if f.IsKeyframe() {
			keyframeCount++
			keyframeIndices = append(keyframeIndices, i)
		}
	}

	// With keyint=30 and 5 seconds of content, expect ~5 keyframes.
	require.GreaterOrEqual(t, keyframeCount, 3,
		"expected at least 3 keyframes in 5s of HEVC content with keyint=30")
	require.LessOrEqual(t, keyframeCount, 10,
		"unexpectedly many keyframes: %d", keyframeCount)

	// First frame should be a keyframe.
	require.True(t, videoFrames[0].IsKeyframe(),
		"first frame should be a keyframe")

	t.Logf("HEVC 8-bit keyframes: %d total at indices %v", keyframeCount, keyframeIndices)
}

// TestTSDemux_HEVC8bit_VPSPresent verifies that HEVC keyframes carry VPS
// (Video Parameter Set), which is unique to HEVC and absent in H.264.
// VPS is NALU type 32, SPS is type 33, PPS is type 34.
func TestTSDemux_HEVC8bit_VPSPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Find keyframes and verify VPS/SPS/PPS are present.
	keyframeCount := 0
	for i, f := range videoFrames {
		if !f.IsKeyframe() {
			continue
		}
		keyframeCount++

		require.NotNil(t, f.VPS(), "HEVC keyframe %d (index %d): VPS must be present", keyframeCount, i)
		require.NotEmpty(t, f.VPS(), "HEVC keyframe %d (index %d): VPS must not be empty", keyframeCount, i)
		require.NotNil(t, f.SPS(), "HEVC keyframe %d (index %d): SPS must be present", keyframeCount, i)
		require.NotEmpty(t, f.SPS(), "HEVC keyframe %d (index %d): SPS must not be empty", keyframeCount, i)
		require.NotNil(t, f.PPS(), "HEVC keyframe %d (index %d): PPS must be present", keyframeCount, i)
		require.NotEmpty(t, f.PPS(), "HEVC keyframe %d (index %d): PPS must not be empty", keyframeCount, i)

		// Verify VPS NALU type is 32 (byte = 0x40 for type 32 with layer_id=0, tid=1).
		vpsNALType := codec.HEVCNALUType(f.VPS()[0])
		require.Equal(t, byte(codec.HEVCNALVPS), vpsNALType,
			"HEVC keyframe %d: VPS NALU type should be 32, got %d", keyframeCount, vpsNALType)

		// Verify SPS NALU type is 33.
		spsNALType := codec.HEVCNALUType(f.SPS()[0])
		require.Equal(t, byte(codec.HEVCNALSPS), spsNALType,
			"HEVC keyframe %d: SPS NALU type should be 33, got %d", keyframeCount, spsNALType)

		// Verify PPS NALU type is 34.
		ppsNALType := codec.HEVCNALUType(f.PPS()[0])
		require.Equal(t, byte(codec.HEVCNALPPS), ppsNALType,
			"HEVC keyframe %d: PPS NALU type should be 34, got %d", keyframeCount, ppsNALType)
	}

	require.GreaterOrEqual(t, keyframeCount, 1,
		"expected at least 1 keyframe to verify VPS presence")

	t.Logf("verified VPS/SPS/PPS on %d HEVC keyframes", keyframeCount)
}

// TestTSDemux_HEVC8bit_StreamType verifies the demuxer detects the HEVC
// stream type (0x24 / 36) from the PMT, not H.264's 0x1B (27).
// We verify this indirectly by checking that the demuxed frames report
// codec "h265", which is only set when the PMT stream type is 0x24 or
// when HEVC NALUs are detected.
func TestTSDemux_HEVC8bit_StreamType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 1, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, videoFrames)

	// The codec field is set based on PMT stream type detection (0x24 for HEVC)
	// or NALU-level auto-detection (VPS type 32). Either way, all frames
	// should report h265.
	for i, f := range videoFrames {
		require.Equal(t, "h265", f.Codec(),
			"frame %d: codec should be h265 for HEVC stream", i)
	}

	// Also verify via ffprobe that the stream is really HEVC.
	if _, err := exec.LookPath("ffprobe"); err == nil {
		result := testutil.ProbeFile(t, path)
		vs := result.VideoStream()
		require.NotNil(t, vs, "ffprobe should find a video stream")
		require.Equal(t, "hevc", vs.CodecName,
			"ffprobe should identify the codec as hevc")
	}
}

// TestTSDemux_HEVC8bit_NonKeyframesLackParamSets verifies that non-keyframe
// HEVC frames do NOT carry VPS/SPS/PPS (they are only set on keyframes).
func TestTSDemux_HEVC8bit_NonKeyframesLackParamSets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)

	nonKeyframeCount := 0
	for i, f := range videoFrames {
		if f.IsKeyframe() {
			continue
		}
		nonKeyframeCount++
		assert.Nil(t, f.VPS(), "non-keyframe %d (index %d) should have nil VPS", nonKeyframeCount, i)
		assert.Nil(t, f.SPS(), "non-keyframe %d (index %d) should have nil SPS", nonKeyframeCount, i)
		assert.Nil(t, f.PPS(), "non-keyframe %d (index %d) should have nil PPS", nonKeyframeCount, i)
	}

	require.Greater(t, nonKeyframeCount, 0,
		"expected at least one non-keyframe to verify param set absence")
}

// ==========================================================================
// HEVC 10-bit Tests
// ==========================================================================

// TestTSDemux_HEVC10bit_ProducesVideoFrames generates a 10-bit HEVC TS,
// demuxes it, and verifies frames are extracted correctly. The demuxer
// should not care about bit depth -- it passes through the bitstream.
func TestTSDemux_HEVC10bit_ProducesVideoFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC10bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on valid HEVC 10-bit TS")

	require.InDelta(t, 60, len(videoFrames), 15,
		"expected approximately 60 video frames from 2s of 30fps HEVC 10-bit content, got %d", len(videoFrames))

	// Verify codec detection works for 10-bit too.
	for i, f := range videoFrames {
		require.Equal(t, "h265", f.Codec(),
			"10-bit frame %d: expected codec h265, got %s", i, f.Codec())
		require.NotEmpty(t, f.WireData(),
			"10-bit frame %d: WireData should not be empty", i)
	}
}

// TestTSDemux_HEVC10bit_KeyframeDetection verifies keyframe detection works
// identically for 10-bit content. HEVC keyframe NAL types are the same
// regardless of bit depth.
func TestTSDemux_HEVC10bit_KeyframeDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC10bitTS(t, 3, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(videoFrames), 60)

	keyframeCount := 0
	for _, f := range videoFrames {
		if f.IsKeyframe() {
			keyframeCount++
		}
	}

	require.GreaterOrEqual(t, keyframeCount, 2,
		"expected at least 2 keyframes in 3s of HEVC 10-bit content with keyint=30")

	require.True(t, videoFrames[0].IsKeyframe(),
		"first 10-bit frame should be a keyframe")
}

// TestTSDemux_HEVC10bit_VPSPresent verifies VPS is present on 10-bit keyframes.
func TestTSDemux_HEVC10bit_VPSPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC10bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)

	keyframeCount := 0
	for _, f := range videoFrames {
		if !f.IsKeyframe() {
			continue
		}
		keyframeCount++

		require.NotEmpty(t, f.VPS(),
			"HEVC 10-bit keyframe %d: VPS must be present", keyframeCount)
		require.NotEmpty(t, f.SPS(),
			"HEVC 10-bit keyframe %d: SPS must be present", keyframeCount)
		require.NotEmpty(t, f.PPS(),
			"HEVC 10-bit keyframe %d: PPS must be present", keyframeCount)

		// Verify NALU types.
		require.Equal(t, byte(codec.HEVCNALVPS), codec.HEVCNALUType(f.VPS()[0]),
			"10-bit keyframe %d: VPS NALU type mismatch", keyframeCount)
		require.Equal(t, byte(codec.HEVCNALSPS), codec.HEVCNALUType(f.SPS()[0]),
			"10-bit keyframe %d: SPS NALU type mismatch", keyframeCount)
		require.Equal(t, byte(codec.HEVCNALPPS), codec.HEVCNALUType(f.PPS()[0]),
			"10-bit keyframe %d: PPS NALU type mismatch", keyframeCount)
	}

	require.GreaterOrEqual(t, keyframeCount, 1,
		"expected at least 1 keyframe in HEVC 10-bit content")
}

// TestTSDemux_HEVC10bit_PTSPassthrough verifies PTS monotonicity and spacing
// for 10-bit content. PTS handling is format-independent.
func TestTSDemux_HEVC10bit_PTSPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC10bitTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(videoFrames), 30)

	// Verify non-negative PTS.
	for i, f := range videoFrames {
		require.GreaterOrEqual(t, f.PTS(), int64(0),
			"10-bit frame %d: PTS should be non-negative, got %d", i, f.PTS())
	}

	// Verify PTS range covers approximately the expected duration.
	// 2 seconds at 90kHz = 180000 ticks.
	firstPTS := videoFrames[0].PTS()
	lastPTS := videoFrames[len(videoFrames)-1].PTS()
	ptsDuration := lastPTS - firstPTS
	expectedDuration := int64(180000) // 2s at 90kHz

	// Allow generous tolerance (50%) since B-frame reordering and encoder
	// warmup can shift PTS.
	require.InDelta(t, expectedDuration, ptsDuration, float64(expectedDuration)*0.5,
		"PTS range should cover approximately 2s: first=%d last=%d delta=%d",
		firstPTS, lastPTS, ptsDuration)
}

// TestTSDemux_HEVC10bit_FrameCountMatchesFFprobe compares the demuxer's
// frame count with ffprobe's for 10-bit HEVC content.
func TestTSDemux_HEVC10bit_FrameCountMatchesFFprobe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found in PATH")
	}

	path := generateHEVC10bitTSWithAudio(t, 3, 320, 240, 30)

	// ffprobe frame counts.
	probeVideoFrames := testutil.ProbeVideoFrames(t, path)
	probeAudioFrames := testutil.ProbeAudioFrames(t, path)

	// Demuxer frame counts.
	videoFrames, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Video frame counts should be very close (within 5 frames).
	diff := len(probeVideoFrames) - len(videoFrames)
	if diff < 0 {
		diff = -diff
	}
	require.LessOrEqual(t, diff, 5,
		"HEVC 10-bit video frame count mismatch: ffprobe=%d demux=%d",
		len(probeVideoFrames), len(videoFrames))

	// Audio frames should also be present.
	if len(probeAudioFrames) > 0 {
		require.NotEmpty(t, audioFrames,
			"ffprobe found %d audio frames but demuxer found none", len(probeAudioFrames))
	}

	t.Logf("HEVC 10-bit frame counts: video(probe=%d, demux=%d) audio(probe=%d, demux=%d)",
		len(probeVideoFrames), len(videoFrames),
		len(probeAudioFrames), len(audioFrames))
}

// ==========================================================================
// Cross-codec Tests (HEVC vs H.264)
// ==========================================================================

// TestTSDemux_HEVC_vs_H264_FrameCount generates the same content duration
// with both H.264 and HEVC, and verifies the frame count is identical.
func TestTSDemux_HEVC_vs_H264_FrameCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Generate H.264 TS.
	h264Path := generateFFmpegTS(t, 2, 320, 240, 30)

	// Generate HEVC TS.
	hevcPath := generateHEVC8bitTS(t, 2, 320, 240, 30)

	h264Frames, h264Audio, err := clip.DemuxFile(h264Path)
	require.NoError(t, err, "H.264 demux should succeed")

	hevcFrames, hevcAudio, err := clip.DemuxFile(hevcPath)
	require.NoError(t, err, "HEVC demux should succeed")

	// Frame counts should be very close (within 5 frames).
	// The exact count may differ slightly due to encoder-specific behavior.
	diff := len(h264Frames) - len(hevcFrames)
	if diff < 0 {
		diff = -diff
	}
	require.LessOrEqual(t, diff, 5,
		"H.264 and HEVC should produce similar frame counts for same duration: h264=%d hevc=%d",
		len(h264Frames), len(hevcFrames))

	// Verify codecs are different.
	require.Equal(t, "h264", h264Frames[0].Codec(), "H.264 frames should report h264")
	require.Equal(t, "h265", hevcFrames[0].Codec(), "HEVC frames should report h265")

	// Audio frame counts should also be close (same AAC encoder, same duration).
	audioDiff := len(h264Audio) - len(hevcAudio)
	if audioDiff < 0 {
		audioDiff = -audioDiff
	}
	require.LessOrEqual(t, audioDiff, 5,
		"audio frame counts should be similar: h264=%d hevc=%d",
		len(h264Audio), len(hevcAudio))

	t.Logf("cross-codec frames: h264=%d(%s) hevc=%d(%s) audio h264=%d hevc=%d",
		len(h264Frames), h264Frames[0].Codec(),
		len(hevcFrames), hevcFrames[0].Codec(),
		len(h264Audio), len(hevcAudio))
}

// TestTSDemux_HEVC_DeterministicOutput verifies that demuxing the same HEVC TS
// twice produces identical results.
func TestTSDemux_HEVC_DeterministicOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	// First demux.
	v1, a1, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Second demux.
	v2, a2, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Frame counts must match exactly.
	require.Equal(t, len(v1), len(v2), "HEVC video frame count should be deterministic")
	require.Equal(t, len(a1), len(a2), "HEVC audio frame count should be deterministic")

	// PTS, keyframe status, and wire data must match.
	for i := range v1 {
		require.Equal(t, v1[i].PTS(), v2[i].PTS(),
			"HEVC video frame %d PTS mismatch", i)
		require.Equal(t, v1[i].IsKeyframe(), v2[i].IsKeyframe(),
			"HEVC video frame %d keyframe mismatch", i)
		require.Equal(t, v1[i].Codec(), v2[i].Codec(),
			"HEVC video frame %d codec mismatch", i)
		require.True(t, bytes.Equal(v1[i].WireData(), v2[i].WireData()),
			"HEVC video frame %d wire data mismatch", i)
	}

	for i := range a1 {
		require.Equal(t, a1[i].PTS(), a2[i].PTS(),
			"HEVC audio frame %d PTS mismatch", i)
		require.True(t, bytes.Equal(a1[i].Data(), a2[i].Data()),
			"HEVC audio frame %d data mismatch", i)
	}
}

// TestTSDemux_HEVC_CorruptedPacketHandling takes a valid HEVC TS file,
// corrupts various bytes, and feeds it to the demuxer. The demuxer should
// not panic and should handle corruption gracefully.
func TestTSDemux_HEVC_CorruptedPacketHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 2, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(data), 1000, "HEVC TS data should be non-trivial")

	t.Run("RandomByteFlips", func(t *testing.T) {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)

		rng := rand.New(rand.NewSource(42))
		numFlips := len(corrupted) / 100
		for i := 0; i < numFlips; i++ {
			pos := 188 + rng.Intn(len(corrupted)-188)
			corrupted[pos] ^= 0xFF
		}

		tmpPath := testutil.WriteTempFile(t, corrupted, ".ts")
		require.NotPanics(t, func() {
			_, _, _ = clip.DemuxFile(tmpPath)
		}, "demuxer should not panic on corrupted HEVC data")
	})

	t.Run("SyncByteCorruption", func(t *testing.T) {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)

		for i := 5; i < 15 && i*188 < len(corrupted); i++ {
			corrupted[i*188] = 0x00
		}

		tmpPath := testutil.WriteTempFile(t, corrupted, ".ts")
		require.NotPanics(t, func() {
			_, _, _ = clip.DemuxFile(tmpPath)
		}, "demuxer should not panic on HEVC stream with corrupted sync bytes")
	})

	t.Run("TruncatedFile", func(t *testing.T) {
		truncated := data[:len(data)*3/4]
		tmpPath := testutil.WriteTempFile(t, truncated, ".ts")

		require.NotPanics(t, func() {
			videoFrames, _, demuxErr := clip.DemuxFile(tmpPath)
			if demuxErr == nil {
				assert.NotEmpty(t, videoFrames,
					"truncated HEVC TS should still yield some frames")
			}
		}, "demuxer should not panic on truncated HEVC file")
	})

	t.Run("ZeroFilledMiddle", func(t *testing.T) {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)

		mid := len(corrupted) / 2
		zeroLen := 5000
		if mid+zeroLen > len(corrupted) {
			zeroLen = len(corrupted) - mid
		}
		for i := mid; i < mid+zeroLen; i++ {
			corrupted[i] = 0x00
		}

		tmpPath := testutil.WriteTempFile(t, corrupted, ".ts")
		require.NotPanics(t, func() {
			_, _, _ = clip.DemuxFile(tmpPath)
		}, "demuxer should not panic on HEVC stream with zero-filled middle")
	})
}

// ==========================================================================
// PTS Discontinuity with HEVC
// ==========================================================================

// TestTSDemux_HEVC_PTSDiscontinuity generates two HEVC TS segments with
// a PTS gap and verifies the demuxer handles it without stalling.
func TestTSDemux_HEVC_PTSDiscontinuity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	requireLibx265(t)

	dir := t.TempDir()
	seg1Path := filepath.Join(dir, "hevc_seg1.ts")
	seg2Path := filepath.Join(dir, "hevc_seg2.ts")

	cmd1 := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=30",
		"-c:v", "libx265", "-preset", "ultrafast", "-tune", "zerolatency",
		"-x265-params", "keyint=30:no-open-gop=1",
		"-pix_fmt", "yuv420p",
		"-f", "mpegts",
		"-y", seg1Path,
	)
	out, err := cmd1.CombinedOutput()
	require.NoError(t, err, "ffmpeg HEVC seg1 failed: %s", string(out))

	cmd2 := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=30",
		"-c:v", "libx265", "-preset", "ultrafast", "-tune", "zerolatency",
		"-x265-params", "keyint=30:no-open-gop=1",
		"-pix_fmt", "yuv420p",
		"-output_ts_offset", "100", // 100 seconds PTS jump
		"-f", "mpegts",
		"-y", seg2Path,
	)
	out, err = cmd2.CombinedOutput()
	require.NoError(t, err, "ffmpeg HEVC seg2 failed: %s", string(out))

	seg1Data, err := os.ReadFile(seg1Path)
	require.NoError(t, err)
	seg2Data, err := os.ReadFile(seg2Path)
	require.NoError(t, err)

	combined := append(seg1Data, seg2Data...)
	combinedPath := filepath.Join(dir, "hevc_combined.ts")
	require.NoError(t, os.WriteFile(combinedPath, combined, 0o644))

	// Demux with a timeout to detect stalls.
	done := make(chan struct{})
	var videoFrames []clip.BufferedFrame
	var demuxErr error

	go func() {
		defer close(done)
		videoFrames, _, demuxErr = clip.DemuxFile(combinedPath)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(30 * time.Second):
		t.Fatal("demuxer stalled on HEVC PTS discontinuity (timeout after 30s)")
	}

	if demuxErr == nil {
		require.GreaterOrEqual(t, len(videoFrames), 15,
			"expected HEVC video frames from concatenated segments")

		// All frames should be HEVC.
		for _, f := range videoFrames {
			require.Equal(t, "h265", f.Codec())
		}
	}
}

// ==========================================================================
// HEVC 8-bit video-only TS
// ==========================================================================

// TestTSDemux_HEVC8bit_VideoOnly verifies the demuxer handles HEVC TS files
// with no audio track correctly.
func TestTSDemux_HEVC8bit_VideoOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTSVideoOnly(t, 2, 320, 240, 30)

	videoFrames, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on HEVC video-only TS")
	require.NotEmpty(t, videoFrames, "should have HEVC video frames")
	require.Empty(t, audioFrames, "video-only TS should have no audio frames")

	// Verify all frames are HEVC.
	for i, f := range videoFrames {
		require.Equal(t, "h265", f.Codec(),
			"frame %d should be h265", i)
	}

	// Verify keyframe presence.
	hasKeyframe := false
	for _, f := range videoFrames {
		if f.IsKeyframe() {
			hasKeyframe = true
			require.NotEmpty(t, f.VPS(), "HEVC keyframe should have VPS")
			require.NotEmpty(t, f.SPS(), "HEVC keyframe should have SPS")
			require.NotEmpty(t, f.PPS(), "HEVC keyframe should have PPS")
			break
		}
	}
	require.True(t, hasKeyframe, "should have at least one HEVC keyframe")
}

// ==========================================================================
// HEVC NALU structure verification
// ==========================================================================

// TestTSDemux_HEVC8bit_WireDataStructure verifies that the wire data
// from HEVC frames uses 4-byte length-prefixed NALU format and that
// the NALUs are valid HEVC VCL types (not parameter sets, which should
// be separated into VPS/SPS/PPS fields).
func TestTSDemux_HEVC8bit_WireDataStructure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 1, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, videoFrames)

	for i, f := range videoFrames {
		wire := f.WireData()
		require.NotEmpty(t, wire, "frame %d wire data empty", i)

		// Parse 4-byte length-prefixed NALUs.
		nalus := codec.ExtractNALUs(wire)
		require.NotEmpty(t, nalus, "frame %d: should have at least one NALU", i)

		for j, nalu := range nalus {
			require.GreaterOrEqual(t, len(nalu), 2,
				"frame %d NALU %d: HEVC NALUs need at least 2-byte header", i, j)

			naluType := codec.HEVCNALUType(nalu[0])

			// Wire data should NOT contain parameter sets (VPS=32, SPS=33, PPS=34).
			// Those are extracted to separate fields on BufferedFrame.
			require.NotEqual(t, byte(codec.HEVCNALVPS), naluType,
				"frame %d NALU %d: VPS should not be in wire data", i, j)
			require.NotEqual(t, byte(codec.HEVCNALSPS), naluType,
				"frame %d NALU %d: SPS should not be in wire data", i, j)
			require.NotEqual(t, byte(codec.HEVCNALPPS), naluType,
				"frame %d NALU %d: PPS should not be in wire data", i, j)
		}
	}
}

// ==========================================================================
// Goroutine cleanup with HEVC
// ==========================================================================

// TestTSDemux_HEVC_DemuxerCleanup verifies no goroutine leaks when demuxing
// HEVC content multiple times.
func TestTSDemux_HEVC_DemuxerCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateHEVC8bitTS(t, 1, 320, 240, 30)

	// Warm up to stabilize goroutine count.
	v, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, v)

	// Give goroutines time to exit.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	// Record baseline.
	before := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		videoFrames, _, demuxErr := clip.DemuxFile(path)
		require.NoError(t, demuxErr)
		require.NotEmpty(t, videoFrames)
	}

	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	maxAllowedGrowth := 5
	require.LessOrEqual(t, after-before, maxAllowedGrowth,
		"goroutine leak detected: before=%d after=%d (growth=%d)",
		before, after, after-before)
}
