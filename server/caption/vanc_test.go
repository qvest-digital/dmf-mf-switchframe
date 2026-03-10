package caption

import (
	"testing"
)

func TestBuildCDP_NilPairs(t *testing.T) {
	if got := BuildCDP(nil, 0, 0); got != nil {
		t.Errorf("BuildCDP(nil) = %v, want nil", got)
	}
}

func TestBuildCDP_Structure(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'H', 'i'}}}
	cdp := BuildCDP(pairs, 42, cdpFrameRate2997)

	if cdp == nil {
		t.Fatal("BuildCDP returned nil")
	}

	// Verify magic.
	if cdp[0] != cdpMagic0 || cdp[1] != cdpMagic1 {
		t.Errorf("magic = %02X %02X, want %02X %02X", cdp[0], cdp[1], cdpMagic0, cdpMagic1)
	}

	// Verify length.
	if int(cdp[2]) != len(cdp) {
		t.Errorf("cdp_length = %d, want %d", cdp[2], len(cdp))
	}

	// Verify frame rate.
	frameRate := cdp[3] >> 4
	if frameRate != cdpFrameRate2997 {
		t.Errorf("frame_rate = %d, want %d", frameRate, cdpFrameRate2997)
	}

	// Verify sequence counter in header.
	seq := uint16(cdp[5])<<8 | uint16(cdp[6])
	if seq != 42 {
		t.Errorf("header seq = %d, want 42", seq)
	}

	// Verify cc_data section marker.
	if cdp[7] != 0x72 {
		t.Errorf("cc_data marker = %02X, want 0x72", cdp[7])
	}

	// Verify checksum: sum of all bytes should be 0 mod 256.
	var sum int
	for _, b := range cdp {
		sum += int(b)
	}
	if sum%256 != 0 {
		t.Errorf("checksum validation failed: sum mod 256 = %d, want 0", sum%256)
	}
}

func TestBuildCDP_DefaultFrameRate(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}
	cdp := BuildCDP(pairs, 0, 0) // frameRate=0 should default to 29.97

	frameRate := cdp[3] >> 4
	if frameRate != cdpFrameRate2997 {
		t.Errorf("default frame_rate = %d, want %d", frameRate, cdpFrameRate2997)
	}
}

func TestBuildCDP_MultiplePairs(t *testing.T) {
	pairs := []CCPair{
		{Data: [2]byte{'H', 'e'}},
		{Data: [2]byte{'l', 'l'}},
		{Data: [2]byte{'o', 0x80}},
	}
	cdp := BuildCDP(pairs, 100, cdpFrameRate2997)

	// Expected length: 7 + 2 + 3*3 + 4 = 22
	if len(cdp) != 22 {
		t.Errorf("cdp length = %d, want 22", len(cdp))
	}

	// Verify cc_count.
	ccCount := int(cdp[8] & 0x1F)
	if ccCount != 3 {
		t.Errorf("cc_count = %d, want 3", ccCount)
	}
}

func TestWrapCaptionST291_RoundTrip(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'T', 'V'}}}
	cdp := BuildCDP(pairs, 1, cdpFrameRate2997)

	packet, err := WrapCaptionST291(cdp)
	if err != nil {
		t.Fatalf("WrapCaptionST291: %v", err)
	}

	// Verify DID/SDID.
	if packet[0] != CaptionDID || packet[1] != CaptionSDID {
		t.Errorf("DID/SDID = %02X/%02X, want %02X/%02X",
			packet[0], packet[1], CaptionDID, CaptionSDID)
	}

	// Round-trip: parse back.
	got, err := ParseCaptionST291(packet)
	if err != nil {
		t.Fatalf("ParseCaptionST291: %v", err)
	}

	if len(got) != len(cdp) {
		t.Fatalf("round-trip: got %d bytes, want %d", len(got), len(cdp))
	}

	for i := range got {
		if got[i] != cdp[i] {
			t.Errorf("round-trip: byte %d = %02X, want %02X", i, got[i], cdp[i])
		}
	}
}

func TestWrapCaptionST291_TooLarge(t *testing.T) {
	data := make([]byte, vancMaxPayload+1)
	_, err := WrapCaptionST291(data)
	if err == nil {
		t.Error("expected error for oversized payload")
	}
}

func TestParseCaptionST291_InvalidDID(t *testing.T) {
	packet := []byte{0x41, CaptionSDID, 0x00, 0x00} // wrong DID
	_, err := ParseCaptionST291(packet)
	if err == nil {
		t.Error("expected error for wrong DID")
	}
}

func TestParseCaptionST291_InvalidSDID(t *testing.T) {
	packet := []byte{CaptionDID, 0x07, 0x00, 0x00} // wrong SDID
	_, err := ParseCaptionST291(packet)
	if err == nil {
		t.Error("expected error for wrong SDID")
	}
}

func TestParseCaptionST291_TooShort(t *testing.T) {
	_, err := ParseCaptionST291([]byte{0x61})
	if err == nil {
		t.Error("expected error for too short packet")
	}
}

func TestParseCaptionST291_BadChecksum(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}
	cdp := BuildCDP(pairs, 0, cdpFrameRate2997)
	packet, _ := WrapCaptionST291(cdp)

	// Corrupt checksum.
	packet[len(packet)-1] ^= 0xFF

	_, err := ParseCaptionST291(packet)
	if err == nil {
		t.Error("expected error for bad checksum")
	}
}

func TestCaptionVANC_CoexistsWithSCTE104(t *testing.T) {
	// Caption DID/SDID should differ from SCTE-104 DID/SDID.
	// SCTE-104 uses DID=0x41, SDID=0x07.
	if CaptionDID == 0x41 {
		t.Error("caption DID should differ from SCTE-104 DID (0x41)")
	}
}
