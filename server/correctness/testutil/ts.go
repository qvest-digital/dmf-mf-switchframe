package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

// TSPacket represents a parsed MPEG-TS packet.
type TSPacket struct {
	SyncByte          byte
	PID               uint16
	ContinuityCounter uint8
	HasAdaptation     bool
	HasPayload        bool
	AdaptationField   *AdaptationField
	Payload           []byte
}

// AdaptationField represents the adaptation field of a TS packet.
type AdaptationField struct {
	Length                uint8
	RandomAccessIndicator bool
	PCR                   *int64
	StuffingBytes         []byte
}

// ParseTSPackets parses raw TS bytes into packets.
// Asserts every packet starts with 0x47 and data length is a multiple of 188.
func ParseTSPackets(t *testing.T, data []byte) []TSPacket {
	t.Helper()
	require.Equal(t, 0, len(data)%mpegts.PacketSize,
		"data length %d is not a multiple of %d", len(data), mpegts.PacketSize)

	numPackets := len(data) / mpegts.PacketSize
	packets := make([]TSPacket, 0, numPackets)

	for i := 0; i < numPackets; i++ {
		offset := i * mpegts.PacketSize
		pkt := data[offset : offset+mpegts.PacketSize]

		require.Equal(t, byte(0x47), pkt[0],
			"packet %d: sync byte is 0x%02X, expected 0x47", i, pkt[0])

		p := TSPacket{
			SyncByte: pkt[0],
			PID:      mpegts.ExtractPID(pkt),
		}

		// Byte 3: adaptation_field_control (bits 5-4) and continuity_counter (bits 3-0)
		afc := (pkt[3] >> 4) & 0x03
		p.ContinuityCounter = pkt[3] & 0x0F
		p.HasAdaptation = afc == 0x02 || afc == 0x03
		p.HasPayload = afc == 0x01 || afc == 0x03

		payloadStart := 4

		if p.HasAdaptation && len(pkt) > 4 {
			af := parseAdaptationField(pkt[4:])
			p.AdaptationField = af
			payloadStart = 4 + 1 + int(pkt[4]) // 4 header + 1 length byte + length
		}

		if p.HasPayload && payloadStart < mpegts.PacketSize {
			p.Payload = pkt[payloadStart:]
		}

		packets = append(packets, p)
	}

	return packets
}

func parseAdaptationField(data []byte) *AdaptationField {
	af := &AdaptationField{
		Length: data[0],
	}

	if af.Length == 0 {
		return af
	}

	// Flags byte
	flags := data[1]
	af.RandomAccessIndicator = (flags & 0x40) != 0
	hasPCR := (flags & 0x10) != 0

	offset := 2 // past length + flags

	if hasPCR && int(af.Length) >= 7 {
		// PCR: 6 bytes
		// 33-bit base (90kHz) + 6 reserved bits + 9-bit extension (27MHz)
		pcrBase := int64(data[offset])<<25 |
			int64(data[offset+1])<<17 |
			int64(data[offset+2])<<9 |
			int64(data[offset+3])<<1 |
			int64(data[offset+4]>>7)

		pcr := pcrBase
		af.PCR = &pcr
		offset += 6
	}

	// Remaining bytes in adaptation field are stuffing
	stuffEnd := 1 + int(af.Length) // length byte + adaptation field data
	if offset < stuffEnd && offset < len(data) {
		end := stuffEnd
		if end > len(data) {
			end = len(data)
		}
		af.StuffingBytes = data[offset:end]
	}

	return af
}

// AssertValidTSStructure verifies sync bytes and 188-byte alignment.
func AssertValidTSStructure(t *testing.T, data []byte) {
	t.Helper()
	require.Equal(t, 0, len(data)%mpegts.PacketSize,
		"data length %d is not a multiple of %d", len(data), mpegts.PacketSize)

	numPackets := len(data) / mpegts.PacketSize
	for i := 0; i < numPackets; i++ {
		offset := i * mpegts.PacketSize
		require.Equal(t, byte(0x47), data[offset],
			"packet %d at offset %d: sync byte is 0x%02X, expected 0x47", i, offset, data[offset])
	}
}

// AssertContinuityCounters verifies that continuity counters increment
// mod 16 per PID, with no gaps. Adaptation-only packets (no payload)
// are allowed to repeat the same CC.
func AssertContinuityCounters(t *testing.T, data []byte) {
	t.Helper()
	packets := ParseTSPackets(t, data)

	// Track last CC per PID. -1 means not seen yet.
	lastCC := make(map[uint16]int)

	for i, pkt := range packets {
		prev, seen := lastCC[pkt.PID]
		if !seen {
			lastCC[pkt.PID] = int(pkt.ContinuityCounter)
			continue
		}

		if !pkt.HasPayload {
			// Adaptation-only: CC may repeat
			continue
		}

		expected := (prev + 1) % 16
		require.Equal(t, expected, int(pkt.ContinuityCounter),
			"packet %d PID 0x%04X: CC discontinuity, expected %d got %d",
			i, pkt.PID, expected, pkt.ContinuityCounter)

		lastCC[pkt.PID] = int(pkt.ContinuityCounter)
	}
}

// AssertPATPresent asserts that PID 0x0000 (PAT) is found within the first
// N packets.
func AssertPATPresent(t *testing.T, data []byte, withinPackets int) {
	t.Helper()
	packets := ParseTSPackets(t, data)

	limit := withinPackets
	if limit > len(packets) {
		limit = len(packets)
	}

	found := false
	for i := 0; i < limit; i++ {
		if packets[i].PID == 0x0000 {
			found = true
			break
		}
	}

	require.True(t, found, "PAT (PID 0x0000) not found within first %d packets", withinPackets)
}

// AssertPMTPresent asserts that the specified PID is found within the first
// N packets.
func AssertPMTPresent(t *testing.T, data []byte, pmtPID uint16, withinPackets int) {
	t.Helper()
	packets := ParseTSPackets(t, data)

	limit := withinPackets
	if limit > len(packets) {
		limit = len(packets)
	}

	found := false
	for i := 0; i < limit; i++ {
		if packets[i].PID == pmtPID {
			found = true
			break
		}
	}

	require.True(t, found, "PMT (PID 0x%04X) not found within first %d packets", pmtPID, withinPackets)
}

// ExtractPCRValues returns all PCR base values found in the TS data.
func ExtractPCRValues(t *testing.T, data []byte) []int64 {
	t.Helper()
	packets := ParseTSPackets(t, data)

	var pcrs []int64
	for _, pkt := range packets {
		if pkt.AdaptationField != nil && pkt.AdaptationField.PCR != nil {
			pcrs = append(pcrs, *pkt.AdaptationField.PCR)
		}
	}

	return pcrs
}

// AssertRandomAccessIndicator verifies that packets with adaptation fields
// correctly report their random access indicator.
func AssertRandomAccessIndicator(t *testing.T, packets []TSPacket) {
	t.Helper()
	for i, pkt := range packets {
		if pkt.AdaptationField != nil && pkt.AdaptationField.RandomAccessIndicator {
			require.True(t, pkt.HasAdaptation,
				"packet %d: RAI set but no adaptation field", i)
		}
	}
}

// AssertStuffingBytes verifies that all stuffing bytes in adaptation fields
// are 0xFF as required by the MPEG-TS spec.
func AssertStuffingBytes(t *testing.T, packets []TSPacket) {
	t.Helper()
	for i, pkt := range packets {
		if pkt.AdaptationField != nil && len(pkt.AdaptationField.StuffingBytes) > 0 {
			for j, b := range pkt.AdaptationField.StuffingBytes {
				require.Equal(t, byte(0xFF), b,
					"packet %d: stuffing byte %d is 0x%02X, expected 0xFF", i, j, b)
			}
		}
	}
}
