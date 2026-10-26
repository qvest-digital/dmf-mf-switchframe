package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// TestConfidenceMonitor_HEVCKeyframe_WithVPS verifies end-to-end that an HEVC
// keyframe with VPS produces a thumbnail. This catches cases where the decoder
// factory creates an H.264-only decoder that rejects HEVC data.
func TestConfidenceMonitor_HEVCKeyframe_WithVPS(t *testing.T) {
	// Use a mock that validates the Annex B data contains VPS+SPS+PPS+IDR NALUs.
	var decodedData []byte
	factory := func() (transition.VideoDecoder, error) {
		return &annexBRecorder{received: &decodedData, w: 4, h: 4}, nil
	}

	cm := NewConfidenceMonitor(factory)
	defer cm.Close()
	cm.minInterval = 0

	hevcIDRAnnexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, 0xBB,
	}
	wireData := codec.AnnexBToWireInto(hevcIDRAnnexB, nil)

	frame := &media.VideoFrame{
		WireData:   wireData,
		IsKeyframe: true,
		Codec:      "h265",
		VPS:        []byte{0x40, 0x01, 0xFF},
		SPS:        []byte{0x42, 0x01, 0xFF},
		PPS:        []byte{0x44, 0x01, 0xFF},
	}

	cm.IngestVideo(frame)
	cm.inFlight.Wait() // join the background decode/encode worker

	thumb := cm.LatestThumbnail()
	require.NotNil(t, thumb, "should produce thumbnail from HEVC keyframe")

	// Verify VPS was prepended in the data sent to decoder.
	require.NotNil(t, decodedData, "decoder should have received data")
	assert.True(t, len(decodedData) > 4, "decoder data should be non-trivial")
	// First NALU should be VPS (0x40).
	assert.Equal(t, byte(0x00), decodedData[0])
	assert.Equal(t, byte(0x00), decodedData[1])
	assert.Equal(t, byte(0x00), decodedData[2])
	assert.Equal(t, byte(0x01), decodedData[3])
	assert.Equal(t, byte(0x40), decodedData[4],
		"first NALU in HEVC data should be VPS (type 32)")
}

// annexBRecorder captures the data passed to Decode for assertion.
type annexBRecorder struct {
	received *[]byte
	w, h     int
}

func (d *annexBRecorder) Decode(data []byte) ([]byte, int, int, error) {
	cp := make([]byte, len(data))
	copy(cp, data)
	*d.received = cp
	return make([]byte, video.YUV420FrameSize(d.w, d.h)), d.w, d.h, nil
}

func (d *annexBRecorder) Close() {}
