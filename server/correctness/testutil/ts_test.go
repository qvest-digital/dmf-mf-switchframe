package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// buildTSPacket creates a minimal valid 188-byte TS packet.
func buildTSPacket(pid uint16, cc uint8, hasAdaptation, hasPayload bool) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47 // sync byte

	// PID: 13 bits split across bytes 1 and 2
	pkt[1] = byte(pid>>8) & 0x1F
	pkt[2] = byte(pid & 0xFF)

	// Adaptation field control (2 bits) + continuity counter (4 bits) in byte 3
	var afc byte
	if hasAdaptation && hasPayload {
		afc = 0x30
	} else if hasAdaptation {
		afc = 0x20
	} else if hasPayload {
		afc = 0x10
	}
	pkt[3] = afc | (cc & 0x0F)

	if hasAdaptation {
		// Minimal adaptation field: length=1, flags=0
		pkt[4] = 1 // adaptation field length
		pkt[5] = 0 // flags
		// Fill rest with stuffing (0xFF)
		for i := 6; i < 188; i++ {
			pkt[i] = 0xFF
		}
	}

	return pkt
}

// buildTSPacketWithPCR creates a TS packet with a PCR in the adaptation field.
func buildTSPacketWithPCR(pid uint16, cc uint8, pcrBase int64) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = byte(pid>>8) & 0x1F
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = 0x20 | (cc & 0x0F) // adaptation only

	// Adaptation field with PCR
	pkt[4] = 7    // adaptation field length (1 flag + 6 PCR bytes)
	pkt[5] = 0x10 // PCR flag set

	// PCR: 33-bit base + 6 reserved + 9-bit extension
	// We set extension to 0 for simplicity
	pkt[6] = byte(pcrBase >> 25)
	pkt[7] = byte(pcrBase >> 17)
	pkt[8] = byte(pcrBase >> 9)
	pkt[9] = byte(pcrBase >> 1)
	pkt[10] = byte((pcrBase&1)<<7) | 0x7E // bit0 of base << 7, then 6 reserved bits = 1
	pkt[11] = 0                           // extension = 0

	// Fill rest with stuffing
	for i := 12; i < 188; i++ {
		pkt[i] = 0xFF
	}

	return pkt
}

func TestParseTSPackets_Basic(t *testing.T) {
	// Build 3 valid packets
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)
	data = append(data, buildTSPacket(0x0100, 2, false, true)...)

	packets := ParseTSPackets(t, data)
	require.Len(t, packets, 3)

	require.Equal(t, byte(0x47), packets[0].SyncByte)
	require.Equal(t, uint16(0x0100), packets[0].PID)
	require.Equal(t, uint8(0), packets[0].ContinuityCounter)
	require.True(t, packets[0].HasPayload)
	require.False(t, packets[0].HasAdaptation)

	require.Equal(t, uint8(1), packets[1].ContinuityCounter)
	require.Equal(t, uint8(2), packets[2].ContinuityCounter)
}

func TestParseTSPackets_WithAdaptation(t *testing.T) {
	data := buildTSPacket(0x0100, 0, true, false)
	packets := ParseTSPackets(t, data)
	require.Len(t, packets, 1)
	require.True(t, packets[0].HasAdaptation)
	require.False(t, packets[0].HasPayload)
	require.NotNil(t, packets[0].AdaptationField)
}

func TestParseTSPackets_WithPCR(t *testing.T) {
	pcrBase := int64(90000) // 1 second at 90kHz
	data := buildTSPacketWithPCR(0x0100, 0, pcrBase)
	packets := ParseTSPackets(t, data)

	require.Len(t, packets, 1)
	require.NotNil(t, packets[0].AdaptationField)
	require.NotNil(t, packets[0].AdaptationField.PCR)
	require.Equal(t, pcrBase, *packets[0].AdaptationField.PCR)
}

func TestAssertValidTSStructure(t *testing.T) {
	data := make([]byte, 0, 188*2)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)

	// Should not fail
	AssertValidTSStructure(t, data)
}

func TestAssertContinuityCounters(t *testing.T) {
	// Valid: sequential CC for same PID
	data := make([]byte, 0, 188*4)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)
	data = append(data, buildTSPacket(0x0100, 2, false, true)...)
	data = append(data, buildTSPacket(0x0100, 3, false, true)...)

	AssertContinuityCounters(t, data)
}

func TestAssertContinuityCounters_Wrap(t *testing.T) {
	// CC wraps from 15 to 0
	data := make([]byte, 0, 188*2)
	data = append(data, buildTSPacket(0x0100, 15, false, true)...)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)

	AssertContinuityCounters(t, data)
}

func TestAssertContinuityCounters_MultiplePIDs(t *testing.T) {
	// Different PIDs track CC independently
	data := make([]byte, 0, 188*4)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0200, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)
	data = append(data, buildTSPacket(0x0200, 1, false, true)...)

	AssertContinuityCounters(t, data)
}

func TestAssertPATPresent(t *testing.T) {
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...) // not PAT
	data = append(data, buildTSPacket(0x0000, 0, false, true)...) // PAT (PID 0)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)

	AssertPATPresent(t, data, 3)
}

func TestAssertPMTPresent(t *testing.T) {
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x1000, 0, false, true)...) // PMT at PID 0x1000
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)

	AssertPMTPresent(t, data, 0x1000, 3)
}

func TestExtractPCRValues(t *testing.T) {
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacketWithPCR(0x0100, 0, 90000)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...) // no PCR
	data = append(data, buildTSPacketWithPCR(0x0100, 2, 180000)...)

	pcrs := ExtractPCRValues(t, data)
	require.Len(t, pcrs, 2)
	require.Equal(t, int64(90000), pcrs[0])
	require.Equal(t, int64(180000), pcrs[1])
}

func TestAssertStuffingBytes(t *testing.T) {
	// Build packet with adaptation field containing stuffing
	pkt := buildTSPacket(0x0100, 0, true, false)
	packets := ParseTSPackets(t, pkt)

	// Should pass: stuffing bytes are 0xFF
	AssertStuffingBytes(t, packets)
}

func TestAssertRandomAccessIndicator(t *testing.T) {
	// Build a packet with RAI set
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = 0x01
	pkt[2] = 0x00
	pkt[3] = 0x20 // adaptation only
	pkt[4] = 1    // adaptation field length
	pkt[5] = 0x40 // random_access_indicator set

	for i := 6; i < 188; i++ {
		pkt[i] = 0xFF
	}

	packets := ParseTSPackets(t, pkt)
	require.True(t, packets[0].AdaptationField.RandomAccessIndicator)
}
