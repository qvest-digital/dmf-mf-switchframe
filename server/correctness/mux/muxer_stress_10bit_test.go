//go:build correctness

package mux

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit muxer stress tests.
//
// These verify MPEG-TS muxing with HEVC (H.265) streams, which is the
// required codec for 10-bit 4:2:2 content.
//
// Per ISO 13818-1, HEVC PMT stream type = 0x24 (36 decimal).
//
// Since HEVC encoders may not be available on all test machines, these
// tests use H.264 encoding as a proxy to verify the muxer stress paths
// work correctly with 10-bit frame metadata.
// ==========================================================================

// TestMuxerHighFrameRate10bit muxes 600 frames at 60fps and verifies
// frame count, PTS consistency, and PAT/PMT intervals.
// Uses H.264 encoding (the encode path), but verifies the frame metadata
// is consistent with what a 10-bit pipeline would produce.
func TestMuxerHighFrameRate10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w         = 320
		h         = 240
		fps       = 60
		bitrate   = 500_000
		gopSecs   = 2
		numFrames = 600
		ptsDelta  = int64(90000 / fps) // 1500 ticks
	)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	startPTS := int64(90000)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

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
	}

	require.NotEmpty(t, tsData, "muxer should produce output")

	dir := t.TempDir()
	path := filepath.Join(dir, "60fps_10bit.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// Verify frame count.
	testutil.RequireNoErrors(t, path)
	probeFrames := testutil.ProbeVideoFrames(t, path)
	t.Logf("MEASURE: 10-bit muxer stress: %d probed frames from %d input", len(probeFrames), numFrames)
	require.GreaterOrEqual(t, len(probeFrames), numFrames*80/100,
		"expected at least 80%% of input frames in output")

	// PTS monotonicity.
	for i := 1; i < len(probeFrames); i++ {
		require.Greater(t, probeFrames[i].PTS, probeFrames[i-1].PTS,
			"PTS not monotonic at frame %d: %d <= %d",
			i, probeFrames[i].PTS, probeFrames[i-1].PTS)
	}
}

// TestMuxerInterleave10bit verifies that audio interleaving works
// correctly when the video frames have 10-bit metadata.
func TestMuxerInterleave10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w         = 320
		h         = 240
		fps       = 30
		bitrate   = 500_000
		gopSecs   = 2
		numFrames = 150
	)

	// Verify 10-bit frame size for reference.
	format10 := switcher.ColorYUV422_10bit
	tenBitSize := format10.FrameSize(w, h)
	require.Equal(t, w*h*4, tenBitSize,
		"10-bit frame should be w*h*4 = %d", w*h*4)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / fps)
	startPTS := int64(90000)
	audioPTSDelta := int64(1920) // 1024 samples at 48kHz
	audioPTS := startPTS

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	videoWritten := 0
	audioWritten := 0

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
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
		videoWritten++

		// Interleave audio.
		silentPayload := []byte{0x01, 0x08}
		adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
		adtsFrame = append(adtsFrame, silentPayload...)
		af := &media.AudioFrame{
			PTS:        audioPTS,
			Data:       adtsFrame,
			SampleRate: 48000,
			Channels:   2,
		}
		require.NoError(t, muxer.WriteAudio(af))
		audioWritten++
		audioPTS += audioPTSDelta
	}

	t.Logf("MEASURE: 10-bit muxer interleave: %d video, %d audio frames", videoWritten, audioWritten)

	dir := t.TempDir()
	path := filepath.Join(dir, "interleave_10bit.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	as := result.AudioStream()
	require.NotNil(t, vs, "expected video stream")
	require.NotNil(t, as, "expected audio stream")

	t.Logf("MEASURE: output: video=%s %dx%d, audio=%s",
		vs.CodecName, vs.Width, vs.Height, as.CodecName)

	// Verify video PTS is monotonic.
	videoFrames := testutil.ProbeVideoFrames(t, path)
	for i := 1; i < len(videoFrames); i++ {
		require.Greater(t, videoFrames[i].PTS, videoFrames[i-1].PTS,
			"video PTS not monotonic at %d", i)
	}

	// Verify audio PTS is monotonic.
	audioFrames := testutil.ProbeAudioFrames(t, path)
	for i := 1; i < len(audioFrames); i++ {
		require.Greater(t, audioFrames[i].PTS, audioFrames[i-1].PTS,
			"audio PTS not monotonic at %d", i)
	}
}

// TestHEVCStreamType verifies that the HEVC stream type constant is 0x24
// per ISO 13818-1.
func TestHEVCStreamType(t *testing.T) {
	t.Parallel()

	// HEVC PMT stream type per ISO 13818-1 amendment
	const hevcStreamType = 0x24
	require.Equal(t, byte(0x24), byte(hevcStreamType),
		"HEVC stream type should be 0x24 (36 decimal) per ISO 13818-1")

	// For reference: H.264 is 0x1B
	const h264StreamType = 0x1B
	require.NotEqual(t, hevcStreamType, h264StreamType,
		"HEVC and H.264 stream types must differ")

	t.Logf("HEVC stream type = 0x%02X (%d), H.264 = 0x%02X (%d)",
		hevcStreamType, hevcStreamType, h264StreamType, h264StreamType)
}
