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
	defer mgr.Close()

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
	defer mgr.Close()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NoError(t, mgr.StopRecording())

	status := mgr.RecordingStatus()
	require.False(t, status.Active)
}

func TestOutputManager_DoubleStartRecording(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	err := mgr.StartRecording(RecorderConfig{Dir: dir})
	require.Error(t, err, "should reject double start")
}

func TestOutputManager_StopRecordingWhenNotActive(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

	err := mgr.StopRecording()
	require.Error(t, err)
}

func TestOutputManager_MuxerStartsOnFirstOutput(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

	require.Nil(t, mgr.viewer)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	require.NotNil(t, mgr.viewer)
	require.NotNil(t, mgr.muxer)
}

func TestOutputManager_MuxerStopsOnLastOutput(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

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
	defer mgr.Close()

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
	defer mgr.Close()

	callCount := 0
	mgr.OnStateChange(func() {
		callCount++
	})

	dir := t.TempDir()
	mgr.StartRecording(RecorderConfig{Dir: dir})
	require.Greater(t, callCount, 0)

	prevCount := callCount
	mgr.StopRecording()
	require.Greater(t, callCount, prevCount)
}

func TestOutputManager_SRTOutputStatus_NotActive(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

	status := mgr.SRTOutputStatus()
	require.False(t, status.Active)
}

func TestOutputManager_DebugSnapshot(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

	snap := mgr.DebugSnapshot()
	require.NotNil(t, snap["recording"], "expected recording in snapshot")
	require.NotNil(t, snap["srt"], "expected srt in snapshot")
	// viewer should be nil when no outputs active
	require.Nil(t, snap["viewer"], "expected nil viewer when no outputs active")
}

func TestOutputManager_DebugSnapshot_WithViewer(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer mgr.Close()

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

func TestOutputManager_Close(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)

	dir := t.TempDir()
	mgr.StartRecording(RecorderConfig{Dir: dir})
	err := mgr.Close()
	require.NoError(t, err)
}
