package output

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// makeKeyframe builds a minimal H.264 keyframe with SPS/PPS in AVC1 wire format.
func makeKeyframe(pts int64) *media.VideoFrame {
	return &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		IsKeyframe: true,
		SPS:        []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
		WireData:   []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:      "h264",
	}
}

// makePFrame builds a minimal H.264 P-frame in AVC1 wire format.
func makePFrame(pts int64) *media.VideoFrame {
	return &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x03, 0x41, 0x9A, 0x24},
		Codec:      "h264",
	}
}

// makeAudioFrame builds a minimal AAC audio frame.
func makeAudioFrame(pts int64) *media.AudioFrame {
	return &media.AudioFrame{
		PTS:        pts,
		Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20, 0x54, 0xE5, 0x00},
		SampleRate: 48000,
		Channels:   2,
	}
}

// TestIntegration_RecordingProducesValidTS verifies the full pipeline:
// program relay → Viewer → TSMuxer → FileRecorder → .ts file.
// The resulting file must be a valid MPEG-TS container with 188-byte aligned
// packets, each starting with the 0x47 sync byte.
func TestIntegration_RecordingProducesValidTS(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	relay := distribution.NewRelay()
	mgr := NewManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Send a mini-GOP: keyframe + P-frame with interleaved audio.
	relay.BroadcastVideo(makeKeyframe(90000))
	relay.BroadcastAudio(makeAudioFrame(90000))
	relay.BroadcastVideo(makePFrame(93000))
	relay.BroadcastAudio(makeAudioFrame(93000))

	// Allow the async viewer goroutine to drain frames through the muxer.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, mgr.StopRecording())

	// Read the recorded file.
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, files, "recording directory must contain a .ts file")

	path := filepath.Join(dir, files[0].Name())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, len(data) > 0, "recorded file must not be empty")

	// Validate MPEG-TS structure.
	require.Equal(t, 0, len(data)%188, "file size must be a multiple of 188 bytes (got %d)", len(data))

	for i := 0; i < len(data); i += 188 {
		require.Equal(t, byte(0x47), data[i], "TS packet at offset %d must start with sync byte 0x47", i)
	}
}

// TestIntegration_ManagerLifecycle verifies that the muxer and viewer
// are created on first output and torn down when the last output stops.
func TestIntegration_ManagerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	relay := distribution.NewRelay()
	mgr := NewManager(relay)

	// Before any output, the viewer should not exist.
	require.Nil(t, mgr.viewer, "viewer must be nil before any output is started")

	// Starting recording should create the viewer.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NotNil(t, mgr.viewer, "viewer must be created when recording starts")
	require.NotNil(t, mgr.muxer, "muxer must be created when recording starts")

	// Stopping recording should tear down the viewer (last adapter removed).
	require.NoError(t, mgr.StopRecording())
	time.Sleep(20 * time.Millisecond) // allow async teardown
	require.Nil(t, mgr.viewer, "viewer must be nil after last output is stopped")
	require.Nil(t, mgr.muxer, "muxer must be nil after last output is stopped")

	// Close should succeed cleanly.
	require.NoError(t, mgr.Close())
}

// TestIntegration_MultipleAdapters verifies that the muxer fans out TS data
// to all registered adapters simultaneously (file recorder + mock adapter).
func TestIntegration_MultipleAdapters(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	relay := distribution.NewRelay()
	mgr := NewManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Add a mock adapter alongside the file recorder to verify fan-out.
	// We insert it after StartRecording so the muxer output callback
	// already exists and will snapshot both adapters.
	var mockReceived atomic.Int64
	mock := &testAdapter{
		id: "mock-fanout",
		writeFn: func(data []byte) (int, error) {
			mockReceived.Add(int64(len(data)))
			return len(data), nil
		},
	}
	current := *mgr.adapters.Load()
	newAdapters := make([]Adapter, len(current)+1)
	copy(newAdapters, current)
	newAdapters[len(current)] = mock
	mgr.adapters.Store(&newAdapters)
	mgr.directAdapters.Store(&newAdapters)

	// Broadcast a keyframe so the muxer initializes and produces TS output.
	relay.BroadcastVideo(makeKeyframe(90000))
	time.Sleep(50 * time.Millisecond)

	// Both the file recorder and mock adapter should have received data.
	recStatus := mgr.RecordingStatus()
	require.True(t, recStatus.BytesWritten > 0, "file recorder must have received TS data")
	require.True(t, mockReceived.Load() > 0, "mock adapter must have received TS data")

	// Both should have received the same amount of data.
	require.Equal(t, recStatus.BytesWritten, mockReceived.Load(),
		"both adapters must receive identical TS data")
}

// TestIntegration_FramesStopAfterManagerClose verifies that no frames are
// delivered after the output manager is closed.
func TestIntegration_FramesStopAfterManagerClose(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	relay := distribution.NewRelay()
	mgr := NewManager(relay)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Send a keyframe to initialize the muxer.
	relay.BroadcastVideo(makeKeyframe(90000))
	time.Sleep(50 * time.Millisecond)

	// Close the manager, which removes the viewer from the relay.
	require.NoError(t, mgr.Close())

	// Capture the file state before sending more frames.
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	path := filepath.Join(dir, files[0].Name())
	dataBefore, err := os.ReadFile(path)
	require.NoError(t, err)
	sizeBefore := len(dataBefore)

	// Send more frames — these must NOT reach the (now closed) recorder.
	relay.BroadcastVideo(makeKeyframe(180000))
	relay.BroadcastAudio(makeAudioFrame(180000))
	time.Sleep(50 * time.Millisecond)

	dataAfter, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, sizeBefore, len(dataAfter),
		"file size must not grow after manager is closed")
}

// TestIntegration_PreKeyframeDropped verifies that frames sent before the
// first keyframe are silently dropped (the muxer waits for a keyframe to
// initialize PAT/PMT tables).
func TestIntegration_PreKeyframeDropped(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	relay := distribution.NewRelay()
	mgr := NewManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Send P-frames and audio before any keyframe.
	relay.BroadcastVideo(makePFrame(90000))
	relay.BroadcastAudio(makeAudioFrame(90000))
	relay.BroadcastVideo(makePFrame(93000))
	time.Sleep(50 * time.Millisecond)

	// No data should have been written (muxer not initialized).
	status := mgr.RecordingStatus()
	require.Equal(t, int64(0), status.BytesWritten,
		"no TS data should be written before first keyframe")

	// Now send a keyframe — muxer should initialize and produce output.
	relay.BroadcastVideo(makeKeyframe(96000))
	time.Sleep(50 * time.Millisecond)

	status = mgr.RecordingStatus()
	require.True(t, status.BytesWritten > 0,
		"TS data should flow after the first keyframe")
}

// testAdapter is a simple Adapter for integration test fan-out verification.
type testAdapter struct {
	id      string
	writeFn func([]byte) (int, error)
}

func (a *testAdapter) ID() string                    { return a.id }
func (a *testAdapter) Start(_ context.Context) error { return nil }
func (a *testAdapter) Write(data []byte) (int, error) {
	if a.writeFn != nil {
		return a.writeFn(data)
	}
	return len(data), nil
}
func (a *testAdapter) Close() error          { return nil }
func (a *testAdapter) Status() AdapterStatus { return AdapterStatus{State: StateActive} }

// Compile-time check that testAdapter satisfies Adapter.
var _ Adapter = (*testAdapter)(nil)
