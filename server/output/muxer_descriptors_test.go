package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// initMuxerAndCollect initializes a TSMuxer with a keyframe and returns the TS output.
func initMuxerAndCollect(t *testing.T) []byte {
	t.Helper()
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))
	return output
}

// findPMTPayload scans TS packets for PID 0x1000 (PMT) and returns the
// raw section payload (after pointer field).
func findPMTPayload(t *testing.T, tsData []byte) []byte {
	t.Helper()
	for i := 0; i+188 <= len(tsData); i += 188 {
		if tsData[i] != 0x47 {
			continue
		}
		pid := uint16(tsData[i+1]&0x1F)<<8 | uint16(tsData[i+2])
		if pid != pmtPID {
			continue
		}
		// payload_unit_start_indicator
		pusi := (tsData[i+1] >> 6) & 1
		if pusi == 0 {
			continue
		}
		// Skip TS header (4 bytes) + pointer field (1 byte)
		return tsData[i+5 : i+188]
	}
	t.Fatal("no PMT packet found")
	return nil
}

// findDescriptorInES scans the PMT section payload for an ES with the given PID,
// then searches its descriptor loop for a descriptor with the given tag.
// Returns the descriptor bytes (tag + length + data) or nil if not found.
func findDescriptorInES(t *testing.T, pmtPayload []byte, esPID uint16, descTag byte) []byte {
	t.Helper()
	// PMT section format:
	//   table_id(1) + flags+section_length(2) + program_number(2) +
	//   version(1) + section_number(1) + last_section_number(1) + PCR_PID(2) +
	//   program_info_length(2) + program_descriptors(...) + ES_info...
	if len(pmtPayload) < 12 {
		t.Fatal("PMT payload too short")
	}

	// program_info_length is at bytes 10-11 (relative to section start)
	progInfoLen := int(pmtPayload[10]&0x0F)<<8 | int(pmtPayload[11])
	esStart := 12 + progInfoLen

	// Parse ES entries
	pos := esStart
	for pos+5 <= len(pmtPayload)-4 { // -4 for CRC32
		streamType := pmtPayload[pos]
		_ = streamType
		pid := uint16(pmtPayload[pos+1]&0x1F)<<8 | uint16(pmtPayload[pos+2])
		esInfoLen := int(pmtPayload[pos+3]&0x0F)<<8 | int(pmtPayload[pos+4])
		descStart := pos + 5
		descEnd := descStart + esInfoLen

		if pid == esPID {
			// Search descriptor loop for our tag
			dp := descStart
			for dp+2 <= descEnd {
				tag := pmtPayload[dp]
				dlen := int(pmtPayload[dp+1])
				if tag == descTag && dp+2+dlen <= descEnd {
					return pmtPayload[dp : dp+2+dlen]
				}
				dp += 2 + dlen
			}
			return nil // ES found but descriptor not present
		}

		pos = descEnd
	}
	t.Fatalf("ES with PID 0x%04X not found in PMT", esPID)
	return nil
}

func TestPMT_VideoES_HasMaxBitrateDescriptor(t *testing.T) {
	tsData := initMuxerAndCollect(t)
	pmtPayload := findPMTPayload(t, tsData)
	desc := findDescriptorInES(t, pmtPayload, videoPID, 0x0E) // tag 0x0E = maximum_bitrate
	require.NotNil(t, desc, "video ES must have maximum bitrate descriptor (0x0E)")

	// Descriptor format: tag(1) + length(1) + reserved(2 bits) + max_bitrate(22 bits)
	assert.Equal(t, byte(0x0E), desc[0])
	assert.Equal(t, byte(3), desc[1], "max bitrate descriptor length must be 3")
	// Extract 22-bit max_bitrate value
	maxBitrate := uint32(desc[2]&0x3F)<<16 | uint32(desc[3])<<8 | uint32(desc[4])
	assert.Greater(t, maxBitrate, uint32(0), "max bitrate should be positive")
}

func TestPMT_VideoES_HasDataStreamAlignmentDescriptor(t *testing.T) {
	tsData := initMuxerAndCollect(t)
	pmtPayload := findPMTPayload(t, tsData)
	desc := findDescriptorInES(t, pmtPayload, videoPID, 0x06) // tag 0x06 = data_stream_alignment
	require.NotNil(t, desc, "video ES must have data stream alignment descriptor (0x06)")

	assert.Equal(t, byte(0x06), desc[0])
	assert.Equal(t, byte(1), desc[1], "alignment descriptor length must be 1")
	assert.Equal(t, byte(0x02), desc[2], "video alignment type should be 0x02 (access unit)")
}

func TestPMT_AudioES_HasLanguageDescriptor(t *testing.T) {
	tsData := initMuxerAndCollect(t)
	pmtPayload := findPMTPayload(t, tsData)
	desc := findDescriptorInES(t, pmtPayload, audioPID, 0x0A) // tag 0x0A = ISO_639_language
	require.NotNil(t, desc, "audio ES must have ISO 639 language descriptor (0x0A)")

	assert.Equal(t, byte(0x0A), desc[0])
	// Language code is 3 bytes + 1 byte audio_type
	assert.GreaterOrEqual(t, int(desc[1]), 4, "language descriptor length >= 4")
	assert.Equal(t, "eng", string(desc[2:5]), "language should be 'eng'")
}

func TestPMT_AudioES_HasDataStreamAlignmentDescriptor(t *testing.T) {
	tsData := initMuxerAndCollect(t)
	pmtPayload := findPMTPayload(t, tsData)
	desc := findDescriptorInES(t, pmtPayload, audioPID, 0x06) // tag 0x06 = data_stream_alignment
	require.NotNil(t, desc, "audio ES must have data stream alignment descriptor (0x06)")

	assert.Equal(t, byte(0x06), desc[0])
	assert.Equal(t, byte(1), desc[1])
	assert.Equal(t, byte(0x01), desc[2], "audio alignment type should be 0x01 (sync word)")
}
