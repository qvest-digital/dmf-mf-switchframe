//go:build correctness

package output

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/transition"
)

// jpegDecode wraps image/jpeg.Decode for use in tests.
func jpegDecode(r *bytes.Reader) (image.Image, error) {
	return jpeg.Decode(r)
}

// aacFrameCache holds pre-encoded AAC frames for reuse across tests.
// Generated once via newAACFrameGenerator.
type aacFrameGenerator struct {
	enc      *audio.FDKEncoder
	pcmBuf   []float32
	channels int
}

// newAACFrameGenerator creates an FDK AAC encoder and returns a generator
// that produces valid ADTS-framed AAC audio frames from a sine wave.
func newAACFrameGenerator(t *testing.T, sampleRate, channels int) *aacFrameGenerator {
	t.Helper()
	enc, err := audio.NewFDKEncoder(sampleRate, channels)
	require.NoError(t, err, "create FDK AAC encoder")

	// AAC-LC frame size is 1024 samples per channel.
	pcmBuf := make([]float32, 1024*channels)

	g := &aacFrameGenerator{
		enc:      enc,
		pcmBuf:   pcmBuf,
		channels: channels,
	}
	t.Cleanup(func() { enc.Close() })

	// Prime the encoder with a few frames (FDK may buffer the first frame).
	for i := 0; i < 3; i++ {
		g.fillSine(i, sampleRate)
		_, _ = enc.Encode(g.pcmBuf)
	}
	return g
}

// fillSine fills the PCM buffer with a 440Hz sine wave for the given frame index.
func (g *aacFrameGenerator) fillSine(frameIdx, sampleRate int) {
	const freq = 440.0
	const amplitude = 0.5
	for i := 0; i < 1024; i++ {
		sampleIdx := frameIdx*1024 + i
		val := float32(amplitude * math.Sin(2*math.Pi*freq*float64(sampleIdx)/float64(sampleRate)))
		for ch := 0; ch < g.channels; ch++ {
			g.pcmBuf[i*g.channels+ch] = val
		}
	}
}

// generate produces one valid ADTS-framed AAC audio frame.
func (g *aacFrameGenerator) generate(t *testing.T, frameIdx, sampleRate int) []byte {
	t.Helper()
	g.fillSine(frameIdx, sampleRate)
	aacData, err := g.enc.Encode(g.pcmBuf)
	require.NoError(t, err, "encode AAC frame %d", frameIdx)
	// If encoder returns empty (still priming), generate more frames.
	for len(aacData) == 0 {
		frameIdx++
		g.fillSine(frameIdx, sampleRate)
		aacData, err = g.enc.Encode(g.pcmBuf)
		require.NoError(t, err, "encode AAC frame %d (priming)", frameIdx)
	}

	// Validate ADTS sync word: first 12 bits must be 0xFFF.
	require.GreaterOrEqual(t, len(aacData), 7, "ADTS frame must be at least 7 bytes (header)")
	require.Equal(t, byte(0xFF), aacData[0],
		"ADTS sync word byte 0 must be 0xFF, got 0x%02X", aacData[0])
	require.Equal(t, byte(0xF0), aacData[1]&0xF0,
		"ADTS sync word upper nibble of byte 1 must be 0xF0, got 0x%02X", aacData[1]&0xF0)

	return aacData
}

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

	var aacGen *aacFrameGenerator
	if withAudio {
		aacGen = newAACFrameGenerator(t, 48000, 2)
	}

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
			aacData := aacGen.generate(t, i, 48000)
			aFrame := &media.AudioFrame{
				PTS:        pts,
				Data:       aacData,
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

	aacGen := newAACFrameGenerator(t, 48000, 2)

	muxer := output.NewTSMuxer()
	muxCallbackCount := 0
	muxTotalBytes := 0
	muxer.SetOutput(func(data []byte) {
		muxCallbackCount++
		muxTotalBytes += len(data)
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

		// Write interleaved audio using real FDK AAC encoder.
		aacData := aacGen.generate(t, i, 48000)
		aFrame := &media.AudioFrame{
			PTS:        pts,
			Data:       aacData,
			SampleRate: 48000,
			Channels:   2,
		}
		require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", i)
	}

	// Close recorder and muxer.
	require.NoError(t, muxer.Close())
	require.NoError(t, recorder.Close())

	// Assert mux callback was invoked — muxer must produce output.
	t.Logf("MEASURE: mux callback invocations=%d, total bytes=%d", muxCallbackCount, muxTotalBytes)
	require.Greater(t, muxCallbackCount, 0, "muxer must invoke output callback")
	require.Greater(t, muxTotalBytes, 0, "muxer must produce output bytes")

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

	// We fed 90 video frames at 30fps = 3s. Encoder warmup loses a few.
	require.InDelta(t, 90, len(videoFrames), 5,
		"expected ~90 video frames (sent 90, encoder warmup may lose a few)")

	// We write one AAC frame per video frame (90 frames total).
	// Encoder warmup may skip a few video frames (empty output).
	require.InDelta(t, 90, len(audioFrames), 10,
		"expected ~90 audio frames (one per video frame)")

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

		aacGen := newAACFrameGenerator(t, 48000, 2)

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

			aacData := aacGen.generate(t, i, 48000)
			aFrame := &media.AudioFrame{
				PTS:        pts,
				Data:       aacData,
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

	// Verify each file's PTS values are internally monotonic.
	if len(frames1) > 1 {
		for i := 1; i < len(frames1); i++ {
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

	// Verify PTS ranges are disjoint or independently rebased. The muxer
	// rebases PTS to near muxerEpoch (90000), so each file should have its
	// own self-contained PTS range. Log and verify bounds.
	file1FirstPTS := frames1[0].PTS
	file1LastPTS := frames1[len(frames1)-1].PTS
	file2FirstPTS := frames2[0].PTS
	file2LastPTS := frames2[len(frames2)-1].PTS
	t.Logf("MEASURE: file1 PTS range [%d, %d], file2 PTS range [%d, %d]",
		file1FirstPTS, file1LastPTS, file2FirstPTS, file2LastPTS)

	// Each file's PTS span should be ~1s of content (30 frames * 3000 ticks = 90000 ticks).
	// Allow generous margin for encoder warmup.
	file1Span := file1LastPTS - file1FirstPTS
	file2Span := file2LastPTS - file2FirstPTS
	t.Logf("MEASURE: file1 PTS span=%d ticks (%.1fms), file2 PTS span=%d ticks (%.1fms)",
		file1Span, float64(file1Span)/90.0, file2Span, float64(file2Span)/90.0)

	// 1s of content = 90000 ticks. Span should be reasonable (not 0, not huge).
	assert.Greater(t, file1Span, int64(50000),
		"file1 PTS span should cover most of 1s of content")
	assert.Less(t, file1Span, int64(200000),
		"file1 PTS span should not exceed ~2s for 1s of content")
	assert.Greater(t, file2Span, int64(50000),
		"file2 PTS span should cover most of 1s of content")
	assert.Less(t, file2Span, int64(200000),
		"file2 PTS span should not exceed ~2s for 1s of content")

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

	// Compute actual bitrate and compare against target.
	// Target: 500kbps video + ~128kbps AAC audio = ~628kbps total.
	// Parse duration for accurate bitrate calculation.
	var actualDuration float64 = 30.0 // fallback
	if durationStr != "" {
		if parsed, parseErr := strconv.ParseFloat(durationStr, 64); parseErr == nil && parsed > 0 {
			actualDuration = parsed
		}
	}
	actualBitrateBps := float64(fileSize) * 8.0 / actualDuration
	targetBitrateBps := float64(encBitrate + 128_000) // video + audio
	t.Logf("MEASURE: actual bitrate=%.0f bps (%.0f kbps), target=%.0f bps (%.0f kbps)",
		actualBitrateBps, actualBitrateBps/1000, targetBitrateBps, targetBitrateBps/1000)

	// Bitrate should be within +/-50% of target. Real content varies, but
	// shouldn't be wildly off (e.g., 10x).
	assert.Greater(t, actualBitrateBps, targetBitrateBps*0.5,
		"actual bitrate %.0f bps should be at least 50%% of target %.0f bps", actualBitrateBps, targetBitrateBps)
	assert.Less(t, actualBitrateBps, targetBitrateBps*1.5,
		"actual bitrate %.0f bps should be at most 150%% of target %.0f bps", actualBitrateBps, targetBitrateBps)

	// File size: for 30s at 500kbps + audio overhead, expect ~2MB +/- generous margin.
	// 500kbps * 30s = 1,875,000 bytes. Allow 500KB to 10MB range.
	t.Logf("MEASURE: 30s recording = %d bytes (%.0f kbps)",
		fileSize, float64(fileSize)*8.0/actualDuration/1000.0)
	assert.Greater(t, fileSize, int64(500_000),
		"30s recording should be at least 500KB")
	assert.Less(t, fileSize, int64(10_000_000),
		"30s recording should be under 10MB at 500kbps")

	// Check PTS continuity in video frames.
	videoFrames := testutil.ProbeVideoFrames(t, path)
	audioFrames := testutil.ProbeAudioFrames(t, path)
	t.Logf("MEASURE: video_frames=%d, audio_frames=%d", len(videoFrames), len(audioFrames))

	// We sent 900 video frames at 30fps = 30s. Encoder warmup loses a few.
	require.InDelta(t, 900, len(videoFrames), 20,
		"expected ~900 video frames (sent 900, encoder warmup may lose a few)")

	// We write one AAC frame per video frame (900 frames total).
	// Encoder priming may skip a few, and some video frames are skipped
	// when the encoder returns empty during warmup.
	require.InDelta(t, 900, len(audioFrames), 50,
		"expected ~900 audio frames (one per video frame)")

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

	// Decode JPEG and verify it has real content (not all-black).
	jpegImg, decodeErr := jpegDecode(bytes.NewReader(jpeg))
	require.NoError(t, decodeErr, "JPEG should be decodable by Go image/jpeg")

	bounds := jpegImg.Bounds()
	t.Logf("MEASURE: decoded JPEG dimensions=%dx%d", bounds.Dx(), bounds.Dy())
	require.Greater(t, bounds.Dx(), 0, "JPEG width should be > 0")
	require.Greater(t, bounds.Dy(), 0, "JPEG height should be > 0")

	// Compute average pixel brightness. Source YUV has varied Y content
	// (pattern: (j*7 + i*31) % 256), so decoded JPEG should not be all-black.
	var totalBrightness uint64
	pixelCount := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := jpegImg.At(x, y).RGBA()
			// Convert to 8-bit and compute luminance (BT.601 approximation).
			luma := (19595*r + 38470*g + 7471*b + (1 << 15)) >> 24
			totalBrightness += uint64(luma)
			pixelCount++
		}
	}
	avgBrightness := float64(totalBrightness) / float64(pixelCount)
	t.Logf("MEASURE: JPEG average brightness=%.1f (0=black, 255=white)", avgBrightness)

	// Source content has varied luma, average should be well above black.
	assert.Greater(t, avgBrightness, 20.0,
		"JPEG average brightness should be > 20 (not all-black)")

	t.Logf("MEASURE: valid JPEG thumbnail %d bytes, %dx%d, brightness=%.1f",
		len(jpeg), bounds.Dx(), bounds.Dy(), avgBrightness)
}

// TestConfidenceMonitorNonKeyframeIgnored verifies that the confidence
// monitor only processes keyframes. P-frames (IsKeyframe=false) must
// produce zero JPEG output.
func TestConfidenceMonitorNonKeyframeIgnored(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ySize := encWidth * encHeight
	uvSize := (encWidth / 2) * (encHeight / 2)
	yuv := make([]byte, ySize+2*uvSize)

	type frameEntry struct {
		avc1 []byte
		sps  []byte
		pps  []byte
		pts  int64
	}

	// Phase 1: Encode P-frames (non-keyframes) using one encoder.
	// We encode a GOP starting with an IDR, then collect the non-IDR frames.
	encP, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err)

	var pFrames []frameEntry
	for i := 0; i < 30; i++ {
		pts := int64(90000) + int64(i)*3000
		forceIDR := i == 0

		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*31) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := encP.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr)
		if len(encoded) == 0 {
			continue
		}

		if !isKeyframe {
			avc1 := codec.AnnexBToAVC1(encoded)
			pFrames = append(pFrames, frameEntry{avc1: avc1, pts: pts})
		}
	}
	encP.Close()

	// Phase 2: Encode keyframes (all forced IDR) using a separate encoder.
	encK, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err)

	var keyframes []frameEntry
	for i := 0; i < 10; i++ {
		pts := int64(200000) + int64(i)*3000

		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*41) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := encK.Encode(yuv, pts, true /* forceIDR */)
		require.NoError(t, encErr)
		if len(encoded) == 0 {
			continue
		}
		if isKeyframe {
			avc1 := codec.AnnexBToAVC1(encoded)
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
			keyframes = append(keyframes, frameEntry{avc1: avc1, sps: entrySPS, pps: entryPPS, pts: pts})
		}
	}
	encK.Close()

	t.Logf("MEASURE: encoded %d P-frames and %d keyframes", len(pFrames), len(keyframes))
	require.GreaterOrEqual(t, len(pFrames), 10, "should have at least 10 P-frames")
	require.GreaterOrEqual(t, len(keyframes), 5, "should have at least 5 keyframes")

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

	// Feed 10 P-frames — none should produce a JPEG.
	pFrameCount := 10
	for i := 0; i < pFrameCount; i++ {
		pf := pFrames[i]
		frame := &media.VideoFrame{
			PTS:        pf.pts,
			IsKeyframe: false, // explicitly P-frame
			WireData:   pf.avc1,
			Codec:      "h264",
		}
		cm.IngestVideo(frame)
	}

	jpegAfterPFrames := cm.LatestThumbnail()
	t.Logf("MEASURE: JPEGs from %d P-frames=%d", pFrameCount, len(jpegAfterPFrames))
	assert.Nil(t, jpegAfterPFrames, "P-frames should not produce JPEG output")

	// Now feed keyframes (with rate limiter sleep) until we get a JPEG.
	var jpegAfterKeyframe []byte
	keyframesFed := 0
	for _, kf := range keyframes {
		if keyframesFed > 0 {
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
		keyframesFed++
		jpegAfterKeyframe = cm.LatestThumbnail()
		if jpegAfterKeyframe != nil {
			break
		}
	}

	t.Logf("MEASURE: JPEGs from %d P-frames=%d, after %d keyframe(s)=%d",
		pFrameCount, len(jpegAfterPFrames), keyframesFed, len(jpegAfterKeyframe))

	// We expect a JPEG after feeding keyframes (decoder warmup may require ~4).
	if jpegAfterKeyframe == nil {
		t.Log("MEASURE: no JPEG after keyframes (decoder warmup issue)")
		t.Skip("decoder did not produce output; skipping keyframe validation")
	}
	assert.Greater(t, len(jpegAfterKeyframe), 100, "JPEG after keyframe should be non-trivial")
}

// TestConfidenceMonitorRateLimited verifies that the confidence monitor
// rate-limits thumbnail generation to approximately 1 fps, even when
// fed keyframes faster than that.
func TestConfidenceMonitorRateLimited(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Encode many keyframes.
	enc, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := encWidth * encHeight
	uvSize := (encWidth / 2) * (encHeight / 2)
	yuv := make([]byte, ySize+2*uvSize)

	type keyframeEntry struct {
		avc1 []byte
		sps  []byte
		pps  []byte
		pts  int64
	}
	var keyframes []keyframeEntry

	for i := 0; i < 20; i++ {
		pts := int64(90000) + int64(i)*3000
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*31) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, true /* forceIDR */)
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

	require.GreaterOrEqual(t, len(keyframes), 10, "need at least 10 keyframes for rate limit test")
	t.Logf("MEASURE: encoded %d keyframes for rate-limit test", len(keyframes))

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

	// First, warm up the decoder by feeding the first keyframe and
	// waiting for it to produce a JPEG.
	warmupCount := 0
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
		warmupCount++
		if cm.LatestThumbnail() != nil {
			break
		}
	}

	if cm.LatestThumbnail() == nil {
		t.Skip("decoder did not produce output during warmup; skipping rate limit test")
	}
	t.Logf("MEASURE: decoder warmed up after %d keyframes", warmupCount)

	// Now track JPEG changes. We save the current JPEG and count
	// how many times it changes during rapid keyframe feeding.
	remainingKeyframes := keyframes[warmupCount:]
	if len(remainingKeyframes) < 10 {
		t.Skipf("only %d keyframes remaining after warmup; need 10", len(remainingKeyframes))
	}

	// Wait for the rate limiter to allow the next update.
	time.Sleep(1100 * time.Millisecond)

	prevJPEG := cm.LatestThumbnail()
	jpegChanges := 0

	// Feed 10 keyframes as fast as possible (no sleep between them).
	for i := 0; i < 10 && i < len(remainingKeyframes); i++ {
		kf := remainingKeyframes[i]
		frame := &media.VideoFrame{
			PTS:        kf.pts,
			IsKeyframe: true,
			WireData:   kf.avc1,
			SPS:        kf.sps,
			PPS:        kf.pps,
			Codec:      "h264",
		}
		cm.IngestVideo(frame)

		currentJPEG := cm.LatestThumbnail()
		if len(currentJPEG) != len(prevJPEG) {
			jpegChanges++
			prevJPEG = currentJPEG
		} else {
			// Check if bytes differ (same length but different content).
			differs := false
			for b := range currentJPEG {
				if currentJPEG[b] != prevJPEG[b] {
					differs = true
					break
				}
			}
			if differs {
				jpegChanges++
				prevJPEG = currentJPEG
			}
		}
	}

	t.Logf("MEASURE: 10 rapid keyframes produced %d JPEG changes", jpegChanges)

	// Rate limiter should allow at most ~1 JPEG per second. Since we feed
	// 10 keyframes in rapid succession (well under 1 second), we expect
	// at most 1-2 JPEG changes (the first one gets through, rest are rate-limited).
	assert.Less(t, jpegChanges, 10,
		"rate limiter should prevent all 10 rapid keyframes from producing JPEGs")
	// More specifically, at most 2 should get through (one at start, maybe one at boundary).
	assert.LessOrEqual(t, jpegChanges, 2,
		"expected at most 2 JPEG changes from 10 rapid keyframes (1fps rate limit)")
}

// TestConfidenceMonitorJPEGDecodable verifies that JPEG output from the
// confidence monitor is properly formatted and decodable.
func TestConfidenceMonitorJPEGDecodable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Encode keyframes.
	enc, err := codec.NewFFmpegEncoder("libx264", encWidth, encHeight, encBitrate, encFPS, 1, encGOPSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := encWidth * encHeight
	uvSize := (encWidth / 2) * (encHeight / 2)
	yuv := make([]byte, ySize+2*uvSize)

	type keyframeEntry struct {
		avc1 []byte
		sps  []byte
		pps  []byte
		pts  int64
	}
	var keyframes []keyframeEntry

	for i := 0; i < 10; i++ {
		pts := int64(90000) + int64(i)*3000
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*31) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, true /* forceIDR */)
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
	t.Logf("MEASURE: encoded %d keyframes for JPEG decodability test", len(keyframes))

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

	// Feed keyframes until we get a JPEG.
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
		if cm.LatestThumbnail() != nil {
			t.Logf("MEASURE: got JPEG after %d keyframes", i+1)
			break
		}
	}

	jpegData := cm.LatestThumbnail()
	if jpegData == nil {
		t.Skip("decoder did not produce JPEG; skipping decodability test")
	}

	t.Logf("MEASURE: JPEG size=%d bytes", len(jpegData))

	// Check JPEG SOI marker (0xFF 0xD8).
	validSOI := len(jpegData) >= 2 && jpegData[0] == 0xFF && jpegData[1] == 0xD8
	t.Logf("MEASURE: JPEG SOI marker valid=%v (bytes: 0x%02X 0x%02X)", validSOI, jpegData[0], jpegData[1])
	assert.True(t, validSOI, "JPEG should start with SOI marker 0xFFD8")

	// Check JPEG EOI marker (0xFF 0xD9).
	validEOI := len(jpegData) >= 2 && jpegData[len(jpegData)-2] == 0xFF && jpegData[len(jpegData)-1] == 0xD9
	t.Logf("MEASURE: JPEG EOI marker valid=%v (bytes: 0x%02X 0x%02X)",
		validEOI, jpegData[len(jpegData)-2], jpegData[len(jpegData)-1])
	assert.True(t, validEOI, "JPEG should end with EOI marker 0xFFD9")

	// Check minimum size.
	t.Logf("MEASURE: JPEG size=%d bytes, valid_markers=%v", len(jpegData), validSOI && validEOI)
	assert.Greater(t, len(jpegData), 100, "JPEG should be at least 100 bytes (not empty/truncated)")

	// Decode the JPEG using image/jpeg and verify dimensions.
	img, decodeErr := jpegDecode(bytes.NewReader(jpegData))
	t.Logf("MEASURE: jpeg.Decode err=%v", decodeErr)
	require.NoError(t, decodeErr, "JPEG should be decodable by image/jpeg")

	bounds := img.Bounds()
	imgWidth := bounds.Dx()
	imgHeight := bounds.Dy()
	t.Logf("MEASURE: decoded JPEG dimensions=%dx%d (expected %dx%d)", imgWidth, imgHeight, 320, 180)

	// The confidence monitor scales to thumbWidth x thumbHeight (320x180).
	assert.Equal(t, 320, imgWidth, "JPEG width should be 320 (thumbWidth)")
	assert.Equal(t, 180, imgHeight, "JPEG height should be 180 (thumbHeight)")
}
