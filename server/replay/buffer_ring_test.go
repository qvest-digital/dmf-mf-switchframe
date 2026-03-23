package replay

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestReplayBufferWithRing_StoresDataInRing(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "test.buf"), 1024*1024)
	require.NoError(t, err)
	defer func() { _ = ring.CloseAndRemove() }()

	buf := newReplayBufferWithRing(60, ring)

	frame := makeVideoFrame(0, true, 1000)
	now := time.Now()
	buf.recordFrameAt(frame, now)

	// Verify the bufferedFrame has nil wireData (data is in the ring).
	buf.mu.RLock()
	require.Len(t, buf.frames, 1)
	require.Nil(t, buf.frames[0].wireData,
		"wireData should be nil when ring is active — data lives in the file ring")
	require.Greater(t, buf.frames[0].ringSize, int32(0),
		"ringSize should be set for ring-backed frames")
	// SPS/PPS should still be on the Go heap.
	require.NotNil(t, buf.frames[0].sps, "SPS should still be on heap")
	require.NotNil(t, buf.frames[0].pps, "PPS should still be on heap")
	buf.mu.RUnlock()

	// Verify ring has the data.
	require.Equal(t, 1, ring.FrameCount())
	require.Greater(t, ring.BytesUsed(), int64(0))
}

func TestReplayBufferWithRing_ExtractClipReadsFromRing(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "test.buf"), 1024*1024)
	require.NoError(t, err)
	defer func() { _ = ring.CloseAndRemove() }()

	buf := newReplayBufferWithRing(60, ring)
	now := time.Now()

	// Record a small GOP.
	kf := makeVideoFrame(0, true, 500)
	buf.recordFrameAt(kf, now)
	df1 := makeVideoFrame(3003, false, 200)
	buf.recordFrameAt(df1, now.Add(33*time.Millisecond))
	df2 := makeVideoFrame(6006, false, 200)
	buf.recordFrameAt(df2, now.Add(66*time.Millisecond))

	// Extract the clip.
	clip, _, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)
	require.Len(t, clip, 3)

	// Verify the extracted clip has actual wireData (read from ring).
	require.NotNil(t, clip[0].wireData, "extracted clip should have wireData read from ring")
	require.Len(t, clip[0].wireData, 500)
	require.True(t, clip[0].isKeyframe)

	// Verify data integrity: the extracted wireData should match the original.
	for i, b := range clip[0].wireData {
		require.Equal(t, byte(i%256), b,
			"extracted wireData byte %d mismatch", i)
	}

	// Verify SPS/PPS on the keyframe.
	require.NotNil(t, clip[0].sps)
	require.NotNil(t, clip[0].pps)

	// Verify delta frames.
	require.NotNil(t, clip[1].wireData)
	require.Len(t, clip[1].wireData, 200)
	require.False(t, clip[1].isKeyframe)
}

func TestReplayBufferWithRing_MultipleGOPs(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "test.buf"), 1024*1024)
	require.NoError(t, err)
	defer func() { _ = ring.CloseAndRemove() }()

	buf := newReplayBufferWithRing(60, ring)
	now := time.Now()

	// Record 3 GOPs.
	for g := 0; g < 3; g++ {
		kf := makeVideoFrame(int64(g)*90000, true, 1000)
		buf.recordFrameAt(kf, now.Add(time.Duration(g)*time.Second))
		for j := 1; j <= 2; j++ {
			df := makeVideoFrame(int64(g)*90000+int64(j)*3003, false, 500)
			buf.recordFrameAt(df, now.Add(time.Duration(g)*time.Second+time.Duration(j)*33*time.Millisecond))
		}
	}

	info := buf.Status()
	require.Equal(t, 9, info.FrameCount)
	require.Equal(t, 3, info.GOPCount)
	require.Greater(t, info.BytesUsed, int64(0), "BytesUsed should include ring data")
}

func TestReplayBufferWithRing_TimeTrimming(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "test.buf"), 1024*1024)
	require.NoError(t, err)
	defer ring.CloseAndRemove()

	// 1 second buffer to force trimming.
	buf := newReplayBufferWithRing(1, ring)
	now := time.Now()

	// Record GOPs spaced 500ms apart — will exceed 1s window.
	for g := 0; g < 4; g++ {
		kf := makeVideoFrame(int64(g)*90000, true, 1000)
		buf.recordFrameAt(kf, now.Add(time.Duration(g)*500*time.Millisecond))
		for j := 1; j <= 2; j++ {
			df := makeVideoFrame(int64(g)*90000+int64(j)*3003, false, 500)
			buf.recordFrameAt(df, now.Add(time.Duration(g)*500*time.Millisecond+time.Duration(j)*33*time.Millisecond))
		}
	}

	info := buf.Status()
	require.Less(t, info.GOPCount, 4, "expected fewer than 4 GOPs after time-based trimming")
	require.GreaterOrEqual(t, info.GOPCount, 1)

	// First frame should still be a keyframe.
	buf.mu.RLock()
	if len(buf.frames) > 0 {
		require.True(t, buf.frames[0].isKeyframe,
			"first frame after trim should be a keyframe")
	}
	buf.mu.RUnlock()
}

func TestReplayBufferWithRing_AudioUnchanged(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "test.buf"), 1024*1024)
	require.NoError(t, err)
	defer ring.CloseAndRemove()

	buf := newReplayBufferWithRing(60, ring)
	now := time.Now()

	// Record a keyframe (required for audio).
	buf.recordFrameAt(makeVideoFrame(0, true, 100), now)

	// Record audio — should go directly to Go heap, not the ring.
	audioData := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	af := &media.AudioFrame{
		PTS:        1000,
		Data:       audioData,
		SampleRate: 48000,
		Channels:   2,
	}
	buf.recordAudioFrameAt(af, now.Add(10*time.Millisecond))

	// Extract and verify audio data.
	_, audioClip, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)
	require.Len(t, audioClip, 1)
	require.Equal(t, audioData, audioClip[0].data)
}

func TestReplayBufferWithRing_InMemoryFallbackUnchanged(t *testing.T) {
	// Verify that in-memory path (ring == nil) still works identically.
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	frame := makeVideoFrame(0, true, 500)
	buf.recordFrameAt(frame, now)

	buf.mu.RLock()
	require.NotNil(t, buf.frames[0].wireData,
		"in-memory path should store wireData on heap")
	require.Equal(t, int32(0), buf.frames[0].ringSize,
		"in-memory path should have zero ringSize")
	buf.mu.RUnlock()

	clip, _, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)
	require.Len(t, clip, 1)
	require.Len(t, clip[0].wireData, 500)
}

func TestReplayBufferWithRing_DeepCopyOnExtract(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "test.buf"), 1024*1024)
	require.NoError(t, err)
	defer ring.CloseAndRemove()

	buf := newReplayBufferWithRing(60, ring)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 100), now)

	clip1, _, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)

	clip2, _, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)

	// Mutate clip1's wireData.
	clip1[0].wireData[0] = 0xFF

	// clip2 should be unaffected (separate reads from ring).
	require.NotEqual(t, byte(0xFF), clip2[0].wireData[0],
		"extracted clips should be independent copies")
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"cam1", "cam1"},
		{"mxl:camera1", "mxl_camera1"},
		{"srt:live/camera1", "srt_live_camera1"},
		{"path\\with\\backslash", "path_with_backslash"},
		{"combo:path/back\\slash", "combo_path_back_slash"},
	}
	for _, tc := range tests {
		got := sanitizeKey(tc.input)
		require.Equal(t, tc.expected, got, "sanitizeKey(%q)", tc.input)
	}
}

func TestDetectTmpfsDir_ValidPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "replay")

	path, ok := detectTmpfsDir(target)
	require.True(t, ok)
	require.Equal(t, target, path)

	// Directory should exist.
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestDetectTmpfsDir_InvalidPath(t *testing.T) {
	_, ok := detectTmpfsDir("/nonexistent/impossible/path/replay")
	require.False(t, ok)
}

func TestManager_RemoveSource_CleansUpRing(t *testing.T) {
	dir := t.TempDir()
	relay := &mockRelay{}
	cfg := DefaultConfig()
	cfg.TmpfsDir = dir

	m := NewManager(relay, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Verify ring file exists.
	ringPath := filepath.Join(dir, "cam1.buf")
	_, err = os.Stat(ringPath)
	require.NoError(t, err, "ring file should exist after AddSource")

	// Record some frames so the ring has data.
	m.mu.Lock()
	buf := m.buffers["cam1"]
	m.mu.Unlock()
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 100), now)

	// Remove and verify cleanup.
	m.RemoveSource("cam1")

	_, err = os.Stat(ringPath)
	require.Error(t, err, "ring file should be removed after RemoveSource")
}

func TestManager_TmpfsDisabled(t *testing.T) {
	relay := &mockRelay{}
	cfg := DefaultConfig()
	cfg.TmpfsDir = "none"

	m := NewManager(relay, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.AddSource("cam1")
	require.NoError(t, err)

	// Buffer should be in-memory (no ring).
	m.mu.Lock()
	buf := m.buffers["cam1"]
	m.mu.Unlock()
	require.Nil(t, buf.ring, "ring should be nil when tmpfs is disabled")
}

func TestManager_TmpfsFallbackOnError(t *testing.T) {
	relay := &mockRelay{}
	cfg := DefaultConfig()
	cfg.TmpfsDir = "/nonexistent/impossible/path"

	m := NewManager(relay, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// tmpfsDir should be empty (detection failed).
	require.Empty(t, m.tmpfsDir, "tmpfsDir should be empty when detection fails")

	// AddSource should still work (in-memory fallback).
	err := m.AddSource("cam1")
	require.NoError(t, err)

	m.mu.Lock()
	buf := m.buffers["cam1"]
	m.mu.Unlock()
	require.Nil(t, buf.ring, "ring should be nil when tmpfs is unavailable")
}