package demo

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// mockSwitcher records calls to SwitcherAPI methods.
type mockSwitcher struct {
	mu       sync.Mutex
	labels   map[string]string
	program  string
	preview  string
	cutCount int
}

func newMockSwitcher() *mockSwitcher {
	return &mockSwitcher{
		labels: make(map[string]string),
	}
}

func (m *mockSwitcher) SetLabel(_ context.Context, key, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels[key] = label
	return nil
}

func (m *mockSwitcher) Cut(_ context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.program = source
	m.cutCount++
	return nil
}

func (m *mockSwitcher) SetPreview(_ context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.preview = source
	return nil
}

func makeRelays(n int) []*distribution.Relay {
	relays := make([]*distribution.Relay, n)
	for i := range n {
		relays[i] = distribution.NewRelay()
	}
	return relays
}

func TestStartSources_LabelsAndState(t *testing.T) {
	sw := newMockSwitcher()
	ctx := context.Background()
	relays := makeRelays(4)

	stop := StartSources(ctx, sw, relays, NewDemoStats(), "", 33*time.Millisecond)
	defer stop()

	sw.mu.Lock()
	defer sw.mu.Unlock()

	// Verify labels.
	assert.Equal(t, "Camera 1", sw.labels["cam1"])
	assert.Equal(t, "Camera 2", sw.labels["cam2"])
	assert.Equal(t, "Camera 3", sw.labels["cam3"])
	assert.Equal(t, "Camera 4", sw.labels["cam4"])

	// Verify program/preview.
	assert.Equal(t, "cam1", sw.program)
	assert.Equal(t, "cam2", sw.preview)
}

func TestStartSources_GeneratesFrames(t *testing.T) {
	sw := newMockSwitcher()
	ctx := context.Background()
	relays := makeRelays(1)

	stop := StartSources(ctx, sw, relays, NewDemoStats(), "", 33*time.Millisecond)

	// Add a viewer to cam1's relay to capture frames.
	viewer := &frameCollector{}
	relays[0].AddViewer(viewer)

	// Wait for some frames to arrive (~100ms = ~3 frames).
	time.Sleep(150 * time.Millisecond)
	stop()

	viewer.mu.Lock()
	videoCount := len(viewer.videoFrames)
	audioCount := len(viewer.audioFrames)
	viewer.mu.Unlock()

	assert.Greater(t, videoCount, 0, "should have received video frames")
	assert.Greater(t, audioCount, 0, "should have received audio frames")
	assert.Equal(t, videoCount, audioCount, "video and audio frame counts should match")

	// First frame should be a keyframe.
	viewer.mu.Lock()
	first := viewer.videoFrames[0]
	viewer.mu.Unlock()
	assert.True(t, first.IsKeyframe, "first frame should be a keyframe")
}

func TestStartSources_StopCancels(t *testing.T) {
	sw := newMockSwitcher()
	ctx := context.Background()
	relays := makeRelays(2)

	stop := StartSources(ctx, sw, relays, NewDemoStats(), "", 33*time.Millisecond)

	viewer := &frameCollector{}
	relays[0].AddViewer(viewer)

	// Let a few frames flow.
	time.Sleep(100 * time.Millisecond)
	stop()

	// Record count after stop.
	time.Sleep(100 * time.Millisecond)
	viewer.mu.Lock()
	countAfterStop := len(viewer.videoFrames)
	viewer.mu.Unlock()

	// Wait more — count should not grow.
	time.Sleep(100 * time.Millisecond)
	viewer.mu.Lock()
	countLater := len(viewer.videoFrames)
	viewer.mu.Unlock()

	assert.Equal(t, countAfterStop, countLater, "no new frames after stop")
}

// frameCollector implements distribution.Viewer for test observation.
type frameCollector struct {
	mu          sync.Mutex
	videoFrames []*media.VideoFrame
	audioFrames []*media.AudioFrame
}

func (f *frameCollector) ID() string { return "test-collector" }
func (f *frameCollector) SendVideo(frame *media.VideoFrame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.videoFrames = append(f.videoFrames, frame)
}
func (f *frameCollector) SendAudio(frame *media.AudioFrame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audioFrames = append(f.audioFrames, frame)
}
func (f *frameCollector) SendCaptions(_ *ccx.CaptionFrame) {}
func (f *frameCollector) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}

// testClipsDir returns the path to test clips, skipping if not present.
func testClipsDir(t *testing.T) string {
	t.Helper()
	// Look relative to repo root (tests run from server/).
	dir := filepath.Join("..", "..", "test", "clips")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// Also try from repo root directly.
		dir = filepath.Join("test", "clips")
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Skip("test clips not found — run 'make' from repo root to generate them")
		}
	}
	return dir
}

func TestDemuxTSFile(t *testing.T) {
	dir := testClipsDir(t)

	result, err := demuxTSFile(filepath.Join(dir, "tears_of_steel.ts"))
	require.NoError(t, err)

	// Should have extracted video and audio frames.
	assert.Greater(t, len(result.Video), 0, "should have video frames")
	assert.Greater(t, len(result.Audio), 0, "should have audio frames")

	// First video frame should be a keyframe with SPS/PPS.
	first := result.Video[0]
	assert.True(t, first.IsKeyframe, "first frame should be a keyframe")
	assert.NotEmpty(t, first.SPS, "first keyframe should have SPS")
	assert.NotEmpty(t, first.PPS, "first keyframe should have PPS")
	assert.NotEmpty(t, first.WireData, "first frame should have wire data")
	assert.Equal(t, "h264", first.Codec)

	// DTS should be monotonically non-decreasing (PTS may not be due to B-frames).
	for i := 1; i < len(result.Video); i++ {
		assert.GreaterOrEqual(t, result.Video[i].DTS, result.Video[i-1].DTS,
			"DTS should be monotonic at frame %d", i)
	}

	// Audio frames should have data.
	for i, af := range result.Audio {
		assert.NotEmpty(t, af.Data, "audio frame %d should have data", i)
		assert.Equal(t, 48000, af.SampleRate)
		assert.Equal(t, 2, af.Channels)
	}
}

func TestDemuxTSFile_ParsesSPS(t *testing.T) {
	dir := testClipsDir(t)

	result, err := demuxTSFile(filepath.Join(dir, "tears_of_steel.ts"))
	require.NoError(t, err)

	// Find first keyframe with SPS.
	var sps []byte
	for _, vf := range result.Video {
		if vf.IsKeyframe && len(vf.SPS) > 0 {
			sps = vf.SPS
			break
		}
	}
	require.NotEmpty(t, sps, "should have found SPS in keyframe")

	codecStr, width, height := parseSPS(sps)
	assert.Contains(t, codecStr, "avc1.", "codec string should start with avc1.")
	assert.Equal(t, 1280, width, "should be 1280 wide")
	assert.Equal(t, 720, height, "should be 720 tall")
}

func TestGenerateFramesFromFile(t *testing.T) {
	dir := testClipsDir(t)

	result, err := demuxTSFile(filepath.Join(dir, "tears_of_steel.ts"))
	require.NoError(t, err)

	relay := distribution.NewRelay()
	viewer := &frameCollector{}
	relay.AddViewer(viewer)

	stats := NewDemoStats()
	ctx, cancel := context.WithCancel(context.Background())

	go generateFramesFromFile(ctx, relay, result.Video, result.Audio, "cam1", stats)

	// Let it play for ~200ms — should get some frames.
	time.Sleep(250 * time.Millisecond)
	cancel()

	// Give goroutine time to exit.
	time.Sleep(50 * time.Millisecond)

	viewer.mu.Lock()
	videoCount := len(viewer.videoFrames)
	audioCount := len(viewer.audioFrames)
	viewer.mu.Unlock()

	assert.Greater(t, videoCount, 0, "should have received video frames")
	assert.Greater(t, audioCount, 0, "should have received audio frames")

	// DTS should be monotonically non-decreasing (frames in decode order).
	viewer.mu.Lock()
	for i := 1; i < len(viewer.videoFrames); i++ {
		assert.GreaterOrEqual(t, viewer.videoFrames[i].DTS, viewer.videoFrames[i-1].DTS,
			"DTS should be monotonic at frame %d", i)
	}
	viewer.mu.Unlock()

	// Stats should reflect sent frames.
	src := stats.Source("cam1")
	assert.Equal(t, int64(videoCount), src.VideoSent.Load())
	assert.Equal(t, int64(audioCount), src.AudioSent.Load())
}

func TestStartSources_WithVideoDir(t *testing.T) {
	dir := testClipsDir(t)

	sw := newMockSwitcher()
	ctx := context.Background()
	relays := makeRelays(4)
	stats := NewDemoStats()

	stop := StartSources(ctx, sw, relays, stats, dir, 33*time.Millisecond)

	// Add viewers to capture frames.
	viewers := make([]*frameCollector, 4)
	for i := range 4 {
		viewers[i] = &frameCollector{}
		relays[i].AddViewer(viewers[i])
	}

	// Let frames flow.
	time.Sleep(300 * time.Millisecond)
	stop()

	// Each camera should have received frames.
	for i, v := range viewers {
		v.mu.Lock()
		count := len(v.videoFrames)
		v.mu.Unlock()
		assert.Greater(t, count, 0, "cam%d should have received video frames", i+1)
	}

	// Stats should show real_video mode.
	snap := stats.DebugSnapshot()
	assert.Equal(t, "real_video", snap["mode"])
}

func TestDemoStats_DebugSnapshot(t *testing.T) {
	stats := NewDemoStats()
	stats.SetFileInfo("real_video", "test.ts", 100, 200)

	src := stats.Source("cam1")
	src.VideoSent.Add(50)
	src.AudioSent.Add(50)
	src.LoopsCompleted.Add(1)

	snap := stats.DebugSnapshot()
	require.Equal(t, "real_video", snap["mode"])
	require.Equal(t, int64(100), snap["video_frames_loaded"])

	perSource, ok := snap["per_source"].(map[string]any)
	require.True(t, ok, "expected per_source map")
	cam1, ok := perSource["cam1"].(map[string]any)
	require.True(t, ok, "expected cam1 map")
	require.Equal(t, int64(50), cam1["video_sent"])
}
