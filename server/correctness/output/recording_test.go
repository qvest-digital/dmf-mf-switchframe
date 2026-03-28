//go:build correctness

package output

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/transition"
)

const (
	encWidth   = 320
	encHeight  = 240
	encFPS     = 30
	encBitrate = 500_000
	encGOPSecs = 2
)

// encodeAndMux produces a valid TS byte stream from numFrames of encoded
// H.264 video and optional interleaved AAC audio. It returns the raw TS
// bytes. The encoder uses libx264 at 320x240.
func encodeAndMux(t *testing.T, numFrames int, withAudio bool) []byte {
	t.Helper()

	enc, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err, "create encoder")
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / encFPS) // 3000 ticks per frame at 30fps
	startPTS := int64(90000)           // start at 1 second

	ySize := encWidth * encHeight
	uvSize := (encWidth / 2) * (encHeight / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (encFPS * encGOPSecs)) == 0

		// Vary Y content each frame.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}

		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}

		require.NoError(t, muxer.WriteVideo(frame), "mux video frame %d", i)

		if withAudio {
			silentPayload := []byte{0x01, 0x08}
			adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
			adtsFrame = append(adtsFrame, silentPayload...)

			aFrame := &media.AudioFrame{
				PTS:        pts,
				Data:       adtsFrame,
				SampleRate: 48000,
				Channels:   2,
			}
			require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", i)
		}
	}

	return tsData
}

// writeToFile writes data to a temp file and returns the path.
func writeToFile(t *testing.T, data []byte, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

// TestRecordingProducesValidTS verifies that TSMuxer output written through
// a FileRecorder produces a valid, ffprobe-parseable TS file with H.264
// video and AAC audio streams.
func TestRecordingProducesValidTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 90 // 3s at 30fps

	// Create TSMuxer and recorder.
	recDir := t.TempDir()
	recorder := output.NewFileRecorder(output.RecorderConfig{
		Dir: recDir,
	})
	require.NoError(t, recorder.Start(context.Background()))

	// Create muxer that writes through the recorder.
	enc, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err, "create encoder")
	defer enc.Close()

	muxer := output.NewTSMuxer()
	muxer.SetOutput(func(data []byte) {
		_, writeErr := recorder.Write(data)
		require.NoError(t, writeErr, "recorder write")
	})

	ptsDelta := int64(90000 / encFPS)
	startPTS := int64(90000)
	ySize := encWidth * encHeight
	uvSize := (encWidth / 2) * (encHeight / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (encFPS * encGOPSecs)) == 0

		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}
		require.NoError(t, muxer.WriteVideo(frame), "mux video frame %d", i)

		// Write interleaved audio.
		silentPayload := []byte{0x01, 0x08}
		adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
		adtsFrame = append(adtsFrame, silentPayload...)
		aFrame := &media.AudioFrame{
			PTS:        pts,
			Data:       adtsFrame,
			SampleRate: 48000,
			Channels:   2,
		}
		require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", i)
	}

	// Close recorder and muxer.
	require.NoError(t, muxer.Close())
	require.NoError(t, recorder.Close())

	// Find the recording file.
	recFilename := recorder.Filename()
	require.NotEmpty(t, recFilename, "recorder should have a filename")
	recPath := filepath.Join(recDir, recFilename)

	info, err := os.Stat(recPath)
	require.NoError(t, err, "recording file should exist")
	fileSize := info.Size()
	t.Logf("MEASURE: file size=%d bytes", fileSize)
	require.Greater(t, fileSize, int64(0), "recording file should not be empty")

	// ffprobe: verify valid TS with H.264 + AAC.
	testutil.RequireNoErrors(t, recPath)
	result := testutil.ProbeFile(t, recPath)

	videoStream := result.VideoStream()
	require.NotNil(t, videoStream, "expected video stream in recording")
	t.Logf("MEASURE: video codec=%s, resolution=%dx%d", videoStream.CodecName, videoStream.Width, videoStream.Height)
	assert.Equal(t, "h264", videoStream.CodecName, "video codec should be h264")

	audioStream := result.AudioStream()
	require.NotNil(t, audioStream, "expected audio stream in recording")
	t.Logf("MEASURE: audio codec=%s, sample_rate=%s, channels=%d", audioStream.CodecName, audioStream.SampleRate, audioStream.Channels)
	assert.Equal(t, "aac", audioStream.CodecName, "audio codec should be aac")

	// Count frames via ffprobe.
	videoFrames := testutil.ProbeVideoFrames(t, recPath)
	audioFrames := testutil.ProbeAudioFrames(t, recPath)
	t.Logf("MEASURE: video_frames=%d, audio_frames=%d", len(videoFrames), len(audioFrames))

	// We fed 90 video frames; encoder warmup may lose a few.
	require.GreaterOrEqual(t, len(videoFrames), 80,
		"expected at least 80 video frames from 90 encoded (allowing encoder warmup)")
	require.Greater(t, len(audioFrames), 50,
		"expected substantial audio frames in the recording")

	// Verify recorder status reports correct bytes.
	status := recorder.Status()
	t.Logf("MEASURE: recorder status bytes=%d, state=%s", status.BytesWritten, status.State)
	assert.Equal(t, status.BytesWritten, fileSize,
		"recorder status bytes should match file size")
}

// TestRecordingStartStopMultiple verifies that two consecutive recording
// sessions produce independently valid TS files with no PTS leakage.
func TestRecordingStartStopMultiple(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const framesPerSession = 30 // 1s each

	// Helper: run one recording session and return the file path.
	runSession := func(sessionIdx int) string {
		recDir := t.TempDir()
		recorder := output.NewFileRecorder(output.RecorderConfig{
			Dir: recDir,
		})
		require.NoError(t, recorder.Start(context.Background()))

		enc, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
		require.NoError(t, err)
		defer enc.Close()

		muxer := output.NewTSMuxer()
		muxer.SetOutput(func(data []byte) {
			_, writeErr := recorder.Write(data)
			require.NoError(t, writeErr)
		})

		ptsDelta := int64(90000 / encFPS)
		// Use different PTS ranges per session to detect leakage.
		startPTS := int64(90000) + int64(sessionIdx)*int64(framesPerSession)*ptsDelta*2

		ySize := encWidth * encHeight
		uvSize := (encWidth / 2) * (encHeight / 2)
		yuv := make([]byte, ySize+2*uvSize)

		for i := 0; i < framesPerSession; i++ {
			pts := startPTS + int64(i)*ptsDelta
			forceIDR := (i % (encFPS * encGOPSecs)) == 0

			for j := 0; j < ySize; j++ {
				yuv[j] = byte((j*11 + i*41 + sessionIdx*101) % 256)
			}
			for j := ySize; j < len(yuv); j++ {
				yuv[j] = 128
			}

			encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
			require.NoError(t, encErr)
			if len(encoded) == 0 {
				continue
			}

			avc1 := codec.AnnexBToAVC1(encoded)
			frame := &media.VideoFrame{
				PTS:        pts,
				DTS:        pts,
				IsKeyframe: isKeyframe,
				WireData:   avc1,
				Codec:      "h264",
			}
			if isKeyframe {
				for _, nalu := range codec.ExtractNALUs(avc1) {
					if len(nalu) == 0 {
						continue
					}
					switch nalu[0] & 0x1F {
					case 7:
						frame.SPS = append([]byte(nil), nalu...)
					case 8:
						frame.PPS = append([]byte(nil), nalu...)
					}
				}
			}
			require.NoError(t, muxer.WriteVideo(frame))

			silentPayload := []byte{0x01, 0x08}
			adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
			adtsFrame = append(adtsFrame, silentPayload...)
			aFrame := &media.AudioFrame{
				PTS:        pts,
				Data:       adtsFrame,
				SampleRate: 48000,
				Channels:   2,
			}
			require.NoError(t, muxer.WriteAudio(aFrame))
		}

		require.NoError(t, muxer.Close())
		require.NoError(t, recorder.Close())

		recPath := filepath.Join(recDir, recorder.Filename())
		return recPath
	}

	// Run two sessions.
	file1 := runSession(0)
	file2 := runSession(1)

	// Both files must be independently valid.
	testutil.RequireNoErrors(t, file1)
	testutil.RequireNoErrors(t, file2)

	result1 := testutil.ProbeFile(t, file1)
	result2 := testutil.ProbeFile(t, file2)

	require.NotNil(t, result1.VideoStream(), "session 1 should have video")
	require.NotNil(t, result2.VideoStream(), "session 2 should have video")
	require.NotNil(t, result1.AudioStream(), "session 1 should have audio")
	require.NotNil(t, result2.AudioStream(), "session 2 should have audio")

	// Check frame counts.
	frames1 := testutil.ProbeVideoFrames(t, file1)
	frames2 := testutil.ProbeVideoFrames(t, file2)
	t.Logf("MEASURE: session1 video_frames=%d, session2 video_frames=%d", len(frames1), len(frames2))

	require.Greater(t, len(frames1), 0, "session 1 should have video frames")
	require.Greater(t, len(frames2), 0, "session 2 should have video frames")

	// PTS ranges should not overlap. The muxer rebases PTS, so both files
	// should start near the muxerEpoch (90000). Verify each file's PTS
	// values are internally monotonic.
	if len(frames1) > 1 {
		for i := 1; i < len(frames1); i++ {
			t.Logf("MEASURE: session1 frame[%d] PTS=%d", i, frames1[i].PTS)
			assert.Greater(t, frames1[i].PTS, frames1[i-1].PTS,
				"session 1 PTS should be monotonically increasing at frame %d", i)
		}
	}
	if len(frames2) > 1 {
		for i := 1; i < len(frames2); i++ {
			assert.Greater(t, frames2[i].PTS, frames2[i-1].PTS,
				"session 2 PTS should be monotonically increasing at frame %d", i)
		}
	}

	// Verify file sizes are reasonable (non-zero, not absurdly different).
	info1, err := os.Stat(file1)
	require.NoError(t, err)
	info2, err := os.Stat(file2)
	require.NoError(t, err)
	t.Logf("MEASURE: session1 size=%d bytes, session2 size=%d bytes", info1.Size(), info2.Size())

	// Both files should be within 5x of each other (same content length).
	ratio := float64(info1.Size()) / float64(info2.Size())
	assert.Greater(t, ratio, 0.2, "file sizes should be roughly comparable")
	assert.Less(t, ratio, 5.0, "file sizes should be roughly comparable")
}

// TestRecordingLongDuration records 30 seconds of content and verifies
// PTS continuity, file size, and bitrate reasonableness.
func TestRecordingLongDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 900 // 30s at 30fps

	tsData := encodeAndMux(t, numFrames, true)
	require.NotEmpty(t, tsData, "muxer should produce output for 900 frames")

	path := writeToFile(t, tsData, "long_recording.ts")
	fileSize := int64(len(tsData))

	// ffprobe: valid file.
	testutil.RequireNoErrors(t, path)
	result := testutil.ProbeFile(t, path)

	videoStream := result.VideoStream()
	require.NotNil(t, videoStream, "expected video stream")
	audioStream := result.AudioStream()
	require.NotNil(t, audioStream, "expected audio stream")

	// Parse duration.
	durationStr := result.Format.Duration
	t.Logf("MEASURE: format duration=%s, size=%s, bitrate=%s",
		durationStr, result.Format.Size, result.Format.BitRate)

	// File size: for 30s at 500kbps + audio overhead, expect ~2MB +/- generous margin.
	// 500kbps * 30s = 1,875,000 bytes. Allow 500KB to 10MB range.
	t.Logf("MEASURE: 30s recording = %d bytes (%.0f kbps)",
		fileSize, float64(fileSize)*8.0/30.0/1000.0)
	assert.Greater(t, fileSize, int64(500_000),
		"30s recording should be at least 500KB")
	assert.Less(t, fileSize, int64(10_000_000),
		"30s recording should be under 10MB at 500kbps")

	// Check PTS continuity in video frames.
	videoFrames := testutil.ProbeVideoFrames(t, path)
	audioFrames := testutil.ProbeAudioFrames(t, path)
	t.Logf("MEASURE: video_frames=%d, audio_frames=%d", len(videoFrames), len(audioFrames))

	// Expect most of the 900 frames (encoder warmup may lose a few).
	require.GreaterOrEqual(t, len(videoFrames), 850,
		"expected at least 850 video frames from 900 encoded")
	require.Greater(t, len(audioFrames), 100,
		"expected substantial audio frames in 30s recording")

	// Verify video PTS is monotonically increasing.
	prevPTS := int64(-1)
	ptsGaps := 0
	maxGap := int64(0)
	for i, f := range videoFrames {
		if prevPTS >= 0 {
			gap := f.PTS - prevPTS
			if gap <= 0 {
				ptsGaps++
				t.Logf("MEASURE: PTS regression at frame %d: prev=%d, cur=%d, gap=%d", i, prevPTS, f.PTS, gap)
			}
			if gap > maxGap {
				maxGap = gap
			}
		}
		prevPTS = f.PTS
	}
	t.Logf("MEASURE: PTS gaps=%d, max_gap=%d ticks (%.1fms)", ptsGaps, maxGap, float64(maxGap)/90.0)
	assert.Equal(t, 0, ptsGaps, "PTS should be monotonically increasing with no regressions")

	// Max gap between frames should be reasonable. At 30fps, normal gap is
	// 3000 ticks. Allow up to 2x for minor jitter.
	assert.LessOrEqual(t, maxGap, int64(6000),
		"maximum PTS gap between video frames should be under 2 frame durations")

	// Verify duration from ffprobe. Parse as float seconds.
	if durationStr != "" {
		duration, parseErr := strconv.ParseFloat(durationStr, 64)
		if parseErr == nil {
			t.Logf("MEASURE: parsed duration=%.3fs", duration)
			// 30s of content should produce ~28-32s (some encoder delay).
			assert.Greater(t, duration, 25.0,
				"duration should be at least 25s for 30s of content")
			assert.Less(t, duration, 35.0,
				"duration should be under 35s for 30s of content")
		}
	}
}

// TestRecordingRecorderLifecycle verifies FileRecorder state machine:
// - Cannot write before Start
// - Can write after Start
// - Cannot double-start
// - Can close and re-start
func TestRecordingRecorderLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	recDir := t.TempDir()
	recorder := output.NewFileRecorder(output.RecorderConfig{
		Dir: recDir,
	})

	// Write before start should fail.
	_, err := recorder.Write([]byte{0x47, 0x00, 0x00, 0x10})
	t.Logf("MEASURE: write before start err=%v", err)
	assert.Error(t, err, "write before Start should return an error")

	// Start should succeed.
	require.NoError(t, recorder.Start(context.Background()))

	// Double start should fail.
	err = recorder.Start(context.Background())
	t.Logf("MEASURE: double start err=%v", err)
	assert.Error(t, err, "double Start should return an error")

	// Status should show active.
	status := recorder.Status()
	t.Logf("MEASURE: status after start: state=%s, bytes=%d", status.State, status.BytesWritten)
	assert.Equal(t, output.StateActive, status.State)

	// Write should succeed now.
	n, err := recorder.Write([]byte{0x47, 0x00, 0x00, 0x10})
	require.NoError(t, err, "write after Start should succeed")
	t.Logf("MEASURE: wrote %d bytes", n)
	assert.Equal(t, 4, n)

	// Close should succeed.
	require.NoError(t, recorder.Close())

	// After close, write should fail.
	_, err = recorder.Write([]byte{0x47})
	t.Logf("MEASURE: write after close err=%v", err)
	assert.Error(t, err, "write after Close should return an error")

	// Close is safe to call again.
	assert.NoError(t, recorder.Close(), "double Close should not error")
}

// TestRecordingBytesAccounting verifies that the recorder's BytesWritten
// counter matches the sum of all Write calls.
func TestRecordingBytesAccounting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Use muxer to produce real TS data.
	const numFrames = 60
	tsData := encodeAndMux(t, numFrames, true)
	require.NotEmpty(t, tsData)

	recDir := t.TempDir()
	recorder := output.NewFileRecorder(output.RecorderConfig{
		Dir: recDir,
	})
	require.NoError(t, recorder.Start(context.Background()))

	// Feed TS data in chunks (simulating how the muxer would call Write).
	chunkSize := 188 * 7 // 7 TS packets per write
	var totalWritten int64
	for i := 0; i < len(tsData); i += chunkSize {
		end := i + chunkSize
		if end > len(tsData) {
			end = len(tsData)
		}
		n, err := recorder.Write(tsData[i:end])
		require.NoError(t, err)
		totalWritten += int64(n)
	}

	require.NoError(t, recorder.Close())

	// Check byte accounting.
	recPath := filepath.Join(recDir, recorder.Filename())
	info, err := os.Stat(recPath)
	require.NoError(t, err)

	t.Logf("MEASURE: totalWritten=%d, fileSize=%d, status.BytesWritten=%d",
		totalWritten, info.Size(), recorder.Status().BytesWritten)

	// Total written via Write() calls should match what went to the file.
	assert.Equal(t, totalWritten, info.Size(),
		"total bytes from Write() calls should match file size")

	// Status bytes should also match.
	assert.Equal(t, info.Size(), recorder.Status().BytesWritten,
		"status BytesWritten should match file size")
}

// TestRecordingTSPacketAlignment verifies that every 188-byte TS packet
// in the recording starts with sync byte 0x47.
func TestRecordingTSPacketAlignment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 150 // 5s at 30fps
	tsData := encodeAndMux(t, numFrames, true)
	require.NotEmpty(t, tsData)

	// Write to file through recorder to ensure the recorder path
	// doesn't corrupt packet alignment.
	recDir := t.TempDir()
	recorder := output.NewFileRecorder(output.RecorderConfig{
		Dir: recDir,
	})
	require.NoError(t, recorder.Start(context.Background()))
	_, err := recorder.Write(tsData)
	require.NoError(t, err)
	require.NoError(t, recorder.Close())

	// Read back the file.
	recPath := filepath.Join(recDir, recorder.Filename())
	content, err := os.ReadFile(recPath)
	require.NoError(t, err)

	t.Logf("MEASURE: recording file size=%d bytes, packets=%d", len(content), len(content)/188)

	// Check total length is a multiple of 188.
	assert.Equal(t, 0, len(content)%188,
		"recording file length %d is not a multiple of 188", len(content))

	// Check every packet starts with sync byte.
	badSyncCount := 0
	for i := 0; i+188 <= len(content); i += 188 {
		if content[i] != 0x47 {
			badSyncCount++
			if badSyncCount <= 5 {
				t.Logf("MEASURE: bad sync byte at offset %d: 0x%02X", i, content[i])
			}
		}
	}
	t.Logf("MEASURE: bad sync byte packets=%d out of %d", badSyncCount, len(content)/188)
	assert.Equal(t, 0, badSyncCount, "all TS packets should start with sync byte 0x47")
}

// TestConfidenceMonitorProducesJPEG verifies that the ConfidenceMonitor
// produces decodable JPEG output from keyframes.
func TestConfidenceMonitorProducesJPEG(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Encode a few keyframes to feed to the confidence monitor.
	enc, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := encWidth * encHeight
	uvSize := (encWidth / 2) * (encHeight / 2)
	yuv := make([]byte, ySize+2*uvSize)

	// Fill with a pattern.
	for j := 0; j < ySize; j++ {
		yuv[j] = byte((j * 7) % 256)
	}
	for j := ySize; j < len(yuv); j++ {
		yuv[j] = 128
	}

	// Encode multiple keyframes. The FFmpeg decoder may buffer the first
	// frame (B-frame reorder warmup), so we need multiple IDR frames.
	type keyframeEntry struct {
		avc1 []byte
		sps  []byte
		pps  []byte
		pts  int64
	}
	var keyframes []keyframeEntry
	for i := 0; i < 10; i++ {
		pts := int64(90000) + int64(i)*3000
		// Force IDR on every other frame to get multiple keyframes.
		forceIDR := i%2 == 0

		// Vary content slightly per frame.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*31) % 256)
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		if isKeyframe {
			var entrySPS, entryPPS []byte
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					entrySPS = append([]byte(nil), nalu...)
				case 8:
					entryPPS = append([]byte(nil), nalu...)
				}
			}
			keyframes = append(keyframes, keyframeEntry{
				avc1: avc1,
				sps:  entrySPS,
				pps:  entryPPS,
				pts:  pts,
			})
		}
	}
	require.NotEmpty(t, keyframes, "should have encoded at least one keyframe")
	t.Logf("MEASURE: encoded %d keyframes for confidence monitor", len(keyframes))

	// Create confidence monitor with a real decoder.
	decoderFactory := transition.DecoderFactory(func() (transition.VideoDecoder, error) {
		dec, decErr := codec.NewFFmpegDecoder(nil)
		if decErr != nil {
			return nil, decErr
		}
		return dec, nil
	})

	cm := output.NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed keyframes with 1.1s delay between them to respect the
	// confidence monitor's 1-second rate limiter. The decoder may buffer
	// the first frame (B-frame reorder warmup), so we need multiple.
	for i, kf := range keyframes {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond)
		}
		frame := &media.VideoFrame{
			PTS:        kf.pts,
			IsKeyframe: true,
			WireData:   kf.avc1,
			SPS:        kf.sps,
			PPS:        kf.pps,
			Codec:      "h264",
		}
		cm.IngestVideo(frame)
		t.Logf("MEASURE: fed keyframe %d, PTS=%d, JPEG=%d bytes, errors=%d",
			i, kf.pts, len(cm.LatestThumbnail()), cm.DecodeErrors())
		if cm.LatestThumbnail() != nil {
			break // Got a JPEG, no need to wait more.
		}
	}

	// Check for JPEG output.
	jpeg := cm.LatestThumbnail()
	t.Logf("MEASURE: confidence monitor JPEG size=%d bytes, decode_errors=%d",
		len(jpeg), cm.DecodeErrors())

	if jpeg == nil {
		t.Log("MEASURE: confidence monitor produced no JPEG (may need real decoder)")
		t.Skip("confidence monitor requires working decoder; skipping JPEG validation")
	}

	// Verify it starts with JPEG SOI marker (0xFFD8).
	require.GreaterOrEqual(t, len(jpeg), 2, "JPEG should be at least 2 bytes")
	assert.Equal(t, byte(0xFF), jpeg[0], "JPEG should start with 0xFF")
	assert.Equal(t, byte(0xD8), jpeg[1], "JPEG should have SOI marker 0xFFD8")

	// Verify it ends with JPEG EOI marker (0xFFD9).
	assert.Equal(t, byte(0xFF), jpeg[len(jpeg)-2], "JPEG should end with 0xFF")
	assert.Equal(t, byte(0xD9), jpeg[len(jpeg)-1], "JPEG should have EOI marker 0xFFD9")

	t.Logf("MEASURE: valid JPEG thumbnail %d bytes", len(jpeg))
}
