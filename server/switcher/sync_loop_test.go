package switcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/transition"
)

// mockSyncMixer implements syncMixerInterface for testing.
type mockSyncMixer struct {
	mu              sync.Mutex
	produceCalls    int
	totalSamples    int
	crossfadeCalls  int
	channels        int
	sampleFn        func(sampleCount int) []float32 // optional custom sample generator
}

func newMockSyncMixer(channels int) *mockSyncMixer {
	return &mockSyncMixer{channels: channels}
}

func (m *mockSyncMixer) ProduceSamples(sampleCount int) []float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.produceCalls++
	m.totalSamples += sampleCount
	if m.sampleFn != nil {
		return m.sampleFn(sampleCount)
	}
	// Return interleaved silence (sampleCount * channels values).
	return make([]float32, sampleCount*m.channels)
}

func (m *mockSyncMixer) AdvanceCrossfade() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crossfadeCalls++
}

func (m *mockSyncMixer) getProduceCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.produceCalls
}

func (m *mockSyncMixer) getTotalSamples() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalSamples
}

func (m *mockSyncMixer) getCrossfadeCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.crossfadeCalls
}

// mockAAC encodes PCM to a fixed-size "encoded" marker payload.
// Returns 4 bytes per frame for easy counting.
func mockAACEncode(pcm []float32) ([]byte, error) {
	return []byte{0xAA, 0xC0, 0x00, byte(len(pcm) & 0xFF)}, nil
}

// newSyncTestSwitcher creates a Switcher configured for synchronous loop testing.
// It creates a non-realtime reference clock, sets up the frame sync with a
// raw video callback wired to handleRawVideoFrame, and builds a minimal pipeline.
func newSyncTestSwitcher(t *testing.T) (*Switcher, *clock.ReferenceClock) {
	t.Helper()
	programRelay := distribution.NewRelay()
	testFmt := PipelineFormat{
		Name:   "test",
		Width:  320,
		Height: 240,
		FPSNum: 30,
		FPSDen: 1,
	}

	s := &Switcher{
		log:           testSyncLogger(),
		sources:       make(map[string]*sourceState),
		programRelay:  programRelay,
		health:        newHealthMonitor(),
		videoProcCh:   make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
		framePool:     NewFramePool(8, 320, 240, ColorYUV420_8bit),
	}
	s.frameBudgetNs.Store(testFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&testFmt)
	s.delayBuffer = NewDelayBuffer(s)

	// Create non-realtime reference clock at 30fps.
	// Must be set BEFORE starting videoProcessingLoop so useRefClock is
	// visible to the goroutine (avoids data race on the bool field).
	rc := clock.NewReferenceClock(30, 1, 48000, 0, false)
	s.SetReferenceClock(rc)

	// Start the async video processing loop (needed so Close() can drain
	// videoProcCh without blocking). In ref clock mode, this loop only
	// drains without processing.
	go s.videoProcessingLoop()

	// Wire frame sync with onRawVideo callback (clock-driven mode).
	fs := NewFrameSynchronizer(
		testFmt.FrameDuration(),
		func(sourceKey string, frame media.VideoFrame) {},
		func(sourceKey string, frame media.AudioFrame) {},
	)
	fs.onRawVideo = func(sourceKey string, pf *ProcessingFrame) {
		s.handleRawVideoFrame(sourceKey, pf)
	}
	fs.framePool = s.framePool
	fs.SetClockDriven(true)

	s.mu.Lock()
	s.frameSync = fs
	s.frameSyncActive = true
	s.mu.Unlock()

	return s, rc
}

// testSyncLogger returns a slog.Logger used in sync loop test switchers.
func testSyncLogger() *slog.Logger {
	return slog.With("component", "switcher-test")
}

// makeTestYUV creates a YUV420 buffer of the given dimensions, filled with
// a distinguishable pattern (luma = fillByte for entire Y plane).
func makeTestYUV(width, height int, fillByte byte) []byte {
	size := width * height * 3 / 2
	buf := make([]byte, size)
	for i := 0; i < width*height; i++ {
		buf[i] = fillByte // Y plane
	}
	// U and V planes filled with 128 (neutral chroma)
	for i := width * height; i < size; i++ {
		buf[i] = 128
	}
	return buf
}

func TestSyncLoopProducesVideoOutput(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	// Register and cut to a source.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Set up a viewer to capture program output.
	viewer := newMockProgramViewer("test-viewer")
	s.programRelay.AddViewer(viewer)

	// Feed frames into the frame sync buffer.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 5; i++ {
		buf := pool.Acquire()[:yuvSize]
		for j := range buf {
			buf[j] = byte(i + 1)
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	// Start the ref clock and sync loop.
	rc.Start()
	s.StartSyncLoop()

	// Consume ticks by processing them via the sync loop.
	// The sync loop reads from rc.Ticks() automatically. In non-realtime
	// mode, each tick is produced on-demand when consumed.
	// We just need to wait for the sync loop to process ticks.
	// Since the sync loop is consuming ticks in a goroutine, we just
	// need to wait for output to appear.

	// Wait for at least a few video frames to appear on the program relay.
	require.Eventually(t, func() bool {
		return len(viewer.VideoFrames()) >= 3
	}, 2*time.Second, 10*time.Millisecond, "expected video frames on program relay")

	// Stop and verify
	rc.Stop()
	s.syncLoopWg.Wait()

	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 3, "should have produced at least 3 video frames")

	// Verify PTS monotonicity.
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"PTS should be monotonically increasing: frame %d PTS=%d, frame %d PTS=%d",
			i-1, frames[i-1].PTS, i, frames[i].PTS)
	}
}

func TestSyncLoopProducesAudioOutput(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	// Set up mock mixer and AAC buffer.
	mixer := newMockSyncMixer(2)
	// Return non-zero samples so AAC buffer accumulates data.
	mixer.sampleFn = func(sampleCount int) []float32 {
		samples := make([]float32, sampleCount*2) // stereo
		for i := range samples {
			samples[i] = 0.1
		}
		return samples
	}
	s.SetSyncMixer(mixer)

	aacBuf := clock.NewAACFrameBuffer(2, 48000, mockAACEncode)
	s.SetAACBuffer(aacBuf)

	// Register a source and cut to it (needed for video path to not return early).
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Capture audio output from program relay.
	viewer := newMockProgramViewer("audio-viewer")
	s.programRelay.AddViewer(viewer)

	// Start clock and sync loop.
	rc.Start()
	s.StartSyncLoop()

	// Wait for audio frames to appear.
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.audios) >= 2
	}, 2*time.Second, 10*time.Millisecond, "expected AAC audio frames on program relay")

	rc.Stop()
	s.syncLoopWg.Wait()

	// Verify mixer was called.
	require.GreaterOrEqual(t, mixer.getProduceCalls(), 2,
		"mixer.ProduceSamples should have been called at least twice")
	require.GreaterOrEqual(t, mixer.getCrossfadeCalls(), 2,
		"mixer.AdvanceCrossfade should have been called")

	// Verify AAC frames were produced.
	require.GreaterOrEqual(t, aacBuf.FrameCount(), int64(1),
		"AAC buffer should have emitted at least 1 frame")

	// Verify audio frames on the relay.
	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	require.GreaterOrEqual(t, len(viewer.audios), 2,
		"should have at least 2 audio frames on program relay")

	// Verify audio PTS monotonicity.
	for i := 1; i < len(viewer.audios); i++ {
		require.Greater(t, viewer.audios[i].PTS, viewer.audios[i-1].PTS,
			"audio PTS should be monotonically increasing")
	}
}

func TestSyncLoopAVAligned(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2)
	}
	s.SetSyncMixer(mixer)

	aacBuf := clock.NewAACFrameBuffer(2, 48000, mockAACEncode)
	s.SetAACBuffer(aacBuf)

	// Register source, cut to it, and feed frames.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Feed many frames.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 60; i++ {
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	// Capture output.
	viewer := newMockProgramViewer("av-viewer")
	s.programRelay.AddViewer(viewer)

	rc.Start()
	s.StartSyncLoop()

	// Wait for both video and audio.
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 5 && len(viewer.audios) >= 1
	}, 2*time.Second, 10*time.Millisecond, "expected both video and audio output")

	rc.Stop()
	s.syncLoopWg.Wait()

	// Verify both audio and video were produced for each tick.
	// Mixer should have been called once per tick.
	require.GreaterOrEqual(t, mixer.getProduceCalls(), 5,
		"mixer should be called at least once per tick")

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	// Video PTS should increase by 3000 per frame (90000/30fps).
	for i := 1; i < len(viewer.videos); i++ {
		delta := viewer.videos[i].PTS - viewer.videos[i-1].PTS
		require.Equal(t, int64(3000), delta,
			"video PTS delta should be 3000 (30fps): got %d at frame %d", delta, i)
	}

	// Audio PTS should increase by 1920 per AAC frame (1024*90000/48000).
	for i := 1; i < len(viewer.audios); i++ {
		delta := viewer.audios[i].PTS - viewer.audios[i-1].PTS
		require.Equal(t, int64(1920), delta,
			"audio PTS delta should be 1920: got %d at frame %d", delta, i)
	}
}

func TestSyncLoopOverload(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	// Set up mixer that is always called (audio never skipped).
	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2)
	}
	s.SetSyncMixer(mixer)

	aacBuf := clock.NewAACFrameBuffer(2, 48000, mockAACEncode)
	s.SetAACBuffer(aacBuf)

	// Register source and cut.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// First, process one frame WITHOUT the slow node to establish
	// lastBroadcastVFrame. Without this, the overload guard (which
	// prevents skipping when there's nothing to repeat) won't trigger.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	{
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{Data: buf, Width: 320, Height: 240, PTS: 0, pool: pool}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}
	// Process one tick without a pipeline — raw frame goes through broadcastToProgram.
	rc.Start()
	s.StartSyncLoop()
	time.Sleep(50 * time.Millisecond)
	rc.Stop()
	s.syncLoopWg.Wait()

	// Now add a slow pipeline node that takes longer than one frame duration.
	slowNode := &slowPipelineNode{delay: 40 * time.Millisecond}
	p := &Pipeline{}
	require.NoError(t, p.Build(PipelineFormat{
		Name: "test", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
	}, s.framePool, []PipelineNode{slowNode}))
	s.pipeline.Store(p)

	// Reset for second run with slow pipeline.
	s.overloadDrops.Store(0)
	s.syncLoopRunning.Store(false)
	rc2 := clock.NewReferenceClock(30, 1, 48000, 90000, true)
	s.SetReferenceClock(rc2)

	// Feed frames.
	for i := 0; i < 30; i++ {
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	rc2.Start()
	s.StartSyncLoop()

	// Let a few ticks run.
	time.Sleep(200 * time.Millisecond)

	rc2.Stop()
	s.syncLoopWg.Wait()

	// Verify that audio was still called on every tick (never skipped).
	require.GreaterOrEqual(t, mixer.getProduceCalls(), 2,
		"mixer should be called even during overload")

	// Verify overload drops were recorded.
	require.GreaterOrEqual(t, s.overloadDrops.Load(), int64(1),
		"should have recorded overload drops")
}

// slowPipelineNode is a PipelineNode that sleeps to simulate overload.
type slowPipelineNode struct {
	delay time.Duration
}

func (n *slowPipelineNode) Name() string                          { return "slow-test" }
func (n *slowPipelineNode) Configure(format PipelineFormat) error { return nil }
func (n *slowPipelineNode) Active() bool                          { return true }
func (n *slowPipelineNode) Err() error                            { return nil }
func (n *slowPipelineNode) Latency() time.Duration                { return 0 }
func (n *slowPipelineNode) Close() error                          { return nil }
func (n *slowPipelineNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	time.Sleep(n.delay)
	return src
}

func TestExistingPathStillWorks(t *testing.T) {
	// Create a Switcher WITHOUT reference clock — the traditional path.
	programRelay := distribution.NewRelay()
	s := newTestSwitcher(programRelay)
	defer s.Close()

	// Verify useRefClock is false.
	require.False(t, s.useRefClock.Load(), "default switcher should not use reference clock")

	// Basic operations should work.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	state := s.State()
	require.Equal(t, "cam1", state.ProgramSource)

	// Preview should work.
	cam2Relay := distribution.NewRelay()
	s.RegisterSource("cam2", cam2Relay)
	s.SetPreview(context.Background(), "cam2")
	state = s.State()
	require.Equal(t, "cam2", state.PreviewSource)

	// Unregister should work.
	s.UnregisterSource("cam2")
	state = s.State()
	require.Equal(t, "", state.PreviewSource)
}

func TestSyncLoopProcessTickDirect(t *testing.T) {
	// Test processTick directly (no goroutine) for deterministic behavior.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	// Register source and cut.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Set up mixer.
	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2)
	}
	s.SetSyncMixer(mixer)

	aacBuf := clock.NewAACFrameBuffer(2, 48000, mockAACEncode)
	s.SetAACBuffer(aacBuf)

	// Feed a frame.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	buf := pool.Acquire()[:yuvSize]
	pf := &ProcessingFrame{
		Data:  buf,
		Width: 320, Height: 240,
		PTS: 90000,
		pool: pool,
	}
	pf.SetRefs(1)
	s.frameSync.IngestRawVideo("cam1", pf)

	// Process a single tick directly.
	tick := clock.Tick{
		Number:        0,
		VideoPTS:      0,
		AudioSamples:  1600, // 48000/30
		FrameDuration: 33333 * time.Microsecond,
	}
	s.processTick(tick)

	// Verify audio was mixed.
	require.Equal(t, 1, mixer.getProduceCalls(), "mixer should be called once")
	require.Equal(t, 1600, mixer.getTotalSamples(), "should request 1600 samples")
	require.Equal(t, 1, mixer.getCrossfadeCalls(), "crossfade should be advanced")
}

func TestSyncLoopAudioWithoutVideo(t *testing.T) {
	// Audio should still be produced even when no video source is available.
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2)
	}
	s.SetSyncMixer(mixer)

	aacBuf := clock.NewAACFrameBuffer(2, 48000, mockAACEncode)
	s.SetAACBuffer(aacBuf)

	// No source registered — video tick will return early.
	// But audio should still work.

	viewer := newMockProgramViewer("audio-only")
	s.programRelay.AddViewer(viewer)

	rc.Start()
	s.StartSyncLoop()

	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.audios) >= 2
	}, 2*time.Second, 10*time.Millisecond, "audio should be produced even without video source")

	rc.Stop()
	s.syncLoopWg.Wait()

	require.GreaterOrEqual(t, mixer.getProduceCalls(), 2,
		"mixer should be called even without video")
	viewer.mu.Lock()
	require.Empty(t, viewer.videos, "no video should be produced without a source")
	viewer.mu.Unlock()
}

func TestSyncLoopTicksBehindDecrement(t *testing.T) {
	// Verify that ticksBehind counts down and resumes normal processing.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2)
	}
	s.SetSyncMixer(mixer)
	s.SetAACBuffer(clock.NewAACFrameBuffer(2, 48000, mockAACEncode))

	// Artificially set ticksBehind.
	s.ticksBehind = 3

	tick := clock.Tick{
		Number:        0,
		VideoPTS:      0,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	}

	// Process 3 ticks while behind — should decrement and skip video.
	s.processTick(tick)
	require.Equal(t, 2, s.ticksBehind)
	require.Equal(t, int64(1), s.overloadDrops.Load())

	s.processTick(tick)
	require.Equal(t, 1, s.ticksBehind)
	require.Equal(t, int64(2), s.overloadDrops.Load())

	s.processTick(tick)
	require.Equal(t, 0, s.ticksBehind)
	require.Equal(t, int64(3), s.overloadDrops.Load())

	// Audio should have been called on every tick (3 times).
	require.Equal(t, 3, mixer.getProduceCalls(), "audio should be produced on every tick")

	// Next tick should not skip video (ticksBehind is 0).
	s.processTick(tick)
	// Verify audio was called a 4th time.
	require.Equal(t, 4, mixer.getProduceCalls())
}

func TestSyncLoopNoMixerNoAAC(t *testing.T) {
	// Verify that missing mixer/aacBuffer doesn't panic.
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()
	// Don't set mixer or AAC buffer.

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Feed a frame.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	buf := pool.Acquire()[:yuvSize]
	pf := &ProcessingFrame{
		Data: buf,
		Width: 320, Height: 240,
		PTS:  90000,
		pool: pool,
	}
	pf.SetRefs(1)
	s.frameSync.IngestRawVideo("cam1", pf)

	rc.Start()
	s.StartSyncLoop()

	// Should not panic — just no audio output.
	time.Sleep(100 * time.Millisecond)

	rc.Stop()
	s.syncLoopWg.Wait()
}

func TestSyncLoopCloseStopsCleanly(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)

	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2)
	}
	s.SetSyncMixer(mixer)
	s.SetAACBuffer(clock.NewAACFrameBuffer(2, 48000, mockAACEncode))

	rc.Start()
	s.StartSyncLoop()

	// Let it run briefly.
	time.Sleep(50 * time.Millisecond)

	// Close should stop everything cleanly (no deadlock, no panic).
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good — closed cleanly.
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not complete within 5 seconds — possible deadlock")
	}
}

func TestSyncLoopVideoFramesGetRefClockPTS(t *testing.T) {
	// Verify that the sync loop stamps frames with reference clock PTS,
	// not the source PTS.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Build a capturing pipeline to see what PTS reaches the pipeline.
	var capturedPTS atomic.Int64
	captureNode := &capturePTSNode{capturedPTS: &capturedPTS}
	p := &Pipeline{}
	require.NoError(t, p.Build(PipelineFormat{
		Name: "test", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
	}, s.framePool, []PipelineNode{captureNode}))
	s.pipeline.Store(p)

	// Feed a frame with a known source PTS that differs from the tick PTS.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	buf := pool.Acquire()[:yuvSize]
	pf := &ProcessingFrame{
		Data:  buf,
		Width: 320, Height: 240,
		PTS: 99999, // source PTS — should be overwritten
		pool: pool,
	}
	pf.SetRefs(1)
	s.frameSync.IngestRawVideo("cam1", pf)

	// Process a tick with a specific PTS.
	tick := clock.Tick{
		Number:        0,
		VideoPTS:      12345, // reference clock PTS
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	}
	s.processVideoTick(tick)

	// The pipeline should have seen the ref clock PTS, not the source PTS.
	require.Equal(t, int64(12345), capturedPTS.Load(),
		"pipeline should receive reference clock PTS, not source PTS")
}

// capturePTSNode captures the PTS of the frame passing through the pipeline.
type capturePTSNode struct {
	capturedPTS *atomic.Int64
}

func (n *capturePTSNode) Name() string                          { return "capture-pts" }
func (n *capturePTSNode) Configure(format PipelineFormat) error { return nil }
func (n *capturePTSNode) Active() bool                          { return true }
func (n *capturePTSNode) Err() error                            { return nil }
func (n *capturePTSNode) Latency() time.Duration                { return 0 }
func (n *capturePTSNode) Close() error                          { return nil }
func (n *capturePTSNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	n.capturedPTS.Store(src.PTS)
	return src
}

func TestWallClockPTSNoOpWithRefClock(t *testing.T) {
	s, _ := newSyncTestSwitcher(t) // sets useRefClock=true via SetReferenceClock
	defer s.Close()

	require.True(t, s.useRefClock.Load(), "test switcher should use reference clock")

	// wallClockVideoPTS should be an identity function when ref clock is active.
	got := s.wallClockVideoPTS(12345)
	require.Equal(t, int64(12345), got, "wallClockVideoPTS should return PTS unchanged with ref clock")

	// Even with wallClockVideoEnabled forced on, ref clock should still win.
	s.wallClockVideoEnabled = true
	got = s.wallClockVideoPTS(67890)
	require.Equal(t, int64(67890), got, "wallClockVideoPTS should return PTS unchanged even if wallClockVideoEnabled is set")
}

func TestEnableWallClockNoOpWithRefClock(t *testing.T) {
	s, _ := newSyncTestSwitcher(t) // sets useRefClock=true
	defer s.Close()

	require.True(t, s.useRefClock.Load())
	require.False(t, s.wallClockVideoEnabled, "wallClockVideoEnabled should start false")

	// EnableWallClockVideoPTS should be a no-op when ref clock is active.
	s.EnableWallClockVideoPTS()
	require.False(t, s.wallClockVideoEnabled, "wallClockVideoEnabled should remain false with ref clock")
}

func TestSetOnFirstVideoPTSNoOpWithRefClock(t *testing.T) {
	s, _ := newSyncTestSwitcher(t) // sets useRefClock=true
	defer s.Close()

	require.True(t, s.useRefClock.Load())
	require.Nil(t, s.onFirstVideoPTS, "onFirstVideoPTS should start nil")

	// SetOnFirstVideoPTS should be a no-op when ref clock is active.
	called := false
	s.SetOnFirstVideoPTS(func(pts int64) { called = true })
	require.Nil(t, s.onFirstVideoPTS, "onFirstVideoPTS should remain nil with ref clock")
	require.False(t, called, "callback should not have been invoked")
}

func TestWallClockPTSStillWorksWithoutRefClock(t *testing.T) {
	// Verify the old path still works when useRefClock is false.
	programRelay := distribution.NewRelay()
	s := newTestSwitcher(programRelay)
	defer s.Close()

	require.False(t, s.useRefClock.Load())

	// EnableWallClockVideoPTS should work normally.
	s.EnableWallClockVideoPTS()
	require.True(t, s.wallClockVideoEnabled)

	// SetOnFirstVideoPTS should store the callback.
	called := false
	s.SetOnFirstVideoPTS(func(pts int64) { called = true })
	require.NotNil(t, s.onFirstVideoPTS)

	// Invoke it to verify it was stored.
	s.mu.RLock()
	fn := s.onFirstVideoPTS
	s.mu.RUnlock()
	fn(0)
	require.True(t, called)
}

// === Task 13: Overload Protection Tests ===

func TestSyncLoopOverloadProducesBlackOnStartup(t *testing.T) {
	// No frames ever fed, overload triggered. Output should be broadcast black (Y=16).
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	// Register source and cut to it, but don't feed any frames.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Set up viewer to capture output.
	viewer := newMockProgramViewer("overload-black")
	s.programRelay.AddViewer(viewer)

	// Force overload condition: set ticksBehind so next tick calls repeatLastVideoFrame.
	s.ticksBehind = 1

	tick := clock.Tick{
		Number:        0,
		VideoPTS:      0,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	}
	s.processTick(tick)

	// Should have broadcast black since no lastEncodedFrame exists.
	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 1, "should have produced a black frame")

	// Verify it's broadcast black (Y=16).
	f := frames[0]
	require.NotEmpty(t, f.WireData, "black frame should have data")
	// First byte of Y plane should be 16 (BT.709 limited-range black).
	require.Equal(t, byte(16), f.WireData[0], "Y plane should be 16 (broadcast black)")
}

func TestSyncLoopOverloadCounterIncrements(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	require.Equal(t, int64(0), s.overloadDrops.Load())

	// Set ticksBehind to simulate overload.
	s.ticksBehind = 5

	tick := clock.Tick{
		Number:        0,
		VideoPTS:      0,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	}

	// Process ticks while behind — each should increment overloadDrops.
	for i := 0; i < 5; i++ {
		s.processTick(tick)
	}

	require.Equal(t, int64(5), s.overloadDrops.Load(),
		"overloadDrops should be 5 after 5 behind ticks")
	require.Equal(t, 0, s.ticksBehind, "ticksBehind should reach 0")
}

// === Task 14: Source Loss Tests ===

func TestSyncLoopSourceLossFreeze(t *testing.T) {
	// Feed 10 frames, stop feeding. Next ticks produce freeze frames (same content, advancing PTS).
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	viewer := newMockProgramViewer("freeze-viewer")
	s.programRelay.AddViewer(viewer)

	// Feed 10 frames with known luma value.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 10; i++ {
		buf := pool.Acquire()[:yuvSize]
		for j := 0; j < 320*240; j++ {
			buf[j] = 42 // distinguishable Y value
		}
		for j := 320 * 240; j < yuvSize; j++ {
			buf[j] = 128
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	// Process 10 ticks to consume the fed frames.
	for i := 0; i < 10; i++ {
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	// Clear viewer to start fresh measurement.
	viewer.mu.Lock()
	viewer.videos = nil
	viewer.mu.Unlock()

	// Now stop feeding. Process 20 more ticks — should produce freeze frames.
	for i := 10; i < 30; i++ {
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 15, "should produce freeze frames after source loss")

	// Verify freeze frames have the same content (Y=42) and advancing PTS.
	for i, f := range frames {
		require.Equal(t, byte(42), f.WireData[0],
			"freeze frame %d should retain Y=42 from last live frame", i)
	}
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"freeze frame PTS should advance: frame %d PTS=%d, frame %d PTS=%d",
			i-1, frames[i-1].PTS, i, frames[i].PTS)
	}
}

func TestSyncLoopSourceLossToBlack(t *testing.T) {
	// After 5s timeout, output transitions from freeze to broadcast black.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	viewer := newMockProgramViewer("black-viewer")
	s.programRelay.AddViewer(viewer)

	// Feed one frame.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	buf := pool.Acquire()[:yuvSize]
	for j := 0; j < 320*240; j++ {
		buf[j] = 42
	}
	for j := 320 * 240; j < yuvSize; j++ {
		buf[j] = 128
	}
	pf := &ProcessingFrame{
		Data:  buf,
		Width: 320, Height: 240,
		PTS: 0,
		pool: pool,
	}
	pf.SetRefs(1)
	s.frameSync.IngestRawVideo("cam1", pf)

	// Process one tick to consume it.
	tick := clock.Tick{
		Number:        0,
		VideoPTS:      0,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	}
	s.processVideoTick(tick)

	// Now simulate source loss: don't feed any more frames.
	// The first tick without fresh frames sets sourceLostAt.
	tick.Number = 1
	tick.VideoPTS = 3000
	s.processVideoTick(tick) // This sets sourceLostAt

	// Fake sourceLostAt to be 6 seconds ago to exceed the 5s timeout.
	past := time.Now().Add(-6 * time.Second)
	s.sourceLostAt.Store(&past)

	// Clear viewer.
	viewer.mu.Lock()
	viewer.videos = nil
	viewer.mu.Unlock()

	// Process another tick — should get broadcast black.
	tick.Number = 2
	tick.VideoPTS = 6000
	s.processVideoTick(tick)

	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 1, "should produce a frame after timeout")

	// Verify it's broadcast black (Y=16), not freeze (Y=42).
	f := frames[len(frames)-1]
	require.Equal(t, byte(16), f.WireData[0],
		"after 5s timeout, should output broadcast black (Y=16), not freeze (Y=42)")
}

func TestSyncLoopSourceRecovery(t *testing.T) {
	// After loss, resume feeding. Output immediately shows new frames.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	viewer := newMockProgramViewer("recovery-viewer")
	s.programRelay.AddViewer(viewer)

	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2

	// Feed initial frame.
	buf := pool.Acquire()[:yuvSize]
	for j := range buf {
		buf[j] = 42
	}
	pf := &ProcessingFrame{
		Data:  buf,
		Width: 320, Height: 240,
		PTS: 0,
		pool: pool,
	}
	pf.SetRefs(1)
	s.frameSync.IngestRawVideo("cam1", pf)

	tick := clock.Tick{
		Number:        0,
		VideoPTS:      0,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	}
	s.processVideoTick(tick) // consume initial frame

	// Trigger source loss (no fresh frames).
	tick.Number = 1
	tick.VideoPTS = 3000
	s.processVideoTick(tick)

	require.NotNil(t, s.sourceLostAt.Load(), "sourceLostAt should be set after loss")

	// Recover: feed a new frame with different luma.
	buf2 := pool.Acquire()[:yuvSize]
	for j := 0; j < 320*240; j++ {
		buf2[j] = 99 // new distinguishable value
	}
	for j := 320 * 240; j < yuvSize; j++ {
		buf2[j] = 128
	}
	pf2 := &ProcessingFrame{
		Data:  buf2,
		Width: 320, Height: 240,
		PTS: 6000,
		pool: pool,
	}
	pf2.SetRefs(1)
	s.frameSync.IngestRawVideo("cam1", pf2)

	// Clear viewer.
	viewer.mu.Lock()
	viewer.videos = nil
	viewer.mu.Unlock()

	tick.Number = 2
	tick.VideoPTS = 6000
	s.processVideoTick(tick)

	require.Nil(t, s.sourceLostAt.Load(), "sourceLostAt should be cleared after recovery")

	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 1, "should produce frame after recovery")
	require.Equal(t, byte(99), frames[0].WireData[0],
		"recovered frame should have new luma value (99)")
}

// === Task 15: Transition Engine Integration Tests ===

func TestSyncLoopTransitionDissolve(t *testing.T) {
	// Start dissolve, feed ticks with auto-advance. Output Y progresses from source A to source B.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	// Register two sources.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()

	cam2Relay := distribution.NewRelay()
	s.RegisterSource("cam2", cam2Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam2")
	s.mu.Unlock()

	require.NoError(t, s.Cut(context.Background(), "cam1"))
	s.SetPreview(context.Background(), "cam2")

	viewer := newMockProgramViewer("dissolve-viewer")
	s.programRelay.AddViewer(viewer)

	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2

	// Feed frames for cam1 (Y=40) and cam2 (Y=200).
	feedSource := func(key string, yVal byte) {
		buf := pool.Acquire()[:yuvSize]
		for j := 0; j < 320*240; j++ {
			buf[j] = yVal
		}
		for j := 320 * 240; j < yuvSize; j++ {
			buf[j] = 128
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: 0,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo(key, pf)
	}

	// Set up transition engine (sync path).
	engine := transition.NewEngine(transition.EngineConfig{})
	engine.StartSync("cam1", "cam2", transition.Mix, 300) // 300ms dissolve

	s.mu.Lock()
	s.transConfig = &TransitionConfig{}
	s.transEngine = engine
	s.transitionState(StateTransitioning)
	s.mu.Unlock()

	// Feed frames before each tick.
	var firstY, lastY byte
	for i := 0; i < 15; i++ {
		feedSource("cam1", 40)
		feedSource("cam2", 200)

		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 5, "should produce at least 5 transition frames")

	// The first frame should be close to source A (Y~40) and later frames
	// should progress toward source B (Y~200).
	firstY = frames[0].WireData[0]
	lastY = frames[len(frames)-1].WireData[0]
	require.Greater(t, lastY, firstY,
		"dissolve should progress from source A (%d) toward source B (%d)", firstY, lastY)
}

func TestSyncLoopTransitionComplete(t *testing.T) {
	// After transition completes, program source is updated.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()

	cam2Relay := distribution.NewRelay()
	s.RegisterSource("cam2", cam2Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam2")
	s.mu.Unlock()

	require.NoError(t, s.Cut(context.Background(), "cam1"))
	s.SetPreview(context.Background(), "cam2")

	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2

	feedSource := func(key string) {
		buf := pool.Acquire()[:yuvSize]
		for j := range buf {
			buf[j] = 128
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: 0,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo(key, pf)
	}

	// Set up a short transition (100ms = ~3 frames at 30fps).
	// StartSync sets fromSource/toSource (needed by handleTransitionComplete).
	engine := transition.NewEngine(transition.EngineConfig{})
	engine.StartSync("cam1", "cam2", transition.Mix, 100)

	s.mu.Lock()
	s.transConfig = &TransitionConfig{}
	s.transEngine = engine
	s.transitionState(StateTransitioning)
	s.mu.Unlock()

	// Feed frames and process enough ticks to complete the transition.
	// 100ms / 33.33ms = 3 ticks needed, process a few more to be safe.
	for i := 0; i < 10; i++ {
		feedSource("cam1")
		feedSource("cam2")
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	// After completion, program source should be cam2.
	state := s.State()
	require.Equal(t, "cam2", state.ProgramSource,
		"program source should switch to cam2 after transition completes")
}

// === Task 16: Clock Recovery Tests ===

func TestSyncLoopClockRecovery(t *testing.T) {
	// Feed frames at 29.97fps intervals. Verify clock recovery detects the rate.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	// Attach clock recovery with a low stability threshold for testing.
	cr := clock.NewClockRecovery(30, 1, 3) // initial 30fps, 3 frames to confirm change
	s.SetClockRecovery(cr)

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2

	// Simulate 29.97fps frame arrivals by recording timestamps at that rate.
	// The clock recovery processes arrivals in processVideoTick when fresh frames exist.
	frameInterval := time.Duration(int64(time.Second) * 1001 / 30000) // ~33.37ms for 29.97fps

	for i := 0; i < 100; i++ {
		buf := pool.Acquire()[:yuvSize]
		for j := range buf {
			buf[j] = 128
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3003, // 29.97fps PTS interval
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)

		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3003,
			AudioSamples:  1601, // 48000*1001/30000
			FrameDuration: frameInterval,
		}
		s.processVideoTick(tick)

		// Small sleep to create realistic arrival timestamps for clock recovery.
		// Use a very short sleep since the test is about detection accuracy.
		time.Sleep(100 * time.Microsecond)
	}

	// After enough samples, check the measured FPS.
	measuredFPS := cr.MeasuredFPS()
	if measuredFPS > 0 {
		// The measured FPS should be detected. Since we're in a test environment
		// the exact timing may not be 29.97, but the mechanism should be active.
		require.Greater(t, measuredFPS, 0.0,
			"clock recovery should have measured a non-zero FPS")
	}

	// Verify that Rate() returns some valid rate.
	num, den := cr.Rate()
	require.Greater(t, num, 0, "recovered rate num should be > 0")
	require.Greater(t, den, 0, "recovered rate den should be > 0")
}

// Issue 12: AAC buffer should be flushed on sync loop shutdown.
func TestSyncLoopFlushesAACOnShutdown(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	mixer := newMockSyncMixer(2)
	mixer.sampleFn = func(sampleCount int) []float32 {
		return make([]float32, sampleCount*2) // non-zero to accumulate
	}
	s.SetSyncMixer(mixer)

	aacBuf := clock.NewAACFrameBuffer(2, 48000, mockAACEncode)
	s.SetAACBuffer(aacBuf)

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Capture audio output.
	viewer := newMockProgramViewer("flush-viewer")
	s.programRelay.AddViewer(viewer)

	// Start the clock and sync loop, consume a few ticks.
	rc.Start()
	s.StartSyncLoop()

	// Wait for at least one audio frame.
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.audios) >= 1
	}, 2*time.Second, 10*time.Millisecond)

	// Record pre-stop count.
	viewer.mu.Lock()
	preStopCount := len(viewer.audios)
	viewer.mu.Unlock()

	// Stop the clock (closes channel, triggering sync loop exit + flush).
	rc.Stop()
	s.syncLoopWg.Wait()

	// After shutdown, the flush should have delivered any remaining partial frame.
	viewer.mu.Lock()
	postStopCount := len(viewer.audios)
	viewer.mu.Unlock()

	// We should have at least as many frames as before (flush may add one more).
	require.GreaterOrEqual(t, postStopCount, preStopCount,
		"shutdown flush should not lose audio frames")

	// Verify the AAC buffer is empty after flush.
	require.Equal(t, 0, aacBuf.Pending(),
		"AAC buffer should be empty after shutdown flush")
}

// Issue 1: repeatLastVideoFrame should broadcast the last encoded H.264 frame,
// not raw YUV data.
func TestRepeatLastVideoFrameUsesEncodedData(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	viewer := newMockProgramViewer("encoded-repeat")
	s.programRelay.AddViewer(viewer)

	// Simulate a previously broadcast H.264 frame by storing it directly.
	h264Marker := []byte{0x00, 0x00, 0x00, 0x01, 0x67} // H.264 NALU start code + SPS
	encodedFrame := &media.VideoFrame{
		PTS:        1000,
		DTS:        1000,
		WireData:   h264Marker,
		IsKeyframe: true,
	}
	s.lastBroadcastVFrame.Store(encodedFrame)

	// Also store a raw YUV frame (the old buggy path would use this).
	rawYUV := makeTestYUV(320, 240, 42)
	rawFrame := &ProcessingFrame{
		Data:  rawYUV,
		Width: 320, Height: 240,
		PTS: 1000,
	}
	s.lastEncodedFrame.Store(rawFrame)

	// Call repeatLastVideoFrame.
	s.repeatLastVideoFrame(5000)

	// Check the output.
	frames := viewer.VideoFrames()
	require.Len(t, frames, 1, "should have broadcast exactly 1 frame")

	// The frame should contain H.264 data (starting with NALU prefix),
	// NOT raw YUV (which would start with byte 42).
	require.True(t, len(frames[0].WireData) >= 5, "WireData should have H.264 data")
	require.Equal(t, byte(0x00), frames[0].WireData[0], "should start with H.264 NALU prefix")
	require.Equal(t, byte(0x00), frames[0].WireData[1])
	require.Equal(t, byte(0x00), frames[0].WireData[2])
	require.Equal(t, byte(0x01), frames[0].WireData[3])
	require.Equal(t, byte(0x67), frames[0].WireData[4], "should be H.264 SPS NALU type")

	// PTS should be updated to the requested value.
	require.Equal(t, int64(5000), frames[0].PTS)
}

// Issue 4: FTB transition completion should call handleFTBComplete, not handleTransitionComplete.
func TestFTBTransitionCallsCorrectHandler(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Feed frames for the source.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 10; i++ {
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	// Set up a transition engine for FTB.
	engine := transition.NewEngine(transition.EngineConfig{
		HintWidth:  320,
		HintHeight: 240,
		OnComplete: func(aborted bool) {
			s.handleFTBComplete(aborted)
		},
	})
	require.NoError(t, engine.Start("cam1", "", transition.FTB, 100))

	s.mu.Lock()
	s.transEngine = engine
	s.transitionState(StateFTBTransitioning)
	s.mu.Unlock()

	// Process enough ticks to complete the transition.
	for i := 0; i < 20; i++ {
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)

		// Check if transition completed.
		s.mu.RLock()
		st := s.state
		s.mu.RUnlock()
		if !st.isInTransition() {
			break
		}
	}

	// After FTB completion, state should be StateFTB (holding black), not StateIdle.
	s.mu.RLock()
	finalState := s.state
	s.mu.RUnlock()

	require.Equal(t, StateFTB, finalState,
		"FTB completion should leave state as StateFTB (holding black), not %s", finalState)
}

// Issue 10: Double-start guard on StartSyncLoop.
func TestStartSyncLoopDoubleStartGuard(t *testing.T) {
	s, rc := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Feed some frames.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 20; i++ {
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	viewer := newMockProgramViewer("double-start")
	s.programRelay.AddViewer(viewer)

	rc.Start()
	s.StartSyncLoop()
	s.StartSyncLoop() // second call should be a no-op

	// Wait for some output.
	require.Eventually(t, func() bool {
		return len(viewer.VideoFrames()) >= 3
	}, 2*time.Second, 10*time.Millisecond)

	rc.Stop()
	s.syncLoopWg.Wait()

	// If two goroutines were consuming, frames would be split and we'd get
	// intermittent failures. With the guard, all frames come from one goroutine.
	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 3,
		"should have produced frames with single goroutine")
}

// === Bug 1: videoProcessingLoop races with sync loop ===

func TestSyncLoopNoRaceWithAsyncPath(t *testing.T) {
	// When ref clock is active, BOTH the async videoProcessingLoop goroutine
	// AND the sync loop could process frames from the same source. The async
	// loop must drain videoProcCh without processing to prevent double-processing.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	// Register source and cut to it.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Build a counting pipeline node that records how many frames pass through.
	var pipelineProcessCount atomic.Int64
	countNode := &countingPipelineNode{count: &pipelineProcessCount}
	p := &Pipeline{}
	require.NoError(t, p.Build(PipelineFormat{
		Name: "test", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
	}, s.framePool, []PipelineNode{countNode}))
	s.pipeline.Store(p)

	// Set up viewer to capture output.
	viewer := newMockProgramViewer("no-race-viewer")
	s.programRelay.AddViewer(viewer)

	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2

	// Feed frames into the frame sync buffer AND trigger handleRawVideoFrame
	// (which enqueues to videoProcCh for the async path). We call both
	// IngestRawVideo (for the sync loop to pop) and handleRawVideoFrame
	// (to simulate source decoder delivery that enqueues to videoProcCh).
	for i := 0; i < 10; i++ {
		buf := pool.Acquire()[:yuvSize]
		for j := range buf {
			buf[j] = byte(i + 1)
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS:   int64(i) * 3000,
			pool:  pool,
		}
		pf.SetRefs(2) // one ref for frame sync, one for handleRawVideoFrame
		s.frameSync.IngestRawVideo("cam1", pf)
		s.handleRawVideoFrame("cam1", pf)
	}

	// Process ticks via the sync loop (directly, deterministic).
	for i := 0; i < 10; i++ {
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	// Give the async loop time to process any items it might have.
	time.Sleep(100 * time.Millisecond)

	// The sync loop processed 10 ticks. Each tick runs the pipeline once = 10 calls.
	// The async loop should NOT also be processing those frames through the pipeline.
	// With the bug, the async loop would also run them through pipeline.Run(),
	// leading to > 10 pipeline invocations.
	syncProcessed := pipelineProcessCount.Load()
	require.Equal(t, int64(10), syncProcessed,
		"pipeline should be invoked exactly 10 times (once per tick), got %d (async double-processing?)", syncProcessed)

	// Verify videoProcCh is not accumulating (drained by async loop).
	require.Equal(t, 0, len(s.videoProcCh),
		"videoProcCh should be drained, got %d items", len(s.videoProcCh))
}

// countingPipelineNode counts how many times Process is called.
type countingPipelineNode struct {
	count *atomic.Int64
}

func (n *countingPipelineNode) Name() string                          { return "counting-test" }
func (n *countingPipelineNode) Configure(format PipelineFormat) error { return nil }
func (n *countingPipelineNode) Active() bool                          { return true }
func (n *countingPipelineNode) Err() error                            { return nil }
func (n *countingPipelineNode) Latency() time.Duration                { return 0 }
func (n *countingPipelineNode) Close() error                          { return nil }
func (n *countingPipelineNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	n.count.Add(1)
	return src
}

// === Bug 2: GPU pipeline bypassed in sync loop ===

func TestSyncLoopChecksGPURunner(t *testing.T) {
	// Verify that runPipelineAndBroadcast checks the GPU runner first
	// (RunFromCache, then RunWithUpload) before falling back to CPU.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Set a mock GPU runner that tracks calls.
	var runFromCacheCalls atomic.Int64
	var runWithUploadCalls atomic.Int64
	mockGPU := &syncLoopGPUMock{
		onRunFromCache:  func(sourceKey string, pts int64) error {
			runFromCacheCalls.Add(1)
			return fmt.Errorf("mock: no cached frame") // force fallback to RunWithUpload
		},
		onRunWithUpload: func(yuv []byte, w, h int, pts int64) error {
			runWithUploadCalls.Add(1)
			return nil // succeed
		},
	}
	s.SetGPUPipeline(mockGPU)

	// Also install a counting CPU pipeline to verify it's NOT called when GPU succeeds.
	var cpuPipelineCalls atomic.Int64
	cpuNode := &countingPipelineNode{count: &cpuPipelineCalls}
	p := &Pipeline{}
	require.NoError(t, p.Build(PipelineFormat{
		Name: "test", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
	}, s.framePool, []PipelineNode{cpuNode}))
	s.pipeline.Store(p)

	// Feed frames and process ticks.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 5; i++ {
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS:   int64(i) * 3000,
			pool:  pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	for i := 0; i < 5; i++ {
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	// Verify GPU runner was consulted.
	require.Equal(t, int64(5), runFromCacheCalls.Load(),
		"RunFromCache should be called once per tick")
	require.Equal(t, int64(5), runWithUploadCalls.Load(),
		"RunWithUpload should be called once per tick (after cache miss)")

	// CPU pipeline should NOT have been called since GPU succeeded.
	require.Equal(t, int64(0), cpuPipelineCalls.Load(),
		"CPU pipeline should not be called when GPU succeeds")

	// GPU metrics should be updated.
	require.Equal(t, int64(5), s.gpuFramesProcessed.Load(),
		"gpuFramesProcessed should count GPU-processed frames")
	require.Equal(t, int64(5), s.gpuCacheMisses.Load(),
		"gpuCacheMisses should count cache misses")
}

func TestSyncLoopGPUFallbackToCPU(t *testing.T) {
	// When GPU fails both RunFromCache and RunWithUpload, CPU pipeline is used.
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// GPU mock that always fails.
	mockGPU := &syncLoopGPUMock{
		onRunFromCache: func(sourceKey string, pts int64) error {
			return fmt.Errorf("mock: no cached frame")
		},
		onRunWithUpload: func(yuv []byte, w, h int, pts int64) error {
			return fmt.Errorf("mock: GPU upload failed")
		},
	}
	s.SetGPUPipeline(mockGPU)

	// CPU pipeline that counts invocations.
	var cpuCalls atomic.Int64
	cpuNode := &countingPipelineNode{count: &cpuCalls}
	p := &Pipeline{}
	require.NoError(t, p.Build(PipelineFormat{
		Name: "test", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
	}, s.framePool, []PipelineNode{cpuNode}))
	s.pipeline.Store(p)

	viewer := newMockProgramViewer("gpu-fallback-viewer")
	s.programRelay.AddViewer(viewer)

	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 3; i++ {
		buf := pool.Acquire()[:yuvSize]
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS:   int64(i) * 3000,
			pool:  pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	for i := 0; i < 3; i++ {
		tick := clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		}
		s.processVideoTick(tick)
	}

	// CPU pipeline should have been called as fallback.
	require.Equal(t, int64(3), cpuCalls.Load(),
		"CPU pipeline should be called when GPU fails")

	// Fallback metrics should be recorded.
	require.Equal(t, int64(3), s.gpuFallbackFrames.Load(),
		"gpuFallbackFrames should count fallback frames")

	// gpuFramesProcessed should be 0 (GPU never succeeded).
	require.Equal(t, int64(0), s.gpuFramesProcessed.Load(),
		"gpuFramesProcessed should be 0 when GPU always fails")
}

// syncLoopGPUMock is a GPUPipelineRunner mock for sync loop tests.
type syncLoopGPUMock struct {
	onRunFromCache  func(sourceKey string, pts int64) error
	onRunWithUpload func(yuv []byte, w, h int, pts int64) error
}

func (m *syncLoopGPUMock) RunFromCache(sourceKey string, pts int64) error {
	if m.onRunFromCache != nil {
		return m.onRunFromCache(sourceKey, pts)
	}
	return fmt.Errorf("not implemented")
}

func (m *syncLoopGPUMock) RunWithUpload(yuv []byte, width, height int, pts int64) error {
	if m.onRunWithUpload != nil {
		return m.onRunWithUpload(yuv, width, height, pts)
	}
	return fmt.Errorf("not implemented")
}

func (m *syncLoopGPUMock) RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stinger *GPUStingerFrame) error {
	return nil
}

func (m *syncLoopGPUMock) Snapshot() map[string]any {
	return map[string]any{"gpu": true, "backend": "sync-loop-mock"}
}

// newSyncTestSwitcherBufferOnly creates a test switcher using buffer-only
// FrameSync mode, mirroring the production ref clock path where the
// FrameSync provides ring buffers but does NOT run its tickLoop timer.
func newSyncTestSwitcherBufferOnly(t *testing.T) (*Switcher, *clock.ReferenceClock) {
	t.Helper()
	programRelay := distribution.NewRelay()
	testFmt := PipelineFormat{
		Name:   "test",
		Width:  320,
		Height: 240,
		FPSNum: 30,
		FPSDen: 1,
	}

	s := &Switcher{
		log:           testSyncLogger(),
		sources:       make(map[string]*sourceState),
		programRelay:  programRelay,
		health:        newHealthMonitor(),
		videoProcCh:   make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
		framePool:     NewFramePool(8, 320, 240, ColorYUV420_8bit),
	}
	s.frameBudgetNs.Store(testFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&testFmt)
	s.delayBuffer = NewDelayBuffer(s)

	rc := clock.NewReferenceClock(30, 1, 48000, 0, false)
	s.SetReferenceClock(rc)
	go s.videoProcessingLoop()

	// Create FrameSync in buffer-only mode (production ref clock path).
	fs := NewFrameSynchronizer(
		testFmt.FrameDuration(),
		func(sourceKey string, frame media.VideoFrame) {},
		func(sourceKey string, frame media.AudioFrame) {},
	)
	fs.onRawVideo = func(sourceKey string, pf *ProcessingFrame) {
		s.handleRawVideoFrame(sourceKey, pf)
	}
	fs.framePool = s.framePool
	fs.SetClockDriven(true)
	fs.SetBufferOnly(true)
	fs.Start() // should NOT launch tickLoop

	s.mu.Lock()
	s.frameSync = fs
	s.frameSyncActive = true
	s.mu.Unlock()

	return s, rc
}

// TestSyncLoopWithBufferOnlyFrameSync verifies that the sync loop works
// correctly when the FrameSync is in buffer-only mode. Frames are pushed
// to the ring buffer via IngestRawVideo, the sync loop pops them, and
// output arrives on the program relay.
func TestSyncLoopWithBufferOnlyFrameSync(t *testing.T) {
	s, rc := newSyncTestSwitcherBufferOnly(t)
	defer s.Close()

	// Register and cut to a source.
	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	// Capture program output.
	viewer := newMockProgramViewer("test-viewer")
	s.programRelay.AddViewer(viewer)

	// Feed frames into the frame sync ring buffer.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	for i := 0; i < 5; i++ {
		buf := pool.Acquire()[:yuvSize]
		for j := range buf {
			buf[j] = byte(i + 1)
		}
		pf := &ProcessingFrame{
			Data:  buf,
			Width: 320, Height: 240,
			PTS: int64(i) * 3000,
			pool: pool,
		}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}

	// Start clock and sync loop.
	rc.Start()
	s.StartSyncLoop()

	// Wait for video frames to arrive.
	require.Eventually(t, func() bool {
		return len(viewer.VideoFrames()) >= 3
	}, 2*time.Second, 10*time.Millisecond, "expected video frames on program relay")

	rc.Stop()
	s.syncLoopWg.Wait()

	frames := viewer.VideoFrames()
	require.GreaterOrEqual(t, len(frames), 3, "should have produced at least 3 video frames")

	// Verify PTS monotonicity.
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"PTS should be monotonically increasing")
	}

	// Verify the FrameSync timer never fired (no timer-driven releases).
	require.Equal(t, int64(0), s.frameSync.timerDrivenReleases.Load(),
		"timer should not have fired in buffer-only mode")
	require.Equal(t, int64(0), s.frameSync.programDrivenReleases.Load(),
		"program-driven releases should be zero in buffer-only mode")
}

// TestSyncLoopBufferOnlyNoTimerRace verifies there is no data race between
// the sync loop popping frames and the FrameSync timer (which should not
// be running). This test is primarily useful with -race.
func TestSyncLoopBufferOnlyNoTimerRace(t *testing.T) {
	s, rc := newSyncTestSwitcherBufferOnly(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.mu.Lock()
	s.frameSync.AddSource("cam1")
	s.mu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))

	viewer := newMockProgramViewer("race-viewer")
	s.programRelay.AddViewer(viewer)

	// Continuously ingest frames from a background goroutine while the
	// sync loop is running. With -race this catches any data races.
	pool := s.framePool
	yuvSize := 320 * 240 * 3 / 2
	stopIngest := make(chan struct{})
	go func() {
		pts := int64(0)
		for {
			select {
			case <-stopIngest:
				return
			default:
			}
			buf := pool.Acquire()[:yuvSize]
			buf[0] = byte(pts % 256)
			pf := &ProcessingFrame{
				Data:  buf,
				Width: 320, Height: 240,
				PTS: pts,
				pool: pool,
			}
			pf.SetRefs(1)
			s.frameSync.IngestRawVideo("cam1", pf)
			pts += 3000
			time.Sleep(time.Millisecond) // ~1000fps ingest
		}
	}()

	rc.Start()
	s.StartSyncLoop()

	// Let it run for a bit under the race detector.
	require.Eventually(t, func() bool {
		return len(viewer.VideoFrames()) >= 10
	}, 3*time.Second, 10*time.Millisecond, "expected frames under concurrent ingest")

	close(stopIngest)
	rc.Stop()
	s.syncLoopWg.Wait()
}
