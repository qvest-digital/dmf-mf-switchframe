package scte104

import (
	"bytes"
	"errors"
	"testing"
)

func mustWrap(t *testing.T, data []byte) []byte {
	t.Helper()
	packet, err := WrapST291(data)
	if err != nil {
		t.Fatalf("WrapST291 error: %v", err)
	}
	return packet
}

func TestParseST291_WrapRoundTrip(t *testing.T) {
	testData := []byte{0x01, 0x02, 0x03, 0x04, 0x05}

	packet := mustWrap(t, testData)
	got, err := ParseST291(packet)
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	if !bytes.Equal(got, testData) {
		t.Errorf("round-trip data = %x, want %x", got, testData)
	}
}

func TestParseST291_InvalidDID(t *testing.T) {
	// Build a valid packet then corrupt the DID.
	packet := mustWrap(t, []byte{0xAA})
	packet[0] = 0x42 // Wrong DID
	// Fix checksum for the corrupted packet.
	csIndex := 3 + int(packet[2])
	packet[csIndex] = computeChecksum(packet[:csIndex])

	_, err := ParseST291(packet)
	if err == nil {
		t.Fatal("expected error for invalid DID")
	}
	if !errors.Is(err, ErrST291InvalidDID) {
		t.Errorf("error = %v, want ErrST291InvalidDID", err)
	}
}

func TestParseST291_InvalidSDID(t *testing.T) {
	packet := mustWrap(t, []byte{0xAA})
	packet[1] = 0x08 // Wrong SDID
	// Fix checksum.
	csIndex := 3 + int(packet[2])
	packet[csIndex] = computeChecksum(packet[:csIndex])

	_, err := ParseST291(packet)
	if err == nil {
		t.Fatal("expected error for invalid SDID")
	}
	if !errors.Is(err, ErrST291InvalidSDID) {
		t.Errorf("error = %v, want ErrST291InvalidSDID", err)
	}
}

func TestParseST291_BadChecksum(t *testing.T) {
	packet := mustWrap(t, []byte{0x01, 0x02, 0x03})
	// Corrupt the checksum byte (last byte).
	packet[len(packet)-1] ^= 0xFF

	_, err := ParseST291(packet)
	if err == nil {
		t.Fatal("expected error for bad checksum")
	}
	if !errors.Is(err, ErrST291BadChecksum) {
		t.Errorf("error = %v, want ErrST291BadChecksum", err)
	}
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
			if err == nil {
				t.Fatal("expected error for too-short packet")
			}
			if !errors.Is(err, ErrST291TooShort) {
				t.Errorf("error = %v, want ErrST291TooShort", err)
			}
		})
	}
}

func TestParseST291_TooShort_DCExceedsLength(t *testing.T) {
	// DID=0x41, SDID=0x07, DC=5, but only 1 UDW byte + CS = 6 bytes total needed, provide 5.
	packet := []byte{0x41, 0x07, 0x05, 0x00, 0x01}

	_, err := ParseST291(packet)
	if err == nil {
		t.Fatal("expected error for insufficient data per DC")
	}
	if !errors.Is(err, ErrST291TooShort) {
		t.Errorf("error = %v, want ErrST291TooShort", err)
	}
}

func TestParseST291_ZeroLengthPayload(t *testing.T) {
	// DID + SDID + DC=0 + CS
	// Checksum = (0x41 + 0x07 + 0x00) & 0xFF = 0x48
	packet := []byte{0x41, 0x07, 0x00, 0x48}

	got, err := ParseST291(packet)
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("expected empty payload, got %d bytes: %x", len(got), got)
	}
}

func TestParseST291_MaxLengthPayload(t *testing.T) {
	// 253 bytes of SCTE-104 data (max for single non-fragmented packet).
	data := make([]byte, 253)
	for i := range data {
		data[i] = byte(i)
	}

	packet := mustWrap(t, data)
	got, err := ParseST291(packet)
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("round-trip mismatch for 253-byte payload")
	}
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
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	expected := []byte{0xAA, 0xBB, 0xCC}
	if !bytes.Equal(got, expected) {
		t.Errorf("data = %x, want %x (payload descriptor should be stripped)", got, expected)
	}
}

func TestParseST291_FragmentedContinued(t *testing.T) {
	// Build a packet with continued_pkt bit set in payload descriptor.
	dc := byte(2)
	packet := []byte{0x41, 0x07, dc, st291PayloadDescContinued, 0xFF, 0x00}
	packet[5] = computeChecksum(packet[:5])

	_, err := ParseST291(packet)
	if err == nil {
		t.Fatal("expected error for continued fragment")
	}
	if !errors.Is(err, ErrST291Fragmented) {
		t.Errorf("error = %v, want ErrST291Fragmented", err)
	}
}

func TestParseST291_FragmentedFollowing(t *testing.T) {
	// Build a packet with following_pkt bit set in payload descriptor.
	dc := byte(2)
	packet := []byte{0x41, 0x07, dc, st291PayloadDescFollowing, 0xFF, 0x00}
	packet[5] = computeChecksum(packet[:5])

	_, err := ParseST291(packet)
	if err == nil {
		t.Fatal("expected error for following fragment")
	}
	if !errors.Is(err, ErrST291Fragmented) {
		t.Errorf("error = %v, want ErrST291Fragmented", err)
	}
}

func TestWrapST291_Structure(t *testing.T) {
	data := []byte{0x10, 0x20, 0x30}
	packet := mustWrap(t, data)

	// Expected: DID(1) + SDID(1) + DC(1) + payload_desc(1) + data(3) + CS(1) = 8
	if len(packet) != 8 {
		t.Fatalf("packet length = %d, want 8", len(packet))
	}

	if packet[0] != ST291DID {
		t.Errorf("DID = 0x%02X, want 0x%02X", packet[0], ST291DID)
	}
	if packet[1] != ST291SDID {
		t.Errorf("SDID = 0x%02X, want 0x%02X", packet[1], ST291SDID)
	}
	if packet[2] != 4 { // DC = 3 data + 1 payload descriptor
		t.Errorf("DC = %d, want 4", packet[2])
	}
	if packet[3] != 0x00 { // Payload descriptor
		t.Errorf("payload descriptor = 0x%02X, want 0x00", packet[3])
	}
	if packet[4] != 0x10 || packet[5] != 0x20 || packet[6] != 0x30 {
		t.Errorf("UDW data = %x, want [10 20 30]", packet[4:7])
	}

	// Verify checksum.
	expectedCS := computeChecksum(packet[:7])
	if packet[7] != expectedCS {
		t.Errorf("checksum = 0x%02X, want 0x%02X", packet[7], expectedCS)
	}
}

func TestWrapST291_EmptyPayload(t *testing.T) {
	data := []byte{}
	packet := mustWrap(t, data)

	// DC = 0 + 1 = 1 (just payload descriptor).
	// Total: DID(1) + SDID(1) + DC(1) + payload_desc(1) + CS(1) = 5
	if len(packet) != 5 {
		t.Fatalf("packet length = %d, want 5", len(packet))
	}

	if packet[2] != 1 { // DC = 1 (payload descriptor only)
		t.Errorf("DC = %d, want 1", packet[2])
	}
	if packet[3] != 0x00 { // Payload descriptor
		t.Errorf("payload descriptor = 0x%02X, want 0x00", packet[3])
	}
}

func TestWrapST291_PayloadTooLarge(t *testing.T) {
	data := make([]byte, 254)
	_, err := WrapST291(data)
	if err == nil {
		t.Fatal("expected error for payload > 253 bytes")
	}
	if !errors.Is(err, ErrST291PayloadTooLarge) {
		t.Errorf("error = %v, want ErrST291PayloadTooLarge", err)
	}
}

func TestParseST291_WrapRoundTrip_EmptyPayload(t *testing.T) {
	data := []byte{}
	packet := mustWrap(t, data)
	got, err := ParseST291(packet)
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	// WrapST291 with empty data produces DC=1 (just payload descriptor).
	// ParseST291 strips the payload descriptor, returning empty data.
	if len(got) != 0 {
		t.Errorf("expected empty payload, got %d bytes: %x", len(got), got)
	}
}

func TestParseST291_WrapRoundTrip_SingleByte(t *testing.T) {
	data := []byte{0xFF}
	packet := mustWrap(t, data)
	got, err := ParseST291(packet)
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("round-trip data = %x, want %x", got, data)
	}
}

func TestParseST291_DoesNotAliasInput(t *testing.T) {
	data := []byte{0xAA, 0xBB, 0xCC}
	packet := mustWrap(t, data)

	got, err := ParseST291(packet)
	if err != nil {
		t.Fatalf("ParseST291 error: %v", err)
	}

	// Mutate the original packet's UDW area.
	packet[4] = 0x00
	packet[5] = 0x00
	packet[6] = 0x00

	// The parsed result should be unaffected.
	if !bytes.Equal(got, data) {
		t.Errorf("parsed data was aliased to input: got %x, want %x", got, data)
	}
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
			if got != tt.want {
				t.Errorf("computeChecksum(%x) = 0x%02X, want 0x%02X", tt.data, got, tt.want)
			}
		})
	}
}
