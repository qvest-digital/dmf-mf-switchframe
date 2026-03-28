package codec

import (
	"bytes"
	"testing"
)

// HEVC test NALU bytes (2-byte headers): first byte = (type << 1), second byte = 0x01 (TID=1)
var (
	testHEVCVPS = []byte{0x40, 0x01, 0xAA, 0xBB} // type 32
	testHEVCSPS = []byte{0x42, 0x01, 0xCC, 0xDD} // type 33
	testHEVCPPS = []byte{0x44, 0x01, 0xEE}        // type 34
	testHEVCIDR = []byte{0x26, 0x01, 0x11, 0x22, 0x33, 0x44} // type 19
	testHEVCSEI = []byte{0x4E, 0x01, 0x55}        // type 39
	testHEVCAUD = []byte{0x46, 0x01}              // type 35
)

func TestHEVCNALUType(t *testing.T) {
	tests := []struct {
		name      string
		firstByte byte
		wantType  byte
	}{
		{"VPS", 0x40, HEVCNALVPS},           // (32 << 1) = 0x40
		{"SPS", 0x42, HEVCNALSPS},           // (33 << 1) = 0x42
		{"PPS", 0x44, HEVCNALPPS},           // (34 << 1) = 0x44
		{"IDR_W_RADL", 0x26, HEVCNALIDRWRadl}, // (19 << 1) = 0x26
		{"IDR_N_LP", 0x28, HEVCNALIDRNLP},   // (20 << 1) = 0x28
		{"SEI_PREFIX", 0x4E, HEVCNALSEIPrefix}, // (39 << 1) = 0x4E
		{"AUD", 0x46, HEVCNALAUD},           // (35 << 1) = 0x46
		{"TRAIL_N", 0x00, HEVCNALTrailN},    // (0 << 1) = 0x00
		{"TRAIL_R", 0x02, HEVCNALTrailR},    // (1 << 1) = 0x02
		{"CRA", 0x2A, HEVCNALCraNut},        // (21 << 1) = 0x2A
		{"BLA_W_LP", 0x20, HEVCNALBlaWLP},   // (16 << 1) = 0x20
		{"filler", 0x4C, HEVCNALFillerData}, // (38 << 1) = 0x4C
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HEVCNALUType(tt.firstByte)
			if got != tt.wantType {
				t.Errorf("HEVCNALUType(0x%02X) = %d, want %d", tt.firstByte, got, tt.wantType)
			}
		})
	}
}

func TestHEVCNALUType_WithForbiddenBit(t *testing.T) {
	// Even with forbidden_zero_bit set (bit 7), type extraction should work.
	// type 33 = SPS: firstByte = 0x42, with forbidden bit: 0xC2
	got := HEVCNALUType(0xC2)
	if got != HEVCNALSPS {
		t.Errorf("HEVCNALUType(0xC2) = %d, want %d (SPS)", got, HEVCNALSPS)
	}
}

func TestIsHEVCKeyframe(t *testing.T) {
	tests := []struct {
		name    string
		nalType byte
		want    bool
	}{
		{"BLA_W_LP", HEVCNALBlaWLP, true},
		{"BLA_W_RADL", HEVCNALBlaWRadl, true},
		{"BLA_N_LP", HEVCNALBlaNLP, true},
		{"IDR_W_RADL", HEVCNALIDRWRadl, true},
		{"IDR_N_LP", HEVCNALIDRNLP, true},
		{"CRA_NUT", HEVCNALCraNut, true},
		{"TRAIL_N", HEVCNALTrailN, false},
		{"TRAIL_R", HEVCNALTrailR, false},
		{"VPS", HEVCNALVPS, false},
		{"SPS", HEVCNALSPS, false},
		{"PPS", HEVCNALPPS, false},
		{"SEI_PREFIX", HEVCNALSEIPrefix, false},
		{"AUD", HEVCNALAUD, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsHEVCKeyframe(tt.nalType)
			if got != tt.want {
				t.Errorf("IsHEVCKeyframe(%d) = %v, want %v", tt.nalType, got, tt.want)
			}
		})
	}
}

func TestIsHEVCVCL(t *testing.T) {
	tests := []struct {
		name    string
		nalType byte
		want    bool
	}{
		{"TRAIL_N", HEVCNALTrailN, true},
		{"TRAIL_R", HEVCNALTrailR, true},
		{"IDR_W_RADL", HEVCNALIDRWRadl, true},
		{"CRA_NUT", HEVCNALCraNut, true},
		{"type_31", 31, true},
		{"VPS_not_VCL", HEVCNALVPS, false},
		{"SPS_not_VCL", HEVCNALSPS, false},
		{"PPS_not_VCL", HEVCNALPPS, false},
		{"SEI_not_VCL", HEVCNALSEIPrefix, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsHEVCVCL(tt.nalType)
			if got != tt.want {
				t.Errorf("IsHEVCVCL(%d) = %v, want %v", tt.nalType, got, tt.want)
			}
		})
	}
}

func TestHVC1ToAnnexB(t *testing.T) {
	// Build HVC1 format: [4-byte length][NALU data]
	hvc1 := buildLengthPrefixed(testHEVCIDR)
	got := HVC1ToAnnexB(hvc1)

	// Expect: [start code][NALU data]
	want := append(annexBStartCode, testHEVCIDR...)
	if !bytes.Equal(got, want) {
		t.Errorf("HVC1ToAnnexB:\ngot  %x\nwant %x", got, want)
	}
}

func TestHVC1ToAnnexB_MultipleNALUs(t *testing.T) {
	hvc1 := append(buildLengthPrefixed(testHEVCSPS), buildLengthPrefixed(testHEVCPPS)...)
	hvc1 = append(hvc1, buildLengthPrefixed(testHEVCIDR)...)

	got := HVC1ToAnnexB(hvc1)

	var want []byte
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCSPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCPPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCIDR...)

	if !bytes.Equal(got, want) {
		t.Errorf("HVC1ToAnnexB multiple NALUs:\ngot  %x\nwant %x", got, want)
	}
}

func TestHVC1ToAnnexB_NilEmpty(t *testing.T) {
	if got := HVC1ToAnnexB(nil); got != nil {
		t.Errorf("HVC1ToAnnexB(nil) = %x, want nil", got)
	}
	if got := HVC1ToAnnexB([]byte{}); got != nil {
		t.Errorf("HVC1ToAnnexB(empty) = %x, want nil", got)
	}
}

func TestHVC1ToAnnexBInto(t *testing.T) {
	hvc1 := buildLengthPrefixed(testHEVCIDR)
	dst := make([]byte, 0, 64)
	got := HVC1ToAnnexBInto(hvc1, dst)

	want := append(annexBStartCode, testHEVCIDR...)
	if !bytes.Equal(got, want) {
		t.Errorf("HVC1ToAnnexBInto:\ngot  %x\nwant %x", got, want)
	}

	// Verify it reused the buffer
	if cap(got) != 64 {
		t.Errorf("HVC1ToAnnexBInto should reuse dst buffer, got cap %d", cap(got))
	}
}

func TestHVC1ToAnnexBInto_NilDst(t *testing.T) {
	hvc1 := buildLengthPrefixed(testHEVCIDR)
	got := HVC1ToAnnexBInto(hvc1, nil)
	want := append(annexBStartCode, testHEVCIDR...)
	if !bytes.Equal(got, want) {
		t.Errorf("HVC1ToAnnexBInto(nil dst):\ngot  %x\nwant %x", got, want)
	}
}

func TestAnnexBToHVC1(t *testing.T) {
	// Build Annex B: [start code][NALU data]
	annexB := append(annexBStartCode, testHEVCIDR...)
	got := AnnexBToHVC1(annexB)

	want := buildLengthPrefixed(testHEVCIDR)
	if !bytes.Equal(got, want) {
		t.Errorf("AnnexBToHVC1:\ngot  %x\nwant %x", got, want)
	}
}

func TestAnnexBToHVC1_MultipleNALUs(t *testing.T) {
	var annexB []byte
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCVPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCSPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCPPS...)

	got := AnnexBToHVC1(annexB)

	var want []byte
	want = append(want, buildLengthPrefixed(testHEVCVPS)...)
	want = append(want, buildLengthPrefixed(testHEVCSPS)...)
	want = append(want, buildLengthPrefixed(testHEVCPPS)...)

	if !bytes.Equal(got, want) {
		t.Errorf("AnnexBToHVC1 multiple NALUs:\ngot  %x\nwant %x", got, want)
	}
}

func TestAnnexBToHVC1_NilEmpty(t *testing.T) {
	if got := AnnexBToHVC1(nil); got != nil {
		t.Errorf("AnnexBToHVC1(nil) = %x, want nil", got)
	}
	if got := AnnexBToHVC1([]byte{}); got != nil {
		t.Errorf("AnnexBToHVC1(empty) = %x, want nil", got)
	}
}

func TestAnnexBToHVC1Into(t *testing.T) {
	annexB := append(annexBStartCode, testHEVCIDR...)
	dst := make([]byte, 0, 64)
	got := AnnexBToHVC1Into(annexB, dst)

	want := buildLengthPrefixed(testHEVCIDR)
	if !bytes.Equal(got, want) {
		t.Errorf("AnnexBToHVC1Into:\ngot  %x\nwant %x", got, want)
	}
}

func TestHVC1RoundTrip(t *testing.T) {
	// HVC1 → Annex B → HVC1 should be identity
	original := buildLengthPrefixed(testHEVCIDR)
	annexB := HVC1ToAnnexB(original)
	roundTripped := AnnexBToHVC1(annexB)

	if !bytes.Equal(roundTripped, original) {
		t.Errorf("HVC1 round-trip failed:\noriginal     %x\nround-tripped %x", original, roundTripped)
	}
}

func TestHVC1RoundTrip_MultipleNALUs(t *testing.T) {
	var original []byte
	original = append(original, buildLengthPrefixed(testHEVCVPS)...)
	original = append(original, buildLengthPrefixed(testHEVCSPS)...)
	original = append(original, buildLengthPrefixed(testHEVCPPS)...)
	original = append(original, buildLengthPrefixed(testHEVCIDR)...)

	annexB := HVC1ToAnnexB(original)
	roundTripped := AnnexBToHVC1(annexB)

	if !bytes.Equal(roundTripped, original) {
		t.Errorf("HVC1 multi-NALU round-trip failed:\noriginal     %x\nround-tripped %x", original, roundTripped)
	}
}

func TestAnnexBRoundTrip(t *testing.T) {
	// Annex B → HVC1 → Annex B should be identity (with 4-byte start codes)
	var original []byte
	original = append(original, annexBStartCode...)
	original = append(original, testHEVCIDR...)

	hvc1 := AnnexBToHVC1(original)
	roundTripped := HVC1ToAnnexB(hvc1)

	if !bytes.Equal(roundTripped, original) {
		t.Errorf("Annex B round-trip failed:\noriginal     %x\nround-tripped %x", original, roundTripped)
	}
}

func TestPrependVPSSPSPPS(t *testing.T) {
	frameData := append(annexBStartCode, testHEVCIDR...)

	got := PrependVPSSPSPPS(testHEVCVPS, testHEVCSPS, testHEVCPPS, frameData)

	// Expected: [sc][VPS][sc][SPS][sc][PPS][sc][IDR]
	var want []byte
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCVPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCSPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCPPS...)
	want = append(want, frameData...)

	if !bytes.Equal(got, want) {
		t.Errorf("PrependVPSSPSPPS:\ngot  %x\nwant %x", got, want)
	}
}

func TestPrependVPSSPSPPS_NilParams(t *testing.T) {
	frameData := append(annexBStartCode, testHEVCIDR...)

	// All nil → just return frame data
	got := PrependVPSSPSPPS(nil, nil, nil, frameData)
	if !bytes.Equal(got, frameData) {
		t.Errorf("PrependVPSSPSPPS(nil,nil,nil) should return frame data unchanged")
	}
}

func TestPrependVPSSPSPPS_PartialParams(t *testing.T) {
	frameData := append(annexBStartCode, testHEVCIDR...)

	// Only VPS provided
	got := PrependVPSSPSPPS(testHEVCVPS, nil, nil, frameData)
	var want []byte
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCVPS...)
	want = append(want, frameData...)
	if !bytes.Equal(got, want) {
		t.Errorf("PrependVPSSPSPPS(vps,nil,nil):\ngot  %x\nwant %x", got, want)
	}

	// Only SPS+PPS
	got = PrependVPSSPSPPS(nil, testHEVCSPS, testHEVCPPS, frameData)
	want = nil
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCSPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCPPS...)
	want = append(want, frameData...)
	if !bytes.Equal(got, want) {
		t.Errorf("PrependVPSSPSPPS(nil,sps,pps):\ngot  %x\nwant %x", got, want)
	}
}

func TestPrependVPSSPSPPSInto(t *testing.T) {
	frameData := append(annexBStartCode, testHEVCIDR...)
	dst := make([]byte, 0, 256)

	got := PrependVPSSPSPPSInto(testHEVCVPS, testHEVCSPS, testHEVCPPS, frameData, dst)

	var want []byte
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCVPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCSPS...)
	want = append(want, annexBStartCode...)
	want = append(want, testHEVCPPS...)
	want = append(want, frameData...)

	if !bytes.Equal(got, want) {
		t.Errorf("PrependVPSSPSPPSInto:\ngot  %x\nwant %x", got, want)
	}

	// Should reuse buffer
	if cap(got) != 256 {
		t.Errorf("PrependVPSSPSPPSInto should reuse dst buffer, got cap %d", cap(got))
	}
}

func TestPrependVPSSPSPPSInto_NilParams(t *testing.T) {
	frameData := append(annexBStartCode, testHEVCIDR...)
	dst := make([]byte, 0, 256)

	got := PrependVPSSPSPPSInto(nil, nil, nil, frameData, dst)
	if !bytes.Equal(got, frameData) {
		t.Errorf("PrependVPSSPSPPSInto(nil,nil,nil) should copy frame data")
	}
}

func TestExtractHEVCParamSets(t *testing.T) {
	// Build Annex B stream with VPS + SPS + PPS + IDR
	var annexB []byte
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCVPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCSPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCPPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCIDR...)

	vps, sps, pps := ExtractHEVCParamSets(annexB)

	if !bytes.Equal(vps, testHEVCVPS) {
		t.Errorf("ExtractHEVCParamSets VPS:\ngot  %x\nwant %x", vps, testHEVCVPS)
	}
	if !bytes.Equal(sps, testHEVCSPS) {
		t.Errorf("ExtractHEVCParamSets SPS:\ngot  %x\nwant %x", sps, testHEVCSPS)
	}
	if !bytes.Equal(pps, testHEVCPPS) {
		t.Errorf("ExtractHEVCParamSets PPS:\ngot  %x\nwant %x", pps, testHEVCPPS)
	}
}

func TestExtractHEVCParamSets_NoParams(t *testing.T) {
	// Stream with only IDR — no param sets
	annexB := append(annexBStartCode, testHEVCIDR...)

	vps, sps, pps := ExtractHEVCParamSets(annexB)

	if vps != nil {
		t.Errorf("ExtractHEVCParamSets no-params VPS: got %x, want nil", vps)
	}
	if sps != nil {
		t.Errorf("ExtractHEVCParamSets no-params SPS: got %x, want nil", sps)
	}
	if pps != nil {
		t.Errorf("ExtractHEVCParamSets no-params PPS: got %x, want nil", pps)
	}
}

func TestExtractHEVCParamSets_NilEmpty(t *testing.T) {
	vps, sps, pps := ExtractHEVCParamSets(nil)
	if vps != nil || sps != nil || pps != nil {
		t.Error("ExtractHEVCParamSets(nil) should return all nils")
	}

	vps, sps, pps = ExtractHEVCParamSets([]byte{})
	if vps != nil || sps != nil || pps != nil {
		t.Error("ExtractHEVCParamSets(empty) should return all nils")
	}
}

func TestExtractHEVCParamSets_PartialParams(t *testing.T) {
	// Stream with only SPS — no VPS or PPS
	annexB := append(annexBStartCode, testHEVCSPS...)

	vps, sps, pps := ExtractHEVCParamSets(annexB)

	if vps != nil {
		t.Errorf("expected nil VPS, got %x", vps)
	}
	if !bytes.Equal(sps, testHEVCSPS) {
		t.Errorf("ExtractHEVCParamSets SPS:\ngot  %x\nwant %x", sps, testHEVCSPS)
	}
	if pps != nil {
		t.Errorf("expected nil PPS, got %x", pps)
	}
}

func TestExtractHEVCParamSets_ReturnsCopies(t *testing.T) {
	var annexB []byte
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCVPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCSPS...)
	annexB = append(annexB, annexBStartCode...)
	annexB = append(annexB, testHEVCPPS...)

	vps, sps, pps := ExtractHEVCParamSets(annexB)

	// Modify returned slices, verify original data unaffected
	origVPS := make([]byte, len(vps))
	copy(origVPS, vps)
	vps[0] = 0xFF

	origSPS := make([]byte, len(sps))
	copy(origSPS, sps)
	sps[0] = 0xFF

	origPPS := make([]byte, len(pps))
	copy(origPPS, pps)
	pps[0] = 0xFF

	// Re-extract and verify unchanged
	vps2, sps2, pps2 := ExtractHEVCParamSets(annexB)
	if !bytes.Equal(vps2, origVPS) {
		t.Error("ExtractHEVCParamSets should return independent copies (VPS)")
	}
	if !bytes.Equal(sps2, origSPS) {
		t.Error("ExtractHEVCParamSets should return independent copies (SPS)")
	}
	if !bytes.Equal(pps2, origPPS) {
		t.Error("ExtractHEVCParamSets should return independent copies (PPS)")
	}
}

func TestWireToAnnexB(t *testing.T) {
	wire := buildLengthPrefixed(testHEVCIDR)
	got := WireToAnnexB(wire)
	want := append(annexBStartCode, testHEVCIDR...)

	if !bytes.Equal(got, want) {
		t.Errorf("WireToAnnexB:\ngot  %x\nwant %x", got, want)
	}
}

func TestWireToAnnexBInto(t *testing.T) {
	wire := buildLengthPrefixed(testHEVCIDR)
	dst := make([]byte, 0, 64)
	got := WireToAnnexBInto(wire, dst)
	want := append(annexBStartCode, testHEVCIDR...)

	if !bytes.Equal(got, want) {
		t.Errorf("WireToAnnexBInto:\ngot  %x\nwant %x", got, want)
	}
}

func TestAnnexBToWire(t *testing.T) {
	annexB := append(annexBStartCode, testHEVCIDR...)
	got := AnnexBToWire(annexB)
	want := buildLengthPrefixed(testHEVCIDR)

	if !bytes.Equal(got, want) {
		t.Errorf("AnnexBToWire:\ngot  %x\nwant %x", got, want)
	}
}

func TestAnnexBToWireInto(t *testing.T) {
	annexB := append(annexBStartCode, testHEVCIDR...)
	dst := make([]byte, 0, 64)
	got := AnnexBToWireInto(annexB, dst)
	want := buildLengthPrefixed(testHEVCIDR)

	if !bytes.Equal(got, want) {
		t.Errorf("AnnexBToWireInto:\ngot  %x\nwant %x", got, want)
	}
}

func TestWireRoundTrip(t *testing.T) {
	original := buildLengthPrefixed(testHEVCIDR)
	annexB := WireToAnnexB(original)
	roundTripped := AnnexBToWire(annexB)

	if !bytes.Equal(roundTripped, original) {
		t.Errorf("Wire round-trip failed:\noriginal     %x\nround-tripped %x", original, roundTripped)
	}
}

// buildLengthPrefixed creates a wire-format (4-byte length prefix) NALU.
func buildLengthPrefixed(nalu []byte) []byte {
	buf := make([]byte, 4+len(nalu))
	buf[0] = byte(len(nalu) >> 24)
	buf[1] = byte(len(nalu) >> 16)
	buf[2] = byte(len(nalu) >> 8)
	buf[3] = byte(len(nalu))
	copy(buf[4:], nalu)
	return buf
}
