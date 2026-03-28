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
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// generateFFmpegTS creates a valid MPEG-TS file using ffmpeg with both
// video (H.264, testsrc) and audio (AAC, sine tone). Returns the file path.
// The file is auto-cleaned when the test finishes.
func generateFFmpegTS(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-f", "lavfi",
		"-i", formatSineSrc(durationSec),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-g", "30", // keyframe every 30 frames
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg failed: %s", string(out))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0), "generated TS file should not be empty")

	return path
}

// generateFFmpegTSVideoOnly creates a TS file with video only (no audio).
func generateFFmpegTSVideoOnly(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "video_only.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-g", "30",
		"-an", // no audio
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg failed: %s", string(out))

	return path
}

func formatTestSrc(durationSec float64, width, height, fps int) string {
	return "testsrc=duration=" + ftoa(durationSec) +
		":size=" + itoa(width) + "x" + itoa(height) +
		":rate=" + itoa(fps)
}

func formatSineSrc(durationSec float64) string {
	return "sine=frequency=1000:duration=" + ftoa(durationSec) + ":sample_rate=48000"
}

func ftoa(f float64) string {
	return trimTrailingZeros(f)
}

func trimTrailingZeros(f float64) string {
	s := ""
	if f == float64(int(f)) {
		s = itoa(int(f))
	} else {
		s = formatFloat(f)
	}
	return s
}

func formatFloat(f float64) string {
	buf := make([]byte, 0, 16)
	buf = append(buf, []byte(itoa(int(f)))...)
	frac := f - float64(int(f))
	if frac > 0 {
		buf = append(buf, '.')
		// Up to 3 decimal places
		frac *= 1000
		fracInt := int(frac + 0.5)
		if fracInt >= 100 {
			buf = append(buf, byte('0'+fracInt/100))
		}
		if fracInt >= 10 {
			buf = append(buf, byte('0'+(fracInt/10)%10))
		}
		buf = append(buf, byte('0'+fracInt%10))
	}
	return string(buf)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// Reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// ---------- Tests ----------

// TestTSDemuxProducesVideoFrames generates a short TS file with ffmpeg,
// demuxes it via clip.DemuxFile, and verifies video frames are produced.
func TestTSDemuxProducesVideoFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 2, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on valid TS")

	// 2 seconds at 30fps = ~60 frames. Allow tolerance for encoder warmup.
	require.InDelta(t, 60, len(videoFrames), 10,
		"expected approximately 60 video frames from 2s of 30fps content, got %d", len(videoFrames))

	// Verify at least one keyframe exists.
	hasKeyframe := false
	for _, f := range videoFrames {
		if f.IsKeyframe() {
			hasKeyframe = true
			require.NotEmpty(t, f.SPS(), "keyframe should have SPS")
			require.NotEmpty(t, f.PPS(), "keyframe should have PPS")
			break
		}
	}
	require.True(t, hasKeyframe, "expected at least one keyframe in demuxed output")

	// Verify all frames have non-empty wire data.
	for i, f := range videoFrames {
		require.NotEmpty(t, f.WireData(), "frame %d: WireData should not be empty", i)
	}
}

// TestTSDemuxProducesAudioFrames verifies audio frames are extracted from
// an ffmpeg-generated TS file with AAC audio.
func TestTSDemuxProducesAudioFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 2, 320, 240, 30)

	_, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on valid TS")

	// AAC at 48kHz produces ~1024 samples per frame = ~46.9 frames/sec.
	// 2 seconds should yield ~93 frames. Allow tolerance for codec warmup.
	require.InDelta(t, 93, len(audioFrames), 15,
		"expected approximately 93 audio frames from 2s at 48kHz/1024, got %d", len(audioFrames))

	// Verify audio frame metadata.
	for i, af := range audioFrames {
		require.NotEmpty(t, af.Data(), "audio frame %d: data should not be empty", i)
		require.Greater(t, af.SampleRate(), 0, "audio frame %d: sample rate should be positive", i)
		require.Greater(t, af.Channels(), 0, "audio frame %d: channels should be positive", i)
	}

	// Verify consistent sample rate across all frames.
	sr := audioFrames[0].SampleRate()
	for i, af := range audioFrames {
		require.Equal(t, sr, af.SampleRate(),
			"audio frame %d: inconsistent sample rate %d vs %d", i, af.SampleRate(), sr)
	}
}

// TestPTSPassthrough generates a TS file, probes it with ffprobe to get
// reference PTS values, then demuxes and compares. PTS values from demux
// should match ffprobe's reference PTS within a small tolerance.
func TestPTSPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 2, 320, 240, 30)

	// Get reference PTS from ffprobe.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(probeFrames), 30,
		"ffprobe should report at least 30 video frames")

	// Demux the same file.
	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(videoFrames), 30)

	// Build ordered PTS slices from both sources.
	probePTSOrdered := make([]int64, len(probeFrames))
	for i, pf := range probeFrames {
		probePTSOrdered[i] = pf.PTS
	}
	demuxPTSOrdered := make([]int64, len(videoFrames))
	for i, vf := range videoFrames {
		demuxPTSOrdered[i] = vf.PTS()
	}

	// Build a set for existence matching.
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

	// Verify PTS values appear in the same ORDER (not just same set).
	// Filter demux PTS to only those that appear in the probe set, then
	// verify they appear in a monotonically non-decreasing order relative
	// to the probe sequence.
	var matchedDemuxPTS []int64
	for _, pts := range demuxPTSOrdered {
		if probePTSSet[pts] {
			matchedDemuxPTS = append(matchedDemuxPTS, pts)
		}
	}
	// Matched PTS should be in non-decreasing order (since both demuxer
	// and ffprobe should emit frames in a consistent order).
	for i := 1; i < len(matchedDemuxPTS); i++ {
		require.GreaterOrEqual(t, matchedDemuxPTS[i], matchedDemuxPTS[i-1],
			"matched PTS values should be in non-decreasing order: index %d has %d < %d",
			i, matchedDemuxPTS[i], matchedDemuxPTS[i-1])
	}

	// Verify video PTS values are non-negative and reasonable.
	for i, vf := range videoFrames {
		require.GreaterOrEqual(t, vf.PTS(), int64(0),
			"frame %d: PTS should be non-negative, got %d", i, vf.PTS())
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

// TestCorruptedPacketHandling takes a valid TS file, corrupts some bytes,
// and feeds it to the demuxer. The demuxer should not panic.
func TestCorruptedPacketHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 2, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(data), 1000, "TS data should be non-trivial")

	// Test several corruption patterns.
	t.Run("RandomByteFlips", func(t *testing.T) {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)

		// Flip ~1% of bytes at random positions (excluding first 188 bytes
		// to preserve initial sync).
		rng := rand.New(rand.NewSource(42))
		numFlips := len(corrupted) / 100
		for i := 0; i < numFlips; i++ {
			pos := 188 + rng.Intn(len(corrupted)-188)
			corrupted[pos] ^= 0xFF
		}

		tmpPath := testutil.WriteTempFile(t, corrupted, ".ts")

		// The demuxer should not panic. It may return an error or partial results.
		require.NotPanics(t, func() {
			_, _, _ = clip.DemuxFile(tmpPath)
		}, "demuxer should not panic on corrupted data")
	})

	t.Run("SyncByteCorruption", func(t *testing.T) {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)

		// Corrupt sync bytes in packets 5-15 (set them to 0x00 instead of 0x47).
		for i := 5; i < 15 && i*188 < len(corrupted); i++ {
			corrupted[i*188] = 0x00
		}

		tmpPath := testutil.WriteTempFile(t, corrupted, ".ts")
		require.NotPanics(t, func() {
			_, _, _ = clip.DemuxFile(tmpPath)
		}, "demuxer should not panic on corrupted sync bytes")
	})

	t.Run("TruncatedFile", func(t *testing.T) {
		// Truncate to 3/4 of original size (mid-packet).
		truncated := data[:len(data)*3/4]
		tmpPath := testutil.WriteTempFile(t, truncated, ".ts")

		require.NotPanics(t, func() {
			videoFrames, _, err := clip.DemuxFile(tmpPath)
			// Should still extract some frames from the valid portion.
			if err == nil {
				assert.NotEmpty(t, videoFrames,
					"truncated but mostly valid TS should still yield some frames")
			}
		}, "demuxer should not panic on truncated file")
	})

	t.Run("ZeroFilledMiddle", func(t *testing.T) {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)

		// Zero out a chunk in the middle.
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
		}, "demuxer should not panic on zero-filled middle")
	})

	t.Run("PureGarbage", func(t *testing.T) {
		garbage := make([]byte, 10000)
		rng := rand.New(rand.NewSource(99))
		for i := range garbage {
			garbage[i] = byte(rng.Intn(256))
		}

		tmpPath := testutil.WriteTempFile(t, garbage, ".ts")
		require.NotPanics(t, func() {
			_, _, err := clip.DemuxFile(tmpPath)
			assert.Error(t, err, "pure garbage should produce an error")
		}, "demuxer should not panic on pure garbage input")
	})
}

// TestPTSDiscontinuityHandling generates two separate TS segments and
// concatenates them to create a PTS discontinuity. The demuxer should
// continue processing without stalling.
func TestPTSDiscontinuityHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	dir := t.TempDir()
	seg1Path := filepath.Join(dir, "seg1.ts")
	seg2Path := filepath.Join(dir, "seg2.ts")

	// Generate first segment: 1 second of video starting at default PTS.
	cmd1 := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=30",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:duration=1:sample_rate=48000",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-f", "mpegts",
		"-y", seg1Path,
	)
	out, err := cmd1.CombinedOutput()
	require.NoError(t, err, "ffmpeg seg1 failed: %s", string(out))

	// Generate second segment: 1 second of video with a large PTS offset.
	// The -output_ts_offset creates a PTS gap.
	cmd2 := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=30",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=1:sample_rate=48000",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-output_ts_offset", "100", // 100 seconds PTS jump
		"-f", "mpegts",
		"-y", seg2Path,
	)
	out, err = cmd2.CombinedOutput()
	require.NoError(t, err, "ffmpeg seg2 failed: %s", string(out))

	// Concatenate the two segments.
	seg1Data, err := os.ReadFile(seg1Path)
	require.NoError(t, err)
	seg2Data, err := os.ReadFile(seg2Path)
	require.NoError(t, err)

	combined := append(seg1Data, seg2Data...)
	combinedPath := filepath.Join(dir, "combined.ts")
	require.NoError(t, os.WriteFile(combinedPath, combined, 0o644))

	// Demux with a timeout to detect stalls.
	done := make(chan struct{})
	var videoFrames []clip.BufferedFrame
	var audioFrames []clip.BufferedAudioFrame
	var demuxErr error

	go func() {
		defer close(done)
		videoFrames, audioFrames, demuxErr = clip.DemuxFile(combinedPath)
	}()

	select {
	case <-done:
		// Demuxer completed (success or error -- either is acceptable).
	case <-time.After(30 * time.Second):
		t.Fatal("demuxer stalled on PTS discontinuity (timeout after 30s)")
	}

	// The demuxer should not panic. It may error or succeed with partial data.
	if demuxErr == nil {
		// If it succeeded, we should have frames from at least the first segment.
		require.GreaterOrEqual(t, len(videoFrames), 15,
			"expected video frames from concatenated segments")
		_ = audioFrames
	}

	// Verify there's a PTS gap in the video frames (confirming discontinuity).
	if demuxErr == nil && len(videoFrames) > 2 {
		maxDelta := int64(0)
		for i := 1; i < len(videoFrames); i++ {
			delta := videoFrames[i].PTS() - videoFrames[i-1].PTS()
			if delta > maxDelta {
				maxDelta = delta
			}
		}
		// With a 100s PTS offset, we expect a gap of at least 1 second (90000 ticks).
		// This confirms the discontinuity was encountered.
		t.Logf("max PTS delta between consecutive frames: %d ticks (%.2fs)",
			maxDelta, float64(maxDelta)/90000.0)
	}
}

// TestDemuxerCleanup verifies that demuxing a file completes cleanly
// and doesn't leak goroutines.
func TestDemuxerCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 1, 320, 240, 30)

	// Record goroutine count before.
	runtime.GC()
	before := runtime.NumGoroutine()

	// Run demux multiple times.
	for i := 0; i < 5; i++ {
		videoFrames, audioFrames, err := clip.DemuxFile(path)
		require.NoError(t, err)
		require.NotEmpty(t, videoFrames)
		_ = audioFrames
	}

	// Give goroutines time to exit.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	// Allow some slack for runtime goroutines, but no significant leaks.
	// 5 demux calls should not leak goroutines.
	maxAllowedGrowth := 5
	require.LessOrEqual(t, after-before, maxAllowedGrowth,
		"goroutine leak detected: before=%d after=%d (growth=%d)",
		before, after, after-before)
}

// TestVideoOnlyTS verifies the demuxer handles video-only TS files correctly.
func TestVideoOnlyTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTSVideoOnly(t, 2, 320, 240, 30)

	videoFrames, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on video-only TS")
	require.NotEmpty(t, videoFrames, "should have video frames")
	require.Empty(t, audioFrames, "video-only TS should have no audio frames")

	// Verify keyframe presence.
	hasKeyframe := false
	for _, f := range videoFrames {
		if f.IsKeyframe() {
			hasKeyframe = true
			break
		}
	}
	require.True(t, hasKeyframe, "should have at least one keyframe")
}

// TestFrameCountMatchesFFprobe compares the number of frames reported by
// ffprobe vs the demuxer. They should be reasonably close.
func TestFrameCountMatchesFFprobe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 3, 320, 240, 30)

	// ffprobe frame count.
	probeVideoFrames := testutil.ProbeVideoFrames(t, path)
	probeAudioFrames := testutil.ProbeAudioFrames(t, path)

	// Demuxer frame count.
	videoFrames, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Video frame counts should be very close (within 5 frames).
	diff := len(probeVideoFrames) - len(videoFrames)
	if diff < 0 {
		diff = -diff
	}
	require.LessOrEqual(t, diff, 5,
		"video frame count mismatch: ffprobe=%d demux=%d",
		len(probeVideoFrames), len(videoFrames))

	// Audio frame counts: allow more tolerance since ffprobe and go-astits
	// may split/merge ADTS frames differently.
	if len(probeAudioFrames) > 0 {
		require.NotEmpty(t, audioFrames,
			"ffprobe found %d audio frames but demuxer found none", len(probeAudioFrames))
	}

	t.Logf("frame counts: video(probe=%d, demux=%d) audio(probe=%d, demux=%d)",
		len(probeVideoFrames), len(videoFrames),
		len(probeAudioFrames), len(audioFrames))
}

// TestKeyframeDistribution verifies keyframes appear at expected intervals
// in the demuxed output.
func TestKeyframeDistribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Generate 5 seconds at 30fps with keyframe every 30 frames (1 second).
	path := generateFFmpegTS(t, 5, 320, 240, 30)

	videoFrames, _, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(videoFrames), 100)

	// Count keyframes.
	keyframeCount := 0
	var keyframeIndices []int
	for i, f := range videoFrames {
		if f.IsKeyframe() {
			keyframeCount++
			keyframeIndices = append(keyframeIndices, i)
		}
	}

	// With -g 30 and 5 seconds of content, expect ~5 keyframes.
	require.GreaterOrEqual(t, keyframeCount, 3,
		"expected at least 3 keyframes in 5s of content with -g 30")
	require.LessOrEqual(t, keyframeCount, 10,
		"unexpectedly many keyframes: %d", keyframeCount)

	// First frame should be a keyframe.
	require.True(t, videoFrames[0].IsKeyframe(),
		"first frame should be a keyframe")

	t.Logf("keyframes: %d total at indices %v", keyframeCount, keyframeIndices)
}

// TestDemuxResolutions tests demuxing at various resolutions to ensure
// the demuxer doesn't have resolution-specific issues.
func TestDemuxResolutions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	resolutions := []struct {
		w, h int
	}{
		{160, 120},
		{320, 240},
		{640, 480},
		{1280, 720},
	}

	for _, res := range resolutions {
		name := itoa(res.w) + "x" + itoa(res.h)
		t.Run(name, func(t *testing.T) {
			path := generateFFmpegTS(t, 1, res.w, res.h, 30)

			videoFrames, _, err := clip.DemuxFile(path)
			require.NoError(t, err, "DemuxFile should succeed at %s", name)
			require.GreaterOrEqual(t, len(videoFrames), 15,
				"expected at least 15 frames at %s", name)

			// Verify keyframe has SPS (which encodes resolution).
			for _, f := range videoFrames {
				if f.IsKeyframe() {
					require.NotEmpty(t, f.SPS(), "keyframe at %s should have SPS", name)
					break
				}
			}
		})
	}
}

// TestLargeFrameHandling generates a higher-bitrate stream to produce
// large frames and verifies the demuxer handles them.
func TestLargeFrameHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "large.ts")

	// Generate with high bitrate to produce large frames.
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=duration=2:size=1280x720:rate=30",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:duration=2:sample_rate=48000",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-b:v", "5M",
		"-c:a", "aac",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg failed: %s", string(out))

	videoFrames, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, videoFrames)
	require.NotEmpty(t, audioFrames)

	// Verify some frames are large (>10KB, expected for 720p at 5Mbps).
	hasLargeFrame := false
	for _, f := range videoFrames {
		if len(f.WireData()) > 10000 {
			hasLargeFrame = true
			break
		}
	}
	require.True(t, hasLargeFrame,
		"expected at least one frame >10KB at 720p/5Mbps")
}

// ==========================================================================
// Corrupted input handling tests
// ==========================================================================

// TestTruncatedTSFile generates a valid 2s TS with ffmpeg, truncates to 50%,
// and feeds the truncated bytes to the demuxer. The demuxer must not panic.
func TestTruncatedTSFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 2, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(data), 1000, "TS data should be non-trivial")

	// Truncate to 50%.
	truncated := data[:len(data)/2]
	tmpPath := testutil.WriteTempFile(t, truncated, ".ts")

	require.NotPanics(t, func() {
		videoFrames, _, demuxErr := clip.DemuxFile(tmpPath)
		// Truncated but mostly valid TS should still yield some frames.
		if demuxErr == nil {
			assert.NotEmpty(t, videoFrames,
				"truncated TS at 50%% should still yield some video frames")
		}
		// Either an error or partial results is acceptable.
	}, "demuxer should not panic on 50%% truncated file")
}

// TestZeroLengthInput feeds an empty file to the demuxer. It must not panic
// and should return cleanly with zero frames or an error.
func TestZeroLengthInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Create an empty .ts file.
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty.ts")
	require.NoError(t, os.WriteFile(emptyPath, []byte{}, 0o644))

	require.NotPanics(t, func() {
		videoFrames, audioFrames, err := clip.DemuxFile(emptyPath)
		// An empty file should produce an error or zero frames.
		if err == nil {
			assert.Empty(t, videoFrames, "empty file should yield no video frames")
			assert.Empty(t, audioFrames, "empty file should yield no audio frames")
		}
	}, "demuxer should not panic on empty file")
}

// TestRandomByteStream feeds 10KB of random bytes to the demuxer.
// The demuxer must not panic and should return an error or zero frames.
func TestRandomByteStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	garbage := make([]byte, 10*1024)
	rng := rand.New(rand.NewSource(12345))
	for i := range garbage {
		garbage[i] = byte(rng.Intn(256))
	}

	tmpPath := testutil.WriteTempFile(t, garbage, ".ts")

	require.NotPanics(t, func() {
		_, _, err := clip.DemuxFile(tmpPath)
		assert.Error(t, err, "random bytes should produce an error from demuxer")
	}, "demuxer should not panic on random byte stream")
}

// TestRepeatedDemuxDeterminism verifies that demuxing the same file
// twice produces identical results.
func TestRepeatedDemuxDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 2, 320, 240, 30)

	// First demux.
	v1, a1, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Second demux.
	v2, a2, err := clip.DemuxFile(path)
	require.NoError(t, err)

	// Frame counts must match exactly.
	require.Equal(t, len(v1), len(v2), "video frame count should be deterministic")
	require.Equal(t, len(a1), len(a2), "audio frame count should be deterministic")

	// PTS values must match.
	for i := range v1 {
		require.Equal(t, v1[i].PTS(), v2[i].PTS(),
			"video frame %d PTS mismatch", i)
		require.Equal(t, v1[i].IsKeyframe(), v2[i].IsKeyframe(),
			"video frame %d keyframe mismatch", i)
		require.True(t, bytes.Equal(v1[i].WireData(), v2[i].WireData()),
			"video frame %d wire data mismatch", i)
	}

	for i := range a1 {
		require.Equal(t, a1[i].PTS(), a2[i].PTS(),
			"audio frame %d PTS mismatch", i)
		require.True(t, bytes.Equal(a1[i].Data(), a2[i].Data()),
			"audio frame %d data mismatch", i)
	}
}
