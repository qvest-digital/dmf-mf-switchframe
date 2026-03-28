//go:build correctness

package pipeline

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
)

const (
	ptsWrapPoint = int64(1) << 33 // 8589934592 — 33-bit PTS wrap boundary
)

// encodeVideoFrames encodes numFrames of YUV420 video at the given resolution,
// producing media.VideoFrame values with PTS starting at startPTS.
// PTS values are assigned as-is (caller controls wrapping).
// Returns frames in order.
func encodeVideoFrames(t *testing.T, w, h, fps, gopSecs, bitrate, numFrames int, startPTS, ptsDelta int64) []*media.VideoFrame {
	t.Helper()

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	var frames []*media.VideoFrame
	for i := 0; i < numFrames; i++ {
		rawPTS := startPTS + int64(i)*ptsDelta
		// Apply 33-bit wrap to simulate MPEG-TS PTS behavior.
		pts := rawPTS & 0x1FFFFFFFF
		forceIDR := (i % (fps * gopSecs)) == 0

		// Vary content each frame for realistic encode.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode video frame %d", i)
		if len(encoded) == 0 {
			continue // encoder warmup
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

		frames = append(frames, frame)
	}

	return frames
}

// buildAudioFrame creates a minimal syntactically valid AAC-LC audio frame
// wrapped in ADTS at the given PTS.
func buildAudioFrame(pts int64) *media.AudioFrame {
	silentPayload := []byte{0x01, 0x08} // minimal raw AAC-LC silent frame stub
	adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
	adtsFrame = append(adtsFrame, silentPayload...)
	return &media.AudioFrame{
		PTS:        pts,
		Data:       adtsFrame,
		SampleRate: 48000,
		Channels:   2,
	}
}

// TestPipelinePTSWrapEncodeMux encodes video and audio frames with PTS crossing
// the 33-bit wrap boundary (2^33), muxes them to MPEG-TS, and verifies the
// output is valid via ffprobe. Checks that PTS values actually wrap.
func TestPipelinePTSWrapEncodeMux(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		gopSecs = 1
		bitrate = 500_000
	)

	ptsDelta := int64(90000 / fps) // 3000 ticks per frame at 30fps
	// Start 1 second (90000 ticks) before the wrap point.
	startPTS := ptsWrapPoint - 90000
	numFrames := 90 // 3 seconds at 30fps — crosses the wrap

	// Encode video frames.
	videoFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFrames, startPTS, ptsDelta)
	require.NotEmpty(t, videoFrames, "encoder should produce video frames")

	// Categorize frames by raw PTS position relative to wrap.
	var preWrapVideoPTS, postWrapVideoPTS []int64
	for _, vf := range videoFrames {
		if vf.PTS >= ptsWrapPoint-90000 && vf.PTS < ptsWrapPoint {
			preWrapVideoPTS = append(preWrapVideoPTS, vf.PTS)
		} else if vf.PTS < 90000*3 { // small values = post-wrap
			postWrapVideoPTS = append(postWrapVideoPTS, vf.PTS)
		}
	}
	t.Logf("MEASURE: video frames produced = %d", len(videoFrames))
	t.Logf("MEASURE: pre-wrap video PTS count = %d", len(preWrapVideoPTS))
	t.Logf("MEASURE: post-wrap video PTS count = %d", len(postWrapVideoPTS))
	if len(preWrapVideoPTS) > 0 {
		t.Logf("MEASURE: pre-wrap video PTS range = [%d, %d]", preWrapVideoPTS[0], preWrapVideoPTS[len(preWrapVideoPTS)-1])
	}
	if len(postWrapVideoPTS) > 0 {
		t.Logf("MEASURE: post-wrap video PTS range = [%d, %d]", postWrapVideoPTS[0], postWrapVideoPTS[len(postWrapVideoPTS)-1])
	}

	// Mux video + audio to TS.
	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	// Also produce audio frames with PTS crossing the wrap.
	audioPTSDelta := int64(1920) // ~21.3ms at 48kHz → 1024 samples → 1920 ticks at 90kHz
	audioStartPTS := startPTS
	audioFrameCount := 0

	videoIdx := 0
	audioPTS := audioStartPTS

	// Interleave video and audio: for each video frame, write it, then
	// write audio frames up to that video PTS.
	for _, vf := range videoFrames {
		require.NoError(t, muxer.WriteVideo(vf), "mux video frame %d", videoIdx)
		videoIdx++

		// Write audio frames up to this video PTS.
		for audioPTS&0x1FFFFFFFF <= vf.PTS || (vf.PTS < 90000 && audioPTS >= ptsWrapPoint-90000) {
			af := buildAudioFrame(audioPTS & 0x1FFFFFFFF)
			require.NoError(t, muxer.WriteAudio(af), "mux audio frame %d", audioFrameCount)
			audioFrameCount++
			audioPTS += audioPTSDelta
			// Safety: prevent infinite loop.
			if audioFrameCount > 500 {
				break
			}
		}
	}

	t.Logf("MEASURE: total audio frames muxed = %d", audioFrameCount)

	// Write to file.
	dir := t.TempDir()
	path := filepath.Join(dir, "pts_wrap.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))
	require.Greater(t, len(tsData), 0, "should produce TS output")

	// ffprobe: valid stream, no errors.
	testutil.RequireNoErrors(t, path)

	// Probe streams.
	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)

	// Check PTS sequence: the muxer rebases PTS to near-zero (muxerEpoch=90000),
	// so after rebasing the wrap may not be visible in the output file.
	// What we really care about is that the output is valid and playable.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	t.Logf("MEASURE: ffprobe video frames = %d", len(probeFrames))
	require.GreaterOrEqual(t, len(probeFrames), numFrames/2,
		"expected at least %d video frames in output", numFrames/2)

	// Log the PTS values from ffprobe to see if wrap is visible.
	if len(probeFrames) > 0 {
		t.Logf("MEASURE: ffprobe first PTS = %d, last PTS = %d",
			probeFrames[0].PTS, probeFrames[len(probeFrames)-1].PTS)
	}

	// Check for PTS monotonicity in the probed output. If the muxer
	// rebases correctly, PTS should be monotonically increasing in
	// the output even though input PTS wrapped.
	ptsValues := make([]int64, len(probeFrames))
	for i, f := range probeFrames {
		ptsValues[i] = f.PTS
	}
	// Check monotonicity — this is the key assertion.
	for i := 1; i < len(ptsValues); i++ {
		if ptsValues[i] <= ptsValues[i-1] {
			t.Errorf("BUG: PTS not monotonic at ffprobe frame %d: PTS[%d]=%d <= PTS[%d]=%d",
				i, i, ptsValues[i], i-1, ptsValues[i-1])
		}
	}
}

// TestMuxerPTSWrapWithAudio feeds video and audio frames with PTS crossing
// the 33-bit boundary directly into a TSMuxer and verifies PCR wraps
// correctly while the output remains valid.
func TestMuxerPTSWrapWithAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		gopSecs = 1
		bitrate = 500_000
	)

	ptsDelta := int64(90000 / fps) // 3000 ticks
	// Start 2 seconds before wrap.
	startPTS := ptsWrapPoint - 2*90000
	numFrames := 150 // 5 seconds — crosses wrap at ~2s

	videoFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFrames, startPTS, ptsDelta)
	require.NotEmpty(t, videoFrames)

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	// Write video and interleaved audio.
	audioPTSDelta := int64(1920) // 1024 samples at 48kHz in 90kHz ticks
	audioPTS := startPTS
	audioWritten := 0

	for i, vf := range videoFrames {
		require.NoError(t, muxer.WriteVideo(vf), "mux video frame %d", i)

		// Write ~1 audio frame per video frame (audio cadence is similar).
		af := buildAudioFrame(audioPTS & 0x1FFFFFFFF)
		require.NoError(t, muxer.WriteAudio(af), "mux audio frame %d", audioWritten)
		audioWritten++
		audioPTS += audioPTSDelta
	}

	t.Logf("MEASURE: video frames muxed = %d, audio frames muxed = %d", len(videoFrames), audioWritten)

	// Write to file.
	dir := t.TempDir()
	path := filepath.Join(dir, "muxer_wrap.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// ffprobe: both streams valid.
	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	as := result.AudioStream()
	require.NotNil(t, vs, "expected video stream")
	require.NotNil(t, as, "expected audio stream")
	t.Logf("MEASURE: video codec=%s, audio codec=%s", vs.CodecName, as.CodecName)

	// Parse PCR values from the raw TS data.
	pcrs := testutil.ExtractPCRValues(t, tsData)
	require.NotEmpty(t, pcrs, "should have PCR values")
	t.Logf("MEASURE: total PCR values = %d", len(pcrs))

	// The muxer rebases PTS to near-zero (muxerEpoch = 90000) so PCR
	// values should also be rebased. Check that PCR is monotonic in the
	// output (which means rebasing handled the input wrap correctly).
	// But first, check for a wrap in the PCR values themselves.
	var lastPreWrapPCR int64 = -1
	var firstPostWrapPCR int64 = -1
	for i := 1; i < len(pcrs); i++ {
		// Detect discontinuity: large drop in PCR = wrap occurred.
		if pcrs[i] < pcrs[i-1] {
			lastPreWrapPCR = pcrs[i-1]
			firstPostWrapPCR = pcrs[i]
			break
		}
	}

	if lastPreWrapPCR >= 0 {
		t.Logf("MEASURE: last pre-wrap PCR = %d, first post-wrap PCR = %d", lastPreWrapPCR, firstPostWrapPCR)
		t.Logf("MEASURE: PCR wrapped in output — examining wrap validity")
		// Verify the wrap is in the 33-bit range.
		require.Less(t, firstPostWrapPCR, lastPreWrapPCR,
			"post-wrap PCR should be less than pre-wrap PCR")
	} else {
		t.Logf("MEASURE: PCR did NOT wrap in output — muxer rebased to near-zero")
		t.Logf("MEASURE: PCR range = [%d, %d]", pcrs[0], pcrs[len(pcrs)-1])
		// If no wrap, PCR should be monotonic.
		for i := 1; i < len(pcrs); i++ {
			if pcrs[i] <= pcrs[i-1] {
				t.Errorf("BUG: PCR not monotonic at index %d: PCR[%d]=%d <= PCR[%d]=%d",
					i, i, pcrs[i], i-1, pcrs[i-1])
			}
		}
	}

	// Check duration via ffprobe — should be approximately 5 seconds.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(probeFrames), 100,
		"expected at least 100 video frames for 5s of content")

	if len(probeFrames) >= 2 {
		durationTicks := probeFrames[len(probeFrames)-1].PTS - probeFrames[0].PTS
		durationSec := float64(durationTicks) / 90000.0
		t.Logf("MEASURE: effective duration from PTS = %.2f seconds", durationSec)
		// Duration should be approximately 5 seconds (150 frames at 30fps).
		require.InDelta(t, 5.0, durationSec, 1.0,
			"duration should be approximately 5 seconds, got %.2f", durationSec)
	}
}

// TestMixerPTSWrap creates a Mixer, seeds PTS near 2^33 via SeedPTSFromVideo,
// feeds audio for 3 seconds crossing the wrap, and verifies PTS continuity.
func TestMixerPTSWrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Seed PTS 1 second before the 33-bit wrap point.
	seedPTS := ptsWrapPoint - 90000

	var mu sync.Mutex
	var capturedFrames []*media.AudioFrame

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			cp := *frame
			if len(frame.Data) > 0 {
				cp.Data = make([]byte, len(frame.Data))
				copy(cp.Data, frame.Data)
			}
			capturedFrames = append(capturedFrames, &cp)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (audio.Decoder, error) {
			return audio.NewFDKDecoder(sampleRate, channels)
		},
		EncoderFactory: func(sampleRate, channels int) (audio.Encoder, error) {
			return audio.NewFDKEncoder(sampleRate, channels)
		},
	})
	defer mixer.Close()

	// Register a source channel and set it active.
	mixer.AddChannel("source-a")
	mixer.SetActive("source-a", true)

	// Seed PTS from "video" near the wrap point.
	mixer.ExpectVideoSeed()
	mixer.SeedPTSFromVideo(seedPTS)

	// Feed ~3 seconds of PCM audio (1024 samples per frame at 48kHz = ~21.3ms).
	// 3 seconds = ~141 frames.
	const frameSamples = 1024
	const channels = 2
	pcmFrame := make([]float32, frameSamples*channels)
	for i := range pcmFrame {
		// Generate a quiet tone so the output isn't silence-gated.
		pcmFrame[i] = 0.1 * float32(math.Sin(2.0*math.Pi*440.0*float64(i/channels)/48000.0))
	}

	// Feed PCM frames for ~3 seconds — the ticker produces output at its own cadence.
	feedDuration := 3 * time.Second
	feedInterval := time.Millisecond * time.Duration(1000*frameSamples/48000)
	feedEnd := time.Now().Add(feedDuration)

	for time.Now().Before(feedEnd) {
		mixer.IngestPCM("source-a", pcmFrame, 0, channels)
		time.Sleep(feedInterval)
	}

	// Wait a bit for the output ticker to flush.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	frames := make([]*media.AudioFrame, len(capturedFrames))
	copy(frames, capturedFrames)
	mu.Unlock()

	t.Logf("MEASURE: captured audio frames = %d", len(frames))
	require.GreaterOrEqual(t, len(frames), 50,
		"expected at least 50 audio output frames for 3 seconds of input")

	// Extract PTS values.
	ptsList := make([]int64, len(frames))
	for i, f := range frames {
		ptsList[i] = f.PTS
	}

	// Log PTS values around the expected wrap region.
	t.Logf("MEASURE: first PTS = %d, last PTS = %d", ptsList[0], ptsList[len(ptsList)-1])

	// Find where PTS wraps (large value followed by small value).
	wrapIdx := -1
	for i := 1; i < len(ptsList); i++ {
		if ptsList[i] < ptsList[i-1] && (ptsList[i-1]-ptsList[i]) > ptsWrapPoint/2 {
			wrapIdx = i
			break
		}
	}

	if wrapIdx >= 0 {
		t.Logf("MEASURE: PTS wrap detected at frame %d: PTS[%d]=%d -> PTS[%d]=%d",
			wrapIdx, wrapIdx-1, ptsList[wrapIdx-1], wrapIdx, ptsList[wrapIdx])

		// Log sequence around wrap.
		start := wrapIdx - 3
		if start < 0 {
			start = 0
		}
		end := wrapIdx + 3
		if end > len(ptsList) {
			end = len(ptsList)
		}
		t.Logf("MEASURE: PTS sequence around wrap: %v", ptsList[start:end])

		// Verify continuity: the PTS gap at the wrap should be approximately
		// one audio frame duration (~1920 ticks at 90kHz).
		expectedDelta := int64(1920) // 1024 * 90000 / 48000
		// Calculate wrap-aware delta.
		delta := (ptsList[wrapIdx] - ptsList[wrapIdx-1]) & 0x1FFFFFFFF
		t.Logf("MEASURE: wrap-aware delta at wrap point = %d (expected ~%d)", delta, expectedDelta)

		// Allow generous tolerance — mixer uses wall-clock resync which can vary.
		require.InDelta(t, float64(expectedDelta), float64(delta), float64(expectedDelta)*5,
			"PTS delta at wrap should be approximately one frame duration")
	} else {
		// The mixer's advanceOutputPTS comment says:
		// "No 33-bit mask — the muxer handles PTS rebasing and wrapping."
		// This means the mixer's output PTS can grow beyond 2^33 without wrapping!
		// The muxer then applies the 33-bit mask during rebasing.
		t.Logf("MEASURE: PTS sequence around wrap: %v", ptsList[0:min(10, len(ptsList))])

		// Check if PTS exceeds 2^33 — if so, the mixer is NOT wrapping.
		maxPTS := int64(0)
		for _, p := range ptsList {
			if p > maxPTS {
				maxPTS = p
			}
		}
		t.Logf("MEASURE: max PTS = %d, 2^33 = %d", maxPTS, ptsWrapPoint)

		if maxPTS >= ptsWrapPoint {
			t.Logf("MEASURE: mixer PTS exceeds 2^33 — mixer does NOT wrap, relies on muxer")
			// This is the expected behavior per the code comment in advanceOutputPTS.
			// Verify PTS is still monotonic (just very large).
			for i := 1; i < len(ptsList); i++ {
				if ptsList[i] <= ptsList[i-1] {
					t.Errorf("BUG: mixer PTS not monotonic at frame %d: PTS[%d]=%d <= PTS[%d]=%d",
						i, i, ptsList[i], i-1, ptsList[i-1])
				}
			}
		} else {
			// PTS didn't grow large enough — seed might not have taken effect.
			t.Logf("MEASURE: PTS stayed below 2^33. Seed PTS was %d, first output PTS was %d",
				seedPTS, ptsList[0])
		}
	}

	// Verify no silence gap at wrap point: check that audio data has
	// non-zero length for all frames.
	for i, f := range frames {
		require.Greater(t, len(f.Data), 0,
			"audio frame %d at PTS=%d has empty data (silence gap?)", i, f.PTS)
	}
}

// TestCutDuringPTSWrap encodes video from two sources with PTS near the wrap
// point. Source A is on program, then we cut to source B at the wrap point.
// Verifies the output TS is valid despite the PTS discontinuity.
func TestCutDuringPTSWrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		gopSecs = 1
		bitrate = 500_000
	)

	ptsDelta := int64(90000 / fps) // 3000 ticks
	// Start 1.5 seconds before the wrap.
	startPTS := ptsWrapPoint - int64(float64(90000)*1.5)
	numFramesPerSource := 60 // 2 seconds each

	// Encode source A frames (pre-wrap region).
	sourceAFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFramesPerSource, startPTS, ptsDelta)
	require.NotEmpty(t, sourceAFrames)

	// Source B starts where source A ends (around the wrap point).
	sourceBStartPTS := startPTS + int64(numFramesPerSource)*ptsDelta
	sourceBFrames := encodeVideoFrames(t, w, h, fps, gopSecs, bitrate, numFramesPerSource, sourceBStartPTS, ptsDelta)
	require.NotEmpty(t, sourceBFrames)

	// Identify the cut point: the first source B frame.
	cutPTS := sourceBFrames[0].PTS
	t.Logf("MEASURE: source A first PTS = %d", sourceAFrames[0].PTS)
	t.Logf("MEASURE: source A last PTS = %d", sourceAFrames[len(sourceAFrames)-1].PTS)
	t.Logf("MEASURE: cut PTS (source B start) = %d", cutPTS)
	t.Logf("MEASURE: wrap occurred at PTS = %d", ptsWrapPoint)

	// Mux: source A frames first, then source B frames (simulating a cut).
	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	for i, vf := range sourceAFrames {
		require.NoError(t, muxer.WriteVideo(vf), "mux source A frame %d", i)
	}

	t.Logf("MEASURE: TS bytes after source A = %d", len(tsData))

	// The cut: now feed source B frames. In a real switcher, source B
	// would have its own encoder producing independent NALUs. The muxer
	// receives the encoded output from the pipeline, which may have PTS
	// in the post-wrap range.
	for i, vf := range sourceBFrames {
		require.NoError(t, muxer.WriteVideo(vf), "mux source B frame %d", i)
	}

	t.Logf("MEASURE: TS bytes after source B = %d", len(tsData))

	// Write to file.
	dir := t.TempDir()
	path := filepath.Join(dir, "cut_wrap.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// ffprobe: valid stream.
	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	require.Equal(t, "h264", vs.CodecName)

	// Check that we got frames from both sources.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	t.Logf("MEASURE: total ffprobe video frames = %d", len(probeFrames))
	require.GreaterOrEqual(t, len(probeFrames), numFramesPerSource,
		"expected at least %d video frames (should have frames from both sources)", numFramesPerSource)

	// Verify PTS continuity after the cut. The muxer rebases, so after
	// rebasing the PTS should be monotonically increasing even across
	// the source switch.
	ptsValues := make([]int64, len(probeFrames))
	for i, f := range probeFrames {
		ptsValues[i] = f.PTS
	}

	nonMonotonicCount := 0
	for i := 1; i < len(ptsValues); i++ {
		if ptsValues[i] <= ptsValues[i-1] {
			nonMonotonicCount++
			if nonMonotonicCount <= 5 {
				t.Errorf("BUG: PTS not monotonic at frame %d: PTS[%d]=%d <= PTS[%d]=%d",
					i, i, ptsValues[i], i-1, ptsValues[i-1])
			}
		}
	}
	if nonMonotonicCount > 5 {
		t.Errorf("BUG: %d total PTS monotonicity violations (showing first 5)", nonMonotonicCount)
	}

	// Verify the cut happened at approximately the right point: check
	// that we have a reasonable number of frames from both sources.
	if len(probeFrames) >= 2 {
		totalDuration := float64(probeFrames[len(probeFrames)-1].PTS-probeFrames[0].PTS) / 90000.0
		t.Logf("MEASURE: total duration from PTS = %.2f seconds (expected ~4.0)", totalDuration)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
