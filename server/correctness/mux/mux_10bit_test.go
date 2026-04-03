//go:build correctness

package mux

import (
	"bytes"
	"context"
	"testing"

	astits "github.com/asticode/go-astits"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/internal/mpegts"
	"github.com/zsiec/switchframe/server/output"
)

// ============================================================================
// HEVC Mux Correctness Tests
//
// Per ISO 13818-1 (MPEG-TS):
//   HEVC PMT stream type = 0x24 (36 decimal)
//   AVC PMT stream type  = 0x1B (27 decimal)
//
// Per ITU-T H.265:
//   VPS NALU type = 32
//   SPS NALU type = 33
//   PPS NALU type = 34
//   Annex B start code = 0x00000001
// ============================================================================

// makeHEVCKeyframeForTest creates a synthetic HEVC keyframe with VPS/SPS/PPS.
func makeHEVCKeyframeForTest(pts int64) *media.VideoFrame {
	// VPS: nal_unit_type = 32 => first byte = (32 << 1) | 0 = 0x40
	vps := []byte{0x40, 0x01, 0x0C, 0x01, 0xFF, 0xFF}
	// SPS: nal_unit_type = 33 => first byte = (33 << 1) | 0 = 0x42
	sps := []byte{0x42, 0x01, 0x01, 0x01, 0x60, 0x00}
	// PPS: nal_unit_type = 34 => first byte = (34 << 1) | 0 = 0x44
	pps := []byte{0x44, 0x01, 0xC0, 0xF7, 0x24}

	// Minimal HEVC IDR slice NALU (IDR_W_RADL type 19, first byte = 0x26)
	idrSlice := []byte{0x26, 0x01, 0xAF, 0x09, 0x40, 0x00, 0x00, 0x03}

	return &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		IsKeyframe: true,
		VPS:        vps,
		SPS:        sps,
		PPS:        pps,
		WireData:   []byte{0x00, 0x00, 0x00, byte(len(idrSlice)), idrSlice[0], idrSlice[1], idrSlice[2], idrSlice[3], idrSlice[4], idrSlice[5], idrSlice[6], idrSlice[7]},
		Codec:      "h265",
	}
}

// ============================================================================
// HEVC Stream Type in PMT
// ============================================================================

// TestMux10bit_HEVCStreamType verifies that when the muxer receives an HEVC
// keyframe (Codec="h265"), the PMT contains stream type 0x24 (36), not 0x1B (27).
// ISO 13818-1 defines stream_type=0x24 for H.265/HEVC video.
func TestMux10bit_HEVCStreamType(t *testing.T) {
	muxer := output.NewTSMuxer()

	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	err := muxer.WriteVideo(makeHEVCKeyframeForTest(90000))
	require.NoError(t, err, "mux HEVC keyframe")
	require.NotEmpty(t, tsData, "muxer should produce output")

	// Verify TS structure is valid
	testutil.AssertValidTSStructure(t, tsData)

	// Use astits demuxer to properly parse the PMT
	demuxer := astits.NewDemuxer(context.Background(), bytes.NewReader(tsData))
	foundH265Stream := false

	for {
		d, err := demuxer.NextData()
		if err != nil {
			break
		}
		if d == nil || d.PMT == nil {
			continue
		}
		for _, es := range d.PMT.ElementaryStreams {
			if es.ElementaryPID == mpegts.VideoPID {
				// ISO 13818-1: stream_type 0x24 = H.265/HEVC
				assert.Equal(t, astits.StreamTypeH265Video, es.StreamType,
					"HEVC video stream type should be H.265 (0x24), got %d", es.StreamType)
				foundH265Stream = true
			}
		}
	}

	assert.True(t, foundH265Stream, "PMT should contain H.265 video stream at PID 0x%04X", mpegts.VideoPID)
}

// ============================================================================
// VPS Prepended to HEVC Keyframes
// ============================================================================

// TestMux10bit_VPSPrepended verifies that HEVC keyframes in the TS output
// contain VPS+SPS+PPS parameter sets in Annex B format before the frame data.
//
// Per ITU-T H.265:
//
//	VPS NALU type = 32 (nal_unit_type bits 1-6 of first header byte >> 1)
//	SPS NALU type = 33
//	PPS NALU type = 34
//	Annex B start code = 0x00000001 (4 bytes)
func TestMux10bit_VPSPrepended(t *testing.T) {
	muxer := output.NewTSMuxer()

	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	// Create HEVC keyframe with known parameter sets
	vps := []byte{0x40, 0x01, 0x0C, 0x01, 0xFF, 0xFF, 0x01, 0x60}
	sps := []byte{0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03}
	pps := []byte{0x44, 0x01, 0xC0, 0xF7, 0x24, 0x00}

	// IDR slice NALU
	idrSlice := []byte{0x26, 0x01, 0xAF, 0x09, 0x40, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03}

	// Build AVC1 (length-prefixed) WireData from the IDR slice only
	wireData := make([]byte, 4+len(idrSlice))
	wireData[0] = byte(len(idrSlice) >> 24)
	wireData[1] = byte(len(idrSlice) >> 16)
	wireData[2] = byte(len(idrSlice) >> 8)
	wireData[3] = byte(len(idrSlice))
	copy(wireData[4:], idrSlice)

	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		Codec:      "h265",
		VPS:        vps,
		SPS:        sps,
		PPS:        pps,
		WireData:   wireData,
	}

	err := muxer.WriteVideo(frame)
	require.NoError(t, err)
	require.NotEmpty(t, tsData)

	// Extract video PES data from TS packets
	packets := testutil.ParseTSPackets(t, tsData)
	var videoPayload []byte
	for _, pkt := range packets {
		if pkt.PID == mpegts.VideoPID && pkt.HasPayload {
			videoPayload = append(videoPayload, pkt.Payload...)
		}
	}
	require.NotEmpty(t, videoPayload, "no video payload found")

	// Scan for Annex B start codes followed by HEVC NALU types
	foundVPS := false
	foundSPS := false
	foundPPS := false

	for i := 0; i < len(videoPayload)-5; i++ {
		// Check for 4-byte start code
		if videoPayload[i] == 0x00 &&
			videoPayload[i+1] == 0x00 &&
			videoPayload[i+2] == 0x00 &&
			videoPayload[i+3] == 0x01 {

			// HEVC NALU type is in bits 1-6 of the first NALU header byte
			naluType := (videoPayload[i+4] >> 1) & 0x3F

			switch naluType {
			case codec.HEVCNALVPS: // 32
				foundVPS = true
			case codec.HEVCNALSPS: // 33
				foundSPS = true
			case codec.HEVCNALPPS: // 34
				foundPPS = true
			}
		}
	}

	assert.True(t, foundVPS, "VPS (NALU type 32) not found in HEVC keyframe output")
	assert.True(t, foundSPS, "SPS (NALU type 33) not found in HEVC keyframe output")
	assert.True(t, foundPPS, "PPS (NALU type 34) not found in HEVC keyframe output")
}
