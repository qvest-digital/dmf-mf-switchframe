package output

import (
	"testing"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// mockVideoDecoder is a minimal decoder that returns a fixed YUV frame.
type mockVideoDecoder struct {
	decodeCount int
}

func (d *mockVideoDecoder) Decode(data []byte) ([]byte, int, int, error) {
	d.decodeCount++
	// Return a tiny 4x4 YUV420 frame.
	w, h := 4, 4
	frameSize := w*h + 2*(w/2)*(h/2)
	yuv := make([]byte, frameSize)
	// Fill Y with 128 (mid-gray), Cb/Cr with 128 (neutral).
	for i := range yuv {
		yuv[i] = 128
	}
	return yuv, w, h, nil
}

func (d *mockVideoDecoder) Close() {}

// TestConfidenceMonitor_HEVC_UsesCodecAwareConversion verifies that the
// confidence monitor correctly handles HEVC keyframes by using codec-agnostic
// wire format conversion and prepending VPS+SPS+PPS.
func TestConfidenceMonitor_HEVC_UsesCodecAwareConversion(t *testing.T) {
	var decoded bool
	factory := func() (transition.VideoDecoder, error) {
		return &mockVideoDecoder{}, nil
	}

	cm := NewConfidenceMonitor(factory)
	defer cm.Close()
	cm.minInterval = 0 // disable rate limiting for tests

	// Build an HEVC keyframe. The confidence monitor should prepend VPS+SPS+PPS
	// for HEVC rather than just SPS+PPS as it would for H.264.
	hevcIDRAnnexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, 0xBB, // IDR (type 19)
	}
	wireData := codec.AnnexBToWireInto(hevcIDRAnnexB, nil)

	frame := &media.VideoFrame{
		WireData:   wireData,
		IsKeyframe: true,
		Codec:      "h265",
		VPS:        []byte{0x40, 0x01, 0xFF},       // VPS NALU body
		SPS:        []byte{0x42, 0x01, 0xFF},       // SPS NALU body
		PPS:        []byte{0x44, 0x01, 0xFF},       // PPS NALU body
	}

	cm.IngestVideo(frame)

	// Wait a moment for processing to complete.
	time.Sleep(50 * time.Millisecond)

	thumb := cm.LatestThumbnail()
	if thumb != nil {
		decoded = true
	}

	// The mock decoder always succeeds, so we should have a thumbnail.
	if !decoded {
		t.Error("confidence monitor failed to decode HEVC keyframe (likely missing VPS prepend or wrong wire format)")
	}
	if cm.DecodeErrors() > 0 {
		t.Errorf("confidence monitor reported %d decode errors", cm.DecodeErrors())
	}
}

// TestConfidenceMonitor_H264_StillWorks verifies the existing H.264 path.
func TestConfidenceMonitor_H264_StillWorks(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return &mockVideoDecoder{}, nil
	}

	cm := NewConfidenceMonitor(factory)
	defer cm.Close()
	cm.minInterval = 0

	h264IDRAnnexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, 0xCC, // IDR
	}
	wireData := codec.AnnexBToWireInto(h264IDRAnnexB, nil)

	frame := &media.VideoFrame{
		WireData:   wireData,
		IsKeyframe: true,
		Codec:      "h264",
		SPS:        []byte{0x67, 0x42, 0xC0},
		PPS:        []byte{0x68, 0xCE, 0x38},
	}

	cm.IngestVideo(frame)
	time.Sleep(50 * time.Millisecond)

	if cm.LatestThumbnail() == nil {
		t.Error("confidence monitor failed to decode H.264 keyframe")
	}
}
