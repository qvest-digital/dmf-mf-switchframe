package scte104

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mustWrap(t *testing.T, data []byte) []byte {
	t.Helper()
	packet, err := WrapST291(data)
	require.NoError(t, err, "WrapST291 error")
	return packet
}

func TestParseST291_WrapRoundTrip(t *testing.T) {
	testData := []byte{0x01, 0x02, 0x03, 0x04, 0x05}

	packet := mustWrap(t, testData)
	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	require.Equal(t, testData, got, "round-trip data mismatch")
}

func TestParseST291_InvalidDID(t *testing.T) {
	// Build a valid packet then corrupt the DID.
	packet := mustWrap(t, []byte{0xAA})
	packet[0] = 0x42 // Wrong DID
	// Fix checksum for the corrupted packet.
	csIndex := 3 + int(packet[2])
	packet[csIndex] = computeChecksum(packet[:csIndex])

	_, err := ParseST291(packet)
	require.Error(t, err, "expected error for invalid DID")
	require.ErrorIs(t, err, ErrST291InvalidDID)
}

func TestParseST291_InvalidSDID(t *testing.T) {
	packet := mustWrap(t, []byte{0xAA})
	packet[1] = 0x08 // Wrong SDID
	// Fix checksum.
	csIndex := 3 + int(packet[2])
	packet[csIndex] = computeChecksum(packet[:csIndex])

	_, err := ParseST291(packet)
	require.Error(t, err, "expected error for invalid SDID")
	require.ErrorIs(t, err, ErrST291InvalidSDID)
}

func TestParseST291_BadChecksum(t *testing.T) {
	packet := mustWrap(t, []byte{0x01, 0x02, 0x03})
	// Corrupt the checksum byte (last byte).
	packet[len(packet)-1] ^= 0xFF

	_, err := ParseST291(packet)
	require.Error(t, err, "expected error for bad checksum")
	require.ErrorIs(t, err, ErrST291BadChecksum)
}

func TestParseST291_TooShort(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
	}{
		{"empty", []byte{}},
		{"1 byte", []byte{0x41}},
		{"2 bytes", []byte{0x41, 0x07}},
		{"3 bytes", []byte{0x41, 0x07, 0x01}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseST291(tt.packet)
			require.Error(t, err, "expected error for too-short packet")
			require.ErrorIs(t, err, ErrST291TooShort)
		})
	}
}

func TestParseST291_TooShort_DCExceedsLength(t *testing.T) {
	// DID=0x41, SDID=0x07, DC=5, but only 1 UDW byte + CS = 6 bytes total needed, provide 5.
	packet := []byte{0x41, 0x07, 0x05, 0x00, 0x01}

	_, err := ParseST291(packet)
	require.Error(t, err, "expected error for insufficient data per DC")
	require.ErrorIs(t, err, ErrST291TooShort)
}

func TestParseST291_ZeroLengthPayload(t *testing.T) {
	// DID + SDID + DC=0 + CS
	// Checksum = (0x41 + 0x07 + 0x00) & 0xFF = 0x48
	packet := []byte{0x41, 0x07, 0x00, 0x48}

	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	require.Empty(t, got, "expected empty payload")
}

func TestParseST291_MaxLengthPayload(t *testing.T) {
	// 254 bytes of SCTE-104 data (max for single non-fragmented packet).
	data := make([]byte, 254)
	for i := range data {
		data[i] = byte(i)
	}

	packet := mustWrap(t, data)
	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	require.Equal(t, data, got, "round-trip mismatch for 254-byte payload")
}

func TestParseST291_PayloadDescriptorStripping(t *testing.T) {
	// Manually construct a packet with payload descriptor = 0x00 and 3 data bytes.
	// DID=0x41, SDID=0x07, DC=4 (1 payload_desc + 3 data), payload_desc=0x00,
	// data=[0xAA, 0xBB, 0xCC], CS
	dc := byte(4)
	packet := []byte{0x41, 0x07, dc, 0x00, 0xAA, 0xBB, 0xCC, 0x00}
	// Compute and set checksum (all bytes before CS).
	packet[7] = computeChecksum(packet[:7])

	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	expected := []byte{0xAA, 0xBB, 0xCC}
	require.Equal(t, expected, got, "payload descriptor should be stripped")
}

func TestParseST291_FragmentedContinued(t *testing.T) {
	// Build a packet with continued_pkt bit set in payload descriptor.
	dc := byte(2)
	packet := []byte{0x41, 0x07, dc, st291PayloadDescContinued, 0xFF, 0x00}
	packet[5] = computeChecksum(packet[:5])

	_, err := ParseST291(packet)
	require.Error(t, err, "expected error for continued fragment")
	require.ErrorIs(t, err, ErrST291Fragmented)
}

func TestParseST291_FragmentedFollowing(t *testing.T) {
	// Build a packet with following_pkt bit set in payload descriptor.
	dc := byte(2)
	packet := []byte{0x41, 0x07, dc, st291PayloadDescFollowing, 0xFF, 0x00}
	packet[5] = computeChecksum(packet[:5])

	_, err := ParseST291(packet)
	require.Error(t, err, "expected error for following fragment")
	require.ErrorIs(t, err, ErrST291Fragmented)
}

func TestWrapST291_Structure(t *testing.T) {
	data := []byte{0x10, 0x20, 0x30}
	packet := mustWrap(t, data)

	// Expected: DID(1) + SDID(1) + DC(1) + payload_desc(1) + data(3) + CS(1) = 8
	require.Len(t, packet, 8, "packet length")

	require.Equal(t, byte(ST291DID), packet[0], "DID mismatch")
	require.Equal(t, byte(ST291SDID), packet[1], "SDID mismatch")
	require.Equal(t, byte(4), packet[2], "DC = 3 data + 1 payload descriptor")
	require.Equal(t, byte(0x00), packet[3], "payload descriptor")
	require.Equal(t, byte(0x10), packet[4], "UDW data[0]")
	require.Equal(t, byte(0x20), packet[5], "UDW data[1]")
	require.Equal(t, byte(0x30), packet[6], "UDW data[2]")

	// Verify checksum.
	expectedCS := computeChecksum(packet[:7])
	require.Equal(t, expectedCS, packet[7], "checksum mismatch")
}

func TestWrapST291_EmptyPayload(t *testing.T) {
	data := []byte{}
	packet := mustWrap(t, data)

	// DC = 0 + 1 = 1 (just payload descriptor).
	// Total: DID(1) + SDID(1) + DC(1) + payload_desc(1) + CS(1) = 5
	require.Len(t, packet, 5, "packet length")

	require.Equal(t, byte(1), packet[2], "DC = 1 (payload descriptor only)")
	require.Equal(t, byte(0x00), packet[3], "payload descriptor")
}

func TestWrapST291_PayloadTooLarge(t *testing.T) {
	data := make([]byte, 255)
	_, err := WrapST291(data)
	require.Error(t, err, "expected error for payload > 254 bytes")
	require.ErrorIs(t, err, ErrST291PayloadTooLarge)
}

func TestParseST291_WrapRoundTrip_EmptyPayload(t *testing.T) {
	data := []byte{}
	packet := mustWrap(t, data)
	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	// WrapST291 with empty data produces DC=1 (just payload descriptor).
	// ParseST291 strips the payload descriptor, returning empty data.
	require.Empty(t, got, "expected empty payload")
}

func TestParseST291_WrapRoundTrip_SingleByte(t *testing.T) {
	data := []byte{0xFF}
	packet := mustWrap(t, data)
	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	require.Equal(t, data, got, "round-trip data mismatch")
}

func TestParseST291_DoesNotAliasInput(t *testing.T) {
	data := []byte{0xAA, 0xBB, 0xCC}
	packet := mustWrap(t, data)

	got, err := ParseST291(packet)
	require.NoError(t, err, "ParseST291 error")

	// Mutate the original packet's UDW area.
	packet[4] = 0x00
	packet[5] = 0x00
	packet[6] = 0x00

	// The parsed result should be unaffected.
	require.Equal(t, data, got, "parsed data was aliased to input")
}

func TestComputeChecksum(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want byte
	}{
		{"empty", []byte{}, 0x00},
		{"single", []byte{0x41}, 0x41},
		{"overflow", []byte{0xFF, 0x01}, 0x00},
		{"typical header", []byte{0x41, 0x07, 0x02}, 0x4A},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeChecksum(tt.data)
			require.Equal(t, tt.want, got, "computeChecksum(%x)", tt.data)
		})
	}
}
