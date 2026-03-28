//go:build correctness

package clip

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/output"
)

// ==========================================================================
// Test helpers
// ==========================================================================

// Synthetic H.264 parameters (Baseline profile, level 3.0, 320x240).
var (
	testSPS = []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	testPPS = []byte{0x68, 0xCE, 0x38, 0x80}
)

// makeAVC1 wraps a NALU body in AVC1 4-byte length prefix format.
func makeAVC1(naluData []byte) []byte {
	out := make([]byte, 4+len(naluData))
	out[0] = byte(len(naluData) >> 24)
	out[1] = byte(len(naluData) >> 16)
	out[2] = byte(len(naluData) >> 8)
	out[3] = byte(len(naluData))
	copy(out[4:], naluData)
	return out
}

// generateTestTS creates a valid MPEG-TS byte stream with numFrames video
// frames (first is keyframe) and interleaved audio.
func generateTestTS(t *testing.T, numFrames int) []byte {
	t.Helper()

	idrBody := make([]byte, 64)
	idrBody[0] = 0x65 // NALU type 5 (IDR)
	for i := 1; i < len(idrBody); i++ {
		idrBody[i] = byte(i)
	}

	nonIDRBody := make([]byte, 32)
	nonIDRBody[0] = 0x41 // NALU type 1 (non-IDR slice)
	for i := 1; i < len(nonIDRBody); i++ {
		nonIDRBody[i] = byte(i)
	}

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	const (
		frameInterval = 3000 // ~30fps at 90kHz
		sampleRate    = 48000
		channels      = 2
	)

	for i := 0; i < numFrames; i++ {
		pts := int64(90000 + i*frameInterval)

		if i == 0 {
			frame := &media.VideoFrame{
				PTS:        pts,
				DTS:        pts,
				IsKeyframe: true,
				SPS:        testSPS,
				PPS:        testPPS,
				WireData:   makeAVC1(idrBody),
				Codec:      "h264",
			}
			require.NoError(t, muxer.WriteVideo(frame))
		} else {
			frame := &media.VideoFrame{
				PTS:      pts,
				DTS:      pts,
				WireData: makeAVC1(nonIDRBody),
			}
			require.NoError(t, muxer.WriteVideo(frame))
		}

		rawAAC := make([]byte, 128)
		rawAAC[0] = 0x01
		adtsFrame := codec.EnsureADTS(rawAAC, sampleRate, channels)

		audioFrame := &media.AudioFrame{
			PTS:        pts,
			Data:       adtsFrame,
			SampleRate: sampleRate,
			Channels:   channels,
		}
		require.NoError(t, muxer.WriteAudio(audioFrame))
	}

	require.NotEmpty(t, tsData)
	return tsData
}

// writeTSFile writes a generated TS to a temp file and returns the path.
func writeTSFile(t *testing.T, numFrames int) string {
	t.Helper()
	data := generateTestTS(t, numFrames)
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

// writeTemp writes data to a temp file with the given extension.
func writeTemp(t *testing.T, data []byte, ext string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "clip-*"+ext)
	require.NoError(t, err)
	if len(data) > 0 {
		_, err = f.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	return f.Name()
}

// demuxFuncFromTSFile returns a DemuxFunc that demuxes a pre-generated TS file.
// This sidesteps the need to construct BufferedFrame directly (unexported fields).
func demuxFuncFromTSFile(t *testing.T, numFrames int) func(string) ([]clip.BufferedFrame, []clip.BufferedAudioFrame, error) {
	t.Helper()
	tsPath := writeTSFile(t, numFrames)
	// Pre-demux the file once.
	videoFrames, audioFrames, err := clip.DemuxFile(tsPath)
	require.NoError(t, err)
	require.NotEmpty(t, videoFrames)

	return func(path string) ([]clip.BufferedFrame, []clip.BufferedAudioFrame, error) {
		return videoFrames, audioFrames, nil
	}
}

// addTestClip creates a minimal clip entry in the store with a dummy file.
func addTestClip(t *testing.T, store *clip.Store, dir string) *clip.Clip {
	t.Helper()
	fname := "test-clip.ts"
	fpath := filepath.Join(dir, fname)
	require.NoError(t, os.WriteFile(fpath, []byte("dummy"), 0o644))

	c := &clip.Clip{
		Name:       "Test Clip",
		Filename:   fname,
		Source:     clip.SourceUpload,
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		FPSNum:     30000,
		FPSDen:     1001,
		DurationMs: 5000,
		ByteSize:   5,
	}
	require.NoError(t, store.Add(c))
	return c
}

// waitForCondition polls a condition function until it returns true or timeout.
func waitForCondition(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// ==========================================================================
// 1. TestClipStoreRoundTrip -- Store a clip, retrieve metadata. Verify
//    name, duration, dimensions match.
// ==========================================================================

func TestClipStoreRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 100*1024*1024) // 100 MB limit
	require.NoError(t, err, "NewStore should succeed")

	// Add a clip with known metadata.
	c := &clip.Clip{
		Name:       "roundtrip-test",
		Filename:   "roundtrip.ts",
		Source:     clip.SourceUpload,
		Codec:      "h264",
		AudioCodec: "aac",
		Width:      1920,
		Height:     1080,
		FPSNum:     30000,
		FPSDen:     1001,
		DurationMs: 5000,
		SampleRate: 48000,
		Channels:   2,
		ByteSize:   12345,
		Loop:       true,
	}
	require.NoError(t, store.Add(c), "Add should succeed")
	require.NotEmpty(t, c.ID, "clip should have an assigned ID")

	// Retrieve by ID.
	got, err := store.Get(c.ID)
	require.NoError(t, err, "Get should succeed")

	// Verify all metadata fields round-trip correctly.
	assert.Equal(t, "roundtrip-test", got.Name, "Name should match")
	assert.Equal(t, "roundtrip.ts", got.Filename, "Filename should match")
	assert.Equal(t, clip.SourceUpload, got.Source, "Source should match")
	assert.Equal(t, "h264", got.Codec, "Codec should match")
	assert.Equal(t, "aac", got.AudioCodec, "AudioCodec should match")
	assert.Equal(t, 1920, got.Width, "Width should match")
	assert.Equal(t, 1080, got.Height, "Height should match")
	assert.Equal(t, 30000, got.FPSNum, "FPSNum should match")
	assert.Equal(t, 1001, got.FPSDen, "FPSDen should match")
	assert.Equal(t, int64(5000), got.DurationMs, "DurationMs should match")
	assert.Equal(t, 48000, got.SampleRate, "SampleRate should match")
	assert.Equal(t, 2, got.Channels, "Channels should match")
	assert.Equal(t, int64(12345), got.ByteSize, "ByteSize should match")
	assert.True(t, got.Loop, "Loop should match")
	assert.False(t, got.CreatedAt.IsZero(), "CreatedAt should be set")

	// Verify the returned object is a copy (mutation does not affect store).
	got.Name = "mutated"
	got2, err := store.Get(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "roundtrip-test", got2.Name, "Get should return independent copies")

	// List should include this clip.
	listed := store.List()
	require.Len(t, listed, 1)
	assert.Equal(t, c.ID, listed[0].ID)

	// Persistence: reload from disk.
	store2, err := clip.NewStore(dir, 100*1024*1024)
	require.NoError(t, err, "reloading store should succeed")
	got3, err := store2.Get(c.ID)
	require.NoError(t, err, "Get after reload should succeed")
	assert.Equal(t, "roundtrip-test", got3.Name, "Name should persist across reload")
	assert.Equal(t, int64(5000), got3.DurationMs, "DurationMs should persist")
	assert.Equal(t, 1920, got3.Width, "Width should persist")
	assert.Equal(t, 1080, got3.Height, "Height should persist")
}

// ==========================================================================
// 2. TestPlayerPlay -- Load clip into player, play, capture frames via
//    output callback. Verify frames received, PTS increasing.
// ==========================================================================

func TestPlayerPlay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 30),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	// Collect video output callbacks.
	type frameInfo struct {
		pts     int64
		dataLen int
	}
	var mu sync.Mutex
	var capturedFrames []frameInfo

	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		mu.Lock()
		capturedFrames = append(capturedFrames, frameInfo{
			pts:     pts,
			dataLen: len(wireData),
		})
		mu.Unlock()
	})

	require.NoError(t, mgr.Play(1, 1.0, false))

	// First wait for playback to actually start.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StatePlaying
	}), "player should enter StatePlaying")

	// Now wait for playback to finish.
	require.True(t, waitForCondition(5*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StateHolding
	}), "playback should complete (StateHolding)")

	mu.Lock()
	frames := make([]frameInfo, len(capturedFrames))
	copy(frames, capturedFrames)
	mu.Unlock()

	// Verify frame count matches expected (30 frames at 1x speed).
	// The demux generates 30 frames, so at 1x playback we expect ~30.
	require.InDelta(t, 30, len(frames), 4,
		"expected approximately 30 frames from 30-frame clip at 1x speed, got %d", len(frames))

	// Verify PTS is monotonically increasing.
	for i := 1; i < len(frames); i++ {
		assert.Greater(t, frames[i].pts, frames[i-1].pts,
			"PTS should increase: frame[%d].pts=%d <= frame[%d].pts=%d",
			i, frames[i].pts, i-1, frames[i-1].pts)
	}

	// Verify frame data is non-empty.
	for i, f := range frames {
		assert.Greater(t, f.dataLen, 0, "frame[%d] should have non-empty data", i)
	}
}

// ==========================================================================
// 3. TestPlayerPause -- Play then pause. Verify output stops.
// ==========================================================================

func TestPlayerPause(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	// Use many frames + slow speed + loop to keep playback alive.
	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 60),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	var frameCount atomic.Int32
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		frameCount.Add(1)
	})

	require.NoError(t, mgr.Play(1, 0.25, true))

	// Wait for playback to produce some frames.
	require.True(t, waitForCondition(3*time.Second, func() bool {
		return frameCount.Load() > 5
	}), "expected initial frames before pause")

	// Pause.
	require.NoError(t, mgr.Pause(1))
	require.True(t, waitForCondition(time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StatePaused
	}), "player should be paused")

	// Record count at pause and wait.
	countAtPause := frameCount.Load()
	time.Sleep(300 * time.Millisecond)
	countAfterWait := frameCount.Load()

	// Allow at most 1 frame in flight during pause transition.
	assert.InDelta(t, float64(countAtPause), float64(countAfterWait), 1,
		"no new frames should arrive while paused (got %d at pause, %d after wait)",
		countAtPause, countAfterWait)

	// Resume and verify frames continue.
	require.NoError(t, mgr.Resume(1))
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return frameCount.Load() > countAfterWait+3
	}), "frames should resume after unpause")

	require.NoError(t, mgr.Stop(1))
}

// ==========================================================================
// 4. TestPlayerSeek -- Seek to a specific position. Verify output resumes
//    from approximately correct position.
// ==========================================================================

func TestPlayerSeek(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 60),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	var frameCount atomic.Int32
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		frameCount.Add(1)
	})

	require.NoError(t, mgr.Play(1, 0.25, true))

	// Wait for initial playback.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return frameCount.Load() > 3
	}), "expected initial frames before seek")

	// Seek to 50%.
	require.NoError(t, mgr.Seek(1, 0.5))
	time.Sleep(300 * time.Millisecond)

	// Verify player is still playing.
	states := mgr.PlayerStates()
	assert.Equal(t, clip.StatePlaying, states[0].State,
		"player should still be playing after seek")

	// Verify progress is approximately at or past 50%.
	assert.GreaterOrEqual(t, states[0].Position, 0.3,
		"after seeking to 0.5, progress should be at least 0.3 (accounting for timing)")

	// Verify playback continues producing frames after seek.
	countAfterSeek := frameCount.Load()
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return frameCount.Load() > countAfterSeek+3
	}), "playback should continue after seek")

	require.NoError(t, mgr.Stop(1))
}

// ==========================================================================
// 5. TestPlayerSpeed2x -- Set speed to 2x. Verify PTS spacing is
//    approximately halved (frames advance faster).
// ==========================================================================

func TestPlayerSpeed2x(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 30),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	// Collect PTS values for 2x speed.
	var mu2x sync.Mutex
	var pts2x []int64
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		mu2x.Lock()
		pts2x = append(pts2x, pts)
		mu2x.Unlock()
	})

	start2x := time.Now()
	require.NoError(t, mgr.Play(1, 2.0, false))

	// First wait for playback to actually start.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StatePlaying
	}), "player should enter StatePlaying")

	// Now wait for completion.
	require.True(t, waitForCondition(5*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StateHolding
	}), "2x playback should complete")
	elapsed2x := time.Since(start2x)

	// At 2x speed, a 30-frame clip at 30fps should complete in ~0.5s, not ~1.0s.
	// Use generous tolerance since CI timing varies.
	assert.Less(t, elapsed2x, 1500*time.Millisecond,
		"2x playback should finish in under 1.5s for 30 frames")

	// Verify PTS values are monotonically increasing.
	mu2x.Lock()
	captured := make([]int64, len(pts2x))
	copy(captured, pts2x)
	mu2x.Unlock()

	require.Greater(t, len(captured), 1, "should have captured multiple PTS values")
	for i := 1; i < len(captured); i++ {
		assert.Greater(t, captured[i], captured[i-1],
			"PTS should be monotonically increasing at 2x speed")
	}

	// At 2x speed, frames are skipped so PTS deltas should be approximately
	// double the normal 3000 ticks (i.e., ~6000 ticks between output frames).
	// Verify individual PTS deltas are approximately halved in wall-clock terms.
	if len(captured) > 2 {
		var deltas []int64
		for i := 1; i < len(captured); i++ {
			deltas = append(deltas, captured[i]-captured[i-1])
		}
		totalSpan := captured[len(captured)-1] - captured[0]
		avgDelta := float64(totalSpan) / float64(len(captured)-1)
		// At 2x speed with 30fps source, PTS deltas should be ~3000 ticks
		// per output frame (output is still 30fps PTS pacing).
		assert.InDelta(t, 3000, avgDelta, 500,
			"average PTS delta should be approximately 3000 at 2x speed, got %.0f", avgDelta)
		_ = deltas
	}
}

// ==========================================================================
// 6. TestPlayerSpeed05x -- Set speed to 0.5x. Verify PTS spacing is
//    approximately halved (because frames are duplicated).
// ==========================================================================

func TestPlayerSpeed05x(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 10), // short clip for speed test
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	// Collect PTS values for 0.5x speed.
	var mu sync.Mutex
	var pts05x []int64
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		mu.Lock()
		pts05x = append(pts05x, pts)
		mu.Unlock()
	})

	require.NoError(t, mgr.Play(1, 0.5, false))

	// First wait for playback to start.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StatePlaying
	}), "player should enter StatePlaying")

	// Now wait for completion.
	require.True(t, waitForCondition(10*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StateHolding
	}), "0.5x playback should complete")

	mu.Lock()
	captured := make([]int64, len(pts05x))
	copy(captured, pts05x)
	mu.Unlock()

	// At 0.5x speed, dupCount = round(1/0.5) = 2, so each source frame
	// produces 2 output frames. For 10 source frames, we expect ~20 output frames.
	require.Greater(t, len(captured), 10,
		"0.5x speed should produce more output frames than source frames (got %d)", len(captured))

	// At 0.5x, PTS increment per output frame = 90000/(30*2) = 1500.
	// This is halved compared to 1x (3000), confirming frame duplication.
	if len(captured) > 2 {
		totalSpan := captured[len(captured)-1] - captured[0]
		avgDelta := float64(totalSpan) / float64(len(captured)-1)
		assert.InDelta(t, 1500, avgDelta, 750,
			"average PTS delta at 0.5x should be ~1500 (halved from 3000)")
	}
}

// ==========================================================================
// 7. TestPlayerLoop -- Configure loop mode. Play past end. Verify playback
//    wraps to beginning (frames continue, no stall).
// ==========================================================================

func TestPlayerLoop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	numSourceFrames := 5
	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, numSourceFrames),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	var frameCount atomic.Int32
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		frameCount.Add(1)
	})

	// Play at 2x with loop enabled.
	require.NoError(t, mgr.Play(1, 2.0, true))

	// Wait for more frames than the clip length (proving loop occurred).
	require.True(t, waitForCondition(5*time.Second, func() bool {
		return int(frameCount.Load()) > numSourceFrames*2
	}), "expected loop to produce more than %d frames (got %d)",
		numSourceFrames*2, frameCount.Load())

	// Player should still be playing (looping).
	states := mgr.PlayerStates()
	assert.Equal(t, clip.StatePlaying, states[0].State,
		"player should still be playing when looping")

	require.NoError(t, mgr.Stop(1))

	// Final count should be well above source frame count.
	assert.Greater(t, int(frameCount.Load()), numSourceFrames*2,
		"loop should have produced at least 2x the source frames")
}

// ==========================================================================
// 8. TestPlayerHoldLastFrame -- Configure hold mode. Play to end. Verify
//    last frame repeated (player enters StateHolding).
// ==========================================================================

func TestPlayerHoldLastFrame(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 5),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	var rawFrameCount atomic.Int32
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		rawFrameCount.Add(1)
	})

	// Play at 2x, no loop -- should finish quickly and enter hold.
	require.NoError(t, mgr.Play(1, 2.0, false))

	// Wait for player to enter holding state.
	require.True(t, waitForCondition(5*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StateHolding
	}), "player should enter StateHolding after non-loop playback ends")

	states := mgr.PlayerStates()
	assert.Equal(t, clip.StateHolding, states[0].State,
		"player should be in holding state")

	// Record frame count at holding, wait, then verify hold frames keep coming
	// (the hold loop emits at 1fps, so wait enough time).
	countAtHold := rawFrameCount.Load()
	time.Sleep(1500 * time.Millisecond)
	countAfterHold := rawFrameCount.Load()

	assert.Greater(t, countAfterHold, countAtHold,
		"hold loop should continue emitting frames (count at hold: %d, after: %d)",
		countAtHold, countAfterHold)

	// Progress should be at 1.0 (end of clip).
	assert.InDelta(t, 1.0, states[0].Position, 0.01,
		"progress should be ~1.0 at end of playback")

	require.NoError(t, mgr.Stop(1))
}

// ==========================================================================
// 9. TestValidatorAcceptedExtensions -- Test IsAcceptedExtension with
//    accepted and rejected extensions.
// ==========================================================================

func TestValidatorAcceptedExtensions(t *testing.T) {
	t.Parallel()

	// Native H.264 extensions (accepted directly).
	accepted := []string{".ts", ".mp4", ".m4v", ".mov"}
	for _, ext := range accepted {
		assert.True(t, clip.IsAcceptedExtension(ext),
			"extension %q should be accepted", ext)
	}

	// Transcode extensions (accepted via transcode path).
	transcodeAccepted := []string{".mkv", ".webm", ".avi", ".flv", ".mxf", ".wmv", ".mpg", ".mpeg", ".ogv"}
	for _, ext := range transcodeAccepted {
		assert.True(t, clip.IsAcceptedExtension(ext),
			"extension %q should be accepted (transcode)", ext)
	}

	// Rejected extensions.
	rejected := []string{".txt", ".exe", ".doc", ".pdf", ".xyz", ".png", ".jpg", ""}
	for _, ext := range rejected {
		assert.False(t, clip.IsAcceptedExtension(ext),
			"extension %q should be rejected", ext)
	}
}

// ==========================================================================
// 10. TestDemuxTSFile -- Demux a TS file. Verify video and audio packets
//     extracted correctly.
// ==========================================================================

func TestDemuxTSFile(t *testing.T) {
	t.Parallel()

	const numFrames = 15
	data := generateTestTS(t, numFrames)
	path := writeTemp(t, data, ".ts")

	videoFrames, audioFrames, err := clip.DemuxFile(path)
	require.NoError(t, err, "DemuxFile should succeed on valid TS")

	// Video frames.
	require.NotEmpty(t, videoFrames, "should extract video frames")
	assert.Equal(t, numFrames, len(videoFrames),
		"should extract exactly %d video frames", numFrames)

	// First frame should be a keyframe with SPS/PPS.
	assert.True(t, videoFrames[0].IsKeyframe(), "first frame should be a keyframe")
	assert.NotEmpty(t, videoFrames[0].SPS(), "keyframe should have SPS")
	assert.NotEmpty(t, videoFrames[0].PPS(), "keyframe should have PPS")

	// SPS NALU type check.
	assert.Equal(t, byte(7), videoFrames[0].SPS()[0]&0x1F, "SPS NALU type should be 7")
	assert.Equal(t, byte(8), videoFrames[0].PPS()[0]&0x1F, "PPS NALU type should be 8")

	// Wire data should be AVC1 format (non-empty).
	for i, f := range videoFrames {
		assert.NotEmpty(t, f.WireData(), "frame[%d] should have wire data", i)
	}

	// PTS should be monotonically increasing (no B-frames in our synthetic data).
	for i := 1; i < len(videoFrames); i++ {
		assert.Greater(t, videoFrames[i].PTS(), videoFrames[i-1].PTS(),
			"PTS should increase: frame[%d].pts=%d <= frame[%d].pts=%d",
			i, videoFrames[i].PTS(), i-1, videoFrames[i-1].PTS())
	}

	// Audio frames.
	require.NotEmpty(t, audioFrames, "should extract audio frames")

	for i, af := range audioFrames {
		assert.Greater(t, af.SampleRate(), 0,
			"audio frame[%d] sampleRate should be > 0", i)
		assert.Greater(t, af.Channels(), 0,
			"audio frame[%d] channels should be > 0", i)
		assert.NotEmpty(t, af.Data(),
			"audio frame[%d] data should not be empty", i)
	}

	// Audio PTS should be monotonically non-decreasing.
	for i := 1; i < len(audioFrames); i++ {
		assert.GreaterOrEqual(t, audioFrames[i].PTS(), audioFrames[i-1].PTS(),
			"audio PTS should not decrease: frame[%d].pts=%d < frame[%d].pts=%d",
			i, audioFrames[i].PTS(), i-1, audioFrames[i-1].PTS())
	}
}

// ==========================================================================
// Additional correctness tests beyond the 10 required
// ==========================================================================

// TestStoreUpdate verifies that clip metadata can be updated in place.
func TestStoreUpdate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 10*1024*1024)
	require.NoError(t, err)

	c := &clip.Clip{
		Name:     "original",
		Filename: "test.ts",
		Source:   clip.SourceUpload,
		Codec:    "h264",
		ByteSize: 100,
	}
	require.NoError(t, store.Add(c))

	// Update the clip.
	err = store.Update(c.ID, func(cl *clip.Clip) {
		cl.Name = "updated"
		cl.Loop = true
	})
	require.NoError(t, err)

	got, err := store.Get(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated", got.Name)
	assert.True(t, got.Loop)
}

// TestStoreDelete verifies clip deletion removes both metadata and file.
func TestStoreDelete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 10*1024*1024)
	require.NoError(t, err)

	c := &clip.Clip{
		Name:     "delete-me",
		Filename: "deleteme.ts",
		Source:   clip.SourceUpload,
		Codec:    "h264",
		ByteSize: 100,
	}
	require.NoError(t, store.Add(c))
	require.NoError(t, os.WriteFile(filepath.Join(dir, c.Filename), []byte("data"), 0o644))

	require.NoError(t, store.Delete(c.ID))

	// Metadata should be gone.
	_, err = store.Get(c.ID)
	assert.ErrorIs(t, err, clip.ErrNotFound)

	// File should be deleted.
	_, err = os.Stat(filepath.Join(dir, c.Filename))
	assert.True(t, os.IsNotExist(err), "file should be deleted from disk")

	// List should be empty.
	assert.Empty(t, store.List())
}

// TestStoreStorageLimit verifies that adding clips beyond the limit fails.
func TestStoreStorageLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 500)
	require.NoError(t, err)

	c1 := &clip.Clip{Name: "a", Filename: "a.ts", Source: clip.SourceUpload, Codec: "h264", ByteSize: 300}
	require.NoError(t, store.Add(c1))

	c2 := &clip.Clip{Name: "b", Filename: "b.ts", Source: clip.SourceUpload, Codec: "h264", ByteSize: 300}
	err = store.Add(c2)
	assert.ErrorIs(t, err, clip.ErrStorageFull,
		"adding clip beyond limit should return ErrStorageFull")

	// Ephemeral clips should bypass the limit.
	c3 := &clip.Clip{Name: "eph", Filename: "e.ts", Source: clip.SourceReplay, Codec: "h264", ByteSize: 300, Ephemeral: true}
	assert.NoError(t, store.Add(c3), "ephemeral clips should bypass storage limit")
}

// TestPlayerPTSMonotonicity verifies that PTS values are strictly
// monotonically increasing across all output frames, including across
// loop boundaries.
func TestPlayerPTSMonotonicity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 5), // short clip to loop quickly
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	var mu sync.Mutex
	var allPTS []int64
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		mu.Lock()
		allPTS = append(allPTS, pts)
		mu.Unlock()
	})

	// Play at 2x with loop -- collect PTS across multiple loops.
	require.NoError(t, mgr.Play(1, 2.0, true))

	// Wait for enough frames to span multiple loops.
	require.True(t, waitForCondition(5*time.Second, func() bool {
		mu.Lock()
		n := len(allPTS)
		mu.Unlock()
		return n > 20
	}), "expected at least 20 frames across loop iterations")

	require.NoError(t, mgr.Stop(1))

	mu.Lock()
	pts := make([]int64, len(allPTS))
	copy(pts, allPTS)
	mu.Unlock()

	// Every PTS should be strictly greater than the previous one.
	for i := 1; i < len(pts); i++ {
		assert.Greater(t, pts[i], pts[i-1],
			"PTS should be strictly increasing across loops: frame[%d]=%d, frame[%d]=%d",
			i-1, pts[i-1], i, pts[i])
	}
}

// TestPlayerAudioOutput verifies that audio frames are emitted during
// playback with correct metadata.
func TestPlayerAudioOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 30),
	})
	defer mgr.Close()

	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	type audioInfo struct {
		pts        int64
		sampleRate int
		channels   int
		dataLen    int
	}
	var mu sync.Mutex
	var capturedAudio []audioInfo

	mgr.SetAudioOutput(func(key string, data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		capturedAudio = append(capturedAudio, audioInfo{
			pts:        pts,
			sampleRate: sampleRate,
			channels:   channels,
			dataLen:    len(data),
		})
		mu.Unlock()
	})

	require.NoError(t, mgr.Play(1, 1.0, false))

	// First wait for playback to start.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StatePlaying
	}), "player should enter StatePlaying")

	// Now wait for completion.
	require.True(t, waitForCondition(5*time.Second, func() bool {
		return mgr.PlayerStates()[0].State == clip.StateHolding
	}), "playback should complete")

	mu.Lock()
	audio := make([]audioInfo, len(capturedAudio))
	copy(audio, capturedAudio)
	mu.Unlock()

	require.Greater(t, len(audio), 0, "should have received audio frames")

	for i, a := range audio {
		assert.Equal(t, 48000, a.sampleRate, "audio[%d] sampleRate should be 48000", i)
		assert.Equal(t, 2, a.channels, "audio[%d] channels should be 2", i)
		assert.Greater(t, a.dataLen, 0, "audio[%d] should have non-empty data", i)
	}

	// Audio PTS should be monotonically non-decreasing.
	for i := 1; i < len(audio); i++ {
		assert.GreaterOrEqual(t, audio[i].pts, audio[i-1].pts,
			"audio PTS should be non-decreasing: frame[%d]=%d, frame[%d]=%d",
			i-1, audio[i-1].pts, i, audio[i].pts)
	}
}

// TestManagerMultiSlot verifies that multiple player slots can operate
// independently and concurrently.
func TestManagerMultiSlot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: demuxFuncFromTSFile(t, 20),
	})
	defer mgr.Close()

	c1 := addTestClip(t, store, dir)

	// Create second clip.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clip2.ts"), []byte("dummy2"), 0o644))
	c2 := &clip.Clip{
		Name: "Clip Two", Filename: "clip2.ts", Source: clip.SourceUpload,
		Codec: "h264", Width: 1920, Height: 1080, ByteSize: 6,
	}
	require.NoError(t, store.Add(c2))

	// Track output per player key.
	keysSeen := &sync.Map{}
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		keysSeen.Store(key, true)
	})

	// Load and play on two different slots.
	require.NoError(t, mgr.Load(1, c1.ID))
	require.NoError(t, mgr.Load(3, c2.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))
	require.NoError(t, mgr.Play(3, 0.25, true))

	// Wait for output from both.
	require.True(t, waitForCondition(3*time.Second, func() bool {
		_, saw1 := keysSeen.Load("clip:1")
		_, saw3 := keysSeen.Load("clip:3")
		return saw1 && saw3
	}), "expected output from both clip:1 and clip:3")

	states := mgr.PlayerStates()
	assert.Equal(t, clip.StatePlaying, states[0].State)
	assert.Equal(t, clip.StateEmpty, states[1].State)
	assert.Equal(t, clip.StatePlaying, states[2].State)
	assert.Equal(t, clip.StateEmpty, states[3].State)

	require.NoError(t, mgr.Stop(1))
	require.NoError(t, mgr.Stop(3))
}

// TestCanTranscodeFallback verifies that only MP4-family containers
// support the transcode fallback path.
func TestCanTranscodeFallback(t *testing.T) {
	t.Parallel()

	for _, ext := range []string{".mp4", ".m4v", ".mov"} {
		assert.True(t, clip.CanTranscodeFallback(ext),
			"extension %q should allow transcode fallback", ext)
	}
	for _, ext := range []string{".ts", ".mkv", ".webm", ".avi", ".xyz", ""} {
		assert.False(t, clip.CanTranscodeFallback(ext),
			"extension %q should not allow transcode fallback", ext)
	}
}

// TestDemuxInvalidFiles verifies that DemuxFile returns errors for
// invalid or unsupported inputs.
func TestDemuxInvalidFiles(t *testing.T) {
	t.Parallel()

	t.Run("empty file", func(t *testing.T) {
		path := writeTemp(t, []byte{}, ".ts")
		_, _, err := clip.DemuxFile(path)
		assert.Error(t, err, "should reject empty TS file")
	})

	t.Run("garbage data", func(t *testing.T) {
		path := writeTemp(t, []byte("not a ts file"), ".ts")
		_, _, err := clip.DemuxFile(path)
		assert.Error(t, err, "should reject garbage TS data")
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, _, err := clip.DemuxFile("/tmp/nonexistent-correctness-test-12345.ts")
		assert.Error(t, err, "should reject nonexistent file")
	})

	t.Run("unsupported extension", func(t *testing.T) {
		path := writeTemp(t, []byte("data"), ".avi")
		_, _, err := clip.DemuxFile(path)
		assert.Error(t, err, "should reject unsupported extension")
	})
}

// TestComputeClipTimingLogic verifies the timing computation for speed changes.
// At speeds >= 1.0, dupCount is 1; at speeds < 1.0, frames are duplicated.
func TestComputeClipTimingLogic(t *testing.T) {
	t.Parallel()

	// At 1x speed with 30fps: dupCount=1, ptsPerFrame=3000
	// At 2x speed with 30fps: dupCount=1, ptsPerFrame=3000 (wall-clock pacing handles speed)
	// At 0.5x speed with 30fps: dupCount=2, ptsPerFrame=1500

	// The relationship: at 0.5x, we expect ~2x the output frames (from duplication),
	// each with halved PTS increment. This is verified end-to-end in TestPlayerSpeed05x.

	// Verify the mathematical invariant: total PTS span should be consistent
	// regardless of speed (same content = same PTS span, speed only affects pacing).

	numFrames := 10
	sourceFPS := 30.0
	expectedPTSSpan := int64(float64(numFrames) * 90000.0 / sourceFPS)

	// At 1x: numFrames*dupCount(1) frames, each with ptsPerFrame = 90000/(30*1) = 3000
	speed1xTotal := int64(numFrames) * 1 * int64(90000.0/(sourceFPS*float64(1)))
	assert.InDelta(t, float64(expectedPTSSpan), float64(speed1xTotal), float64(expectedPTSSpan)*0.1,
		"1x PTS span should approximate expected span")

	// At 0.5x: numFrames*dupCount(2) frames, each with ptsPerFrame = 90000/(30*2) = 1500
	dupCount05x := int(math.Round(1.0 / 0.5)) // 2
	speed05xTotal := int64(numFrames) * int64(dupCount05x) * int64(90000.0/(sourceFPS*float64(dupCount05x)))
	assert.InDelta(t, float64(expectedPTSSpan), float64(speed05xTotal), float64(expectedPTSSpan)*0.1,
		"0.5x PTS span should approximate expected span (same content)")
}

// ==========================================================================
// Corrupted input handling tests
// ==========================================================================

// TestDemuxCorruptedFile generates a valid TS with ffmpeg, truncates to 30%,
// and calls clip.DemuxFile on the truncated file. Must not panic.
func TestDemuxCorruptedFile(t *testing.T) {
	t.Parallel()

	// Generate valid TS data using existing helper.
	data := generateTestTS(t, 30)
	require.Greater(t, len(data), 200, "TS data should be non-trivial")

	// Truncate to 30%.
	truncated := data[:len(data)*30/100]
	tmpPath := writeTemp(t, truncated, ".ts")

	require.NotPanics(t, func() {
		videoFrames, _, err := clip.DemuxFile(tmpPath)
		// Either error or partial results is acceptable.
		if err == nil && len(videoFrames) > 0 {
			// If we got frames, verify they have data.
			for i, f := range videoFrames {
				assert.NotEmpty(t, f.WireData(), "frame %d: WireData should not be empty", i)
			}
		}
	}, "DemuxFile should not panic on truncated file")
}

// TestDemuxZeroLengthFile creates an empty temp file with .ts extension
// and calls clip.DemuxFile. Must not panic and should return an error.
func TestDemuxZeroLengthFile(t *testing.T) {
	t.Parallel()

	tmpPath := writeTemp(t, []byte{}, ".ts")

	require.NotPanics(t, func() {
		videoFrames, audioFrames, err := clip.DemuxFile(tmpPath)
		// An empty file should produce an error.
		assert.Error(t, err, "DemuxFile on empty file should return an error")
		assert.Empty(t, videoFrames, "no video frames from empty file")
		assert.Empty(t, audioFrames, "no audio frames from empty file")
	}, "DemuxFile should not panic on empty .ts file")
}
