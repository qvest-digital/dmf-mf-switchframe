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

	// Each pair produces 2 triples (field 1 data + field 2 null), so 3 pairs → 6 triples.
	// Expected length: 7 + 2 + 3*6 + 4 = 31
	if len(cdp) != 31 {
		t.Errorf("cdp length = %d, want 31", len(cdp))
	}

	// Verify cc_count = 6 (3 pairs * 2 triples each).
	ccCount := int(cdp[8] & 0x1F)
	if ccCount != 6 {
		t.Errorf("cc_count = %d, want 6", ccCount)
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

func TestBuildCDP_Field2NullTriples(t *testing.T) {
	// BuildCDP should emit field 2 null triples (0xF9, 0x80, 0x80) after each
	// field 1 data triple, matching the SEI path in BuildSEINALU. Some broadcast
	// VANC receivers expect paired field 1 + field 2 entries per CEA-708 CDP convention.
	pairs := []CCPair{
		{Data: [2]byte{'H', 'i'}},
		{Data: [2]byte{'!', 0x80}},
	}
	cdp := BuildCDP(pairs, 7, cdpFrameRate2997)
	if cdp == nil {
		t.Fatal("BuildCDP returned nil")
	}

	// cc_count should be len(pairs)*2 = 4 (field 1 + field 2 null per pair).
	ccCount := int(cdp[8] & 0x1F)
	if ccCount != 4 {
		t.Errorf("cc_count = %d, want 4 (2 pairs * 2 triples each)", ccCount)
	}

	// Expected CDP length:
	//   Header: magic(2) + length(1) + frame_rate(1) + flags(1) + seq(2) = 7
	//   cc_data section: marker(1) + cc_count(1) + triples(3*4) = 14
	//   Footer: marker(1) + seq(2) + checksum(1) = 4
	//   Total = 7 + 14 + 4 = 25
	wantLen := 7 + 2 + 3*4 + 4
	if len(cdp) != wantLen {
		t.Errorf("cdp length = %d, want %d", len(cdp), wantLen)
	}

	// Verify cdp_length field matches actual length.
	if int(cdp[2]) != len(cdp) {
		t.Errorf("cdp_length field = %d, actual length = %d", cdp[2], len(cdp))
	}

	// cc_data triples start at offset 9 (after header[7] + marker[1] + cc_count[1]).
	tripleStart := 9

	// Pair 0: field 1 data triple.
	if cdp[tripleStart] != 0xFC {
		t.Errorf("pair 0 field 1 marker = 0x%02X, want 0xFC", cdp[tripleStart])
	}
	if cdp[tripleStart+1] != 'H' || cdp[tripleStart+2] != 'i' {
		t.Errorf("pair 0 field 1 data = [%02X %02X], want [%02X %02X]",
			cdp[tripleStart+1], cdp[tripleStart+2], byte('H'), byte('i'))
	}

	// Pair 0: field 2 null triple.
	f2Start := tripleStart + 3
	if cdp[f2Start] != 0xF9 {
		t.Errorf("pair 0 field 2 marker = 0x%02X, want 0xF9", cdp[f2Start])
	}
	if cdp[f2Start+1] != 0x80 || cdp[f2Start+2] != 0x80 {
		t.Errorf("pair 0 field 2 data = [%02X %02X], want [80 80]",
			cdp[f2Start+1], cdp[f2Start+2])
	}

	// Pair 1: field 1 data triple.
	p1Start := tripleStart + 6
	if cdp[p1Start] != 0xFC {
		t.Errorf("pair 1 field 1 marker = 0x%02X, want 0xFC", cdp[p1Start])
	}
	if cdp[p1Start+1] != '!' || cdp[p1Start+2] != 0x80 {
		t.Errorf("pair 1 field 1 data = [%02X %02X], want [%02X %02X]",
			cdp[p1Start+1], cdp[p1Start+2], byte('!'), byte(0x80))
	}

	// Pair 1: field 2 null triple.
	p1f2Start := tripleStart + 9
	if cdp[p1f2Start] != 0xF9 {
		t.Errorf("pair 1 field 2 marker = 0x%02X, want 0xF9", cdp[p1f2Start])
	}
	if cdp[p1f2Start+1] != 0x80 || cdp[p1f2Start+2] != 0x80 {
		t.Errorf("pair 1 field 2 data = [%02X %02X], want [80 80]",
			cdp[p1f2Start+1], cdp[p1f2Start+2])
	}

	// Verify CDP checksum: sum of all bytes should be 0 mod 256.
	var sum int
	for _, b := range cdp {
		sum += int(b)
	}
	if sum%256 != 0 {
		t.Errorf("checksum validation failed: sum mod 256 = %d, want 0", sum%256)
	}

	// Verify round-trip: BuildCDP → WrapCaptionST291 → ParseCaptionST291 still works.
	packet, err := WrapCaptionST291(cdp)
	if err != nil {
		t.Fatalf("WrapCaptionST291: %v", err)
	}
	got, err := ParseCaptionST291(packet)
	if err != nil {
		t.Fatalf("ParseCaptionST291 round-trip: %v", err)
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

func TestBuildCDP_ClampsPairsTo15(t *testing.T) {
	// cc_count is a 5-bit field (max 31). Each pair produces 2 triples,
	// so max input is 15 pairs → 30 triples. Verify >15 pairs are clamped.
	pairs := make([]CCPair, 20)
	for i := range pairs {
		pairs[i] = CCPair{Data: [2]byte{byte(i + 0x20), 0x80}}
	}
	cdp := BuildCDP(pairs, 1, cdpFrameRate2997)

	ccCount := int(cdp[8] & 0x1F)
	// 15 pairs * 2 triples = 30
	if ccCount != 30 {
		t.Errorf("cc_count = %d, want 30 (15 pairs clamped from 20)", ccCount)
	}

	// CDP length: 7 + 2 + 3*30 + 4 = 103
	if len(cdp) != 103 {
		t.Errorf("cdp length = %d, want 103", len(cdp))
	}
}

func TestCaptionVANC_CoexistsWithSCTE104(t *testing.T) {
	// Caption DID/SDID should differ from SCTE-104 DID/SDID.
	// SCTE-104 uses DID=0x41, SDID=0x07.
	if CaptionDID == 0x41 {
		t.Error("caption DID should differ from SCTE-104 DID (0x41)")
	}
}
