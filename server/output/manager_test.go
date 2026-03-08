package output

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

func newTestRelay() *distribution.Relay {
	return distribution.NewRelay()
}

func TestOutputManager_New(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	require.NotNil(t, mgr)
}

func TestOutputManager_StartRecording(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	err := mgr.StartRecording(RecorderConfig{Dir: dir})
	require.NoError(t, err)

	status := mgr.RecordingStatus()
	require.True(t, status.Active)
	require.NotEmpty(t, status.Filename)
}

func TestOutputManager_StopRecording(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NoError(t, mgr.StopRecording())

	status := mgr.RecordingStatus()
	require.False(t, status.Active)
}

func TestOutputManager_DoubleStartRecording(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	err := mgr.StartRecording(RecorderConfig{Dir: dir})
	require.Error(t, err, "should reject double start")
}

func TestOutputManager_StopRecordingWhenNotActive(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	err := mgr.StopRecording()
	require.Error(t, err)
}

func TestOutputManager_MuxerStartsOnFirstOutput(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	require.Nil(t, mgr.viewer)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	require.NotNil(t, mgr.viewer)
	require.NotNil(t, mgr.muxer)
}

func TestOutputManager_MuxerStopsOnLastOutput(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NotNil(t, mgr.viewer)

	require.NoError(t, mgr.StopRecording())
	time.Sleep(10 * time.Millisecond)
	require.Nil(t, mgr.viewer)
}

func TestOutputManager_RecordingReceivesFrames(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	idrFrame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		SPS:        []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
		WireData:   []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:      "h264",
	}
	relay.BroadcastVideo(idrFrame)

	time.Sleep(50 * time.Millisecond)

	status := mgr.RecordingStatus()
	require.True(t, status.BytesWritten > 0, "recorder should have received TS data")
}

func TestOutputManager_StateCallback(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	callCount := 0
	mgr.OnStateChange(func() {
		callCount++
	})

	dir := t.TempDir()
	_ = mgr.StartRecording(RecorderConfig{Dir: dir})
	require.Greater(t, callCount, 0)

	prevCount := callCount
	_ = mgr.StopRecording()
	require.Greater(t, callCount, prevCount)
}

func TestOutputManager_MuxStartCallback(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	muxStartCount := 0
	mgr.OnMuxerStart(func() {
		muxStartCount++
	})

	// First output starts the muxer — callback should fire once.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.Equal(t, 1, muxStartCount, "muxStart should fire on first output")

	// Second output reuses the muxer — callback should NOT fire again.
	id, err := mgr.AddDestination(DestinationConfig{
		Type: "srt-caller", Address: "192.168.1.100", Port: 9000,
	})
	require.NoError(t, err)
	// StartDestination will fail to connect (no real SRT), but the muxer
	// check happens before the adapter start, so we just verify the count.
	_ = mgr.StartDestination(id)
	require.Equal(t, 1, muxStartCount, "muxStart should NOT fire when muxer already running")
}

func TestOutputManager_SRTOutputStatus_NotActive(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	status := mgr.SRTOutputStatus()
	require.False(t, status.Active)
}

func TestOutputManager_DebugSnapshot(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	snap := mgr.DebugSnapshot()
	require.NotNil(t, snap["recording"], "expected recording in snapshot")
	require.NotNil(t, snap["srt"], "expected srt in snapshot")
	// viewer should be nil when no outputs active
	require.Nil(t, snap["viewer"], "expected nil viewer when no outputs active")
}

func TestOutputManager_DebugSnapshot_WithViewer(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	snap := mgr.DebugSnapshot()
	require.NotNil(t, snap["viewer"], "expected viewer snapshot when output active")

	viewerSnap, ok := snap["viewer"].(map[string]any)
	require.True(t, ok, "viewer snapshot should be a map")
	require.Contains(t, viewerSnap, "video_sent")
	require.Contains(t, viewerSnap, "audio_sent")
	require.Contains(t, viewerSnap, "caption_sent")
	require.Contains(t, viewerSnap, "video_dropped")
	require.Contains(t, viewerSnap, "audio_dropped")
	require.Contains(t, viewerSnap, "caption_dropped")
}

func TestOutputManager_RecordingStatus_DroppedPackets(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Access the async wrapper for the recorder via the manager's internals.
	mgr.mu.Lock()
	wrapper := mgr.asyncWrappers[mgr.recorder.ID()]
	mgr.mu.Unlock()
	require.NotNil(t, wrapper, "recorder should have async wrapper")

	// Verify initially zero drops.
	status := mgr.RecordingStatus()
	require.Equal(t, int64(0), status.DroppedPackets)

	// Simulate drops by directly incrementing the atomic counter on the wrapper.
	// In production, drops happen when the channel buffer overflows.
	wrapper.dropped.Add(5)

	status = mgr.RecordingStatus()
	require.Equal(t, int64(5), status.DroppedPackets)
}

func TestOutputManager_Close(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)

	dir := t.TempDir()
	_ = mgr.StartRecording(RecorderConfig{Dir: dir})
	err := mgr.Close()
	require.NoError(t, err)
}

func TestOutputManagerMuxerCallbackNoLock(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Grab the muxer reference while we still can.
	mgr.mu.Lock()
	muxer := mgr.muxer
	mgr.mu.Unlock()
	require.NotNil(t, muxer)

	// Hold the manager lock for the duration of the test. If the muxer
	// output callback still acquired m.mu, writing a frame would deadlock.
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		idrFrame := &media.VideoFrame{
			PTS: 90000, DTS: 90000, IsKeyframe: true,
			SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
			PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
			WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
			Codec:    "h264",
		}
		_ = muxer.WriteVideo(idrFrame)
	}()

	select {
	case <-done:
		// Callback completed without deadlock.
	case <-time.After(2 * time.Second):
		t.Fatal("muxer callback deadlocked — still acquiring m.mu")
	}
}
