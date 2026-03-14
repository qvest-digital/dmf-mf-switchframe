package clip

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/output"
)

// --- Test helpers ---

// generateTestTS creates a valid MPEG-TS byte stream with numFrames video
// frames (first is a keyframe) and interleaved audio. Uses the output.TSMuxer
// to produce standards-compliant TS packets.
func generateTestTS(t *testing.T, numFrames int) []byte {
	t.Helper()

	// Synthetic SPS (Baseline profile, level 3.0, 320x240).
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}

	// Synthetic IDR NALU body (type 5).
	idrBody := make([]byte, 64)
	idrBody[0] = 0x65 // NALU type 5 (IDR)
	for i := 1; i < len(idrBody); i++ {
		idrBody[i] = byte(i)
	}

	// Synthetic non-IDR NALU body (type 1).
	nonIDRBody := make([]byte, 32)
	nonIDRBody[0] = 0x41 // NALU type 1 (non-IDR slice)
	for i := 1; i < len(nonIDRBody); i++ {
		nonIDRBody[i] = byte(i)
	}

	// Build AVC1 wire data helpers.
	makeAVC1 := func(naluData []byte) []byte {
		out := make([]byte, 4+len(naluData))
		out[0] = byte(len(naluData) >> 24)
		out[1] = byte(len(naluData) >> 16)
		out[2] = byte(len(naluData) >> 8)
		out[3] = byte(len(naluData))
		copy(out[4:], naluData)
		return out
	}

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	const (
		frameInterval = 3000 // 33.3ms at 90kHz = ~30fps
		sampleRate    = 48000
		channels      = 2
	)

	for i := 0; i < numFrames; i++ {
		pts := int64(90000 + i*frameInterval)

		if i == 0 {
			// First frame: keyframe with SPS/PPS.
			frame := &media.VideoFrame{
				PTS:        pts,
				DTS:        pts,
				IsKeyframe: true,
				SPS:        sps,
				PPS:        pps,
				WireData:   makeAVC1(idrBody),
				Codec:      "h264",
			}
			require.NoError(t, muxer.WriteVideo(frame))
		} else {
			// Subsequent frames: non-IDR.
			frame := &media.VideoFrame{
				PTS:      pts,
				DTS:      pts,
				WireData: makeAVC1(nonIDRBody),
			}
			require.NoError(t, muxer.WriteVideo(frame))
		}

		// Interleave an audio frame with each video frame.
		// Build a raw AAC payload and wrap it with ADTS.
		rawAAC := make([]byte, 128)
		rawAAC[0] = 0x01 // arbitrary AAC payload
		adtsFrame := codec.EnsureADTS(rawAAC, sampleRate, channels)

		audioFrame := &media.AudioFrame{
			PTS:        pts,
			Data:       adtsFrame,
			SampleRate: sampleRate,
			Channels:   channels,
		}
		require.NoError(t, muxer.WriteAudio(audioFrame))
	}

	require.NotEmpty(t, tsData, "TSMuxer should have produced output")
	return tsData
}

func writeTemp(t *testing.T, data []byte, ext string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "clip-*"+ext)
	require.NoError(t, err)
	if len(data) > 0 {
		_, err = f.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	return f.Name()
}

// --- Tests ---

func TestDemuxTS(t *testing.T) {
	data := generateTestTS(t, 10)
	tmpFile := writeTemp(t, data, ".ts")

	frames, audioFrames, err := DemuxFile(tmpFile)
	require.NoError(t, err)
	require.NotEmpty(t, frames, "expected video frames")
	assert.True(t, frames[0].isKeyframe, "first frame should be a keyframe")
	assert.NotEmpty(t, frames[0].sps, "keyframe should have SPS")
	assert.NotEmpty(t, frames[0].pps, "keyframe should have PPS")

	// Wire data should be AVC1 format (4-byte length prefix, not Annex B).
	require.True(t, len(frames[0].wireData) > 4, "wireData should contain NALU data")

	// Verify it's valid AVC1: first 4 bytes are a length prefix.
	nalus := codec.ExtractNALUs(frames[0].wireData)
	assert.NotEmpty(t, nalus, "wireData should be valid AVC1")

	_ = audioFrames
}

func TestDemuxTS_AudioFrames(t *testing.T) {
	data := generateTestTS(t, 5)
	tmpFile := writeTemp(t, data, ".ts")

	_, audioFrames, err := DemuxFile(tmpFile)
	require.NoError(t, err)
	require.NotEmpty(t, audioFrames, "expected audio frames")

	// Audio frames should have valid metadata.
	for i, af := range audioFrames {
		assert.Greater(t, af.sampleRate, 0, "frame %d: sampleRate should be > 0", i)
		assert.Greater(t, af.channels, 0, "frame %d: channels should be > 0", i)
		assert.NotEmpty(t, af.data, "frame %d: data should not be empty", i)
	}
}

func TestDemuxTSPTSOrder(t *testing.T) {
	data := generateTestTS(t, 30)
	tmpFile := writeTemp(t, data, ".ts")

	frames, _, err := DemuxFile(tmpFile)
	require.NoError(t, err)
	require.True(t, len(frames) > 1, "need at least 2 frames to check order")

	for i := 1; i < len(frames); i++ {
		if frames[i].pts <= frames[i-1].pts {
			t.Errorf("PTS not monotonic: frame[%d].pts=%d <= frame[%d].pts=%d",
				i, frames[i].pts, i-1, frames[i-1].pts)
		}
	}
}

func TestDemuxTS_AudioPTSOrder(t *testing.T) {
	data := generateTestTS(t, 15)
	tmpFile := writeTemp(t, data, ".ts")

	_, audioFrames, err := DemuxFile(tmpFile)
	require.NoError(t, err)
	require.True(t, len(audioFrames) > 1, "need at least 2 audio frames")

	for i := 1; i < len(audioFrames); i++ {
		if audioFrames[i].pts < audioFrames[i-1].pts {
			t.Errorf("Audio PTS not monotonic: frame[%d].pts=%d < frame[%d].pts=%d",
				i, audioFrames[i].pts, i-1, audioFrames[i-1].pts)
		}
	}
}

func TestDemuxEmptyFile(t *testing.T) {
	tmpFile := writeTemp(t, []byte{}, ".ts")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for empty file")
}

func TestDemuxInvalidFile(t *testing.T) {
	tmpFile := writeTemp(t, []byte("not a ts file"), ".ts")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for invalid file")
}

func TestDemuxMP4InvalidData(t *testing.T) {
	tmpFile := writeTemp(t, []byte("fake mp4 data"), ".mp4")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for invalid MP4 data")
}

func TestDemuxUnknownExtension(t *testing.T) {
	tmpFile := writeTemp(t, []byte("data"), ".avi")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for unknown extension")
	assert.Contains(t, err.Error(), "unsupported", "error should mention unsupported format")
}

func TestDemuxNonExistentFile(t *testing.T) {
	_, _, err := DemuxFile("/tmp/nonexistent-clip-12345.ts")
	assert.Error(t, err, "expected error for non-existent file")
}

func TestDemuxTS_NonIDRFrames(t *testing.T) {
	data := generateTestTS(t, 10)
	tmpFile := writeTemp(t, data, ".ts")

	frames, _, err := DemuxFile(tmpFile)
	require.NoError(t, err)

	// After the first keyframe, remaining frames should be non-IDR.
	nonIDRCount := 0
	for _, f := range frames {
		if !f.isKeyframe {
			nonIDRCount++
		}
	}
	assert.Greater(t, nonIDRCount, 0, "should have at least one non-keyframe")
}

func TestDemuxTS_KeyframeHasSPSPPS(t *testing.T) {
	data := generateTestTS(t, 5)
	tmpFile := writeTemp(t, data, ".ts")

	frames, _, err := DemuxFile(tmpFile)
	require.NoError(t, err)

	for _, f := range frames {
		if f.isKeyframe {
			assert.NotEmpty(t, f.sps, "keyframe should have SPS")
			assert.NotEmpty(t, f.pps, "keyframe should have PPS")
			// SPS starts with NALU type 7.
			assert.Equal(t, byte(7), f.sps[0]&0x1F, "SPS NALU type should be 7")
			// PPS starts with NALU type 8.
			assert.Equal(t, byte(8), f.pps[0]&0x1F, "PPS NALU type should be 8")
		}
	}
}

func TestDemuxTS_MultipleKeyframes(t *testing.T) {
	// Generate TS with enough frames that we manually insert a second keyframe.
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}

	idrBody := make([]byte, 64)
	idrBody[0] = 0x65

	nonIDRBody := make([]byte, 32)
	nonIDRBody[0] = 0x41

	makeAVC1 := func(naluData []byte) []byte {
		out := make([]byte, 4+len(naluData))
		out[0] = byte(len(naluData) >> 24)
		out[1] = byte(len(naluData) >> 16)
		out[2] = byte(len(naluData) >> 8)
		out[3] = byte(len(naluData))
		copy(out[4:], naluData)
		return out
	}

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	for i := 0; i < 20; i++ {
		pts := int64(90000 + i*3000)
		isKey := i == 0 || i == 10
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKey,
			WireData:   makeAVC1(idrBody),
			Codec:      "h264",
		}
		if isKey {
			frame.SPS = sps
			frame.PPS = pps
			frame.WireData = makeAVC1(idrBody)
		} else {
			frame.WireData = makeAVC1(nonIDRBody)
		}
		require.NoError(t, muxer.WriteVideo(frame))
	}

	tmpFile := writeTemp(t, tsData, ".ts")
	frames, _, err := DemuxFile(tmpFile)
	require.NoError(t, err)

	keyframeCount := 0
	for _, f := range frames {
		if f.isKeyframe {
			keyframeCount++
		}
	}
	assert.GreaterOrEqual(t, keyframeCount, 2, "should have at least 2 keyframes")
}

func TestDemuxTS_VideoOnly(t *testing.T) {
	// Generate TS with only video frames (no audio).
	sps := []byte{0x67, 0x42, 0xC0, 0x1E}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}
	idrBody := make([]byte, 32)
	idrBody[0] = 0x65

	makeAVC1 := func(naluData []byte) []byte {
		out := make([]byte, 4+len(naluData))
		out[0] = byte(len(naluData) >> 24)
		out[1] = byte(len(naluData) >> 16)
		out[2] = byte(len(naluData) >> 8)
		out[3] = byte(len(naluData))
		copy(out[4:], naluData)
		return out
	}

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
		WireData:   makeAVC1(idrBody),
		Codec:      "h264",
	}
	require.NoError(t, muxer.WriteVideo(frame))

	tmpFile := writeTemp(t, tsData, ".ts")
	frames, audioFrames, err := DemuxFile(tmpFile)
	require.NoError(t, err)
	assert.NotEmpty(t, frames, "should have video frames")
	assert.Empty(t, audioFrames, "should have no audio frames")
}
