package demo

import (
	"context"
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
	sources  map[string]*distribution.Relay
	labels   map[string]string
	program  string
	preview  string
	cutCount int
}

func newMockSwitcher() *mockSwitcher {
	return &mockSwitcher{
		sources: make(map[string]*distribution.Relay),
		labels:  make(map[string]string),
	}
}

func (m *mockSwitcher) RegisterSource(key string, relay *distribution.Relay) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[key] = relay
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

// mockMixer records AddChannel calls.
type mockMixer struct {
	mu       sync.Mutex
	channels []string
}

func (m *mockMixer) AddChannel(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels = append(m.channels, key)
}

func TestStartSources_RegistersAndLabels(t *testing.T) {
	sw := newMockSwitcher()
	mixer := &mockMixer{}
	ctx := context.Background()

	stop := StartSources(ctx, sw, mixer, 4)
	defer stop()

	sw.mu.Lock()
	defer sw.mu.Unlock()
	mixer.mu.Lock()
	defer mixer.mu.Unlock()

	// Verify all 4 sources registered.
	require.Len(t, sw.sources, 4)
	for i := 1; i <= 4; i++ {
		key := "cam" + string(rune('0'+i))
		assert.Contains(t, sw.sources, key, "source %s should be registered", key)
	}

	// Verify labels.
	assert.Equal(t, "Camera 1", sw.labels["cam1"])
	assert.Equal(t, "Camera 2", sw.labels["cam2"])
	assert.Equal(t, "Camera 3", sw.labels["cam3"])
	assert.Equal(t, "Camera 4", sw.labels["cam4"])

	// Verify program/preview.
	assert.Equal(t, "cam1", sw.program)
	assert.Equal(t, "cam2", sw.preview)

	// Verify mixer channels.
	assert.Equal(t, []string{"cam1", "cam2", "cam3", "cam4"}, mixer.channels)
}

func TestStartSources_GeneratesFrames(t *testing.T) {
	sw := newMockSwitcher()
	mixer := &mockMixer{}
	ctx := context.Background()

	stop := StartSources(ctx, sw, mixer, 1)

	// Add a viewer to cam1's relay to capture frames.
	sw.mu.Lock()
	relay := sw.sources["cam1"]
	sw.mu.Unlock()
	require.NotNil(t, relay)

	viewer := &frameCollector{}
	relay.AddViewer(viewer)

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
	mixer := &mockMixer{}
	ctx := context.Background()

	stop := StartSources(ctx, sw, mixer, 2)

	sw.mu.Lock()
	relay := sw.sources["cam1"]
	sw.mu.Unlock()

	viewer := &frameCollector{}
	relay.AddViewer(viewer)

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
