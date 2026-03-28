//go:build correctness

package avsync

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
)

const (
	sampleRate    = 48000
	channels      = 2
	aacFrameSize  = 1024                                       // samples per channel per AAC-LC frame
	aacDuration90 = int64(aacFrameSize) * 90000 / sampleRate   // 1920 ticks at 90kHz
	videoDelta30  = int64(90000 / 30)                          // 3000 ticks at 90kHz for 30fps
)

// encodeAVToTS generates synchronized video + audio, muxes to MPEG-TS, and
// writes the result to a temp file. Returns the file path.
//
// Video: libx264 320x240 @ 30fps, varying gradient content.
// Audio: FDK AAC-LC stereo @ 48kHz, 1kHz sine tone.
//
// Both streams share the same PTS origin (startPTS). Audio PTS advances by
// 1920 ticks (1024 samples at 48kHz mapped to 90kHz). Video PTS advances by
// 3000 ticks (90kHz / 30fps). Frames are interleaved chronologically.
func encodeAVToTS(t *testing.T, durationSec int, startPTS int64) string {
	t.Helper()

	w, h := 320, 240
	fps := 30
	gopSecs := 2
	bitrate := 500_000

	// Create video encoder.
	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err, "create video encoder")
	defer enc.Close()

	// Create audio encoder.
	aacEnc, err := audio.NewFDKEncoder(sampleRate, channels)
	require.NoError(t, err, "create audio encoder")
	defer aacEnc.Close()

	// Create muxer.
	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	// Prepare video frame buffer.
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	// Prepare audio: 1kHz stereo sine tone.
	// Generate enough PCM for the entire duration, then consume in aacFrameSize chunks.
	totalAudioSamples := sampleRate * durationSec
	pcmMono := testutil.GenerateSineWavePCM(1000.0, -20.0, sampleRate, float64(durationSec))

	// Interleave to stereo.
	pcmStereo := make([]float32, totalAudioSamples*channels)
	for i := 0; i < totalAudioSamples; i++ {
		sample := float32(0)
		if i < len(pcmMono) {
			sample = pcmMono[i]
		}
		pcmStereo[i*channels] = sample
		pcmStereo[i*channels+1] = sample
	}

	// Calculate total frames.
	numVideoFrames := fps * durationSec
	numAudioFrames := totalAudioSamples / aacFrameSize

	// Build a timeline of events sorted by PTS. We alternate between writing
	// video and audio frames in PTS order, but the muxer requires the first
	// video keyframe before any audio is accepted. So we write the first video
	// frame first, then interleave.
	//
	// Write all frames in chronological PTS order.
	vi := 0 // video frame index
	ai := 0 // audio frame index

	// Prime audio encoder: FDK may need 1-2 frames before producing output.
	// We'll track the first audio frame that produces output.
	var primedAudioPTS int64
	audioPrimed := false

	for vi < numVideoFrames || ai < numAudioFrames {
		videoPTS := startPTS + int64(vi)*videoDelta30
		audioPTS := startPTS + int64(ai)*aacDuration90

		writeVideo := false
		writeAudio := false

		if vi < numVideoFrames && ai < numAudioFrames {
			if videoPTS <= audioPTS {
				writeVideo = true
			} else {
				writeAudio = true
			}
		} else if vi < numVideoFrames {
			writeVideo = true
		} else {
			writeAudio = true
		}

		if writeVideo {
			pts := videoPTS
			forceIDR := (vi % (fps * gopSecs)) == 0

			// Vary content each frame.
			for j := 0; j < ySize; j++ {
				yuv[j] = byte((j*7 + vi*37) % 256)
			}
			for j := ySize; j < len(yuv); j++ {
				yuv[j] = 128
			}

			encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
			require.NoError(t, encErr, "encode video frame %d", vi)

			if len(encoded) > 0 {
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
				require.NoError(t, muxer.WriteVideo(frame), "mux video frame %d", vi)
			}
			vi++
		}

		if writeAudio {
			pts := audioPTS
			sampleStart := ai * aacFrameSize * channels
			sampleEnd := sampleStart + aacFrameSize*channels
			if sampleEnd > len(pcmStereo) {
				ai++
				continue
			}

			chunk := pcmStereo[sampleStart:sampleEnd]
			aacData, encErr := aacEnc.Encode(chunk)
			require.NoError(t, encErr, "encode audio frame %d", ai)

			if len(aacData) > 0 {
				if !audioPrimed {
					// FDK encoder may have priming delay; record the PTS of
					// the first frame that actually produces output.
					primedAudioPTS = pts
					audioPrimed = true
				}
				aFrame := &media.AudioFrame{
					PTS:        pts,
					Data:       aacData,
					SampleRate: sampleRate,
					Channels:   channels,
				}
				require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", ai)
			}
			ai++
		}
	}

	_ = primedAudioPTS // diagnostic — not used in assertions

	require.Greater(t, len(tsData), 0, "muxer should produce output")

	dir := t.TempDir()
	path := filepath.Join(dir, "avsync.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	return path
}

// findClosestAudioPTS returns the audio frame whose PTS is nearest to the given
// video PTS, along with the signed delta (video - audio) in 90kHz ticks.
func findClosestAudioPTS(videoPTS int64, audioFrames []testutil.FrameInfo) (int64, int64) {
	bestDelta := int64(math.MaxInt64)
	bestPTS := int64(0)
	for _, af := range audioFrames {
		delta := videoPTS - af.PTS
		if absi64(delta) < absi64(bestDelta) {
			bestDelta = delta
			bestPTS = af.PTS
		}
	}
	return bestPTS, bestDelta
}

func absi64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestEncodeAVAlignment encodes 5s of synchronized A/V, muxes to TS, and
// verifies that the first video PTS and first audio PTS differ by at most
// 1 AAC frame (1920 ticks at 90kHz).
func TestEncodeAVAlignment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeAVToTS(t, 5, 0)
	testutil.RequireNoErrors(t, path)

	// Verify both streams are present.
	result := testutil.ProbeFile(t, path)
	require.NotNil(t, result.VideoStream(), "expected video stream")
	require.NotNil(t, result.AudioStream(), "expected audio stream")

	videoFrames := testutil.ProbeVideoFrames(t, path)
	audioFrames := testutil.ProbeAudioFrames(t, path)
	require.GreaterOrEqual(t, len(videoFrames), 100, "expected at least 100 video frames")
	require.GreaterOrEqual(t, len(audioFrames), 100, "expected at least 100 audio frames")

	// First video and first audio PTS should be within 1 AAC frame.
	firstVideoPTS := videoFrames[0].PTS
	firstAudioPTS := audioFrames[0].PTS
	delta := absi64(firstVideoPTS - firstAudioPTS)

	t.Logf("first video PTS: %d, first audio PTS: %d, delta: %d ticks (%.2f ms)",
		firstVideoPTS, firstAudioPTS, delta, float64(delta)/90.0)

	require.LessOrEqual(t, delta, aacDuration90,
		"first video/audio PTS delta %d exceeds 1 AAC frame (%d ticks)", delta, aacDuration90)
}

// TestAVSyncConsistency encodes 10s and measures video-audio PTS delta at
// the start, middle, and end. All deltas must be within 1 AAC frame.
func TestAVSyncConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeAVToTS(t, 10, 0)
	testutil.RequireNoErrors(t, path)

	videoFrames := testutil.ProbeVideoFrames(t, path)
	audioFrames := testutil.ProbeAudioFrames(t, path)
	require.GreaterOrEqual(t, len(videoFrames), 200, "expected at least 200 video frames")
	require.GreaterOrEqual(t, len(audioFrames), 200, "expected at least 200 audio frames")

	// Measure at start, middle, and end.
	checkPoints := []struct {
		name  string
		index int
	}{
		{"start", 0},
		{"middle", len(videoFrames) / 2},
		{"end", len(videoFrames) - 1},
	}

	for _, cp := range checkPoints {
		videoPTS := videoFrames[cp.index].PTS
		_, delta := findClosestAudioPTS(videoPTS, audioFrames)

		t.Logf("%s: video PTS %d, closest audio delta %d ticks (%.2f ms)",
			cp.name, videoPTS, delta, float64(delta)/90.0)

		require.LessOrEqual(t, absi64(delta), aacDuration90,
			"%s: A/V delta %d exceeds 1 AAC frame (%d ticks)", cp.name, delta, aacDuration90)
	}
}

// TestAVSyncLongRunning encodes 60s (or shorter in CI) and verifies no
// cumulative A/V drift. The delta at t=0 and t=end must both be within
// 1 AAC frame.
func TestAVSyncLongRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running A/V sync drift test in short mode")
	}

	// Use 30s to keep the test reasonable while still detecting drift.
	duration := 30
	path := encodeAVToTS(t, duration, 0)
	testutil.RequireNoErrors(t, path)

	videoFrames := testutil.ProbeVideoFrames(t, path)
	audioFrames := testutil.ProbeAudioFrames(t, path)

	minExpectedVideo := duration * 30 * 3 / 4 // 75% of expected frames
	require.GreaterOrEqual(t, len(videoFrames), minExpectedVideo,
		"expected at least %d video frames for %ds", minExpectedVideo, duration)
	require.GreaterOrEqual(t, len(audioFrames), 100, "expected at least 100 audio frames")

	// Measure delta at start.
	startVideoPTS := videoFrames[0].PTS
	_, startDelta := findClosestAudioPTS(startVideoPTS, audioFrames)

	// Measure delta at end.
	endVideoPTS := videoFrames[len(videoFrames)-1].PTS
	_, endDelta := findClosestAudioPTS(endVideoPTS, audioFrames)

	t.Logf("start delta: %d ticks (%.2f ms), end delta: %d ticks (%.2f ms)",
		startDelta, float64(startDelta)/90.0, endDelta, float64(endDelta)/90.0)
	t.Logf("drift: %d ticks (%.2f ms)",
		absi64(endDelta)-absi64(startDelta),
		float64(absi64(endDelta)-absi64(startDelta))/90.0)

	require.LessOrEqual(t, absi64(startDelta), aacDuration90,
		"start A/V delta %d exceeds 1 AAC frame (%d ticks)", startDelta, aacDuration90)
	require.LessOrEqual(t, absi64(endDelta), aacDuration90,
		"end A/V delta %d exceeds 1 AAC frame (%d ticks)", endDelta, aacDuration90)
}

// TestAVPTSMonotonic verifies that both video and audio PTS values are
// independently monotonically increasing.
func TestAVPTSMonotonic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeAVToTS(t, 5, 0)

	videoFrames := testutil.ProbeVideoFrames(t, path)
	audioFrames := testutil.ProbeAudioFrames(t, path)
	require.GreaterOrEqual(t, len(videoFrames), 100, "expected at least 100 video frames")
	require.GreaterOrEqual(t, len(audioFrames), 100, "expected at least 100 audio frames")

	// Video PTS must be strictly increasing.
	videoPTS := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		videoPTS[i] = f.PTS
	}
	testutil.AssertMonotonic(t, videoPTS, "video PTS")

	// Audio PTS must be strictly increasing.
	audioPTS := make([]int64, len(audioFrames))
	for i, f := range audioFrames {
		audioPTS[i] = f.PTS
	}
	testutil.AssertMonotonic(t, audioPTS, "audio PTS")
}

// TestAudioFrameSpacing verifies that audio PTS deltas are all approximately
// 1920 ticks (1024 samples at 48kHz mapped to 90kHz), with a tolerance of
// +/-1 tick for rounding.
func TestAudioFrameSpacing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := encodeAVToTS(t, 5, 0)

	audioFrames := testutil.ProbeAudioFrames(t, path)
	require.GreaterOrEqual(t, len(audioFrames), 100, "expected at least 100 audio frames")

	testutil.AssertPTSDelta(t, audioFrames, aacDuration90, 1)
}
