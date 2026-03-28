package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestBuildSDTPacket_ValidTSPacket(t *testing.T) {
	pkt := buildSDTPacket()
	assert.Equal(t, 188, len(pkt), "SDT packet must be 188 bytes")
	assert.Equal(t, byte(0x47), pkt[0], "must start with sync byte")

	// PID should be 0x0011 (SDT)
	pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
	assert.Equal(t, uint16(0x0011), pid, "PID must be 0x0011 (SDT)")

	// Payload unit start indicator should be set
	assert.Equal(t, byte(1), (pkt[1]>>6)&1, "payload_unit_start_indicator must be 1")

	// Table ID should be 0x42 (SDT actual)
	pointerField := pkt[4]
	assert.Equal(t, byte(0), pointerField, "pointer field should be 0")
	assert.Equal(t, byte(0x42), pkt[5], "table_id must be 0x42 (SDT actual)")
}

func TestBuildSDTPacket_ContainsServiceName(t *testing.T) {
	pkt := buildSDTPacket()

	// The packet should contain both "Switchframe" and "switchframe.dev"
	found := false
	for i := 0; i < len(pkt)-11; i++ {
		if string(pkt[i:i+11]) == "Switchframe" {
			found = true
			break
		}
	}
	assert.True(t, found, "SDT packet must contain 'Switchframe' service name")

	found = false
	for i := 0; i < len(pkt)-15; i++ {
		if string(pkt[i:i+15]) == "switchframe.dev" {
			found = true
			break
		}
	}
	assert.True(t, found, "SDT packet must contain 'switchframe.dev' provider name")
}

func TestBuildSDTPacket_ValidCRC(t *testing.T) {
	pkt := buildSDTPacket()

	// Extract the section: starts at pkt[5] (after TS header + pointer field)
	// section_length tells us how long the section is (including CRC)
	sectionStart := 5
	sectionLen := int(pkt[sectionStart+1]&0x0F)<<8 | int(pkt[sectionStart+2])
	// Total section bytes: table_id(1) + flags+length(2) + section_length
	totalSection := 3 + sectionLen

	section := pkt[sectionStart : sectionStart+totalSection]

	// Verify CRC: CRC of entire section (including CRC field) should be 0
	// when computed over the whole thing. Or: CRC of section minus CRC field
	// should equal the stored CRC.
	sectionWithoutCRC := section[:len(section)-4]
	storedCRC := uint32(section[len(section)-4])<<24 |
		uint32(section[len(section)-3])<<16 |
		uint32(section[len(section)-2])<<8 |
		uint32(section[len(section)-1])
	computedCRC := crc32MPEG2(sectionWithoutCRC)
	assert.Equal(t, storedCRC, computedCRC, "CRC-32/MPEG-2 must be valid")
}

func TestTSMuxer_SDTInOutput(t *testing.T) {
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
	require.NotEmpty(t, output)

	// Scan output for an SDT packet (PID 0x0011)
	foundSDT := false
	for i := 0; i+188 <= len(output); i += 188 {
		if output[i] != 0x47 {
			continue
		}
		pid := uint16(output[i+1]&0x1F)<<8 | uint16(output[i+2])
		if pid == 0x0011 {
			foundSDT = true
			// Verify it contains the service name
			pkt := output[i : i+188]
			nameFound := false
			for j := 0; j < len(pkt)-11; j++ {
				if string(pkt[j:j+11]) == "Switchframe" {
					nameFound = true
					break
				}
			}
			assert.True(t, nameFound, "SDT packet must contain 'Switchframe'")
			break
		}
	}
	assert.True(t, foundSDT, "muxer output must contain SDT packet on PID 0x0011")
}

func TestCRC32MPEG2_KnownValues(t *testing.T) {
	// Known CRC-32/MPEG-2 test vectors
	// "123456789" → 0x0376E6E7
	assert.Equal(t, uint32(0x0376E6E7), crc32MPEG2([]byte("123456789")))
}
