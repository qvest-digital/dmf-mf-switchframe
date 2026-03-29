package caption

import (
	"bytes"
	"testing"
)

// removeEPBStateful removes emulation prevention bytes using output-state tracking.
// Unlike the simpler removeEPB in sei_test.go, this correctly handles cascading
// EPB patterns (e.g., 00 00 03 00 00 03 00) where removal of one EPB creates a
// new 00 00 XX pattern that was not originally present.
func removeEPBStateful(data []byte) []byte {
	result := make([]byte, 0, len(data))
	zeros := 0 // consecutive output zeros
	for i := 0; i < len(data); i++ {
		if zeros >= 2 && data[i] == 0x03 && i+1 < len(data) && data[i+1] <= 0x03 {
			// This is an EPB — skip the 0x03 byte.
			zeros = 0
			continue
		}
		// Also handle EPB at end of stream (0x03 is the last byte after 00 00).
		if zeros >= 2 && data[i] == 0x03 && i+1 == len(data) {
			zeros = 0
			continue
		}
		result = append(result, data[i])
		if data[i] == 0x00 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return result
}

// --- bitWriter unit tests ---

func TestBitWriter_SingleByte(t *testing.T) {
	var bw bitWriter
	bw.init(4)
	bw.writeBits(0xFF, 8)
	got := bw.bytes()
	if len(got) != 1 || got[0] != 0xFF {
		t.Errorf("got %X, want [FF]", got)
	}
}

func TestBitWriter_PartialByte(t *testing.T) {
	var bw bitWriter
	bw.init(4)
	// Write 4 bits = 0b1010 then byte-align.
	bw.writeBits(0xA, 4)
	bw.byteAlign()
	got := bw.bytes()
	if len(got) != 1 || got[0] != 0xA0 {
		t.Errorf("got %X, want [A0]", got)
	}
}

func TestBitWriter_MultipleCalls(t *testing.T) {
	var bw bitWriter
	bw.init(4)
	// Write 24 bits = 0x123456 in three 8-bit writes.
	bw.writeBits(0x12, 8)
	bw.writeBits(0x34, 8)
	bw.writeBits(0x56, 8)
	got := bw.bytes()
	if !bytes.Equal(got, []byte{0x12, 0x34, 0x56}) {
		t.Errorf("got %X, want [12 34 56]", got)
	}
}

func TestBitWriter_CrossByteBoundary(t *testing.T) {
	var bw bitWriter
	bw.init(4)
	// Write 4 bits, then 8 bits — should cross byte boundary.
	bw.writeBits(0xA, 4) // 1010 ....
	bw.writeBits(0xBC, 8) // .... 1011 1100 -> 1010 1011 | 1100 0000
	bw.byteAlign()
	got := bw.bytes()
	if len(got) != 2 || got[0] != 0xAB || got[1] != 0xC0 {
		t.Errorf("got %X, want [AB C0]", got)
	}
}

func TestBitWriter_24Bit(t *testing.T) {
	var bw bitWriter
	bw.init(4)
	// Write a 24-bit value.
	bw.writeBits(0xABCDEF, 24)
	got := bw.bytes()
	if !bytes.Equal(got, []byte{0xAB, 0xCD, 0xEF}) {
		t.Errorf("got %X, want [AB CD EF]", got)
	}
}

func TestBitWriter_ByteAlignNoOp(t *testing.T) {
	var bw bitWriter
	bw.init(4)
	bw.writeBits(0xFF, 8)
	bw.byteAlign() // already aligned — should be no-op
	got := bw.bytes()
	if len(got) != 1 {
		t.Errorf("byteAlign on aligned writer produced extra byte: %X", got)
	}
}

// --- pic_timing SEI payload tests ---

func TestBuildPicTimingSEI_H264_NoHRD(t *testing.T) {
	// Minimal pic_timing: no HRD, only pic_struct present.
	// Should contain pic_struct(4 bits) = 0000 + clock_timestamp_flag[0](1 bit) = 0
	// = 5 bits total, padded to 1 byte = 0x00.
	cfg := PicTimingConfig{
		NalHRDParametersPresent: false,
		VclHRDParametersPresent: false,
		PicStructPresent:        true,
	}

	payload := BuildPicTimingSEI(cfg, 0, nil)
	if len(payload) != 1 {
		t.Fatalf("payload length = %d, want 1", len(payload))
	}
	// pic_struct=0 (4 bits) + clock_timestamp_flag=0 (1 bit) + 3 zero-fill bits = 0b00000_000 = 0x00
	if payload[0] != 0x00 {
		t.Errorf("payload = %02X, want 0x00", payload[0])
	}
}

func TestBuildPicTimingSEI_H264_NoHRD_NoPicStruct(t *testing.T) {
	// Edge case: no HRD and no pic_struct — empty payload.
	cfg := PicTimingConfig{
		NalHRDParametersPresent: false,
		VclHRDParametersPresent: false,
		PicStructPresent:        false,
	}

	payload := BuildPicTimingSEI(cfg, 0, nil)
	if len(payload) != 0 {
		t.Errorf("payload length = %d, want 0 (no data to encode)", len(payload))
	}
}

func TestBuildPicTimingSEI_H264_WithHRD(t *testing.T) {
	// Full pic_timing with NAL HRD + pic_struct.
	// cpb_removal_delay_length = 24, dpb_output_delay_length = 24.
	cfg := PicTimingConfig{
		NalHRDParametersPresent: true,
		PicStructPresent:        true,
		CpbRemovalDelayLength:   24,
		DpbOutputDelayLength:    24,
	}

	// Frame 100: cpb_removal_delay = 200 (0x0000C8), dpb_output_delay = 0.
	payload := BuildPicTimingSEI(cfg, 100, nil)

	// Expected bit layout:
	// cpb_removal_delay (24 bits): 0x0000C8 = 200
	// dpb_output_delay (24 bits):  0x000000
	// pic_struct (4 bits):         0b0000
	// clock_timestamp_flag (1 bit): 0
	// Padding (3 bits):            0b000
	// Total: 24 + 24 + 4 + 1 + 3 = 56 bits = 7 bytes

	if len(payload) != 7 {
		t.Fatalf("payload length = %d, want 7", len(payload))
	}

	// cpb_removal_delay = 200 = 0x0000C8 in 24 bits.
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0xC8 {
		t.Errorf("cpb_removal_delay bytes = %02X %02X %02X, want 00 00 C8",
			payload[0], payload[1], payload[2])
	}

	// dpb_output_delay = 0 in 24 bits.
	if payload[3] != 0x00 || payload[4] != 0x00 || payload[5] != 0x00 {
		t.Errorf("dpb_output_delay bytes = %02X %02X %02X, want 00 00 00",
			payload[3], payload[4], payload[5])
	}

	// pic_struct(0000) + clock_timestamp_flag(0) + padding(000) = 0x00.
	if payload[6] != 0x00 {
		t.Errorf("pic_struct byte = %02X, want 0x00", payload[6])
	}
}

func TestBuildPicTimingSEI_H264_WithHRD_Frame0(t *testing.T) {
	// Frame 0: cpb_removal_delay = 0.
	cfg := DefaultPicTimingConfig()

	payload := BuildPicTimingSEI(cfg, 0, nil)
	if len(payload) != 7 {
		t.Fatalf("payload length = %d, want 7", len(payload))
	}

	// cpb_removal_delay = 0.
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x00 {
		t.Errorf("cpb_removal_delay = %02X %02X %02X, want 00 00 00",
			payload[0], payload[1], payload[2])
	}
}

func TestBuildPicTimingSEI_H264_VclHRD(t *testing.T) {
	// VCL HRD present (no NAL) — should still include delays.
	cfg := PicTimingConfig{
		NalHRDParametersPresent: false,
		VclHRDParametersPresent: true,
		PicStructPresent:        true,
		CpbRemovalDelayLength:   24,
		DpbOutputDelayLength:    24,
	}

	payload := BuildPicTimingSEI(cfg, 10, nil)
	// Same structure as NAL HRD: 7 bytes.
	if len(payload) != 7 {
		t.Fatalf("payload length = %d, want 7 (VCL HRD present)", len(payload))
	}

	// cpb_removal_delay = 20 = 0x000014.
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x14 {
		t.Errorf("cpb_removal_delay = %02X %02X %02X, want 00 00 14",
			payload[0], payload[1], payload[2])
	}
}

func TestBuildPicTimingSEI_H264_HRDOnly_NoPicStruct(t *testing.T) {
	// HRD present but no pic_struct — just delays, no pic_struct field.
	cfg := PicTimingConfig{
		NalHRDParametersPresent: true,
		PicStructPresent:        false,
		CpbRemovalDelayLength:   24,
		DpbOutputDelayLength:    24,
	}

	payload := BuildPicTimingSEI(cfg, 5, nil)
	// 24 + 24 = 48 bits = 6 bytes (no pic_struct, already byte-aligned).
	if len(payload) != 6 {
		t.Fatalf("payload length = %d, want 6", len(payload))
	}

	// cpb_removal_delay = 10 = 0x00000A.
	if payload[2] != 0x0A {
		t.Errorf("cpb_removal_delay low byte = %02X, want 0A", payload[2])
	}
}

func TestBuildPicTimingSEI_HEVC(t *testing.T) {
	// HEVC with frame_field_info + CpbDpbDelays.
	cfg := DefaultHEVCPicTimingConfig()

	payload := BuildPicTimingSEI(cfg, 50, nil)

	// Expected bit layout:
	// pic_struct (4 bits):                0b0000
	// source_scan_type (2 bits):          0b01
	// duplicate_flag (1 bit):             0b0
	// au_cpb_removal_delay_minus1 (24 bits): 50*2-1 = 99 = 0x000063
	// pic_dpb_output_delay (24 bits):     0x000000
	// Total: 4+2+1+24+24 = 55 bits → 7 bytes (with 1 bit padding)

	if len(payload) != 7 {
		t.Fatalf("payload length = %d, want 7", len(payload))
	}

	// First byte: pic_struct(0000) + source_scan_type(01) + duplicate_flag(0) + 1 bit of delay
	// = 0b0000_010_0 + MSB of delay
	// au_cpb_removal_delay_minus1 = 99 = 0x63 = 0b0000_0000_0000_0000_0110_0011
	// First bit of delay (bit 23) = 0
	// So first byte = 0b0000_0100 = 0x04
	if payload[0] != 0x04 {
		t.Errorf("first byte = %02X, want 0x04", payload[0])
	}
}

func TestBuildPicTimingSEI_HEVC_Frame0(t *testing.T) {
	// HEVC frame 0: au_cpb_removal_delay_minus1 = 0 (since 0*2=0, stays 0).
	cfg := DefaultHEVCPicTimingConfig()

	payload := BuildPicTimingSEI(cfg, 0, nil)

	// pic_struct(0000) + source_scan_type(01) + duplicate_flag(0) = 7 bits = 0b0000010
	// au_cpb_removal_delay_minus1(24 bits) = 0
	// pic_dpb_output_delay(24 bits) = 0
	// Total: 7+24+24 = 55 bits = 7 bytes
	if len(payload) != 7 {
		t.Fatalf("payload length = %d, want 7", len(payload))
	}

	// All delays are 0, first byte = 0b0000_0100 = 0x04 (source_scan_type=01).
	// Wait — 7 bits of header + 24 bits delay = 31 bits. First byte is 7+1 bits.
	// 0b0000010_0 = 0x04 ← header(7 bits) + MSB of 24-bit delay (0).
	if payload[0] != 0x04 {
		t.Errorf("first byte = %02X, want 0x04", payload[0])
	}
}

func TestBuildPicTimingSEI_HEVC_NoFrameFieldInfo(t *testing.T) {
	// HEVC without frame_field_info — only delays.
	cfg := PicTimingConfig{
		IsHEVC:                true,
		FrameFieldInfoPresent: false,
		CpbDpbDelaysPresent:   true,
		CpbRemovalDelayLength: 24,
		DpbOutputDelayLength:  24,
	}

	payload := BuildPicTimingSEI(cfg, 10, nil)
	// 24 + 24 = 48 bits = 6 bytes.
	if len(payload) != 6 {
		t.Fatalf("payload length = %d, want 6", len(payload))
	}

	// au_cpb_removal_delay_minus1 = 10*2-1 = 19 = 0x000013.
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x13 {
		t.Errorf("au_cpb_removal_delay bytes = %02X %02X %02X, want 00 00 13",
			payload[0], payload[1], payload[2])
	}
}

func TestBuildPicTimingSEI_HEVC_FrameFieldInfoOnly(t *testing.T) {
	// HEVC with only frame_field_info, no delays.
	cfg := PicTimingConfig{
		IsHEVC:                true,
		FrameFieldInfoPresent: true,
		CpbDpbDelaysPresent:   false,
	}

	payload := BuildPicTimingSEI(cfg, 0, nil)
	// 4+2+1 = 7 bits, padded to 1 byte.
	if len(payload) != 1 {
		t.Fatalf("payload length = %d, want 1", len(payload))
	}

	// 0b0000_010_0 = 0x04 (pic_struct=0, source_scan_type=1, duplicate_flag=0, pad=0)
	if payload[0] != 0x04 {
		t.Errorf("payload = %02X, want 0x04", payload[0])
	}
}

// --- Combined pic_timing + caption SEI NALU tests ---

func TestBuildPicTimingAndCaptionSEI_NilPairs_EmitsNullCC(t *testing.T) {
	// nil pairs should still produce pic_timing + null cc_data SEI to maintain
	// continuous caption carriage for downstream decoder acquisition.
	cfg := DefaultPicTimingConfig()
	result := BuildPicTimingAndCaptionSEI(nil, cfg, 0, nil, "h264")
	if result == nil {
		t.Fatal("expected non-nil SEI with null cc_data, got nil")
	}
	if len(result) < 4 || result[0] != 0x00 || result[1] != 0x00 || result[2] != 0x00 || result[3] != 0x01 {
		t.Errorf("expected Annex B start code, got %x", result[:4])
	}
}

func TestBuildPicTimingAndCaptionSEI_EmptyPairs_EmitsNullCC(t *testing.T) {
	cfg := DefaultPicTimingConfig()
	result := BuildPicTimingAndCaptionSEI([]CCPair{}, cfg, 0, nil, "h264")
	if result == nil {
		t.Fatal("expected non-nil SEI with null cc_data, got nil")
	}
}

func TestBuildPicTimingAndCaptionSEI_H264_Structure(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'H', 'i'}}}
	cfg := DefaultPicTimingConfig()

	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h264")
	if nalu == nil {
		t.Fatal("returned nil")
	}

	// Should start with Annex B start code.
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Error("missing Annex B start code")
	}

	// H.264 SEI NALU type.
	if nalu[4] != naluTypeSEI {
		t.Errorf("NALU type = %02X, want %02X (SEI)", nalu[4], naluTypeSEI)
	}

	// Should end with RBSP trailing bits 0x80.
	if nalu[len(nalu)-1] != 0x80 {
		t.Errorf("last byte = %02X, want 0x80 (RBSP trailing)", nalu[len(nalu)-1])
	}
}

func TestBuildPicTimingAndCaptionSEI_HEVC_Structure(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'H', 'i'}}}
	cfg := DefaultHEVCPicTimingConfig()

	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h265")
	if nalu == nil {
		t.Fatal("returned nil")
	}

	// Should start with Annex B start code.
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Error("missing Annex B start code")
	}

	// HEVC PREFIX_SEI header.
	if nalu[4] != hevcSEIByte0 || nalu[5] != hevcSEIByte1 {
		t.Errorf("HEVC header = %02X %02X, want %02X %02X",
			nalu[4], nalu[5], hevcSEIByte0, hevcSEIByte1)
	}

	// Should end with RBSP trailing bits 0x80.
	if nalu[len(nalu)-1] != 0x80 {
		t.Errorf("last byte = %02X, want 0x80 (RBSP trailing)", nalu[len(nalu)-1])
	}
}

func TestPicTimingBeforeCaptionInNALU(t *testing.T) {
	// Verify that pic_timing (payload_type=1) appears before caption (payload_type=4)
	// in the combined SEI NALU byte stream.
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}
	cfg := DefaultPicTimingConfig()

	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h264")
	if nalu == nil {
		t.Fatal("returned nil")
	}

	// Strip Annex B start code (4 bytes) and H.264 NALU header (1 byte).
	// Use stateful EPB removal because the pic_timing payload at frame 0
	// contains all-zero bytes that create cascading EPB patterns.
	body := removeEPBStateful(nalu[5:])

	// First SEI message should be pic_timing (type 1).
	if len(body) < 1 {
		t.Fatal("body too short")
	}
	if body[0] != seiPayloadTypePicTiming {
		t.Errorf("first SEI message type = %02X, want %02X (pic_timing)",
			body[0], seiPayloadTypePicTiming)
	}

	// Parse past pic_timing to find caption SEI.
	pos := 1
	// Read pic_timing payload size.
	ptSize := 0
	for pos < len(body) && body[pos] == 0xFF {
		ptSize += 255
		pos++
	}
	if pos >= len(body) {
		t.Fatal("truncated pic_timing size")
	}
	ptSize += int(body[pos])
	pos++
	pos += ptSize

	// Second SEI message should be user_data_registered (type 4).
	if pos >= len(body) {
		t.Fatal("no second SEI message found")
	}
	if body[pos] != seiPayloadTypeT35 {
		t.Errorf("second SEI message type = %02X, want %02X (user_data_registered)",
			body[pos], seiPayloadTypeT35)
	}
}

func TestPicTimingBeforeCaptionInNALU_HEVC(t *testing.T) {
	// Same test for HEVC — pic_timing must precede caption SEI.
	pairs := []CCPair{{Data: [2]byte{'X', 'Y'}}}
	cfg := DefaultHEVCPicTimingConfig()

	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h265")
	if nalu == nil {
		t.Fatal("returned nil")
	}

	// Strip start code (4) + HEVC header (2).
	// Use stateful EPB removal for cascading zero patterns.
	body := removeEPBStateful(nalu[6:])

	// First SEI message: pic_timing.
	if body[0] != seiPayloadTypePicTiming {
		t.Errorf("first SEI type = %02X, want %02X", body[0], seiPayloadTypePicTiming)
	}

	// Skip to second message.
	pos := 1
	ptSize := 0
	for pos < len(body) && body[pos] == 0xFF {
		ptSize += 255
		pos++
	}
	ptSize += int(body[pos])
	pos++
	pos += ptSize

	// Second SEI: caption.
	if body[pos] != seiPayloadTypeT35 {
		t.Errorf("second SEI type = %02X, want %02X", body[pos], seiPayloadTypeT35)
	}
}

func TestBuildPicTimingAndCaptionSEI_CaptionRoundTrip(t *testing.T) {
	// The caption data in the combined NALU should be extractable.
	pairs := []CCPair{
		{Data: [2]byte{'H', 'e'}},
		{Data: [2]byte{'l', 'l'}},
	}
	cfg := DefaultPicTimingConfig()

	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 42, nil, "h264")
	if nalu == nil {
		t.Fatal("returned nil")
	}

	// Strip start code + H.264 header, remove EPB.
	// Use stateful removal for cascading EPB safety.
	body := removeEPBStateful(nalu[5:])

	// Skip past pic_timing SEI message to reach caption SEI.
	pos := 0
	// payload type
	if body[pos] != seiPayloadTypePicTiming {
		t.Fatalf("expected pic_timing at pos 0, got %02X", body[pos])
	}
	pos++
	// payload size
	ptSize := 0
	for pos < len(body) && body[pos] == 0xFF {
		ptSize += 255
		pos++
	}
	ptSize += int(body[pos])
	pos++
	pos += ptSize

	// Now body[pos:] should be the caption SEI message followed by RBSP trailing.
	captionBody := body[pos:]
	extracted := ExtractCCPairsFromSEI(captionBody)
	if len(extracted) != len(pairs) {
		t.Fatalf("extracted %d pairs, want %d", len(extracted), len(pairs))
	}
	for i, p := range extracted {
		if p.Data != pairs[i].Data {
			t.Errorf("pair %d: got %02X %02X, want %02X %02X",
				i, p.Data[0], p.Data[1], pairs[i].Data[0], pairs[i].Data[1])
		}
	}
}

func TestBuildPicTimingAndCaptionSEI_InsertBeforeVCL(t *testing.T) {
	// End-to-end: build combined SEI, insert before VCL, verify order.
	pairs := []CCPair{{Data: [2]byte{'O', 'K'}}}
	cfg := DefaultPicTimingConfig()

	sei := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h264")

	// SPS + PPS + IDR.
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xC0, 0x1E, // SPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, 0x80, // PPS
		0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, 0xCC, // IDR
	}

	result := InsertSEIBeforeVCLInto(sei, annexB, nil)

	spsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x67})
	ppsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x68})
	seiIdx := bytes.Index(result, sei)
	idrIdx := bytes.LastIndex(result, []byte{0x00, 0x00, 0x00, 0x01, 0x65})

	if spsIdx >= ppsIdx || ppsIdx >= seiIdx || seiIdx >= idrIdx {
		t.Errorf("order should be SPS(%d) < PPS(%d) < SEI(%d) < IDR(%d)",
			spsIdx, ppsIdx, seiIdx, idrIdx)
	}
}

func TestBuildPicTimingAndCaptionSEI_HEVC_InsertBeforeVCL(t *testing.T) {
	// End-to-end for HEVC.
	pairs := []CCPair{{Data: [2]byte{'O', 'K'}}}
	cfg := DefaultHEVCPicTimingConfig()

	sei := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h265")

	// VPS + SPS + PPS + IDR (HEVC).
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0x01, 0x02, // VPS
		0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0x03, 0x04, // SPS
		0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0x05, 0x06, // PPS
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, 0xBB, // IDR
	}

	result := InsertSEIBeforeVCLInto(sei, annexB, nil, "h265")

	vpsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x40})
	spsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x42})
	ppsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x44})
	seiIdx := bytes.Index(result, sei)
	idrIdx := bytes.LastIndex(result, []byte{0x00, 0x00, 0x00, 0x01, 0x26})

	if vpsIdx >= spsIdx || spsIdx >= ppsIdx || ppsIdx >= seiIdx || seiIdx >= idrIdx {
		t.Errorf("order should be VPS(%d) < SPS(%d) < PPS(%d) < SEI(%d) < IDR(%d)",
			vpsIdx, spsIdx, ppsIdx, seiIdx, idrIdx)
	}
}

func TestBuildPicTimingAndCaptionSEI_MonotonicallyIncreasingDelay(t *testing.T) {
	// Verify that cpb_removal_delay increases with frame number.
	cfg := DefaultPicTimingConfig()
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}

	for _, frame := range []int64{0, 1, 100, 10000} {
		nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, frame, nil, "h264")
		if nalu == nil {
			t.Fatalf("returned nil for frame %d", frame)
		}

		// Extract cpb_removal_delay from the pic_timing payload.
		// Strip start code(4) + H.264 header(1) + pic_timing_type(1) + size(1).
		body := removeEPB(nalu[5:])
		if body[0] != seiPayloadTypePicTiming {
			t.Fatalf("frame %d: first byte = %02X, want pic_timing type", frame, body[0])
		}
		ptSize := int(body[1])
		ptPayload := body[2 : 2+ptSize]

		// First 24 bits = cpb_removal_delay = frame * 2.
		if len(ptPayload) < 3 {
			t.Fatalf("frame %d: pic_timing payload too short: %d", frame, len(ptPayload))
		}
		delay := uint32(ptPayload[0])<<16 | uint32(ptPayload[1])<<8 | uint32(ptPayload[2])
		expected := uint32(frame * 2)
		if delay != expected {
			t.Errorf("frame %d: cpb_removal_delay = %d, want %d", frame, delay, expected)
		}
	}
}

// --- Default config tests ---

func TestDefaultPicTimingConfig(t *testing.T) {
	cfg := DefaultPicTimingConfig()
	if !cfg.NalHRDParametersPresent {
		t.Error("NalHRDParametersPresent should be true")
	}
	if cfg.VclHRDParametersPresent {
		t.Error("VclHRDParametersPresent should be false")
	}
	if !cfg.PicStructPresent {
		t.Error("PicStructPresent should be true")
	}
	if cfg.CpbRemovalDelayLength != 24 {
		t.Errorf("CpbRemovalDelayLength = %d, want 24", cfg.CpbRemovalDelayLength)
	}
	if cfg.DpbOutputDelayLength != 24 {
		t.Errorf("DpbOutputDelayLength = %d, want 24", cfg.DpbOutputDelayLength)
	}
	if cfg.IsHEVC {
		t.Error("IsHEVC should be false for default")
	}
}

func TestDefaultHEVCPicTimingConfig(t *testing.T) {
	cfg := DefaultHEVCPicTimingConfig()
	if !cfg.IsHEVC {
		t.Error("IsHEVC should be true")
	}
	if !cfg.FrameFieldInfoPresent {
		t.Error("FrameFieldInfoPresent should be true")
	}
	if !cfg.CpbDpbDelaysPresent {
		t.Error("CpbDpbDelaysPresent should be true")
	}
	if cfg.CpbRemovalDelayLength != 24 {
		t.Errorf("CpbRemovalDelayLength = %d, want 24", cfg.CpbRemovalDelayLength)
	}
}

// --- Helper/edge case tests ---

func TestSEISizeLen(t *testing.T) {
	tests := []struct {
		size int
		want int
	}{
		{0, 1},
		{1, 1},
		{254, 1},
		{255, 2}, // 0xFF + 0x00
		{256, 2}, // 0xFF + 0x01
		{510, 3}, // 0xFF + 0xFF + 0x00
	}
	for _, tt := range tests {
		got := seiSizeLen(tt.size)
		if got != tt.want {
			t.Errorf("seiSizeLen(%d) = %d, want %d", tt.size, got, tt.want)
		}
	}
}

func TestAppendSEISize(t *testing.T) {
	tests := []struct {
		size int
		want []byte
	}{
		{0, []byte{0x00}},
		{7, []byte{0x07}},
		{254, []byte{0xFE}},
		{255, []byte{0xFF, 0x00}},
		{256, []byte{0xFF, 0x01}},
		{510, []byte{0xFF, 0xFF, 0x00}},
	}
	for _, tt := range tests {
		got := appendSEISize(nil, tt.size)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("appendSEISize(%d) = %X, want %X", tt.size, got, tt.want)
		}
	}
}

// bitReader reads bits from a byte slice (for test verification).
type bitReader struct {
	data   []byte
	pos    int // byte position
	bitPos int // bit position within current byte (0-7)
}

func (r *bitReader) readBits(n int) int {
	val := 0
	for i := 0; i < n; i++ {
		if r.pos >= len(r.data) {
			return val
		}
		bit := (r.data[r.pos] >> uint(7-r.bitPos)) & 1
		val = (val << 1) | int(bit)
		r.bitPos++
		if r.bitPos == 8 {
			r.bitPos = 0
			r.pos++
		}
	}
	return val
}

func TestBuildPicTimingSEI_H264_WithTimecode_NDF(t *testing.T) {
	cfg := DefaultPicTimingConfig()
	cfg.TimecodeEnabled = true
	cfg.FPSNum = 30
	cfg.FPSDen = 1

	tc := &Timecode{Hours: 12, Minutes: 30, Seconds: 45, Frames: 15}
	payload := BuildPicTimingSEI(cfg, 100, tc)

	if len(payload) <= 7 {
		t.Fatalf("payload length = %d, expected > 7 with timecode", len(payload))
	}

	br := &bitReader{data: payload}

	// cpb_removal_delay (24 bits) = 200
	if v := br.readBits(24); v != 200 {
		t.Errorf("cpb_removal_delay = %d, want 200", v)
	}
	// dpb_output_delay (24 bits) = 0
	if v := br.readBits(24); v != 0 {
		t.Errorf("dpb_output_delay = %d, want 0", v)
	}
	// pic_struct (4 bits) = 0
	if v := br.readBits(4); v != 0 {
		t.Errorf("pic_struct = %d, want 0", v)
	}
	// clock_timestamp_flag[0] = 1
	if v := br.readBits(1); v != 1 {
		t.Fatalf("clock_timestamp_flag = %d, want 1", v)
	}
	// ct_type = 0
	if v := br.readBits(2); v != 0 {
		t.Errorf("ct_type = %d, want 0", v)
	}
	// nuit_field_based_flag = 0
	if v := br.readBits(1); v != 0 {
		t.Errorf("nuit_field_based_flag = %d, want 0", v)
	}
	// counting_type = 1 (NDF for 30/1)
	if v := br.readBits(5); v != 1 {
		t.Errorf("counting_type = %d, want 1", v)
	}
	// full_timestamp_flag = 1
	if v := br.readBits(1); v != 1 {
		t.Errorf("full_timestamp_flag = %d, want 1", v)
	}
	// discontinuity_flag = 0
	if v := br.readBits(1); v != 0 {
		t.Errorf("discontinuity_flag = %d, want 0", v)
	}
	// cnt_dropped_flag = 0
	if v := br.readBits(1); v != 0 {
		t.Errorf("cnt_dropped_flag = %d, want 0", v)
	}
	// n_frames = 15
	if v := br.readBits(8); v != 15 {
		t.Errorf("n_frames = %d, want 15", v)
	}
	// seconds = 45
	if v := br.readBits(6); v != 45 {
		t.Errorf("seconds = %d, want 45", v)
	}
	// minutes = 30
	if v := br.readBits(6); v != 30 {
		t.Errorf("minutes = %d, want 30", v)
	}
	// hours = 12
	if v := br.readBits(5); v != 12 {
		t.Errorf("hours = %d, want 12", v)
	}
}

func TestBuildPicTimingSEI_H264_WithTimecode_DF(t *testing.T) {
	cfg := DefaultPicTimingConfig()
	cfg.TimecodeEnabled = true
	cfg.FPSNum = 30000
	cfg.FPSDen = 1001

	tc := &Timecode{
		Hours: 0, Minutes: 1, Seconds: 0, Frames: 2,
		DropFrame: true, CntDropped: true,
	}
	payload := BuildPicTimingSEI(cfg, 0, tc)

	br := &bitReader{data: payload}
	br.readBits(24) // cpb_removal_delay
	br.readBits(24) // dpb_output_delay
	br.readBits(4)  // pic_struct
	br.readBits(1)  // clock_timestamp_flag
	br.readBits(2)  // ct_type
	br.readBits(1)  // nuit_field_based_flag

	// counting_type = 4 (DF)
	if v := br.readBits(5); v != 4 {
		t.Errorf("counting_type = %d, want 4", v)
	}
	br.readBits(1) // full_timestamp_flag
	br.readBits(1) // discontinuity_flag

	// cnt_dropped_flag = 1
	if v := br.readBits(1); v != 1 {
		t.Errorf("cnt_dropped_flag = %d, want 1", v)
	}
	// n_frames = 2
	if v := br.readBits(8); v != 2 {
		t.Errorf("n_frames = %d, want 2", v)
	}
}

func TestBuildPicTimingSEI_H264_NilTimecode_BackwardsCompat(t *testing.T) {
	cfg := DefaultPicTimingConfig()
	payload := BuildPicTimingSEI(cfg, 100, nil)
	if len(payload) != 7 {
		t.Fatalf("payload length = %d, want 7 (backwards compat)", len(payload))
	}
}

func TestBuildPicTimingAndCaptionSEI_WithTimecode(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}
	cfg := DefaultPicTimingConfig()
	cfg.TimecodeEnabled = true
	cfg.FPSNum = 30000
	cfg.FPSDen = 1001

	tc := &Timecode{Hours: 14, Minutes: 30, Seconds: 0, Frames: 2, DropFrame: true, CntDropped: true}
	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 42, tc, "h264")
	if nalu == nil {
		t.Fatal("returned nil")
	}

	// Verify pic_timing comes before caption.
	body := removeEPBStateful(nalu[5:])
	if body[0] != seiPayloadTypePicTiming {
		t.Errorf("first SEI type = %02X, want pic_timing", body[0])
	}

	// Skip pic_timing to find caption.
	pos := 1
	ptSize := 0
	for pos < len(body) && body[pos] == 0xFF {
		ptSize += 255
		pos++
	}
	ptSize += int(body[pos])
	pos++
	pos += ptSize

	if pos >= len(body) {
		t.Fatal("no second SEI message found")
	}
	if body[pos] != seiPayloadTypeT35 {
		t.Errorf("second SEI type = %02X, want caption", body[pos])
	}

	// Verify caption data round-trips.
	extracted := ExtractCCPairsFromSEI(body[pos:])
	if len(extracted) != 1 || extracted[0].Data != [2]byte{'A', 'B'} {
		t.Errorf("caption round-trip failed: got %v", extracted)
	}
}

func TestBuildPicTimingAndCaptionSEI_NilTimecode_BackwardsCompat(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'X', 'Y'}}}
	cfg := DefaultPicTimingConfig()

	nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, 0, nil, "h264")
	if nalu == nil {
		t.Fatal("returned nil")
	}
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Error("missing Annex B start code")
	}
}
