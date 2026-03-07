package output

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestConfidenceMonitor_ProducesThumbnail(t *testing.T) {
	// Use a mock decoder that returns known YUV data
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 180), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed a mock keyframe
	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}, // fake AVC data
	}

	cm.IngestVideo(frame)

	// Should have a JPEG thumbnail available
	jpg := cm.LatestThumbnail()
	require.NotNil(t, jpg, "should produce thumbnail from keyframe")
	require.True(t, len(jpg) > 0)
	// Verify JPEG magic bytes (SOI marker)
	require.Equal(t, byte(0xFF), jpg[0])
	require.Equal(t, byte(0xD8), jpg[1])
}

func TestConfidenceMonitor_SkipsNonKeyframes(t *testing.T) {
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 180), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed a non-keyframe
	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x88},
	}

	cm.IngestVideo(frame)

	// Should NOT have a thumbnail
	require.Nil(t, cm.LatestThumbnail(), "should not produce thumbnail from non-keyframe")
}

func TestConfidenceMonitor_RateLimitsTo1fps(t *testing.T) {
	decodeCount := 0
	decoderFactory := func() (transition.VideoDecoder, error) {
		return &countingDecoder{count: &decodeCount}, nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	cm.minInterval = 100 * time.Millisecond // speed up for test
	defer cm.Close()

	keyframe := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	// First frame should be processed
	cm.IngestVideo(keyframe)
	require.Equal(t, 1, decodeCount)

	// Immediate second frame should be rate-limited (skipped)
	cm.IngestVideo(keyframe)
	require.Equal(t, 1, decodeCount, "should rate-limit and skip second frame")

	// Wait past the interval
	time.Sleep(150 * time.Millisecond)

	// Third frame should be processed
	cm.IngestVideo(keyframe)
	require.Equal(t, 2, decodeCount, "should process frame after interval")
}

func TestConfidenceMonitor_NilDecoderFactory(t *testing.T) {
	cm := NewConfidenceMonitor(nil)
	defer cm.Close()

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	// Should not panic
	cm.IngestVideo(frame)
	require.Nil(t, cm.LatestThumbnail())
}

func TestConfidenceMonitor_ScalesToThumbnailSize(t *testing.T) {
	// Use a larger-than-thumbnail decoder
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(1920, 1080), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	cm.IngestVideo(frame)

	jpg := cm.LatestThumbnail()
	require.NotNil(t, jpg, "should produce thumbnail from large frame")
	// Verify JPEG magic bytes
	require.Equal(t, byte(0xFF), jpg[0])
	require.Equal(t, byte(0xD8), jpg[1])
}

func TestConfidenceMonitor_ConvertsAVC1ToAnnexB(t *testing.T) {
	var receivedData []byte
	decoderFactory := func() (transition.VideoDecoder, error) {
		return &recordingDecoder{received: &receivedData}, nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed AVC1-format wire data (4-byte length prefix)
	avc1Data := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}
	sps := []byte{0x67, 0x64, 0x00, 0x28}
	pps := []byte{0x68, 0xEE, 0x3C, 0x80}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   avc1Data,
		SPS:        sps,
		PPS:        pps,
	}

	cm.IngestVideo(frame)

	require.NotNil(t, receivedData)
	// Should start with Annex B start code (0x00 0x00 0x00 0x01), not AVC1 length prefix
	require.True(t, len(receivedData) >= 4)
	require.Equal(t, byte(0x00), receivedData[0])
	require.Equal(t, byte(0x00), receivedData[1])
	require.Equal(t, byte(0x00), receivedData[2])
	require.Equal(t, byte(0x01), receivedData[3])
}

// recordingDecoder captures the data passed to Decode for assertion.
type recordingDecoder struct {
	received *[]byte
}

func (d *recordingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	cp := make([]byte, len(data))
	copy(cp, data)
	*d.received = cp
	w, h := 320, 180
	return make([]byte, w*h*3/2), w, h, nil
}

func (d *recordingDecoder) Close() {}

// countingDecoder tracks decode calls and returns a fixed-size YUV buffer.
type countingDecoder struct {
	count *int
}

func (d *countingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	*d.count++
	w, h := 320, 180
	return make([]byte, w*h*3/2), w, h, nil
}

func (d *countingDecoder) Close() {}
