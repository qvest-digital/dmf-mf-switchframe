package caption

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// --- ConsumeForFrame continuous carriage tests ---
// Verify that every mode emits at least a null pair per frame, establishing
// continuous ATSC A/53 cc_data presence for downstream decoder acquisition.

func TestContinuousCarriage_ModeOff_EmitsNullPair(t *testing.T) {
	t.Parallel()
	m := NewManager()
	// Default mode is ModeOff.
	pairs := m.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("ModeOff must emit at least one pair for continuous carriage")
	}
	if !pairs[0].IsNull() {
		t.Errorf("ModeOff pair should be null (0x80,0x80), got %v", pairs[0])
	}
}

func TestContinuousCarriage_ModePassThrough_IdleEmitsNullPair(t *testing.T) {
	t.Parallel()
	m := NewManager()
	m.SetMode(ModePassThrough)
	// No text set — idle pass-through.
	pairs := m.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("ModePassThrough (idle) must emit at least one pair")
	}
	if !pairs[0].IsNull() {
		t.Errorf("idle pass-through pair should be null, got %v", pairs[0])
	}
}

func TestContinuousCarriage_ModeAuthor_IdleEmitsNullPair(t *testing.T) {
	t.Parallel()
	m := NewManager()
	m.SetMode(ModeAuthor)
	// No text ingested — idle author mode.
	pairs := m.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("ModeAuthor (idle) must emit at least one pair")
	}
	if !pairs[0].IsNull() {
		t.Errorf("idle author pair should be null, got %v", pairs[0])
	}
}

func TestContinuousCarriage_ModeAuto_IdleEmitsNullPair(t *testing.T) {
	t.Parallel()
	m := NewManager()
	m.SetMode(ModeAuto)
	pairs := m.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("ModeAuto (idle) must emit at least one pair")
	}
	if !pairs[0].IsNull() {
		t.Errorf("idle auto pair should be null, got %v", pairs[0])
	}
}

func TestContinuousCarriage_ModeOff_ConsistentAcrossFrames(t *testing.T) {
	t.Parallel()
	m := NewManager()
	// Verify null pairs are emitted consistently over many frames.
	for i := 0; i < 300; i++ {
		pairs := m.ConsumeForFrame()
		if len(pairs) == 0 {
			t.Fatalf("frame %d: no pairs emitted", i)
		}
		if !pairs[0].IsNull() {
			t.Fatalf("frame %d: expected null pair, got %v", i, pairs[0])
		}
	}
}

func TestContinuousCarriage_ModeTransition_NeverDrops(t *testing.T) {
	t.Parallel()
	m := NewManager()
	// Cycle through all modes, consuming a frame after each.
	// Pairs must never be empty.
	modes := []Mode{ModeOff, ModeAuthor, ModePassThrough, ModeAuto, ModeOff}
	for _, mode := range modes {
		m.SetMode(mode)
		pairs := m.ConsumeForFrame()
		if len(pairs) == 0 {
			t.Fatalf("mode %v: no pairs emitted after mode change", mode)
		}
	}
}

func TestContinuousCarriage_AuthorText_ThenIdle_StillEmits(t *testing.T) {
	t.Parallel()
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("Hi")

	// Drain the "Hi" text pairs.
	for i := 0; i < 100; i++ {
		m.ConsumeForFrame()
	}

	// After draining, should still emit null pairs (not nil).
	pairs := m.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("after draining text, must still emit null pair")
	}
	if !pairs[0].IsNull() {
		t.Errorf("post-drain pair should be null, got %v", pairs[0])
	}
}

func TestContinuousCarriage_VANC_NullPairsDispatched(t *testing.T) {
	t.Parallel()
	m := NewManager()

	var called int
	var lastPairs []CCPair
	m.SetVANCSink(func(pairs []CCPair) {
		called++
		lastPairs = pairs
	})

	// Even in ModeOff, VANC sink should fire with null pairs.
	m.ConsumeForFrameWithVANC()
	if called == 0 {
		t.Fatal("VANC sink should be called even with null pairs")
	}
	if len(lastPairs) != 1 || !lastPairs[0].IsNull() {
		t.Errorf("VANC sink should receive null pair, got %v", lastPairs)
	}
}

// --- BuildSEINALU null pair wire format tests ---
// Verify the SEI NALU structure is correct per ATSC A/53 when emitting null pairs.

func TestBuildSEINALU_NullPair_WireFormat_H264(t *testing.T) {
	t.Parallel()
	nalu := BuildSEINALU([]CCPair{NullPair()})
	if nalu == nil {
		t.Fatal("null pair should produce valid SEI NALU")
	}

	// Annex B start code: 00 00 00 01
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Fatalf("missing Annex B start code: got %x", nalu[:4])
	}

	// H.264 SEI NALU type: 0x06
	if nalu[4] != 0x06 {
		t.Errorf("H.264 NALU type should be 0x06 (SEI), got 0x%02x", nalu[4])
	}

	// SEI payload type: 4 (user_data_registered_itu_t_t35)
	if nalu[5] != 0x04 {
		t.Errorf("payload type should be 0x04 (T.35), got 0x%02x", nalu[5])
	}

	// Payload size byte (should be > 0)
	payloadSize := int(nalu[6])
	if payloadSize == 0 {
		t.Fatal("payload size must not be zero")
	}

	// Verify ATSC A/53 header at payload start.
	payloadStart := 7
	if nalu[payloadStart] != 0xB5 {
		t.Errorf("country code should be 0xB5, got 0x%02x", nalu[payloadStart])
	}
	providerCode := binary.BigEndian.Uint16(nalu[payloadStart+1 : payloadStart+3])
	if providerCode != 0x0031 {
		t.Errorf("provider code should be 0x0031, got 0x%04x", providerCode)
	}
	// GA94 user identifier
	ga94 := string(nalu[payloadStart+3 : payloadStart+7])
	if ga94 != "GA94" {
		t.Errorf("user identifier should be 'GA94', got %q", ga94)
	}
	// user_data_type_code = 0x03 (cc_data)
	if nalu[payloadStart+7] != 0x03 {
		t.Errorf("user data type should be 0x03 (cc_data), got 0x%02x", nalu[payloadStart+7])
	}

	// cc_count byte: process_em_data(1) | process_cc_data(1) | additional_data(0) | cc_count(5)
	ccCountByte := nalu[payloadStart+8]
	processCCData := (ccCountByte >> 6) & 0x01
	if processCCData != 1 {
		t.Error("process_cc_data_flag must be 1 for continuous carriage")
	}
	ccCount := int(ccCountByte & 0x1F)
	if ccCount != 2 {
		t.Errorf("cc_count should be 2 (1 pair × 2 triples), got %d", ccCount)
	}

	// em_data byte must be 0xFF per ATSC A/53.
	if nalu[payloadStart+9] != 0xFF {
		t.Errorf("em_data must be 0xFF, got 0x%02x", nalu[payloadStart+9])
	}

	// First cc_data triple (field 1): marker(11111) cc_valid(1) cc_type(00) = 0xFC
	tripleStart := payloadStart + 10
	if nalu[tripleStart] != 0xFC {
		t.Errorf("field 1 cc_byte should be 0xFC (cc_valid=1, cc_type=0), got 0x%02x", nalu[tripleStart])
	}
	// Null data bytes.
	if nalu[tripleStart+1] != 0x80 || nalu[tripleStart+2] != 0x80 {
		t.Errorf("null pair data should be 0x80 0x80, got 0x%02x 0x%02x",
			nalu[tripleStart+1], nalu[tripleStart+2])
	}

	// Second cc_data triple (field 2 null): cc_valid(0) cc_type(01) = 0xF9
	if nalu[tripleStart+3] != 0xF9 {
		t.Errorf("field 2 cc_byte should be 0xF9 (cc_valid=0, cc_type=1), got 0x%02x", nalu[tripleStart+3])
	}
}

func TestBuildSEINALU_NullPair_WireFormat_HEVC(t *testing.T) {
	t.Parallel()
	nalu := BuildSEINALU([]CCPair{NullPair()}, "h265")
	if nalu == nil {
		t.Fatal("null pair should produce valid HEVC SEI NALU")
	}

	// Annex B start code.
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Fatalf("missing Annex B start code")
	}

	// HEVC PREFIX_SEI: 2-byte header (nal_unit_type=39).
	// First byte: (39 << 1) | 0 = 0x4E
	if nalu[4] != 0x4E {
		t.Errorf("HEVC NALU byte 0 should be 0x4E (PREFIX_SEI), got 0x%02x", nalu[4])
	}
	// Second byte: nuh_layer_id=0, nuh_temporal_id_plus1=1 → 0x01
	if nalu[5] != 0x01 {
		t.Errorf("HEVC NALU byte 1 should be 0x01, got 0x%02x", nalu[5])
	}

	// SEI payload type at offset 6 (after 2-byte NALU header).
	if nalu[6] != 0x04 {
		t.Errorf("payload type should be 0x04 (T.35), got 0x%02x", nalu[6])
	}
}

// --- Round-trip: BuildSEINALU → ExtractCCPairsFromSEI ---

func TestBuildSEINALU_NullPair_RoundTrip(t *testing.T) {
	t.Parallel()
	nalu := BuildSEINALU([]CCPair{NullPair()})
	if nalu == nil {
		t.Fatal("null pair SEI should not be nil")
	}

	// Strip start code (4 bytes) and NALU header (1 byte for H.264).
	naluBody := nalu[5:]

	// Strip emulation prevention bytes for the parser.
	clean := stripEPB(naluBody)
	extracted := ExtractCCPairsFromSEI(clean)

	// Should extract exactly one pair — the null pair with cc_valid=1.
	if len(extracted) != 1 {
		t.Fatalf("expected 1 extracted pair, got %d", len(extracted))
	}
	if !extracted[0].IsNull() {
		t.Errorf("extracted pair should be null, got %v", extracted[0])
	}
}

func TestBuildSEINALU_NullPair_RoundTrip_HEVC(t *testing.T) {
	t.Parallel()
	nalu := BuildSEINALU([]CCPair{NullPair()}, "h265")
	if nalu == nil {
		t.Fatal("null pair HEVC SEI should not be nil")
	}

	// Strip start code (4 bytes) and HEVC NALU header (2 bytes).
	naluBody := nalu[6:]
	clean := stripEPB(naluBody)
	extracted := ExtractCCPairsFromSEI(clean)

	if len(extracted) != 1 {
		t.Fatalf("expected 1 extracted pair, got %d", len(extracted))
	}
	if !extracted[0].IsNull() {
		t.Errorf("extracted pair should be null, got %v", extracted[0])
	}
}

func TestBuildSEINALU_EmptyInput_RoundTrip(t *testing.T) {
	t.Parallel()
	// Empty input should auto-substitute a null pair.
	nalu := BuildSEINALU(nil)
	if nalu == nil {
		t.Fatal("nil input should produce SEI with null cc_data")
	}

	naluBody := nalu[5:]
	clean := stripEPB(naluBody)
	extracted := ExtractCCPairsFromSEI(clean)

	if len(extracted) != 1 {
		t.Fatalf("expected 1 extracted pair from nil input, got %d", len(extracted))
	}
	if !extracted[0].IsNull() {
		t.Errorf("extracted pair should be null, got %v", extracted[0])
	}
}

// --- BuildPicTimingAndCaptionSEI null pair tests ---

func TestPicTimingAndCaption_NullPair_ContainsBothMessages(t *testing.T) {
	t.Parallel()
	cfg := DefaultPicTimingConfig()
	nalu := BuildPicTimingAndCaptionSEI([]CCPair{NullPair()}, cfg, 0, "h264")
	if nalu == nil {
		t.Fatal("null pair should produce valid combined SEI NALU")
	}

	// Strip start code + H.264 NALU header.
	body := nalu[5:]
	clean := stripEPB(body)

	// First SEI message should be pic_timing (payload_type=1).
	if len(clean) < 1 {
		t.Fatal("NALU body too short")
	}
	if clean[0] != 0x01 {
		t.Errorf("first SEI payload type should be 0x01 (pic_timing), got 0x%02x", clean[0])
	}

	// After pic_timing, we should find caption SEI (payload_type=4).
	// Skip pic_timing: type(1) + size(1) + payload.
	ptSize := int(clean[1])
	captionStart := 2 + ptSize
	if captionStart >= len(clean) {
		t.Fatalf("NALU too short for caption message after pic_timing (ptSize=%d, body len=%d)",
			ptSize, len(clean))
	}
	if clean[captionStart] != 0x04 {
		t.Errorf("second SEI payload type should be 0x04 (T.35), got 0x%02x", clean[captionStart])
	}
}

func TestPicTimingAndCaption_EmptyInput_ProducesValidSEI(t *testing.T) {
	t.Parallel()
	cfg := DefaultPicTimingConfig()
	// nil pairs should auto-substitute null pair.
	nalu := BuildPicTimingAndCaptionSEI(nil, cfg, 0, "h264")
	if nalu == nil {
		t.Fatal("nil pairs should produce SEI with pic_timing + null cc_data")
	}
	// Should have Annex B start code.
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Error("missing Annex B start code")
	}
}

func TestPicTimingAndCaption_EmptyInput_HEVC(t *testing.T) {
	t.Parallel()
	cfg := DefaultHEVCPicTimingConfig()
	nalu := BuildPicTimingAndCaptionSEI(nil, cfg, 0, "h265")
	if nalu == nil {
		t.Fatal("nil pairs should produce HEVC SEI with pic_timing + null cc_data")
	}
	// HEVC PREFIX_SEI header.
	if nalu[4] != 0x4E || nalu[5] != 0x01 {
		t.Errorf("expected HEVC PREFIX_SEI header 0x4E01, got 0x%02x%02x", nalu[4], nalu[5])
	}
}

func TestPicTimingAndCaption_FrameCounter_IncrementsWithNullPairs(t *testing.T) {
	t.Parallel()
	cfg := DefaultPicTimingConfig()
	// Generate two NALUs with consecutive frame numbers.
	nalu0 := BuildPicTimingAndCaptionSEI([]CCPair{NullPair()}, cfg, 0, "h264")
	nalu1 := BuildPicTimingAndCaptionSEI([]CCPair{NullPair()}, cfg, 1, "h264")
	if nalu0 == nil || nalu1 == nil {
		t.Fatal("both NALUs should be non-nil")
	}
	// NALUs should differ (different cpb_removal_delay in pic_timing).
	if bytes.Equal(nalu0, nalu1) {
		t.Error("consecutive frame NALUs should differ due to pic_timing counter")
	}
}

// --- InsertSEIBeforeVCL with null pair SEI ---

func TestInsertSEI_NullPair_BeforeIDR_H264(t *testing.T) {
	t.Parallel()
	sei := BuildSEINALU([]CCPair{NullPair()})
	// Minimal IDR slice: start code + IDR NALU type (5).
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAB, 0xCD}

	result := InsertSEIBeforeVCLInto(sei, idr, nil)

	// Result should contain both SEI and IDR.
	if len(result) < len(sei)+len(idr) {
		t.Fatalf("result too short: %d bytes, expected at least %d", len(result), len(sei)+len(idr))
	}
	// SEI should come before IDR.
	seiIdx := bytes.Index(result, sei)
	idrIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x65})
	if seiIdx < 0 || idrIdx < 0 {
		t.Fatal("could not find SEI or IDR in result")
	}
	if seiIdx >= idrIdx {
		t.Error("SEI must be inserted before IDR NALU")
	}
}

func TestInsertSEI_NullPair_BeforeIDR_HEVC(t *testing.T) {
	t.Parallel()
	sei := BuildSEINALU([]CCPair{NullPair()}, "h265")
	// Minimal HEVC IDR_W_RADL (type=19): start code + 2-byte header.
	// (19 << 1) | 0 = 0x26, second byte = 0x01
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAB, 0xCD}

	result := InsertSEIBeforeVCLInto(sei, idr, nil, "h265")

	seiIdx := bytes.Index(result, []byte{0x4E, 0x01}) // HEVC PREFIX_SEI header
	idrIdx := bytes.Index(result, []byte{0x26, 0x01}) // HEVC IDR header
	if seiIdx < 0 || idrIdx < 0 {
		t.Fatal("could not find SEI or IDR in result")
	}
	if seiIdx >= idrIdx {
		t.Error("SEI must be inserted before HEVC IDR NALU")
	}
}

// --- CDP (VANC) null pair tests ---

func TestBuildCDP_NullPair_ValidStructure(t *testing.T) {
	t.Parallel()
	cdp := BuildCDP([]CCPair{NullPair()}, 1, CDPFrameRate2997)
	if cdp == nil {
		t.Fatal("null pair should produce valid CDP")
	}

	// CDP magic: 0x96 0x69
	if cdp[0] != 0x96 || cdp[1] != 0x69 {
		t.Errorf("CDP magic should be 0x96 0x69, got 0x%02x 0x%02x", cdp[0], cdp[1])
	}

	// Flags: ccdata_present=1, caption_service_active=1.
	if cdp[4]&0xC0 != 0xC0 {
		t.Errorf("ccdata_present and caption_service_active flags should be set, got 0x%02x", cdp[4])
	}

	// cc_data section marker: 0x72
	ccDataMarker := cdp[7]
	if ccDataMarker != 0x72 {
		t.Errorf("cc_data section marker should be 0x72, got 0x%02x", ccDataMarker)
	}

	// cc_count.
	ccCount := int(cdp[8] & 0x1F)
	if ccCount != 2 {
		t.Errorf("cc_count should be 2, got %d", ccCount)
	}

	// First triple: field 1 with null data.
	if cdp[9] != 0xFC {
		t.Errorf("field 1 cc_byte should be 0xFC, got 0x%02x", cdp[9])
	}
	if cdp[10] != 0x80 || cdp[11] != 0x80 {
		t.Errorf("field 1 data should be 0x80 0x80, got 0x%02x 0x%02x", cdp[10], cdp[11])
	}

	// Second triple: field 2 null.
	if cdp[12] != 0xF9 {
		t.Errorf("field 2 cc_byte should be 0xF9, got 0x%02x", cdp[12])
	}

	// CDP footer marker: 0x74
	footerIdx := 9 + 3*ccCount
	if cdp[footerIdx] != 0x74 {
		t.Errorf("CDP footer marker should be 0x74, got 0x%02x", cdp[footerIdx])
	}

	// Checksum: sum of all bytes mod 256 should equal 0.
	var sum int
	for _, b := range cdp {
		sum += int(b)
	}
	if sum%256 != 0 {
		t.Errorf("CDP checksum failed: byte sum mod 256 = %d, want 0", sum%256)
	}
}

func TestBuildCDP_NullPair_RoundTrip_ST291(t *testing.T) {
	t.Parallel()
	cdp := BuildCDP([]CCPair{NullPair()}, 1, CDPFrameRate2997)
	if cdp == nil {
		t.Fatal("null pair should produce valid CDP")
	}

	// Wrap in ST 291.
	packet, err := WrapCaptionST291(cdp)
	if err != nil {
		t.Fatalf("WrapCaptionST291 failed: %v", err)
	}

	// Parse back.
	parsed, err := ParseCaptionST291(packet)
	if err != nil {
		t.Fatalf("ParseCaptionST291 failed: %v", err)
	}

	if !bytes.Equal(parsed, cdp) {
		t.Error("ST 291 round-trip produced different CDP data")
	}
}

// --- End-to-end: Manager → SEI NALU → extract ---

func TestContinuousCarriage_EndToEnd_ModeOff_H264(t *testing.T) {
	t.Parallel()
	m := NewManager()
	pairs := m.ConsumeForFrame()

	nalu := BuildSEINALU(pairs)
	if nalu == nil {
		t.Fatal("ModeOff pairs should produce valid SEI")
	}

	// Extract and verify round-trip.
	naluBody := nalu[5:] // skip start code + H.264 header
	clean := stripEPB(naluBody)
	extracted := ExtractCCPairsFromSEI(clean)
	if len(extracted) != 1 {
		t.Fatalf("expected 1 extracted pair, got %d", len(extracted))
	}
	if !extracted[0].IsNull() {
		t.Errorf("extracted pair should be null, got %v", extracted[0])
	}
}

func TestContinuousCarriage_EndToEnd_ModeOff_HEVC(t *testing.T) {
	t.Parallel()
	m := NewManager()
	pairs := m.ConsumeForFrame()

	nalu := BuildSEINALU(pairs, "h265")
	if nalu == nil {
		t.Fatal("ModeOff pairs should produce valid HEVC SEI")
	}

	naluBody := nalu[6:] // skip start code + HEVC 2-byte header
	clean := stripEPB(naluBody)
	extracted := ExtractCCPairsFromSEI(clean)
	if len(extracted) != 1 {
		t.Fatalf("expected 1 extracted pair, got %d", len(extracted))
	}
	if !extracted[0].IsNull() {
		t.Errorf("extracted pair should be null, got %v", extracted[0])
	}
}

func TestContinuousCarriage_EndToEnd_WithPicTiming(t *testing.T) {
	t.Parallel()
	m := NewManager()
	cfg := DefaultPicTimingConfig()

	// Simulate 10 frames of continuous carriage.
	for i := int64(0); i < 10; i++ {
		pairs := m.ConsumeForFrame()
		nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, i, "h264")
		if nalu == nil {
			t.Fatalf("frame %d: combined SEI should not be nil", i)
		}
		if len(nalu) < 20 {
			t.Fatalf("frame %d: SEI NALU suspiciously short (%d bytes)", i, len(nalu))
		}
	}
}

func TestContinuousCarriage_EndToEnd_WithPicTiming_HEVC(t *testing.T) {
	t.Parallel()
	m := NewManager()
	cfg := DefaultHEVCPicTimingConfig()

	for i := int64(0); i < 10; i++ {
		pairs := m.ConsumeForFrame()
		nalu := BuildPicTimingAndCaptionSEI(pairs, cfg, i, "h265")
		if nalu == nil {
			t.Fatalf("frame %d: HEVC combined SEI should not be nil", i)
		}
	}
}

// --- Verify real text still works correctly after null pair changes ---

func TestContinuousCarriage_RealText_NotAffected(t *testing.T) {
	t.Parallel()
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("AB")

	// Consume frames until we get the 'AB' text pair.
	var foundText bool
	for i := 0; i < 100; i++ {
		pairs := m.ConsumeForFrame()
		if len(pairs) == 0 {
			t.Fatalf("frame %d: pairs should never be empty", i)
		}
		if !pairs[0].IsNull() {
			foundText = true
			break
		}
	}
	if !foundText {
		t.Error("should have found non-null text pair in author mode")
	}
}

func TestContinuousCarriage_ModeSwitch_AuthorToOff(t *testing.T) {
	t.Parallel()
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("Hi")

	// Drain some frames.
	for i := 0; i < 50; i++ {
		m.ConsumeForFrame()
	}

	// Switch to ModeOff.
	m.SetMode(ModeOff)
	pairs := m.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("ModeOff after author should still emit pairs")
	}
	if !pairs[0].IsNull() {
		t.Errorf("ModeOff pair should be null, got %v", pairs[0])
	}
}

// --- Helper: strip emulation prevention bytes ---

func stripEPB(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if i+2 < len(data) && data[i] == 0x00 && data[i+1] == 0x00 && data[i+2] == 0x03 {
			out = append(out, 0x00, 0x00)
			i += 2 // skip 0x03
		} else {
			out = append(out, data[i])
		}
	}
	return out
}
