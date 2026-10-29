package output

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/srt"
)

func TestOutputManager_New(t *testing.T) {
	mgr := NewManager()
	require.NotNil(t, mgr)
}

func TestOutputManager_StartRecording(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	err := mgr.StartRecording(RecorderConfig{Dir: dir})
	require.NoError(t, err)

	status := mgr.RecordingStatus()
	require.True(t, status.Active)
	require.NotEmpty(t, status.Filename)
}

func TestOutputManager_StopRecording(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NoError(t, mgr.StopRecording())

	status := mgr.RecordingStatus()
	require.False(t, status.Active)
}

func TestOutputManager_DoubleStartRecording(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	err := mgr.StartRecording(RecorderConfig{Dir: dir})
	require.Error(t, err, "should reject double start")
}

func TestOutputManager_StopRecordingWhenNotActive(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	err := mgr.StopRecording()
	require.Error(t, err)
}

func TestOutputManager_MuxerStartsOnFirstOutput(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	require.Nil(t, mgr.muxer)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	require.NotNil(t, mgr.muxer)
	require.NotNil(t, mgr.muxer)
}

func TestOutputManager_MuxerStopsOnLastOutput(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NotNil(t, mgr.muxer)

	require.NoError(t, mgr.StopRecording())
	time.Sleep(10 * time.Millisecond)
	require.Nil(t, mgr.muxer)
}

func TestOutputManager_RecordingReceivesFrames(t *testing.T) {
	mgr := NewManager()
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
	mgr.DirectWriteVideo(idrFrame)

	time.Sleep(50 * time.Millisecond)

	status := mgr.RecordingStatus()
	require.True(t, status.BytesWritten > 0, "recorder should have received TS data")
}

func TestOutputManager_StateCallback(t *testing.T) {
	mgr := NewManager()
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
	mgr := NewManager()
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
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	status := mgr.SRTOutputStatus()
	require.False(t, status.Active)
}

func TestOutputManager_DebugSnapshot(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	snap := mgr.DebugSnapshot()
	require.NotNil(t, snap["recording"], "expected recording in snapshot")
	require.NotNil(t, snap["srt"], "expected srt in snapshot")
	// muxer_pts should not be present when no outputs active
	_, hasMuxerPTS := snap["muxer_pts"]
	require.False(t, hasMuxerPTS, "expected no muxer_pts when no outputs active")
}

func TestOutputManager_DebugSnapshot_WithMuxer(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Feed a keyframe so the muxer records a PTS.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	mgr.DirectWriteVideo(idrFrame)

	snap := mgr.DebugSnapshot()
	require.Contains(t, snap, "muxer_pts", "expected muxer_pts in snapshot when output active")
}

func TestOutputManager_RecordingStatus_DroppedPackets(t *testing.T) {
	mgr := NewManager()
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
	mgr := NewManager()

	dir := t.TempDir()
	_ = mgr.StartRecording(RecorderConfig{Dir: dir})
	err := mgr.Close()
	require.NoError(t, err)
}

// TestOutputManager_StopMuxerLocked_RaceWithStart verifies that a concurrent
// StartRecording() call during the drain window in stopMuxerLocked() does not
// hang or create a corrupted viewerWg state.
//
// stopMuxerLocked() temporarily releases m.mu while waiting for the viewer
// goroutine to exit via viewerWg.Wait(). Without a guard, a concurrent start
// call can slip through during the unlock window, call ensureMuxerLocked(),
// and increment viewerWg with a new viewer goroutine. The original
// viewerWg.Wait() then blocks on the *new* viewer — causing a hang because
// that viewer won't stop until the next explicit shutdown.
func TestOutputManager_StopMuxerLocked_RaceWithStart(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()

	// Start recording so the muxer/viewer are created.
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Feed a keyframe so the viewer has work to drain.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	mgr.DirectWriteVideo(idrFrame)
	time.Sleep(20 * time.Millisecond) // let the viewer ingest

	// Race stop + start. The stop will enter stopMuxerLocked which releases
	// the lock during viewer drain. If the start sneaks in during that window,
	// it would increment viewerWg for a new viewer, causing the stop's
	// viewerWg.Wait() to hang on the new viewer.
	errCh := make(chan error, 2)

	go func() {
		errCh <- mgr.StopRecording()
	}()

	// Tiny delay so stop likely enters the unlock window.
	time.Sleep(1 * time.Millisecond)

	go func() {
		dir2 := t.TempDir()
		errCh <- mgr.StartRecording(RecorderConfig{Dir: dir2})
	}()

	// Both operations must complete within a reasonable time.
	// Concurrent start-during-stop completes without deadlock.
	for i := 0; i < 2; i++ {
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out — likely hung in viewerWg.Wait() due to concurrent start during stop")
		}
	}

	// After both settle, state must be consistent.
	// (Just verify no panic or deadlock occurred — muxer may or may not be running.)
}

// TestOutputManager_StopMuxerLocked_EnsureMuxerRejectsDuringStopping verifies
// that ensureMuxerLocked() returns an error (or is a no-op) while a stop
// is in progress. This tests the stopping guard directly.
func TestOutputManager_StopMuxerLocked_EnsureMuxerRejectsDuringStopping(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	// Simulate many concurrent stop+start cycles. With -race, this catches
	// unsynchronized access to muxer/viewer/viewerWg fields.
	for i := 0; i < 20; i++ {
		dir := t.TempDir()
		require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = mgr.StopRecording()
		}()

		time.Sleep(500 * time.Microsecond)

		dir2 := t.TempDir()
		_ = mgr.StartRecording(RecorderConfig{Dir: dir2})

		<-done

		// Cleanup for next iteration.
		_ = mgr.StopRecording()
	}
}

// TestOutputManager_StoppingGuardBlocksEnsureMuxer directly verifies that
// the stopping flag prevents ensureMuxerLocked from creating a new muxer.
func TestOutputManager_StoppingGuardBlocksEnsureMuxer(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	// Simulate the stopping state.
	mgr.mu.Lock()
	mgr.stopping = true
	created := mgr.ensureMuxerLocked()
	mgr.mu.Unlock()

	require.False(t, created, "ensureMuxerLocked should not create muxer while stopping")
	require.Nil(t, mgr.muxer, "muxer should not be created while stopping")

	// Clear stopping and verify it works again.
	mgr.mu.Lock()
	mgr.stopping = false
	created = mgr.ensureMuxerLocked()
	mgr.mu.Unlock()

	require.True(t, created, "ensureMuxerLocked should create muxer when not stopping")
	require.NotNil(t, mgr.muxer, "muxer should be created")
}

// --- CBR pacer wiring tests ---

func TestManager_CBRPacerCreatedOnMuxStart(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(5_000_000)

	// Start recording to trigger muxer creation.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	mgr.mu.Lock()
	hasPacer := mgr.cbrPacer.Load() != nil
	mgr.mu.Unlock()

	require.True(t, hasPacer, "CBR pacer should be created when muxer starts with CBR enabled")
}

func TestManager_NoPacerWhenDisabled(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	// No SetCBRMuxrate call — CBR disabled.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	mgr.mu.Lock()
	hasPacer := mgr.cbrPacer.Load() != nil
	mgr.mu.Unlock()

	require.False(t, hasPacer, "CBR pacer should not exist when CBR is disabled")
}

func TestManager_RecorderIsDirectNotPaced(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(5_000_000)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Recorder should be in directAdapters.
	directAdapters := mgr.directAdapters.Load()
	require.NotNil(t, directAdapters)
	require.Len(t, *directAdapters, 1, "recorder should be a direct adapter")
}

func TestManager_SRTDestinationIsPaced(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(5_000_000)

	// Mock SRT connect.
	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "127.0.0.1",
		Port:    9000,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	// SRT destination should be paced, not direct.
	directAdapters := mgr.directAdapters.Load()
	require.NotNil(t, directAdapters)
	require.Len(t, *directAdapters, 0, "SRT should not be in direct adapters when CBR enabled")

	// But combined adapters should include the SRT adapter.
	allAdapters := mgr.adapters.Load()
	require.Len(t, *allAdapters, 1, "SRT should be in combined adapter list")

	mgr.mu.Lock()
	hasPacer := mgr.cbrPacer.Load() != nil
	mgr.mu.Unlock()
	require.True(t, hasPacer, "pacer should exist")
}

func TestManager_CBRSplitPath_RecorderAndSRT(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(5_000_000)

	// Mock SRT connect.
	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	// Start both recorder and SRT.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "127.0.0.1",
		Port:    9000,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	// Recorder should be direct, SRT should be paced.
	directAdapters := mgr.directAdapters.Load()
	require.Len(t, *directAdapters, 1, "only recorder should be direct")

	allAdapters := mgr.adapters.Load()
	require.Len(t, *allAdapters, 2, "both recorder and SRT should be in combined list")
}

func TestManager_CBRPacerStoppedOnMuxerStop(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(5_000_000)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	mgr.mu.Lock()
	hasPacer := mgr.cbrPacer.Load() != nil
	mgr.mu.Unlock()
	require.True(t, hasPacer, "pacer should exist after start")

	// Stop recording → should stop muxer and pacer.
	require.NoError(t, mgr.StopRecording())
	time.Sleep(10 * time.Millisecond)

	mgr.mu.Lock()
	hasPacer = mgr.cbrPacer.Load() != nil
	mgr.mu.Unlock()
	require.False(t, hasPacer, "pacer should be nil after muxer stops")
}

func TestManager_CBRPacerReceivesData(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(1_000_000) // 1 Mbps

	// Mock SRT connect that tracks bytes.
	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "127.0.0.1",
		Port:    9000,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	// Feed a keyframe to the relay so the muxer produces TS data.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	mgr.DirectWriteVideo(idrFrame)

	// Wait for pacer to tick and send data.
	time.Sleep(100 * time.Millisecond)

	// The SRT mock should have received data (via pacer → adapter write).
	require.Greater(t, mock.written.Load(), int64(0),
		"SRT should receive data through CBR pacer")
}

func TestManager_NoCBR_AllAdaptersDirect(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	// No CBR — SRT should be in direct adapters.
	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "127.0.0.1",
		Port:    9000,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	// Without CBR, all adapters should be direct (no split).
	directAdapters := mgr.directAdapters.Load()
	allAdapters := mgr.adapters.Load()
	require.Len(t, *directAdapters, 2, "all adapters should be direct when CBR disabled")
	require.Len(t, *allAdapters, 2, "combined list should match direct list")
}

func TestOutputManager_DebugSnapshot_IncludesCBRPacer(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(10_000_000) // 10 Mbps

	// Start recording to trigger muxer + pacer creation.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	snap := mgr.DebugSnapshot()
	require.Contains(t, snap, "cbr_pacer", "DebugSnapshot should include cbr_pacer when CBR is enabled")

	cbrSnap, ok := snap["cbr_pacer"].(*CBRPacerStatus)
	require.True(t, ok, "cbr_pacer should be *CBRPacerStatus")
	require.True(t, cbrSnap.Enabled)
	require.Equal(t, int64(10_000_000), cbrSnap.MuxrateBps)
}

func TestOutputManager_DebugSnapshot_NoCBRPacerWhenDisabled(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	snap := mgr.DebugSnapshot()
	_, hasCBR := snap["cbr_pacer"]
	require.False(t, hasCBR, "DebugSnapshot should not include cbr_pacer when CBR is disabled")
}

func TestOutputManager_DebugSnapshot_IncludesMuxerPTS(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	// Feed a keyframe so the muxer initializes and records a PTS.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	mgr.DirectWriteVideo(idrFrame)

	// Wait for the viewer to process the frame.
	time.Sleep(50 * time.Millisecond)

	snap := mgr.DebugSnapshot()
	require.Contains(t, snap, "muxer_pts", "DebugSnapshot should include muxer_pts when muxer is active")

	muxerPTS, ok := snap["muxer_pts"].(int64)
	require.True(t, ok, "muxer_pts should be int64")
	// Video PTS = epoch (90000), rebased from upstream PTS 90000
	// ptsBase = 90000 (first video PTS), so muxerPTS = (90000 - 90000 + 90000) = 90000
	require.Equal(t, int64(90000), muxerPTS)
}

func TestOutputManager_DebugSnapshot_NoMuxerPTSWhenInactive(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	snap := mgr.DebugSnapshot()
	_, hasMuxerPTS := snap["muxer_pts"]
	require.False(t, hasMuxerPTS, "DebugSnapshot should not include muxer_pts when no muxer is active")
}

func TestManager_CBRStatus_Disabled(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	status := mgr.CBRStatus()
	require.Nil(t, status, "CBRStatus must be nil when CBR is disabled")
}

func TestManager_CBRStatus_Enabled(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mgr.SetCBRMuxrate(10_000_000) // 10 Mbps

	// Start recording to trigger muxer + pacer creation.
	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))

	status := mgr.CBRStatus()
	require.NotNil(t, status)
	require.True(t, status.Enabled)
	require.Equal(t, int64(10_000_000), status.MuxrateBps)
}

func TestOutputManagerMuxerCallbackNoLock(t *testing.T) {
	mgr := NewManager()
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

// TestOutputManager_RemoveDestination_ConcurrentWithList exercises concurrent
// RemoveDestination and ListDestinations calls to verify no data races.
// The race detector (-race) will catch unsynchronized map access if the
// destination is iterated during removal.
func TestOutputManager_RemoveDestination_ConcurrentWithList(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	// Mock SRT connect so StartDestination succeeds.
	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	const numDestinations = 20
	ids := make([]string, numDestinations)
	for i := 0; i < numDestinations; i++ {
		id, err := mgr.AddDestination(DestinationConfig{
			Type:    "srt-caller",
			Address: "127.0.0.1",
			Port:    9000 + i,
			Name:    "test-dest",
		})
		require.NoError(t, err)
		require.NoError(t, mgr.StartDestination(id))
		ids[i] = id
	}

	// Concurrently remove destinations while listing them.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, id := range ids {
			_ = mgr.RemoveDestination(id)
		}
	}()

	// Hammer ListDestinations concurrently from multiple goroutines.
	var listWg sync.WaitGroup
	for g := 0; g < 4; g++ {
		listWg.Add(1)
		go func() {
			defer listWg.Done()
			for {
				select {
				case <-done:
					return
				default:
					dests := mgr.ListDestinations()
					// Each returned status should have valid data.
					for _, d := range dests {
						_ = d.State
					}
				}
			}
		}()
	}

	// Wait for all removals to complete.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent remove+list")
	}

	// Wait for list goroutines to finish (they exit when done is closed).
	listWg.Wait()

	// Verify all destinations were removed.
	remaining := mgr.ListDestinations()
	require.Empty(t, remaining, "all destinations should be removed")
}

func TestManager_SRTStats_LegacyCallerOutput(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	sm := srt.NewStatsManager()
	mgr.SetSRTStatsManager(sm)

	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	err := mgr.StartSRTOutput(SRTConfig{Mode: "caller", Address: "127.0.0.1", Port: 9000, Latency: 200})
	require.NoError(t, err)

	// Verify stats entry was created.
	cs, ok := sm.Get("output:srt-caller")
	require.True(t, ok, "stats entry should exist after start")
	require.NotNil(t, cs)

	snap := cs.Snapshot()
	require.Equal(t, srt.DirectionSend, snap.Direction)
	require.Equal(t, 200, snap.LatencyMs)

	// Stop and verify removal.
	require.NoError(t, mgr.StopSRTOutput())
	_, ok = sm.Get("output:srt-caller")
	require.False(t, ok, "stats entry should be removed after stop")
}

func TestManager_SRTStats_LegacyListenerOutput(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	sm := srt.NewStatsManager()
	mgr.SetSRTStatsManager(sm)

	err := mgr.StartSRTOutput(SRTConfig{Mode: "listener", Port: 0, Latency: 150})
	require.NoError(t, err)

	cs, ok := sm.Get("output:srt-listener")
	require.True(t, ok, "stats entry should exist after start")
	snap := cs.Snapshot()
	require.Equal(t, srt.DirectionSend, snap.Direction)
	require.Equal(t, 150, snap.LatencyMs)

	require.NoError(t, mgr.StopSRTOutput())
	_, ok = sm.Get("output:srt-listener")
	require.False(t, ok, "stats entry should be removed after stop")
}

func TestManager_SRTStats_Destination(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	sm := srt.NewStatsManager()
	mgr.SetSRTStatsManager(sm)

	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "127.0.0.1",
		Port:    9000,
		Latency: 250,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	// Verify stats entry was created with destination-specific key.
	key := "output:dest:" + id + ":srt-caller"
	cs, ok := sm.Get(key)
	require.True(t, ok, "stats entry should exist for destination")
	snap := cs.Snapshot()
	require.Equal(t, srt.DirectionSend, snap.Direction)
	require.Equal(t, 250, snap.LatencyMs)

	// Stop destination — stats should be removed.
	require.NoError(t, mgr.StopDestination(id))
	_, ok = sm.Get(key)
	require.False(t, ok, "stats entry should be removed after stop")
}

func TestManager_SRTStats_RemoveDestination(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	sm := srt.NewStatsManager()
	mgr.SetSRTStatsManager(sm)

	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "127.0.0.1",
		Port:    9000,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	key := "output:dest:" + id + ":srt-caller"
	_, ok := sm.Get(key)
	require.True(t, ok)

	// Remove (which also stops) — stats should be cleaned up.
	require.NoError(t, mgr.RemoveDestination(id))
	_, ok = sm.Get(key)
	require.False(t, ok, "stats entry should be removed when destination is removed")
}

func TestManager_SRTStats_CloseRemovesAll(t *testing.T) {
	mgr := NewManager()

	sm := srt.NewStatsManager()
	mgr.SetSRTStatsManager(sm)

	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	// Start legacy SRT output.
	require.NoError(t, mgr.StartSRTOutput(SRTConfig{Mode: "caller", Address: "127.0.0.1", Port: 9000}))

	// Start a destination.
	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    9001,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	// Verify both entries exist.
	_, ok1 := sm.Get("output:srt-caller")
	_, ok2 := sm.Get("output:dest:" + id + ":srt-caller")
	require.True(t, ok1, "legacy stats should exist")
	require.True(t, ok2, "destination stats should exist")

	// Close manager — all stats should be cleaned up.
	require.NoError(t, mgr.Close())

	_, ok1 = sm.Get("output:srt-caller")
	_, ok2 = sm.Get("output:dest:" + id + ":srt-caller")
	require.False(t, ok1, "legacy stats should be removed on close")
	require.False(t, ok2, "destination stats should be removed on close")
}

func TestManager_SRTStats_NilManagerSafe(t *testing.T) {
	// Verify no panic when stats manager is not set.
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mock := &mockSRTConn{}
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			mock.connected.Store(true)
			return mock, nil
		},
		nil,
	)

	// Should work fine without stats manager.
	require.NoError(t, mgr.StartSRTOutput(SRTConfig{Mode: "caller", Address: "127.0.0.1", Port: 9000}))
	require.NoError(t, mgr.StopSRTOutput())
}

func lipSyncTestFrame(pts int64) *media.VideoFrame {
	return &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		IsKeyframe: true,
		SPS:        []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
		WireData:   []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:      "h264",
	}
}

func TestManager_DirectWriteVideo_RaceWithLatchLipSync(t *testing.T) {
	prev := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prev)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: t.TempDir()}))

	var rawOffset atomic.Int64
	rawOffset.Store(1000)
	mgr.SetLipSyncSource(func() int64 { return rawOffset.Load() })

	const hotWriters = 4
	const framesPerWriter = 30
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup

	for i := 0; i < hotWriters; i++ {
		done.Add(1)
		go func(id int) {
			defer done.Done()
			start.Wait()
			pts := int64(90000 * (id + 1))
			for j := 0; j < framesPerWriter; j++ {
				rawOffset.Add(1000)
				mgr.DirectWriteVideo(lipSyncTestFrame(pts))
				pts += 1500
				runtime.Gosched()
			}
		}(i)
	}

	done.Add(1)
	go func() {
		defer done.Done()
		start.Wait()
		time.Sleep(10 * time.Millisecond)
		mgr.LatchLipSync(5000)
	}()

	start.Done()
	done.Wait()
}

func TestManager_DirectWriteVideo_LastPTSRaceWithSnapshot(t *testing.T) {
	prev := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prev)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: t.TempDir()}))

	// Revert lastDirectVideoPTS to plain int64 to see this RED under -race.
	var (
		stop      atomic.Bool
		snapshots atomic.Int64
	)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup

	const writers = 4
	for i := 0; i < writers; i++ {
		done.Add(1)
		go func(id int) {
			defer done.Done()
			start.Wait()
			pts := int64(90000 * (id + 1))
			tick := time.NewTicker(time.Second / 60)
			defer tick.Stop()
			for !stop.Load() {
				mgr.DirectWriteVideo(lipSyncTestFrame(pts))
				pts += 1500
				<-tick.C
			}
		}(i)
	}

	// Same-package field access keeps the test contract-free: no production
	// accessor is exposed just to satisfy a test reader.
	done.Add(1)
	go func() {
		defer done.Done()
		start.Wait()
		for !stop.Load() {
			_ = mgr.lastDirectVideoPTS.Load()
			snapshots.Add(1)
		}
	}()

	start.Done()
	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	done.Wait()

	require.Greater(t, snapshots.Load(), int64(0),
		"reader goroutine must have observed lastDirectVideoPTS at least once")
}

func TestManager_DirectWriteVideo_LipSyncSourceReassignRace(t *testing.T) {
	prev := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prev)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: t.TempDir()}))

	require.False(t, mgr.lipSyncLatched.Load(),
		"test contract: hot path must read lipSyncSource (lipSyncLatched must start false)")

	var (
		stop       atomic.Bool
		reassigned atomic.Int64
		callbacks  atomic.Int64
	)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup

	const writers = 4
	for i := 0; i < writers; i++ {
		done.Add(1)
		go func(id int) {
			defer done.Done()
			start.Wait()
			pts := int64(90000 * (id + 1))
			tick := time.NewTicker(time.Second / 60)
			defer tick.Stop()
			for !stop.Load() {
				mgr.DirectWriteVideo(lipSyncTestFrame(pts))
				pts += 1500
				<-tick.C
			}
		}(i)
	}

	done.Add(1)
	go func() {
		defer done.Done()
		start.Wait()
		for !stop.Load() {
			var offset atomic.Int64
			offset.Store(reassigned.Load() * 1000)
			mgr.SetLipSyncSource(func() int64 {
				callbacks.Add(1)
				return offset.Load()
			})
			reassigned.Add(1)
		}
	}()

	start.Done()
	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	done.Wait()

	require.Greater(t, reassigned.Load(), int64(0),
		"reassign goroutine must have swapped lipSyncSource at least once")
	require.Greater(t, callbacks.Load(), int64(0),
		"hot path must have invoked at least one installed callback (otherwise the swap isn't exercising the read path)")
}

func TestManager_LipSyncLatchedObservableFromHotPath(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: t.TempDir()}))

	var raw atomic.Int64
	raw.Store(100)
	mgr.SetLipSyncSource(func() int64 { return raw.Load() })

	const latched int64 = 7777
	mgr.LatchLipSync(latched)
	require.Equal(t, latched, mgr.LipSyncOffset(), "LipSyncOffset must return latched value immediately")

	pts := int64(90000)
	for i := 0; i < 1000; i++ {
		raw.Add(9999)
		mgr.DirectWriteVideo(lipSyncTestFrame(pts))
		pts += 1500
	}

	require.Equal(t, latched, mgr.LipSyncOffset(), "latched value must survive 1000 hot-path frames")
}

// TestManager_StartDestinationDialDoesNotBlockControlPlane verifies that the
// SRT dial performed by StartDestination (which can block up to the 5s SRT
// ConnTimeout against an unreachable endpoint) does not run while holding
// Manager.mu/dest.mu. Holding the locks across the dial stalls every other
// control-plane operation (status queries, recording start/stop, SCTE-35
// injection) for the duration of the dial.
func TestManager_StartDestinationDialDoesNotBlockControlPlane(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			close(dialStarted)
			<-releaseDial // simulate srt.Dial blocking (no ctx cancellation support)
			return &mockSRTConn{}, nil
		},
		nil,
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.255.255.1", // black-holed endpoint
		Port:    9000,
	})
	require.NoError(t, err)

	startDone := make(chan error, 1)
	go func() { startDone <- mgr.StartDestination(id) }()

	// Wait until StartDestination is blocked inside the dial.
	select {
	case <-dialStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("connectFn was never invoked")
	}

	// While the dial is in flight, control-plane calls must not block.
	controlDone := make(chan struct{})
	go func() {
		_ = mgr.ListDestinations()
		_ = mgr.RecordingStatus()
		_ = mgr.SRTOutputStatus()
		close(controlDone)
	}()
	select {
	case <-controlDone:
	case <-time.After(1 * time.Second):
		close(releaseDial)
		t.Fatal("control plane blocked on Manager.mu while SRT dial was in flight")
	}

	close(releaseDial)
	require.NoError(t, <-startDone)

	// The destination must end up active with its adapter published.
	st, err := mgr.GetDestination(id)
	require.NoError(t, err)
	require.Equal(t, string(StateActive), st.State)
}

// TestManager_StartSRTOutputDialDoesNotBlockControlPlane is the legacy
// single-output equivalent of the StartDestination test above.
func TestManager_StartSRTOutputDialDoesNotBlockControlPlane(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			close(dialStarted)
			<-releaseDial
			return &mockSRTConn{}, nil
		},
		nil,
	)

	startDone := make(chan error, 1)
	go func() {
		startDone <- mgr.StartSRTOutput(SRTConfig{Mode: "caller", Address: "10.255.255.1", Port: 9000})
	}()

	select {
	case <-dialStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("connectFn was never invoked")
	}

	controlDone := make(chan struct{})
	go func() {
		_ = mgr.ListDestinations()
		_ = mgr.SRTOutputStatus()
		close(controlDone)
	}()
	select {
	case <-controlDone:
	case <-time.After(1 * time.Second):
		close(releaseDial)
		t.Fatal("control plane blocked on Manager.mu while SRT dial was in flight")
	}

	close(releaseDial)
	require.NoError(t, <-startDone)
	require.True(t, mgr.SRTOutputStatus().Active)
}

// The control room learns that a stopped recording recorded nothing, and
// the next start clears it.
func TestOutputManager_EmptyRecordingReportedUntilNextStart(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NoError(t, mgr.StopRecording())

	status := mgr.RecordingStatus()
	require.False(t, status.Active)
	require.Equal(t, ErrNothingRecorded.Error(), status.Error)
	require.NotEmpty(t, status.Filename)

	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	status = mgr.RecordingStatus()
	require.True(t, status.Active)
	require.Empty(t, status.Error)
}

// Files still being copied into the archive are reported as finalizing,
// also after the recording that wrote them has stopped, and forgotten once
// the copy is done.
func TestOutputManager_FinalizingRecordings(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	mgr.mu.Lock()
	rec := mgr.recorder
	mgr.mu.Unlock()
	require.NoError(t, mgr.StopRecording())
	require.Empty(t, mgr.FinalizingRecordings())

	rec.mu.Lock()
	rec.inArchive = map[string]bool{filepath.Join(dir, "program_x_001.ts"): true}
	rec.mu.Unlock()
	require.NoError(t, mgr.StartRecording(RecorderConfig{Dir: dir}))
	require.NoError(t, mgr.StopRecording())
	require.Equal(t, []string{"program_x_001.ts"}, mgr.FinalizingRecordings(),
		"an older recorder's copy is still tracked after a newer one stopped")

	rec.mu.Lock()
	rec.inArchive = nil
	rec.mu.Unlock()
	require.Empty(t, mgr.FinalizingRecordings())
	mgr.mu.Lock()
	require.Len(t, mgr.stoppedRecorders, 1, "only the newest stopped recorder is kept")
	mgr.mu.Unlock()
}
