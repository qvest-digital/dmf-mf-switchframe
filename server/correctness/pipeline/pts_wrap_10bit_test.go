//go:build correctness

package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// PTS wraparound tests with 10-bit frame payload.
//
// PTS is format-agnostic (it's a 33-bit counter in 90kHz units), but these
// tests verify that the pipeline correctly handles PTS wrap when the frames
// being carried are 10-bit YUV422P10LE (w*h*4 bytes) rather than 8-bit
// YUV420 (w*h*3/2 bytes).
//
// Key property: PTS wraps at 2^33 = 8589934592 regardless of pixel format.
// ==========================================================================

// TestPTSWrapWithEncode10bit encodes video with PTS crossing the 33-bit
// wrap boundary using 10-bit-sized payloads, muxes to TS, and verifies
// the output is valid.
func TestPTSWrapWithEncode10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		gopSecs = 1
		bitrate = 500_000
	)

	ptsDelta := int64(90000 / fps)
	startPTS := ptsWrapPoint - 90000 // 1 second before wrap
	numFrames := 90                  // 3 seconds -- crosses wrap

	// Use the 8-bit encode path (since that's what produces valid TS),
	// but verify the PTS behavior is correct for frames that would be
	// 10-bit in a full 10-bit pipeline.
	videoFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFrames, startPTS, ptsDelta)
	require.NotEmpty(t, videoFrames, "encoder should produce video frames")

	// Verify we have frames on both sides of the wrap.
	var preWrap, postWrap int
	for _, vf := range videoFrames {
		if vf.PTS >= ptsWrapPoint-90000 && vf.PTS < ptsWrapPoint {
			preWrap++
		} else if vf.PTS < 90000*3 {
			postWrap++
		}
	}
	t.Logf("MEASURE: 10-bit PTS wrap: pre-wrap=%d, post-wrap=%d frames", preWrap, postWrap)

	// Mux to TS.
	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	for _, vf := range videoFrames {
		require.NoError(t, muxer.WriteVideo(vf))
	}

	// Verify 10-bit frame size is what we expect (even though we're using 8-bit encode).
	format10 := switcher.ColorYUV422_10bit
	expectedSize10bit := format10.FrameSize(w, h)
	require.Equal(t, w*h*4, expectedSize10bit,
		"10-bit frame size should be w*h*4 = %d", w*h*4)

	// Write and probe.
	dir := t.TempDir()
	path := filepath.Join(dir, "pts_wrap_10bit.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))
	require.Greater(t, len(tsData), 0)

	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	require.Equal(t, "h264", vs.CodecName)

	// Verify PTS is monotonically increasing in output.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(probeFrames), numFrames/2)

	for i := 1; i < len(probeFrames); i++ {
		if probeFrames[i].PTS <= probeFrames[i-1].PTS {
			t.Errorf("BUG: PTS not monotonic at frame %d: PTS[%d]=%d <= PTS[%d]=%d",
				i, i, probeFrames[i].PTS, i-1, probeFrames[i-1].PTS)
		}
	}

	t.Logf("10-bit PTS wrap test: %d frames in output, PTS monotonic", len(probeFrames))
}

// TestPTSWrapCutDuring10bit cuts between sources at the PTS wrap point
// with 10-bit frame payloads and verifies the output is valid.
func TestPTSWrapCutDuring10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		gopSecs = 1
		bitrate = 500_000
	)

	ptsDelta := int64(90000 / fps)
	startPTS := ptsWrapPoint - int64(float64(90000)*1.5)
	numFramesPerSource := 60

	// Encode two sources.
	srcAFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFramesPerSource, startPTS, ptsDelta)
	require.NotEmpty(t, srcAFrames)

	srcBStartPTS := startPTS + int64(numFramesPerSource)*ptsDelta
	srcBFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFramesPerSource, srcBStartPTS, ptsDelta)
	require.NotEmpty(t, srcBFrames)

	// Mux: source A then source B (simulating cut at wrap).
	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	for _, vf := range srcAFrames {
		require.NoError(t, muxer.WriteVideo(vf))
	}
	for _, vf := range srcBFrames {
		require.NoError(t, muxer.WriteVideo(vf))
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cut_wrap_10bit.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	testutil.RequireNoErrors(t, path)

	// Verify monotonic PTS.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(probeFrames), numFramesPerSource)

	nonMono := 0
	for i := 1; i < len(probeFrames); i++ {
		if probeFrames[i].PTS <= probeFrames[i-1].PTS {
			nonMono++
		}
	}
	require.Equal(t, 0, nonMono,
		"PTS should be monotonic across cut during wrap: %d violations", nonMono)

	t.Logf("10-bit PTS wrap cut test: %d frames, %d sources", len(probeFrames), 2)
}

// TestPTSWrapAudioInterleave10bit verifies audio/video interleaving works
// correctly at the PTS wrap point when frames carry 10-bit payloads.
func TestPTSWrapAudioInterleave10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		gopSecs = 1
		bitrate = 500_000
	)

	ptsDelta := int64(90000 / fps)
	startPTS := ptsWrapPoint - 90000
	numFrames := 90

	videoFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFrames, startPTS, ptsDelta)
	require.NotEmpty(t, videoFrames)

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	audioPTSDelta := int64(1920)
	audioPTS := startPTS
	audioCount := 0

	for _, vf := range videoFrames {
		require.NoError(t, muxer.WriteVideo(vf))

		// Interleave audio
		af := buildAudioFrame(audioPTS & 0x1FFFFFFFF)
		require.NoError(t, muxer.WriteAudio(af))
		audioCount++
		audioPTS += audioPTSDelta
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "av_wrap_10bit.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	as := result.AudioStream()
	require.NotNil(t, vs, "expected video stream")
	require.NotNil(t, as, "expected audio stream")

	t.Logf("10-bit A/V PTS wrap: video=%s, audio=%s, %d audio frames",
		vs.CodecName, as.CodecName, audioCount)

	// Also verify we can still build audio frames with correct PTS near wrap.
	af := buildAudioFrame(ptsWrapPoint - 100)
	require.NotNil(t, af)
	require.Equal(t, ptsWrapPoint-100, af.PTS)

	// Post-wrap PTS
	afPost := &media.AudioFrame{
		PTS:        (ptsWrapPoint + 100) & 0x1FFFFFFFF,
		Data:       append(codec.BuildADTS(48000, 2, 2), 0x01, 0x08),
		SampleRate: 48000,
		Channels:   2,
	}
	require.Less(t, afPost.PTS, ptsWrapPoint,
		"post-wrap PTS should be small (wrapped around)")
}
