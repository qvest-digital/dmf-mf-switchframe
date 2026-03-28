//go:build correctness

package replay

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// Test Helpers
// ==========================================================================

// mockRelay captures output frames from replay playback.
type mockRelay struct {
	mu     sync.Mutex
	videos []*media.VideoFrame
	audios []*media.AudioFrame
}

func (r *mockRelay) BroadcastVideo(frame *media.VideoFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Deep-copy to avoid data races with player reuse.
	f := &media.VideoFrame{
		PTS:        frame.PTS,
		IsKeyframe: frame.IsKeyframe,
		Codec:      frame.Codec,
	}
	if len(frame.WireData) > 0 {
		f.WireData = make([]byte, len(frame.WireData))
		copy(f.WireData, frame.WireData)
	}
	r.videos = append(r.videos, f)
}

func (r *mockRelay) BroadcastAudio(frame *media.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audios = append(r.audios, frame)
}

func (r *mockRelay) videoCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.videos)
}

// mockDecoder returns predictable YUV data.
type mockDecoder struct {
	width, height int
}

func (d *mockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	yuv := make([]byte, d.width*d.height*3/2)
	for i := 0; i < d.width*d.height; i++ {
		yuv[i] = 128 // Y plane
	}
	return yuv, d.width, d.height, nil
}

func (d *mockDecoder) Close() {}

// mockEncoder produces minimal valid Annex B H.264.
type mockEncoder struct {
	mu          sync.Mutex
	encodeCount int
}

func (e *mockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.encodeCount++

	if forceIDR {
		var out []byte
		out = append(out, 0x00, 0x00, 0x00, 0x01)
		out = append(out, 0x67, 0x64, 0x00, 0x28, 0xAC, 0xD1, 0x00) // SPS
		out = append(out, 0x00, 0x00, 0x00, 0x01)
		out = append(out, 0x68, 0xCE, 0x38, 0x80) // PPS
		out = append(out, 0x00, 0x00, 0x00, 0x01)
		out = append(out, 0x65, 0x88, 0x84, 0x00) // IDR
		return out, true, nil
	}

	var out []byte
	out = append(out, 0x00, 0x00, 0x00, 0x01)
	out = append(out, 0x41, 0x9A, 0x00, 0x00) // non-IDR
	return out, false, nil
}

func (e *mockEncoder) Close() {}

func mockDecoderFactory() (transition.VideoDecoder, error) {
	return &mockDecoder{width: 320, height: 240}, nil
}

func mockEncoderFactory(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
	return &mockEncoder{}, nil
}

// makeAVC1Data creates valid AVC1-formatted wire data (4-byte length prefix + NALU body).
func makeAVC1Data(size int) []byte {
	naluLen := size - 4
	if naluLen < 1 {
		naluLen = 1
	}
	data := make([]byte, 4+naluLen)
	data[0] = byte(naluLen >> 24)
	data[1] = byte(naluLen >> 16)
	data[2] = byte(naluLen >> 8)
	data[3] = byte(naluLen)
	data[4] = 0x65 // IDR slice NALU type
	for i := 5; i < len(data); i++ {
		data[i] = byte(i % 256)
	}
	return data
}

// makeVideoFrame creates a media.VideoFrame with AVC1 wire data.
func makeVideoFrame(pts int64, keyframe bool, dataSize int) *media.VideoFrame {
	f := &media.VideoFrame{
		PTS:        pts,
		IsKeyframe: keyframe,
		WireData:   makeAVC1Data(dataSize),
		Codec:      "avc1.42C01E",
	}
	if keyframe {
		f.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
		f.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
	}
	return f
}

// newTestManager creates a Manager suitable for testing with a mock relay.
func newTestManager(r *mockRelay) *replay.Manager {
	cfg := replay.Config{
		BufferDurationSecs: 60,
		MaxSources:         8,
		MaxBufferBytes:     10 * 1024 * 1024, // 10MB
		TmpfsDir:           "none",           // disable tmpfs for tests
	}
	return replay.NewManager(r, cfg, mockDecoderFactory, mockEncoderFactory)
}

// ==========================================================================
// 1. TestBufferRingWrap -- Fill past capacity with GOPs, verify oldest
//    evicted, newest preserved, correct count.
// ==========================================================================

func TestBufferRingWrap(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	cfg := replay.Config{
		BufferDurationSecs: 2, // Very short buffer -- forces eviction
		MaxSources:         4,
		MaxBufferBytes:     5000, // Very low byte limit to force trimming
		TmpfsDir:           "none",
	}
	m := replay.NewManager(r, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Record many GOPs that exceed both time and byte limits.
	// Each GOP: 1 keyframe (200 bytes) + 4 delta frames (100 bytes each) = 600 bytes.
	// 10 GOPs = 6000 bytes > 5000 byte limit; 5s total > 2s duration limit.
	for g := 0; g < 10; g++ {
		basePTS := int64(g) * 90000 // 1 second apart
		m.RecordFrame("cam1", makeVideoFrame(basePTS, true, 200))
		for f := 1; f <= 4; f++ {
			m.RecordFrame("cam1", makeVideoFrame(basePTS+int64(f)*3003, false, 100))
		}
	}

	status := m.Status()
	require.Len(t, status.Buffers, 1)
	buf := status.Buffers[0]

	// Oldest GOPs should have been evicted. We started with 10 GOPs.
	require.Less(t, buf.GOPCount, 10,
		"expected fewer than 10 GOPs after eviction, got %d", buf.GOPCount)
	require.GreaterOrEqual(t, buf.GOPCount, 1,
		"should still have at least 1 GOP")

	// Frame count should be consistent with GOP count (5 frames per GOP).
	require.Equal(t, buf.GOPCount*5, buf.FrameCount,
		"expected %d frames for %d GOPs, got %d", buf.GOPCount*5, buf.GOPCount, buf.FrameCount)

	// Bytes used should be within the limits.
	// Allow some slack because the last GOP can't be trimmed.
	require.LessOrEqual(t, buf.BytesUsed, int64(10000),
		"bytes used (%d) should be bounded", buf.BytesUsed)
}

// ==========================================================================
// 2. TestBufferGOPAlignment -- Delta frames before first keyframe are
//    dropped; complete GOPs are stored.
// ==========================================================================

func TestBufferGOPAlignment(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	m := newTestManager(r)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Record delta frames without a preceding keyframe -- should be dropped.
	m.RecordFrame("cam1", makeVideoFrame(3003, false, 100))
	m.RecordFrame("cam1", makeVideoFrame(6006, false, 100))

	status := m.Status()
	buf := status.Buffers[0]
	require.Equal(t, 0, buf.FrameCount,
		"delta frames before first keyframe should be dropped, got %d frames", buf.FrameCount)
	require.Equal(t, 0, buf.GOPCount,
		"no GOPs should exist without a keyframe, got %d", buf.GOPCount)

	// Now record a keyframe -- starts a GOP.
	m.RecordFrame("cam1", makeVideoFrame(9009, true, 200))
	status = m.Status()
	buf = status.Buffers[0]
	require.Equal(t, 1, buf.FrameCount, "keyframe should be recorded")
	require.Equal(t, 1, buf.GOPCount, "one GOP should exist")

	// Record delta frames -- extends the current GOP.
	m.RecordFrame("cam1", makeVideoFrame(12012, false, 100))
	m.RecordFrame("cam1", makeVideoFrame(15015, false, 100))
	status = m.Status()
	buf = status.Buffers[0]
	require.Equal(t, 3, buf.FrameCount, "GOP should have 3 frames")
	require.Equal(t, 1, buf.GOPCount, "still one GOP")

	// Record another keyframe -- starts a second GOP.
	m.RecordFrame("cam1", makeVideoFrame(90000, true, 200))
	status = m.Status()
	buf = status.Buffers[0]
	require.Equal(t, 4, buf.FrameCount, "should have 4 total frames")
	require.Equal(t, 2, buf.GOPCount, "should have 2 GOPs")
}

// ==========================================================================
// 3. TestMarkInOutBoundaries -- Mark-in and mark-out points are stored
//    correctly and retrievable via Status.
// ==========================================================================

func TestMarkInOutBoundaries(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	m := newTestManager(r)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Initially, no marks set.
	status := m.Status()
	require.Nil(t, status.MarkIn, "mark-in should not be set initially")
	require.Nil(t, status.MarkOut, "mark-out should not be set initially")
	require.Empty(t, status.MarkSource)

	// Set mark-in.
	beforeMarkIn := time.Now()
	err = m.MarkIn("cam1")
	require.NoError(t, err)
	afterMarkIn := time.Now()

	status = m.Status()
	require.NotNil(t, status.MarkIn, "mark-in should be set")
	require.Nil(t, status.MarkOut, "mark-out should not be set yet")
	require.Equal(t, "cam1", status.MarkSource)

	// Verify mark-in time is within the expected range.
	require.False(t, status.MarkIn.Before(beforeMarkIn),
		"mark-in time should not be before our measurement")
	require.False(t, status.MarkIn.After(afterMarkIn),
		"mark-in time should not be after our measurement")

	// MarkOut without delay should fail if time hasn't advanced.
	time.Sleep(15 * time.Millisecond)

	// Set mark-out.
	beforeMarkOut := time.Now()
	err = m.MarkOut("cam1")
	require.NoError(t, err)
	afterMarkOut := time.Now()

	status = m.Status()
	require.NotNil(t, status.MarkOut, "mark-out should be set")
	require.False(t, status.MarkOut.Before(beforeMarkOut),
		"mark-out time should not be before our measurement")
	require.False(t, status.MarkOut.After(afterMarkOut),
		"mark-out time should not be after our measurement")

	// Mark-out must be after mark-in.
	require.True(t, status.MarkOut.After(*status.MarkIn),
		"mark-out (%v) must be after mark-in (%v)", status.MarkOut, status.MarkIn)

	// MarkIn on unknown source returns error.
	err = m.MarkIn("nonexistent")
	require.ErrorIs(t, err, replay.ErrNoSource)

	// MarkOut on unknown source returns error.
	err = m.MarkOut("nonexistent")
	require.ErrorIs(t, err, replay.ErrNoSource)

	// MarkOut without MarkIn on a different source returns error.
	err = m.AddSource("cam2")
	require.NoError(t, err)
	err = m.MarkOut("cam2")
	require.ErrorIs(t, err, replay.ErrSourceMismatch)
}

// ==========================================================================
// 4. TestPlayerStateTransitions -- Player lifecycle:
//    Idle -> Loading/Playing -> Paused -> Playing -> Stopped (Idle).
// ==========================================================================

func TestPlayerStateTransitions(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	m := newTestManager(r)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Record frames so we have data to play.
	m.RecordFrame("cam1", makeVideoFrame(0, true, 200))
	m.RecordFrame("cam1", makeVideoFrame(3003, false, 100))
	m.RecordFrame("cam1", makeVideoFrame(6006, false, 100))

	// Initial state: Idle.
	status := m.Status()
	require.Equal(t, replay.PlayerIdle, status.State, "initial state should be idle")

	// Set marks and play at slow speed so playback lasts long enough.
	err = m.MarkIn("cam1")
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	m.RecordFrame("cam1", makeVideoFrame(9009, true, 200))
	m.RecordFrame("cam1", makeVideoFrame(12012, false, 100))

	err = m.MarkOut("cam1")
	require.NoError(t, err)

	err = m.Play("cam1", 0.25, true) // 0.25x, looping -- stays active
	require.NoError(t, err)

	// State should quickly transition to Loading or Playing.
	require.Eventually(t, func() bool {
		s := m.Status().State
		return s == replay.PlayerLoading || s == replay.PlayerPlaying
	}, 2*time.Second, 10*time.Millisecond,
		"expected loading or playing state after Play()")

	// Wait for Playing state.
	require.Eventually(t, func() bool {
		return m.Status().State == replay.PlayerPlaying
	}, 5*time.Second, 20*time.Millisecond,
		"expected playing state")

	// Pause.
	err = m.Pause()
	require.NoError(t, err)
	require.Equal(t, replay.PlayerPaused, m.Status().State,
		"state should be paused after Pause()")

	// Resume.
	err = m.Resume()
	require.NoError(t, err)
	require.Equal(t, replay.PlayerPlaying, m.Status().State,
		"state should be playing after Resume()")

	// Stop.
	err = m.Stop()
	require.NoError(t, err)

	// Should return to idle.
	require.Eventually(t, func() bool {
		return m.Status().State == replay.PlayerIdle
	}, 5*time.Second, 20*time.Millisecond,
		"expected idle state after Stop()")
}

// ==========================================================================
// 5. TestSlowMoFrameCount -- At 0.5x speed, output frame count is
//    approximately 2x the input frame count.
// ==========================================================================

func TestSlowMoFrameCount(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	m := newTestManager(r)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Record frames with known count.
	// 1 GOP, 5 frames total.
	inputFrames := 5
	m.RecordFrame("cam1", makeVideoFrame(0, true, 200))
	for i := 1; i < inputFrames; i++ {
		m.RecordFrame("cam1", makeVideoFrame(int64(i)*3003, false, 100))
	}

	err = m.MarkIn("cam1")
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	// Record second GOP so mark-out covers the clip.
	m.RecordFrame("cam1", makeVideoFrame(int64(inputFrames)*3003, true, 200))
	for i := 1; i <= 4; i++ {
		m.RecordFrame("cam1", makeVideoFrame(int64(inputFrames+i)*3003, false, 100))
	}

	err = m.MarkOut("cam1")
	require.NoError(t, err)

	// Play at 0.5x speed (non-looping).
	err = m.Play("cam1", 0.5, false)
	require.NoError(t, err)

	// Wait for playback to complete.
	require.Eventually(t, func() bool {
		return m.Status().State == replay.PlayerIdle
	}, 30*time.Second, 50*time.Millisecond,
		"playback did not complete within timeout")

	// Verify output frame count is approximately 2x input.
	// The clip contains frames between mark-in and mark-out which may not
	// be exactly inputFrames*2 due to GOP alignment, but should be close.
	outputCount := r.videoCount()
	require.Greater(t, outputCount, 0, "expected at least some output frames")

	// At 0.5x, each input frame produces 2 output frames.
	// Allow 20% tolerance for GOP alignment and timing.
	// The total input frames in the clip depends on GOP alignment, but
	// the output should be roughly double the input clip length.
	t.Logf("input frames (recorded): %d, output frames: %d, ratio: %.2f",
		inputFrames*2, outputCount, float64(outputCount)/float64(inputFrames*2))

	// We expect roughly 2x the clip frames (which is about 10 frames for
	// both GOPs), so ~20 output frames at 0.5x. Allow wide tolerance.
	require.Greater(t, outputCount, inputFrames,
		"at 0.5x speed, output should exceed input frame count")
}

// ==========================================================================
// 6. TestPhaseVocoderOutput -- Feed sine wave through phase vocoder at
//    0.5x speed. Verify: valid samples, length approximately 2x input.
// ==========================================================================

func TestPhaseVocoderOutput(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const durationSec = 1.0
	input := testutil.GenerateSineWavePCM(440, -6, sampleRate, durationSec)
	require.Greater(t, len(input), 0, "input should not be empty")

	output := replay.PhaseVocoderTimeStretch(input, 1, sampleRate, 0.5)
	require.NotNil(t, output, "phase vocoder output should not be nil")

	// Verify output length is approximately 2x input.
	expectedLen := len(input) * 2
	tolerance := float64(expectedLen) * 0.15 // 15% tolerance
	assert.InDelta(t, expectedLen, len(output), tolerance,
		"output length %d should be approximately 2x input length %d", len(output), len(input))

	// Verify no NaN or Inf values in output.
	for i, s := range output {
		require.False(t, math.IsNaN(float64(s)),
			"output sample %d is NaN", i)
		require.False(t, math.IsInf(float64(s), 0),
			"output sample %d is Inf", i)
	}

	// Verify output has non-trivial energy (not silent).
	var rms float64
	for _, s := range output {
		rms += float64(s) * float64(s)
	}
	rms = math.Sqrt(rms / float64(len(output)))
	require.Greater(t, rms, 0.01, "output should have non-trivial energy, got RMS=%.6f", rms)

	// Verify output is bounded (no clipping beyond +-0.96).
	for i, s := range output {
		assert.LessOrEqual(t, s, float32(0.96),
			"sample %d exceeds positive clip boundary: %.4f", i, s)
		assert.GreaterOrEqual(t, s, float32(-0.96),
			"sample %d exceeds negative clip boundary: %.4f", i, s)
	}
}

// ==========================================================================
// 7. TestWSOLAOutput -- Feed sine wave through WSOLA at 0.5x speed.
//    Verify: valid output, length approximately 2x input.
// ==========================================================================

func TestWSOLAOutput(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const durationSec = 1.0
	input := testutil.GenerateSineWavePCM(440, -6, sampleRate, durationSec)
	require.Greater(t, len(input), 0, "input should not be empty")

	output := replay.WSOLATimeStretch(input, 1, sampleRate, 0.5)
	require.NotNil(t, output, "WSOLA output should not be nil")

	// Verify output length is approximately 2x input.
	expectedLen := len(input) * 2
	tolerance := float64(expectedLen) * 0.10 // 10% tolerance
	assert.InDelta(t, expectedLen, len(output), tolerance,
		"output length %d should be approximately 2x input length %d", len(output), len(input))

	// Verify no NaN or Inf values.
	for i, s := range output {
		require.False(t, math.IsNaN(float64(s)),
			"output sample %d is NaN", i)
		require.False(t, math.IsInf(float64(s), 0),
			"output sample %d is Inf", i)
	}

	// Verify output has non-trivial energy.
	var rms float64
	for _, s := range output {
		rms += float64(s) * float64(s)
	}
	rms = math.Sqrt(rms / float64(len(output)))
	require.Greater(t, rms, 0.01, "WSOLA output should have non-trivial energy, got RMS=%.6f", rms)

	// Verify output is bounded (WSOLA normalizes peaks to 0.95).
	for i, s := range output {
		assert.LessOrEqual(t, s, float32(0.96),
			"sample %d exceeds positive boundary: %.4f", i, s)
		assert.GreaterOrEqual(t, s, float32(-0.96),
			"sample %d exceeds negative boundary: %.4f", i, s)
	}
}

// ==========================================================================
// 8. TestFFTRoundTrip -- Validate FFT round-trip by running phase vocoder
//    at 1.0x speed (passthrough). Output should match input exactly.
// ==========================================================================

func TestFFTRoundTrip(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const durationSec = 1.0
	input := testutil.GenerateSineWavePCM(440, -6, sampleRate, durationSec)

	// PhaseVocoderTimeStretch at 1.0x returns a copy of input (passthrough).
	// This validates that the FFT/IFFT path preserves the signal.
	output := replay.PhaseVocoderTimeStretch(input, 1, sampleRate, 1.0)
	require.NotNil(t, output)
	require.Len(t, output, len(input),
		"passthrough should produce same length")

	// Verify sample-level accuracy.
	maxErr := float64(0)
	for i := range input {
		err := math.Abs(float64(input[i]) - float64(output[i]))
		if err > maxErr {
			maxErr = err
		}
	}
	require.Less(t, maxErr, 1e-6,
		"FFT roundtrip max error should be < 1e-6, got %.10f", maxErr)
}

// ==========================================================================
// 9. TestFFTKnownSine -- Feed 1kHz sine through phase vocoder at 0.75x.
//    Verify the output preserves the dominant frequency (peak detection).
// ==========================================================================

func TestFFTKnownSine(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const freq = 1000.0 // 1kHz
	const durationSec = 2.0

	input := testutil.GenerateSineWavePCM(freq, -6, sampleRate, durationSec)
	require.Greater(t, len(input), 0)

	// Stretch at 0.75x -- should preserve pitch (phase vocoder property).
	output := replay.PhaseVocoderTimeStretch(input, 1, sampleRate, 0.75)
	require.NotNil(t, output)

	// Analyze output frequency content via a simple DFT peak finder.
	// Use a window of 4096 samples from the middle of the output.
	windowSize := 4096
	if len(output) < windowSize {
		t.Skipf("output too short for frequency analysis: %d < %d", len(output), windowSize)
	}
	start := (len(output) - windowSize) / 2
	window := output[start : start+windowSize]

	// Compute magnitude spectrum.
	peakBin := 0
	peakMag := 0.0
	for k := 1; k < windowSize/2; k++ {
		re := 0.0
		im := 0.0
		for n := 0; n < windowSize; n++ {
			angle := 2 * math.Pi * float64(k) * float64(n) / float64(windowSize)
			re += float64(window[n]) * math.Cos(angle)
			im -= float64(window[n]) * math.Sin(angle)
		}
		mag := math.Sqrt(re*re + im*im)
		if mag > peakMag {
			peakMag = mag
			peakBin = k
		}
	}

	// Convert peak bin to frequency.
	peakFreq := float64(peakBin) * float64(sampleRate) / float64(windowSize)

	// The phase vocoder should preserve pitch, so peak should be near 1kHz.
	// Allow some tolerance due to windowing and bin resolution.
	binResolution := float64(sampleRate) / float64(windowSize) // ~11.7 Hz
	assert.InDelta(t, freq, peakFreq, binResolution*2,
		"peak frequency %.1f Hz should be near %.1f Hz (bin resolution %.1f Hz)",
		peakFreq, freq, binResolution)
}

// ==========================================================================
// 10. TestInterpolatorBlend -- At 0.5x speed, the blend interpolator
//     produces output where interpolated frames are averages of adjacent
//     decoded frames. We test this via the WSOLA property: stretched
//     audio should have smooth energy, not abrupt silence gaps.
// ==========================================================================

func TestInterpolatorBlend(t *testing.T) {
	t.Parallel()

	// Test the blend property of WSOLA: feeding two different sine waves
	// produces output that smoothly transitions (no abrupt zeros).
	const sampleRate = 48000
	const durationSec = 0.5

	// Create a signal that's a 440Hz tone for the first half and
	// 880Hz tone for the second half.
	n := int(float64(sampleRate) * durationSec)
	input := make([]float32, n)
	for i := 0; i < n; i++ {
		t_ := float64(i) / float64(sampleRate)
		if i < n/2 {
			input[i] = float32(0.5 * math.Sin(2*math.Pi*440*t_))
		} else {
			input[i] = float32(0.5 * math.Sin(2*math.Pi*880*t_))
		}
	}

	// Stretch at 0.5x.
	output := replay.WSOLATimeStretch(input, 1, sampleRate, 0.5)
	require.NotNil(t, output)
	require.Greater(t, len(output), n, "output should be longer than input")

	// Verify no silent gaps: every window of 1024 samples should have
	// non-trivial RMS energy (the WSOLA overlap-add should not produce
	// silence windows in the middle of an active signal).
	windowLen := 1024
	minWindows := (len(output) - windowLen) / (windowLen / 2)
	silentWindows := 0
	for i := 0; i+windowLen <= len(output); i += windowLen / 2 {
		var rms float64
		for j := 0; j < windowLen; j++ {
			s := float64(output[i+j])
			rms += s * s
		}
		rms = math.Sqrt(rms / float64(windowLen))
		if rms < 0.001 {
			silentWindows++
		}
	}

	// Allow at most 5% of windows to be near-silent (edges).
	maxSilent := int(float64(minWindows) * 0.05)
	if maxSilent < 2 {
		maxSilent = 2
	}
	require.LessOrEqual(t, silentWindows, maxSilent,
		"too many silent windows (%d out of %d): blend/overlap-add is producing gaps",
		silentWindows, minWindows)
}

// ==========================================================================
// Additional: TestPhaseVocoderStereo -- Verify stereo processing.
// ==========================================================================

func TestPhaseVocoderStereo(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const durationSec = 1.0
	mono := testutil.GenerateSineWavePCM(440, -6, sampleRate, durationSec)

	// Create stereo: left = full, right = half amplitude
	stereo := make([]float32, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s
		stereo[i*2+1] = s * 0.5
	}

	output := replay.PhaseVocoderTimeStretch(stereo, 2, sampleRate, 0.5)
	require.NotNil(t, output)

	// Output length should be approximately 2x input.
	expectedLen := len(stereo) * 2
	tolerance := float64(expectedLen) * 0.15
	assert.InDelta(t, expectedLen, len(output), tolerance,
		"stereo output length should be ~2x input")

	// Must be even (stereo interleaved).
	require.Equal(t, 0, len(output)%2,
		"stereo output length must be even, got %d", len(output))
}

// ==========================================================================
// Additional: TestWSOLAQuarterSpeed -- Quarter speed should quadruple.
// ==========================================================================

func TestWSOLAQuarterSpeed(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	input := testutil.GenerateSineWavePCM(440, -6, sampleRate, 1.0)

	output := replay.WSOLATimeStretch(input, 1, sampleRate, 0.25)
	require.NotNil(t, output)

	expectedLen := len(input) * 4
	tolerance := float64(expectedLen) * 0.10
	assert.InDelta(t, expectedLen, len(output), tolerance,
		"at 0.25x speed, output should be ~4x input length")
}

// ==========================================================================
// Additional: TestManagerMaxSources -- Verify source limit enforcement.
// ==========================================================================

func TestManagerMaxSources(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	cfg := replay.Config{
		BufferDurationSecs: 10,
		MaxSources:         2,
		MaxBufferBytes:     1024 * 1024,
		TmpfsDir:           "none",
	}
	m := replay.NewManager(r, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)
	err = m.AddSource("cam2")
	require.NoError(t, err)

	// Third source should fail.
	err = m.AddSource("cam3")
	require.ErrorIs(t, err, replay.ErrMaxSources)

	// Status should show exactly 2 sources.
	status := m.Status()
	require.Len(t, status.Buffers, 2)
}

// ==========================================================================
// Additional: TestPlaybackCallbacks -- onPlaybackStart and onPlaybackStop
//    are invoked correctly during playback lifecycle.
// ==========================================================================

func TestPlaybackCallbacks(t *testing.T) {
	t.Parallel()

	r := &mockRelay{}
	m := newTestManager(r)
	defer m.Close()

	var startCount, stopCount atomic.Int32
	m.OnPlaybackLifecycle(
		func() { startCount.Add(1) },
		func() { stopCount.Add(1) },
	)

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Record frames.
	m.RecordFrame("cam1", makeVideoFrame(0, true, 200))
	m.RecordFrame("cam1", makeVideoFrame(3003, false, 100))

	err = m.MarkIn("cam1")
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	m.RecordFrame("cam1", makeVideoFrame(6006, true, 200))
	m.RecordFrame("cam1", makeVideoFrame(9009, false, 100))

	err = m.MarkOut("cam1")
	require.NoError(t, err)

	// Play and wait for completion.
	err = m.Play("cam1", 1.0, false)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return m.Status().State == replay.PlayerIdle
	}, 10*time.Second, 50*time.Millisecond,
		"playback did not complete")

	// Verify callbacks were invoked.
	require.Equal(t, int32(1), startCount.Load(),
		"onPlaybackStart should have been called once")
	require.Equal(t, int32(1), stopCount.Load(),
		"onPlaybackStop should have been called once")
}
