package switcher

import (
	"bytes"
	"testing"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
)

// TestBroadcastWithCaptions_ModeOff_InjectsSEI verifies that broadcastWithCaptions
// injects a cc_data SEI NALU into the encoded frame even when the caption manager
// is in ModeOff. This is the integration point for continuous ATSC A/53 carriage.
func TestBroadcastWithCaptions_ModeOff_InjectsSEI(t *testing.T) {
	t.Parallel()

	mgr := caption.NewManager() // default ModeOff

	// Build a minimal H.264 IDR frame in wire format (4-byte length prefix).
	// SPS + PPS + IDR slice.
	sps := []byte{0x67, 0x42, 0x00, 0x1E, 0xAB}                   // SPS
	pps := []byte{0x68, 0xCE, 0x38, 0x80}                         // PPS
	idr := []byte{0x65, 0x88, 0x84, 0x00, 0x33, 0xFF, 0xAA, 0xBB} // IDR slice

	var wire []byte
	// Length-prefix each NALU.
	for _, nalu := range [][]byte{sps, pps, idr} {
		lenBuf := []byte{0, 0, 0, byte(len(nalu))}
		wire = append(wire, lenBuf...)
		wire = append(wire, nalu...)
	}

	// Consume pairs from the manager (should be null pair in ModeOff).
	pairs := mgr.ConsumeForFrame()
	if len(pairs) == 0 {
		t.Fatal("ModeOff should produce at least one null pair")
	}

	// Build the SEI NALU.
	seiNALU := caption.BuildSEINALU(pairs)
	if seiNALU == nil {
		t.Fatal("BuildSEINALU should produce valid SEI for null pairs")
	}

	// Simulate what broadcastWithCaptions does:
	// wire → Annex B → insert SEI → Annex B → wire
	annexB := codec.WireToAnnexBInto(wire, nil)
	inserted := caption.InsertSEIBeforeVCLInto(seiNALU, annexB, nil)
	resultWire := codec.AnnexBToWireInto(inserted, nil)

	// The result wire data should be longer (SEI added).
	if len(resultWire) <= len(wire) {
		t.Fatalf("result wire (%d bytes) should be longer than input (%d bytes)",
			len(resultWire), len(wire))
	}

	// Convert back to Annex B to verify the SEI is present.
	resultAnnexB := codec.WireToAnnexBInto(resultWire, nil)

	// Look for the SEI NALU type (0x06) in the result.
	nalus := extractNALUTypes(resultAnnexB)
	hasSEI := false
	hasIDR := false
	for _, nt := range nalus {
		if nt == 6 {
			hasSEI = true
		}
		if nt == 5 {
			hasIDR = true
		}
	}
	if !hasSEI {
		t.Error("result should contain SEI NALU (type 6)")
	}
	if !hasIDR {
		t.Error("result should still contain IDR NALU (type 5)")
	}

	// Verify the GA94 identifier is present in the output.
	if !bytes.Contains(resultAnnexB, []byte("GA94")) {
		t.Error("result should contain ATSC A/53 GA94 identifier")
	}

	// Verify process_cc_data_flag is set: look for the cc_count byte with bit 6 set.
	// The GA94 payload has: country(1) + provider(2) + "GA94"(4) + type(1) + cc_count_byte
	ga94Idx := bytes.Index(resultAnnexB, []byte("GA94"))
	if ga94Idx >= 0 {
		ccCountByteIdx := ga94Idx + 4 + 1 // skip "GA94" + user_data_type_code
		if ccCountByteIdx < len(resultAnnexB) {
			ccCountByte := resultAnnexB[ccCountByteIdx]
			processCCData := (ccCountByte >> 6) & 1
			if processCCData != 1 {
				t.Error("process_cc_data_flag must be 1 for continuous carriage")
			}
		}
	}
}

// TestBroadcastWithCaptions_HEVC_ModeOff_InjectsSEI verifies the same for HEVC.
func TestBroadcastWithCaptions_HEVC_ModeOff_InjectsSEI(t *testing.T) {
	t.Parallel()

	mgr := caption.NewManager()
	pairs := mgr.ConsumeForFrame()

	seiNALU := caption.BuildSEINALU(pairs, "h265")
	if seiNALU == nil {
		t.Fatal("HEVC null pair SEI should not be nil")
	}

	// Build minimal HEVC stream: VPS + SPS + PPS + IDR_W_RADL.
	// HEVC NALU header: 2 bytes. First byte: (type << 1). Second byte: 0x01.
	vps := []byte{0x40, 0x01, 0xFF}        // VPS (type=32)
	spsh := []byte{0x42, 0x01, 0xFF}       // SPS (type=33)
	ppsh := []byte{0x44, 0x01, 0xFF}       // PPS (type=34)
	idrh := []byte{0x26, 0x01, 0xAA, 0xBB} // IDR_W_RADL (type=19)

	var wire []byte
	for _, nalu := range [][]byte{vps, spsh, ppsh, idrh} {
		lenBuf := []byte{0, 0, 0, byte(len(nalu))}
		wire = append(wire, lenBuf...)
		wire = append(wire, nalu...)
	}

	annexB := codec.WireToAnnexBInto(wire, nil)
	inserted := caption.InsertSEIBeforeVCLInto(seiNALU, annexB, nil, "h265")
	resultWire := codec.AnnexBToWireInto(inserted, nil)

	if len(resultWire) <= len(wire) {
		t.Fatalf("HEVC result wire should be longer than input")
	}

	resultAnnexB := codec.WireToAnnexBInto(resultWire, nil)

	// Verify HEVC PREFIX_SEI (type 39) is present.
	nalus := extractHEVCNALUTypes(resultAnnexB)
	hasSEI := false
	hasIDR := false
	for _, nt := range nalus {
		if nt == 39 {
			hasSEI = true
		}
		if nt == 19 {
			hasIDR = true
		}
	}
	if !hasSEI {
		t.Error("HEVC result should contain PREFIX_SEI NALU (type 39)")
	}
	if !hasIDR {
		t.Error("HEVC result should still contain IDR NALU (type 19)")
	}

	if !bytes.Contains(resultAnnexB, []byte("GA94")) {
		t.Error("HEVC result should contain ATSC A/53 GA94 identifier")
	}
}

// TestBroadcastWithCaptions_PicTiming_ModeOff_InjectsCombinedSEI verifies
// that when pic_timing is configured, ModeOff still produces a combined
// pic_timing + cc_data SEI with the frame counter advancing.
func TestBroadcastWithCaptions_PicTiming_ModeOff_InjectsCombinedSEI(t *testing.T) {
	t.Parallel()

	mgr := caption.NewManager()
	cfg := caption.DefaultPicTimingConfig()

	// Simulate 5 frames.
	for i := int64(0); i < 5; i++ {
		pairs := mgr.ConsumeForFrame()
		nalu := caption.BuildPicTimingAndCaptionSEI(pairs, cfg, i, nil, "h264")
		if nalu == nil {
			t.Fatalf("frame %d: combined SEI should not be nil in ModeOff", i)
		}

		// Strip start code + header, check first SEI type is pic_timing (1).
		body := nalu[5:]
		body = stripEPBHelper(body)
		if len(body) < 1 || body[0] != 0x01 {
			t.Fatalf("frame %d: first SEI type should be 0x01 (pic_timing), got 0x%02x",
				i, body[0])
		}

		// Verify GA94 is present (caption data follows pic_timing).
		if !bytes.Contains(nalu, []byte("GA94")) {
			t.Fatalf("frame %d: combined SEI should contain GA94", i)
		}
	}
}

// TestBroadcastWithCaptions_ModeOff_SEIBeforeVCL verifies that the SEI
// is positioned correctly in the access unit — after parameter sets
// but before the VCL NALU (IDR/slice).
func TestBroadcastWithCaptions_ModeOff_SEIBeforeVCL(t *testing.T) {
	t.Parallel()

	mgr := caption.NewManager()
	pairs := mgr.ConsumeForFrame()
	sei := caption.BuildSEINALU(pairs)

	// Annex B: SPS + PPS + IDR.
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1E, // SPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, 0x80, // PPS
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, // IDR
	}

	result := caption.InsertSEIBeforeVCLInto(sei, annexB, nil)

	// Find positions of SPS, SEI, IDR.
	spsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x67})
	ppsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x68})
	seiIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x06})
	idrIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x65})

	if spsIdx < 0 || ppsIdx < 0 || seiIdx < 0 || idrIdx < 0 {
		t.Fatalf("missing NALUs: SPS=%d PPS=%d SEI=%d IDR=%d",
			spsIdx, ppsIdx, seiIdx, idrIdx)
	}

	// Order: SPS < PPS < SEI < IDR.
	if !(spsIdx < ppsIdx && ppsIdx < seiIdx && seiIdx < idrIdx) {
		t.Errorf("wrong NALU order: SPS=%d PPS=%d SEI=%d IDR=%d (want SPS<PPS<SEI<IDR)",
			spsIdx, ppsIdx, seiIdx, idrIdx)
	}
}

// TestBroadcastWithCaptions_TextThenOff_TransitionsCleanly verifies that
// switching from active text to ModeOff doesn't break the SEI injection path.
func TestBroadcastWithCaptions_TextThenOff_TransitionsCleanly(t *testing.T) {
	t.Parallel()

	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuthor)
	mgr.IngestText("Hello")

	// Drain text frames.
	for i := 0; i < 100; i++ {
		pairs := mgr.ConsumeForFrame()
		sei := caption.BuildSEINALU(pairs)
		if sei == nil {
			t.Fatalf("frame %d (author): SEI should not be nil", i)
		}
	}

	// Switch to ModeOff.
	mgr.SetMode(caption.ModeOff)
	pairs := mgr.ConsumeForFrame()
	sei := caption.BuildSEINALU(pairs)
	if sei == nil {
		t.Fatal("ModeOff after author: SEI should not be nil")
	}
	if !bytes.Contains(sei, []byte("GA94")) {
		t.Error("ModeOff SEI should contain GA94")
	}
}

// extractNALUTypes extracts H.264 NALU types from an Annex B bitstream.
func extractNALUTypes(data []byte) []byte {
	var types []byte
	i := 0
	for i+4 <= len(data) {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			if i+4 < len(data) {
				types = append(types, data[i+4]&0x1F)
			}
			i += 4
		} else {
			i++
		}
	}
	return types
}

// extractHEVCNALUTypes extracts HEVC NALU types from an Annex B bitstream.
func extractHEVCNALUTypes(data []byte) []byte {
	var types []byte
	i := 0
	for i+4 <= len(data) {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			if i+4 < len(data) {
				types = append(types, (data[i+4]>>1)&0x3F)
			}
			i += 4
		} else {
			i++
		}
	}
	return types
}

// stripEPBHelper strips emulation prevention bytes from NALU body.
func stripEPBHelper(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if i+2 < len(data) && data[i] == 0x00 && data[i+1] == 0x00 && data[i+2] == 0x03 {
			out = append(out, 0x00, 0x00)
			i += 2
		} else {
			out = append(out, data[i])
		}
	}
	return out
}
