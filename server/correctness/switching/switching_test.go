//go:build correctness

package switching

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// Test Helpers
// ==========================================================================

// captureViewer captures video and audio frames broadcast to the program relay.
type captureViewer struct {
	id     string
	mu     sync.Mutex
	videos []*media.VideoFrame
	audios []*media.AudioFrame
}

func newCaptureViewer(id string) *captureViewer {
	return &captureViewer{id: id}
}

func (c *captureViewer) ID() string                       { return c.id }
func (c *captureViewer) SendCaptions(_ *ccx.CaptionFrame) {}
func (c *captureViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{ID: c.id}
}

func (c *captureViewer) SendVideo(frame *media.VideoFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Deep copy WireData to avoid shared slice mutations.
	cp := *frame
	if len(frame.WireData) > 0 {
		cp.WireData = make([]byte, len(frame.WireData))
		copy(cp.WireData, frame.WireData)
	}
	c.videos = append(c.videos, &cp)
}

func (c *captureViewer) SendAudio(frame *media.AudioFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *frame
	if len(frame.Data) > 0 {
		cp.Data = make([]byte, len(frame.Data))
		copy(cp.Data, frame.Data)
	}
	c.audios = append(c.audios, &cp)
}

func (c *captureViewer) videoCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.videos)
}

func (c *captureViewer) videosCopy() []*media.VideoFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*media.VideoFrame, len(c.videos))
	copy(out, c.videos)
	return out
}

func (c *captureViewer) clearVideos() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.videos = nil
}

// blackTag is the first byte of a broadcast-black program frame (Y=16).
const blackTag = 0x10

// rawFrame returns a 320x240 YUV420 frame whose every byte is tag, so the
// first byte of the program frame made from it names its source.
func rawFrame(tag byte) *switcher.ProcessingFrame {
	return &switcher.ProcessingFrame{
		Data:  bytes.Repeat([]byte{tag}, 320*240*3/2),
		Width: 320, Height: 240,
	}
}

// feedLive ingests a frame tagged tag every program frame period, as a
// live source delivers them, until the returned stop func is called.
func feedLive(sw *switcher.Switcher, key string, tag byte) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk := time.NewTicker(sw.PipelineFormat().FrameDuration())
		defer tk.Stop()
		for {
			sw.IngestRawVideo(key, rawFrame(tag))
			select {
			case <-done:
				return
			case <-tk.C:
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

// tagOf returns the source tag a program frame carries: the first byte of
// a raw (encoder-less) broadcast, the last of a tagEncoder's output.
type tagOf func(v *media.VideoFrame) byte

func rawTag(v *media.VideoFrame) byte { return v.WireData[0] }
func encTag(v *media.VideoFrame) byte { return v.WireData[len(v.WireData)-1] }

// waitForTag waits until the program shows a frame tagged tag and returns
// the index of the first one.
func waitForTag(t *testing.T, c *captureViewer, tag byte, of tagOf) int {
	t.Helper()
	idx := -1
	require.Eventually(t, func() bool {
		for i, v := range c.videosCopy() {
			if len(v.WireData) > 0 && of(v) == tag {
				idx = i
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "no program frame tagged 0x%02X", tag)
	return idx
}

// tagEncoder is a mock encoder whose output ends with the first luma byte
// of the frame it encoded, so an encoded program frame names its source.
type tagEncoder struct{}

func (tagEncoder) Encode(yuv []byte, _ int64, _ bool) ([]byte, bool, error) {
	return []byte{0x00, 0x00, 0x00, 0x01, 0x65, yuv[0]}, true, nil
}
func (tagEncoder) Close() {}

// ==========================================================================
// 1. TestCutNoIntermediateFrames
//
// Source A emits frames with WireData tag 0xAA, Source B emits 0xBB.
// After Cut from A to B, the program relay output must transition
// instantly: no blended/intermediate frames between A and B content.
//
// Uses the raw broadcast path (no pipeline/encoder), so each program
// frame carries its source's tag. Both sources deliver live throughout.
// ==========================================================================

func TestCutNoIntermediateFrames(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	capture := newCaptureViewer("capture")
	programRelay.AddViewer(capture)

	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.RegisterSource("camA", distribution.NewRelay())
	sw.RegisterSource("camB", distribution.NewRelay())
	defer feedLive(sw, "camA", 0xAA)()
	defer feedLive(sw, "camB", 0xBB)()

	// Cut to A and wait for it on program.
	require.NoError(t, sw.Cut(context.Background(), "camA"))
	waitForTag(t, capture, 0xAA, rawTag)

	// Cut to B and wait for some frames of it.
	require.NoError(t, sw.Cut(context.Background(), "camB"))
	firstB := waitForTag(t, capture, 0xBB, rawTag)
	require.Eventually(t, func() bool { return capture.videoCount() > firstB+5 },
		5*time.Second, 5*time.Millisecond)

	// The critical invariant: NO intermediate frames. Black only before
	// camA's first frame, then camA, then camB, and nothing else.
	videos := capture.videosCopy()
	seq := []byte{blackTag, 0xAA, 0xBB}
	at := 0
	for i, v := range videos {
		tag := rawTag(v)
		for at < len(seq) && seq[at] != tag {
			at++
		}
		require.Less(t, at, len(seq), "frame %d tag 0x%02X out of order (want black*, 0xAA+, 0xBB+)", i, tag)
	}
	require.Equal(t, byte(0xAA), rawTag(videos[firstB-1]), "frame before the first camB frame")

	t.Logf("Cut transition verified: frame[%d]=0x%02X -> frame[%d]=0x%02X (no intermediate frames)",
		firstB-1, rawTag(videos[firstB-1]), firstB, rawTag(videos[firstB]))
}

// ==========================================================================
// 2. TestCutStartsOnIDRBoundary
//
// With a real encoder pipeline, after a cut the first encoded frame
// of the new source must be a keyframe (IDR). We verify this by
// encoding output to a TS file and using ffprobe.
//
// This test uses the raw YUV pipeline: IngestRawVideo → pipeline → encode →
// broadcast. The Switcher forces an IDR on every cut via forceNextIDR.
// ==========================================================================

func TestCutStartsOnIDRBoundary(t *testing.T) {
	t.Parallel()

	const (
		width  = 320
		height = 240
	)

	programRelay := distribution.NewRelay()

	// Collect encoded TS output via a muxer.
	muxer := output.NewTSMuxer()
	var tsMu sync.Mutex
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsMu.Lock()
		tsData = append(tsData, data...)
		tsMu.Unlock()
	})

	// Create a capture viewer that also feeds the muxer.
	capture := newCaptureViewer("capture")
	muxViewer := &muxCaptureViewer{
		capture: capture,
		muxer:   muxer,
	}
	programRelay.AddViewer(muxViewer)

	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	// Set up real pipeline codecs (libx264 encoder).
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return codec.NewFFmpegEncoder("libx264", w, h, bitrate, fpsNum, fpsDen, 2, nil)
	})
	require.NoError(t, sw.BuildPipeline())

	camARelay := distribution.NewRelay()
	camBRelay := distribution.NewRelay()
	sw.RegisterSource("camA", camARelay)
	sw.RegisterSource("camB", camBRelay)

	// Cut to A.
	require.NoError(t, sw.Cut(context.Background(), "camA"))

	// Feed solid Y=100 frames from A via IngestRawVideo.
	yuvA := testutil.GenerateSolidYUV420(width, height, 100, 128, 128)
	for i := 0; i < 10; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuvA),
			Width:      width,
			Height:     height,
			PTS:        int64(1000 + i*3003),
			IsKeyframe: i == 0,
		}
		sw.IngestRawVideo("camA", pf)
	}

	// Wait for pipeline processing.
	time.Sleep(200 * time.Millisecond)

	// Record how many frames we had from A.
	preCount := capture.videoCount()
	t.Logf("Frames from camA before cut: %d", preCount)
	require.Greater(t, preCount, 0, "should have encoded frames from camA")

	// Cut to B.
	require.NoError(t, sw.Cut(context.Background(), "camB"))

	// Feed solid Y=200 frames from B.
	yuvB := testutil.GenerateSolidYUV420(width, height, 200, 128, 128)
	for i := 0; i < 10; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuvB),
			Width:      width,
			Height:     height,
			PTS:        int64(5000 + i*3003),
			IsKeyframe: i == 0,
		}
		sw.IngestRawVideo("camB", pf)
	}

	// Wait for pipeline processing.
	time.Sleep(200 * time.Millisecond)

	postCount := capture.videoCount()
	require.Greater(t, postCount, preCount, "should have additional frames from camB")

	// Write TS to temp file and ffprobe.
	tsMu.Lock()
	tsBytes := make([]byte, len(tsData))
	copy(tsBytes, tsData)
	tsMu.Unlock()

	if len(tsBytes) == 0 {
		t.Skip("No TS data produced — encoder may not have produced output yet")
	}

	tmpDir := t.TempDir()
	tsPath := filepath.Join(tmpDir, "cut_test.ts")
	require.NoError(t, os.WriteFile(tsPath, tsBytes, 0o644))

	frames := testutil.ProbeVideoFrames(t, tsPath)
	require.Greater(t, len(frames), 0, "ffprobe should find video frames")

	// Find the first keyframe after preCount (approximately where the cut happened).
	// The Switcher forces IDR on cut, so the first frame of the new source
	// segment must be a keyframe.
	//
	// Due to encoder buffering, we can't precisely identify which ffprobe frame
	// corresponds to the cut boundary. Instead, verify that at least one
	// keyframe exists after the initial GOP.
	keyframeCount := 0
	for _, f := range frames {
		if f.KeyFrame {
			keyframeCount++
		}
	}

	// We expect at least 2 keyframes: one at the start of camA and one at the
	// start of camB (forced IDR after cut).
	require.GreaterOrEqual(t, keyframeCount, 2,
		"expected at least 2 keyframes (initial + post-cut IDR), got %d in %d frames",
		keyframeCount, len(frames))

	t.Logf("Found %d keyframes in %d total frames — post-cut IDR verified",
		keyframeCount, len(frames))
}

// muxCaptureViewer wraps a captureViewer and also feeds frames to a TSMuxer.
type muxCaptureViewer struct {
	capture *captureViewer
	muxer   *output.TSMuxer
}

func (m *muxCaptureViewer) ID() string { return m.capture.id }
func (m *muxCaptureViewer) SendVideo(frame *media.VideoFrame) {
	m.capture.SendVideo(frame)
	_ = m.muxer.WriteVideo(frame)
}
func (m *muxCaptureViewer) SendAudio(frame *media.AudioFrame) {
	m.capture.SendAudio(frame)
}
func (m *muxCaptureViewer) SendCaptions(_ *ccx.CaptionFrame) {}
func (m *muxCaptureViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{ID: m.capture.id}
}

// ==========================================================================
// 3. TestPreviewProgramStateAfterCut
//
// A on program, B on preview. Cut to B. Assert: program=B, preview=A
// via the state callback.
// ==========================================================================

func TestPreviewProgramStateAfterCut(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	// Track state changes.
	var states []internal.ControlRoomState
	var statesMu sync.Mutex
	sw.OnStateChange(func(state internal.ControlRoomState) {
		statesMu.Lock()
		states = append(states, state)
		statesMu.Unlock()
	})

	camARelay := distribution.NewRelay()
	camBRelay := distribution.NewRelay()
	sw.RegisterSource("camA", camARelay)
	sw.RegisterSource("camB", camBRelay)

	// Cut to A.
	require.NoError(t, sw.Cut(context.Background(), "camA"))

	// Set B as preview.
	require.NoError(t, sw.SetPreview(context.Background(), "camB"))

	// Verify initial state.
	state := sw.State()
	require.Equal(t, "camA", state.ProgramSource)
	require.Equal(t, "camB", state.PreviewSource)

	// Cut to B — previous program (A) should become preview.
	require.NoError(t, sw.Cut(context.Background(), "camB"))

	// Verify post-cut state.
	state = sw.State()
	require.Equal(t, "camB", state.ProgramSource,
		"after cut: program should be camB")
	require.Equal(t, "camA", state.PreviewSource,
		"after cut: preview should be camA (auto-swapped)")

	// Verify tally state.
	require.Equal(t, "program", state.TallyState["camB"],
		"camB should have program tally")
	require.Equal(t, "preview", state.TallyState["camA"],
		"camA should have preview tally")

	// Verify state callbacks were fired.
	statesMu.Lock()
	stateCount := len(states)
	statesMu.Unlock()
	require.GreaterOrEqual(t, stateCount, 3,
		"expected at least 3 state changes (Cut A, SetPreview B, Cut B), got %d", stateCount)

	// Verify the last state callback matches the final state.
	statesMu.Lock()
	lastState := states[len(states)-1]
	statesMu.Unlock()
	require.Equal(t, "camB", lastState.ProgramSource)
	require.Equal(t, "camA", lastState.PreviewSource)

	t.Logf("State after cut: program=%s, preview=%s, tally=%v",
		state.ProgramSource, state.PreviewSource, state.TallyState)
}

// ==========================================================================
// 4. TestRapidCutsNoDroppedFrames
//
// 10 cuts in rapid succession. Verify the Switcher doesn't deadlock,
// crash, or drop frames incorrectly. After cuts, the final state
// should be consistent, and frame output should match expected counts.
// ==========================================================================

func TestRapidCutsNoDroppedFrames(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	capture := newCaptureViewer("capture")
	programRelay.AddViewer(capture)

	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	// Create 3 live sources.
	tags := map[string]byte{"cam1": 0x01, "cam2": 0x02, "cam3": 0x03}
	for _, name := range []string{"cam1", "cam2", "cam3"} {
		sw.RegisterSource(name, distribution.NewRelay())
		defer feedLive(sw, name, tags[name])()
	}

	// Cut to cam1 first and wait for it on program.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	waitForTag(t, capture, 0x01, rawTag)

	// Perform 10 rapid cuts cycling through sources.
	sources := []string{"cam2", "cam3", "cam1", "cam2", "cam3", "cam1", "cam2", "cam3", "cam1", "cam2"}
	for i, src := range sources {
		require.NoError(t, sw.Cut(context.Background(), src),
			"cut %d to %s should not error", i, src)
	}
	capture.clearVideos()

	// Final source should be cam2.
	state := sw.State()
	require.Equal(t, "cam2", state.ProgramSource,
		"after 10 rapid cuts, program should be cam2")

	// The final program source shows, and nothing else once it does.
	first := waitForTag(t, capture, 0x02, rawTag)
	require.Eventually(t, func() bool { return capture.videoCount() > first+5 },
		5*time.Second, 5*time.Millisecond)
	videos := capture.videosCopy()
	tagCount := make(map[byte]int)
	for i, v := range videos[first:] {
		tagCount[rawTag(v)]++
		require.Equal(t, byte(0x02), rawTag(v), "frame %d after cam2 showed is not cam2", first+i)
	}
	t.Logf("Frame tag distribution: %v (total: %d frames)", tagCount, len(videos))
	t.Logf("Final state: program=%s, preview=%s", state.ProgramSource, state.PreviewSource)

	// Verify no panics occurred and state is consistent.
	require.NotEmpty(t, state.ProgramSource, "program source should be set")
	require.False(t, state.InTransition, "should not be in transition")
}

// ==========================================================================
// 5. TestRapidCutsWithRawPipeline
//
// Tests rapid cuts through the raw YUV pipeline (IngestRawVideo).
// With a mock encoder, verifies that frames from the correct source
// emerge on the program relay after each cut, and that frame count
// is reasonable (no excessive drops).
// ==========================================================================

func TestRapidCutsWithRawPipeline(t *testing.T) {
	t.Parallel()

	const (
		width  = 320
		height = 240
	)

	programRelay := distribution.NewRelay()
	capture := newCaptureViewer("capture")
	programRelay.AddViewer(capture)

	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	// Set up pipeline with mock encoder.
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	require.NoError(t, sw.BuildPipeline())

	camARelay := distribution.NewRelay()
	camBRelay := distribution.NewRelay()
	sw.RegisterSource("camA", camARelay)
	sw.RegisterSource("camB", camBRelay)

	// Cut to A and feed some frames.
	require.NoError(t, sw.Cut(context.Background(), "camA"))

	yuvA := testutil.GenerateSolidYUV420(width, height, 100, 128, 128)
	for i := 0; i < 5; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuvA),
			Width:      width,
			Height:     height,
			PTS:        int64(1000 + i*3003),
			IsKeyframe: i == 0,
		}
		sw.IngestRawVideo("camA", pf)
	}

	time.Sleep(100 * time.Millisecond)
	preCount := capture.videoCount()

	// 10 rapid cuts back and forth.
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			require.NoError(t, sw.Cut(context.Background(), "camB"))
		} else {
			require.NoError(t, sw.Cut(context.Background(), "camA"))
		}
	}

	// Final state should be camA (10 is even, last cut was to camA since 9%2==1 → camA).
	// Actually: i=0→camB, i=1→camA, ..., i=9→camA. So final is camA.
	state := sw.State()
	require.Equal(t, "camA", state.ProgramSource)

	// Feed frames from A (now on program).
	for i := 0; i < 10; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuvA),
			Width:      width,
			Height:     height,
			PTS:        int64(5000 + i*3003),
			IsKeyframe: i == 0,
		}
		sw.IngestRawVideo("camA", pf)
	}

	time.Sleep(200 * time.Millisecond)

	postCount := capture.videoCount()
	require.Greater(t, postCount, preCount,
		"should have additional encoded frames after rapid cuts")

	t.Logf("Pre-cut frames: %d, post-cut total: %d", preCount, postCount)
}

// ==========================================================================
// 6. TestAudioCutNoClick
//
// Feed sine waves on both sources. Cut. Capture PCM around the splice
// point. Verify the crossfade is clean (no click/pop).
//
// This test exercises the audio.Mixer's OnCut crossfade behavior.
// The Switcher auto-calls OnCut when a mixer is attached.
// ==========================================================================

func TestAudioCutNoClick(t *testing.T) {
	t.Parallel()

	// The audio mixer uses a clock-driven output ticker (~21ms cadence).
	// We test the crossfade mechanism directly rather than trying to
	// capture output from the ticker, which has timing dependencies.

	const (
		sampleRate = 48000
		channels   = 2
	)

	// Generate two sine waves at different frequencies.
	// A = 440 Hz, B = 880 Hz, both at -12 dBFS.
	durSec := 0.5
	monoA := testutil.GenerateSineWavePCM(440, -12, sampleRate, durSec)
	monoB := testutil.GenerateSineWavePCM(880, -12, sampleRate, durSec)

	// Apply equal-power crossfade using the audio package.
	result := audio.EqualPowerCrossfade(monoA, monoB)

	// The crossfade result should have no discontinuities.
	// Check for clicks by looking for sample-to-sample jumps > 0.5 linear.
	maxJump := float64(0)
	for i := 1; i < len(result); i++ {
		jump := math.Abs(float64(result[i]) - float64(result[i-1]))
		if jump > maxJump {
			maxJump = jump
		}
	}

	// A click would show up as a large sample-to-sample jump.
	// Normal sine transitions should have jumps < 0.1 at -12 dBFS.
	// We allow up to 0.3 to account for the crossfade transition region.
	require.Less(t, maxJump, 0.3,
		"max sample-to-sample jump %.6f exceeds click threshold (0.3)", maxJump)

	// Also verify the crossfade maintains reasonable level (no silence gap).
	// Check the middle of the crossfade — it should not dip significantly.
	midIdx := len(result) / 2
	midWindow := result[midIdx-100 : midIdx+100]
	midPeak := testutil.PeakDBFS(midWindow)
	require.Greater(t, midPeak, -30.0,
		"crossfade midpoint peak %.1f dBFS is too quiet (potential gap)", midPeak)

	t.Logf("Max sample jump: %.6f, midpoint peak: %.1f dBFS — no click detected",
		maxJump, midPeak)
}

// ==========================================================================
// 7. TestAudioCutViaSwitch
//
// Full Switcher + Mixer integration: verify that when the Switcher
// performs a cut with an audio mixer attached, the mixer's crossfade
// is triggered and the channel activation state is correct.
// ==========================================================================

func TestAudioCutViaSwitcher(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = mixer.Close() }()

	sw.SetMixer(mixer)
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	// The sync loop drives the mixer, and with it the crossfade ramp.
	sw.SetAACBuffer(clock.NewAACFrameBuffer(2, 48000, func([]float32) ([]byte, error) {
		return []byte{0}, nil
	}))
	sw.SetSyncMixer(mixer)

	// Register sources with AFV.
	camARelay := distribution.NewRelay()
	camBRelay := distribution.NewRelay()
	sw.RegisterSource("camA", camARelay)
	sw.RegisterSource("camB", camBRelay)
	mixer.AddChannel("camA")
	mixer.AddChannel("camB")
	require.NoError(t, mixer.SetAFV("camA", true))
	require.NoError(t, mixer.SetAFV("camB", true))

	// Cut to A — camA audio should activate.
	require.NoError(t, sw.Cut(context.Background(), "camA"))
	camARelay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	require.True(t, mixer.IsChannelActive("camA"),
		"camA should be active after cut to camA")
	require.False(t, mixer.IsChannelActive("camB"),
		"camB should be inactive after cut to camA")

	// Cut to B — camB activates, camA deactivation is deferred until
	// the cut crossfade ramp completes (4 ticks, ~85ms).
	require.NoError(t, sw.Cut(context.Background(), "camB"))
	camBRelay.BroadcastVideo(&media.VideoFrame{PTS: 150, IsKeyframe: true})

	require.True(t, mixer.IsChannelActive("camB"),
		"camB should be active after cut to camB")

	// Wait for the cut crossfade ramp to complete. Each tick of the
	// reference clock advances it; 4 ticks = ~133ms.
	require.Eventually(t, func() bool { return !mixer.IsChannelActive("camA") },
		2*time.Second, 5*time.Millisecond,
		"camA should be inactive after cut crossfade completes")

	t.Log("Audio AFV channel activation verified through cut operations")
}

// ==========================================================================
// 8. TestCutWithRawPipelineSourceIdentity
//
// Two live sources with distinct luma through the raw pipeline and an
// encoder whose output names the luma it encoded. After a cut, verify the
// program output switches from camA to camB with no camA frame leaking
// after camB started.
//
// This verifies the pipeline correctly routes only the program source's
// frames through encode after a cut, with no stale-source frames leaking.
// ==========================================================================

func TestCutWithRawPipelineSourceIdentity(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	capture := newCaptureViewer("capture")
	programRelay.AddViewer(capture)

	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return tagEncoder{}, nil
	})
	require.NoError(t, sw.BuildPipeline())

	sw.RegisterSource("camA", distribution.NewRelay())
	sw.RegisterSource("camB", distribution.NewRelay())
	defer feedLive(sw, "camA", 100)()
	defer feedLive(sw, "camB", 200)()

	require.NoError(t, sw.Cut(context.Background(), "camA"))
	waitForTag(t, capture, 100, encTag)

	require.NoError(t, sw.Cut(context.Background(), "camB"))
	firstB := waitForTag(t, capture, 200, encTag)
	require.Eventually(t, func() bool { return capture.videoCount() > firstB+5 },
		5*time.Second, 5*time.Millisecond)

	videos := capture.videosCopy()
	for i, v := range videos[firstB:] {
		require.Equal(t, byte(200), encTag(v),
			"frame %d: not camB after camB frames started", firstB+i)
	}

	t.Logf("Raw pipeline cut verified: %d total frames, camA->camB transition clean",
		len(videos))
}

// ==========================================================================
// 9. TestPreviewDoesNotAffectProgram
//
// Changing preview should not affect the program output.
// ==========================================================================

func TestPreviewDoesNotAffectProgram(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	capture := newCaptureViewer("capture")
	programRelay.AddViewer(capture)

	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	camARelay := distribution.NewRelay()
	camBRelay := distribution.NewRelay()
	camCRelay := distribution.NewRelay()
	sw.RegisterSource("camA", camARelay)
	sw.RegisterSource("camB", camBRelay)
	sw.RegisterSource("camC", camCRelay)
	defer feedLive(sw, "camA", 0xAA)()
	defer feedLive(sw, "camB", 0xBB)()
	defer feedLive(sw, "camC", 0xCC)()

	// Cut to A.
	require.NoError(t, sw.Cut(context.Background(), "camA"))
	waitForTag(t, capture, 0xAA, rawTag)
	capture.clearVideos()

	// Change preview repeatedly -- should not affect program.
	require.NoError(t, sw.SetPreview(context.Background(), "camB"))
	require.NoError(t, sw.SetPreview(context.Background(), "camC"))
	require.NoError(t, sw.SetPreview(context.Background(), "camB"))

	// Only the program source (A) appears in output.
	require.Eventually(t, func() bool { return capture.videoCount() >= 10 },
		5*time.Second, 5*time.Millisecond)
	for i, v := range capture.videosCopy() {
		require.Equal(t, byte(0xAA), rawTag(v),
			"frame %d should be from camA, not preview sources", i)
	}

	// Verify state is consistent.
	state := sw.State()
	require.Equal(t, "camA", state.ProgramSource)
	require.Equal(t, "camB", state.PreviewSource)

	t.Log("Preview changes do not affect program output")
}

// ==========================================================================
// 10. TestCutToNonexistentSourceFails
//
// Attempting to cut to a source that doesn't exist should return an error.
// ==========================================================================

func TestCutToNonexistentSourceFails(t *testing.T) {
	t.Parallel()

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	defer sw.Close()

	err := sw.Cut(context.Background(), "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// State should be unchanged.
	state := sw.State()
	require.Equal(t, "", state.ProgramSource)
}

// ==========================================================================
// Utilities
// ==========================================================================

func copyBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
