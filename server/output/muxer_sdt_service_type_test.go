package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// --- Bug 1: SDT service_type must reflect actual codec and resolution ---

func TestDVBServiceType_AVC_HD(t *testing.T) {
	// H.264 at 1080p is "advanced codec HD digital television" (0x19)
	st := dvbServiceType("h264", 1920, 1080)
	assert.Equal(t, byte(0x19), st)
}

func TestDVBServiceType_AVC_SD(t *testing.T) {
	// H.264 at 720p is still "advanced codec" — not MPEG-2
	st := dvbServiceType("h264", 1280, 720)
	assert.Equal(t, byte(0x19), st)
}

func TestDVBServiceType_HEVC_HD(t *testing.T) {
	// HEVC at 1080p is "advanced codec HD digital television" (0x19)
	st := dvbServiceType("h265", 1920, 1080)
	assert.Equal(t, byte(0x19), st)
}

func TestDVBServiceType_HEVC_UHD(t *testing.T) {
	// HEVC at 4K is "HEVC UHD digital television" (0x1F)
	st := dvbServiceType("h265", 3840, 2160)
	assert.Equal(t, byte(0x1F), st)
}

func TestDVBServiceType_HEVC_GreaterThan1080p(t *testing.T) {
	// Anything above 1080p with HEVC should be UHD
	st := dvbServiceType("h265", 2560, 1440)
	assert.Equal(t, byte(0x1F), st)
}

func TestDVBServiceType_UnknownCodec_Defaults(t *testing.T) {
	// Unknown codec defaults to advanced codec HD
	st := dvbServiceType("", 1920, 1080)
	assert.Equal(t, byte(0x19), st)
}

func TestBuildSDTPacket_ServiceType_HEVC_HD(t *testing.T) {
	pkt := buildSDTPacket("h265", 1920, 1080)
	require.Equal(t, 188, len(pkt))

	// Find service_descriptor (tag 0x48) and check service_type byte
	serviceType := extractSDTServiceType(t, pkt)
	assert.Equal(t, byte(0x19), serviceType, "HEVC 1080p should be advanced codec HD (0x19)")
}

func TestBuildSDTPacket_ServiceType_HEVC_UHD(t *testing.T) {
	pkt := buildSDTPacket("h265", 3840, 2160)
	require.Equal(t, 188, len(pkt))

	serviceType := extractSDTServiceType(t, pkt)
	assert.Equal(t, byte(0x1F), serviceType, "HEVC 4K should be HEVC UHD (0x1F)")
}

func TestBuildSDTPacket_ServiceType_AVC(t *testing.T) {
	pkt := buildSDTPacket("h264", 1920, 1080)
	require.Equal(t, 188, len(pkt))

	serviceType := extractSDTServiceType(t, pkt)
	assert.Equal(t, byte(0x19), serviceType, "AVC 1080p should be advanced codec HD (0x19)")
}

func TestTSMuxer_SDTServiceType_MatchesCodec(t *testing.T) {
	// Full integration: HEVC muxer should emit SDT with service_type 0x19
	m := NewTSMuxer()
	m.SetResolution(1920, 1080)
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// HEVC keyframe (VPS + SPS + PPS required for h265)
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		VPS:      []byte{0x40, 0x01, 0x0C, 0x01, 0xFF, 0xFF},
		SPS:      []byte{0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00},
		PPS:      []byte{0x44, 0x01, 0xC1, 0x73},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x26, 0x01, 0xAF, 0x08, 0x40},
		Codec:    "h265",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	// Find SDT packet and verify service_type
	sdtPkt := findTSPacketByPID(output, 0x0011)
	require.NotNil(t, sdtPkt, "output must contain SDT")

	serviceType := extractSDTServiceType(t, sdtPkt)
	assert.Equal(t, byte(0x19), serviceType, "HEVC 1080p should produce service_type 0x19")
}

// --- Bug 2: NIT/SDT must be emitted periodically, not just once ---

func TestTSMuxer_SDTRepeatedPeriodically(t *testing.T) {
	m := NewTSMuxer()
	m.SetResolution(1920, 1080)
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Write first keyframe (PTS = 1s)
	idrFrame := makeH264IDR(90000)
	require.NoError(t, m.WriteVideo(idrFrame))

	// Write 3 seconds of P-frames at 29.97fps (~90 frames)
	// SDT should repeat every 2s per ETSI EN 300 468
	pts := int64(90000 + 3003)
	for i := 0; i < 90; i++ {
		pFrame := &media.VideoFrame{
			PTS: pts, DTS: pts, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
			Codec:    "h264",
		}
		require.NoError(t, m.WriteVideo(pFrame))
		pts += 3003
	}

	// Count SDT packets in output
	sdtCount := countTSPacketsByPID(output, 0x0011)
	assert.GreaterOrEqual(t, sdtCount, 2, "SDT must be emitted at least twice in 3s (2s max interval)")
}

func TestTSMuxer_NITRepeatedPeriodically(t *testing.T) {
	m := NewTSMuxer()
	m.SetResolution(1920, 1080)
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Write first keyframe
	idrFrame := makeH264IDR(90000)
	require.NoError(t, m.WriteVideo(idrFrame))

	// Write 11 seconds of P-frames at 29.97fps (~330 frames)
	// NIT should repeat every 10s per ETSI EN 300 468
	pts := int64(90000 + 3003)
	for i := 0; i < 330; i++ {
		pFrame := &media.VideoFrame{
			PTS: pts, DTS: pts, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
			Codec:    "h264",
		}
		require.NoError(t, m.WriteVideo(pFrame))
		pts += 3003
	}

	nitCount := countTSPacketsByPID(output, 0x0010)
	assert.GreaterOrEqual(t, nitCount, 2, "NIT must be emitted at least twice in 11s (10s max interval)")
}

func TestTSMuxer_NITSD_ContinuityCounters(t *testing.T) {
	m := NewTSMuxer()
	m.SetResolution(1920, 1080)
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Write enough frames for multiple SDT emissions (>4s at 29.97fps)
	idrFrame := makeH264IDR(90000)
	require.NoError(t, m.WriteVideo(idrFrame))

	pts := int64(90000 + 3003)
	for i := 0; i < 130; i++ {
		pFrame := &media.VideoFrame{
			PTS: pts, DTS: pts, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
			Codec:    "h264",
		}
		require.NoError(t, m.WriteVideo(pFrame))
		pts += 3003
	}

	// Collect all SDT packets and verify CC increments
	sdtCCs := collectCCByPID(output, 0x0011)
	require.GreaterOrEqual(t, len(sdtCCs), 2, "need at least 2 SDT emissions")
	for i := 1; i < len(sdtCCs); i++ {
		expected := (sdtCCs[i-1] + 1) & 0x0F
		assert.Equal(t, expected, sdtCCs[i],
			"SDT CC must increment: packet %d has CC=%d, expected %d", i, sdtCCs[i], expected)
	}
}

// --- Helpers ---

func makeH264IDR(pts int64) *media.VideoFrame {
	return &media.VideoFrame{
		PTS: pts, DTS: pts, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
}

func findTSPacketByPID(data []byte, pid uint16) []byte {
	for i := 0; i+188 <= len(data); i += 188 {
		if data[i] != 0x47 {
			continue
		}
		pktPID := uint16(data[i+1]&0x1F)<<8 | uint16(data[i+2])
		if pktPID == pid {
			pkt := make([]byte, 188)
			copy(pkt, data[i:i+188])
			return pkt
		}
	}
	return nil
}

func countTSPacketsByPID(data []byte, pid uint16) int {
	count := 0
	for i := 0; i+188 <= len(data); i += 188 {
		if data[i] != 0x47 {
			continue
		}
		pktPID := uint16(data[i+1]&0x1F)<<8 | uint16(data[i+2])
		if pktPID == pid {
			count++
		}
	}
	return count
}

func collectCCByPID(data []byte, pid uint16) []uint8 {
	var ccs []uint8
	for i := 0; i+188 <= len(data); i += 188 {
		if data[i] != 0x47 {
			continue
		}
		pktPID := uint16(data[i+1]&0x1F)<<8 | uint16(data[i+2])
		if pktPID == pid {
			cc := data[i+3] & 0x0F
			ccs = append(ccs, cc)
		}
	}
	return ccs
}

// extractSDTServiceType finds the service_descriptor (tag 0x48) in an SDT
// packet and returns the service_type byte.
func extractSDTServiceType(t *testing.T, pkt []byte) byte {
	t.Helper()
	require.Equal(t, 188, len(pkt))
	// Walk the packet looking for descriptor tag 0x48
	// Service descriptor format: tag(0x48) + length + service_type + ...
	for i := 5; i < 180; i++ {
		if pkt[i] == 0x48 && i+2 < 188 {
			descLen := pkt[i+1]
			if descLen > 0 && descLen < 150 && i+2+int(descLen) <= 188 {
				return pkt[i+2] // service_type is first byte after tag+length
			}
		}
	}
	t.Fatal("service_descriptor (tag 0x48) not found in SDT packet")
	return 0
}
