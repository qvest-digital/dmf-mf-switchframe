package caption

import (
	"bytes"
	"testing"
)

func TestBuildSEINALU_NilPairs_EmitsNullCC(t *testing.T) {
	// nil pairs should still produce a valid SEI with null cc_data to maintain
	// continuous ATSC A/53 caption carriage for downstream decoder acquisition.
	got := BuildSEINALU(nil)
	if got == nil {
		t.Fatal("BuildSEINALU(nil) should emit null cc_data SEI, got nil")
	}
	// Verify it's a valid Annex B NALU with start code.
	if len(got) < 4 || got[0] != 0x00 || got[1] != 0x00 || got[2] != 0x00 || got[3] != 0x01 {
		t.Errorf("expected Annex B start code, got %x", got[:4])
	}
}

func TestBuildSEINALU_EmptyPairs_EmitsNullCC(t *testing.T) {
	got := BuildSEINALU([]CCPair{})
	if got == nil {
		t.Fatal("BuildSEINALU([]) should emit null cc_data SEI, got nil")
	}
}

func TestBuildSEINALU_Structure(t *testing.T) {
	pairs := []CCPair{
		{Data: [2]byte{'H', 'i'}},
	}

	nalu := BuildSEINALU(pairs)
	if nalu == nil {
		t.Fatal("BuildSEINALU returned nil")
	}

	// Should start with Annex B start code.
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Error("missing Annex B start code")
	}

	// NALU type should be 6 (SEI).
	if nalu[4] != naluTypeSEI {
		t.Errorf("NALU type = %02X, want %02X (SEI)", nalu[4], naluTypeSEI)
	}

	// SEI payload type should be 4 (T.35).
	if nalu[5] != seiPayloadTypeT35 {
		t.Errorf("payload type = %02X, want %02X", nalu[5], seiPayloadTypeT35)
	}

	// Should end with RBSP trailing bits 0x80.
	if nalu[len(nalu)-1] != 0x80 {
		t.Errorf("last byte = %02X, want 0x80 (RBSP trailing)", nalu[len(nalu)-1])
	}
}

func TestBuildSEINALU_RoundTrip(t *testing.T) {
	pairs := []CCPair{
		{Data: [2]byte{'H', 'e'}},
		{Data: [2]byte{'l', 'l'}},
		{Data: [2]byte{'o', 0x80}},
	}

	nalu := BuildSEINALU(pairs)
	if nalu == nil {
		t.Fatal("BuildSEINALU returned nil")
	}

	// Remove EPB for parsing — strip start code and NALU header.
	body := removeEPB(nalu[5:])

	// Parse back.
	extracted := ExtractCCPairsFromSEI(body)
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

// removeEPB removes emulation prevention bytes from a NALU body.
func removeEPB(data []byte) []byte {
	result := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if i+2 < len(data) && data[i] == 0x00 && data[i+1] == 0x00 && data[i+2] == 0x03 {
			result = append(result, 0x00, 0x00)
			if i+3 < len(data) {
				result = append(result, data[i+3])
				i += 3
			} else {
				i += 2
			}
		} else {
			result = append(result, data[i])
		}
	}
	return result
}

func TestBuildSEINALU_MultiplePairs(t *testing.T) {
	pairs := make([]CCPair, 5)
	for i := range pairs {
		pairs[i] = CCPair{Data: [2]byte{byte('A' + i), byte('a' + i)}}
	}

	nalu := BuildSEINALU(pairs)
	if nalu == nil {
		t.Fatal("BuildSEINALU returned nil")
	}

	body := removeEPB(nalu[5:])
	extracted := ExtractCCPairsFromSEI(body)
	if len(extracted) != 5 {
		t.Fatalf("extracted %d pairs, want 5", len(extracted))
	}
}

// insertEPB must handle cascading zero-byte patterns. When consecutive zeros
// span more than one EPB pattern (e.g., 00 00 00 00 00), the insertion of the
// first 03 must not create a new unescaped 00 00 {00-03} pattern in the output.
func TestInsertEPB_CascadingZeros(t *testing.T) {
	tests := []struct {
		name  string
		input []byte // raw NALU body (after start code + header at bytes 0-4)
	}{
		{
			name: "five_consecutive_zeros",
			// Bytes 0-4: start code + NALU header (not scanned).
			// Bytes 5+: payload with 00 00 00 00 00.
			input: []byte{0x00, 0x00, 0x00, 0x01, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80},
		},
		{
			name: "six_consecutive_zeros",
			input: []byte{0x00, 0x00, 0x00, 0x01, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80},
		},
		{
			name: "three_zeros_twice",
			input: []byte{0x00, 0x00, 0x00, 0x01, 0x06, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x00, 0x00, 0x80},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := insertEPB(tt.input, 5)

			// Verify no unescaped 00 00 {00,01,02,03} patterns exist in the
			// NALU body (bytes 5 onward) of the result.
			for i := 5; i < len(result)-2; i++ {
				if result[i] == 0x00 && result[i+1] == 0x00 && result[i+2] <= 0x03 {
					// Check this isn't already an EPB (00 00 03 XX).
					if result[i+2] == 0x03 {
						// This IS an EPB — skip past it.
						i += 2
						continue
					}
					t.Errorf("unescaped pattern at offset %d: %02X %02X %02X",
						i, result[i], result[i+1], result[i+2])
				}
			}
		})
	}
}

func TestInsertSEIBeforeVCLInto_NoSEI(t *testing.T) {
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB} // IDR slice
	dst := make([]byte, 0, 100)

	result := InsertSEIBeforeVCLInto(nil, annexB, dst)
	if !bytes.Equal(result, annexB) {
		t.Errorf("with nil SEI, result should equal annexB")
	}
}

func TestInsertSEIBeforeVCLInto_IDRSlice(t *testing.T) {
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'A', 'B'}}})
	// IDR slice NALU (type 5).
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB}

	result := InsertSEIBeforeVCLInto(sei, idr, nil)

	// SEI should come before IDR.
	seiIdx := bytes.Index(result, sei)
	idrIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x65})
	if seiIdx < 0 || idrIdx < 0 {
		t.Fatal("could not find SEI or IDR in result")
	}
	if seiIdx >= idrIdx {
		t.Errorf("SEI at %d should be before IDR at %d", seiIdx, idrIdx)
	}
}

func TestInsertSEIBeforeVCLInto_WithSPSPPS(t *testing.T) {
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'X', 'Y'}}})

	// SPS(type 7) + PPS(type 8) + IDR(type 5).
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x01, 0x02, // SPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0x03, 0x04, // PPS
		0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, // IDR
	}

	result := InsertSEIBeforeVCLInto(sei, annexB, nil)

	// SPS and PPS should remain before SEI.
	spsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x67})
	ppsIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x68})
	seiIdx := bytes.Index(result, sei)
	idrIdx := bytes.LastIndex(result, []byte{0x00, 0x00, 0x00, 0x01, 0x65})

	if spsIdx >= ppsIdx || ppsIdx >= seiIdx || seiIdx >= idrIdx {
		t.Errorf("order should be SPS(%d) < PPS(%d) < SEI(%d) < IDR(%d)",
			spsIdx, ppsIdx, seiIdx, idrIdx)
	}
}

func TestInsertSEIBeforeVCLInto_NonIDRSlice(t *testing.T) {
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'A', 'B'}}})
	// Non-IDR slice (type 1).
	slice := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0xCC, 0xDD}

	result := InsertSEIBeforeVCLInto(sei, slice, nil)

	seiIdx := bytes.Index(result, sei)
	sliceIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x41})
	if seiIdx >= sliceIdx {
		t.Errorf("SEI at %d should be before slice at %d", seiIdx, sliceIdx)
	}
}

func TestInsertSEIBeforeVCLInto_BufferReuse(t *testing.T) {
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'A', 'B'}}})
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA}

	// Allocate a large buffer to be reused.
	dst := make([]byte, 0, 1024)

	result1 := InsertSEIBeforeVCLInto(sei, idr, dst)
	result2 := InsertSEIBeforeVCLInto(sei, idr, dst)

	if !bytes.Equal(result1, result2) {
		t.Error("results should be identical")
	}
}

func TestInsertSEIBeforeVCLInto_DstAliasesAnnexB(t *testing.T) {
	// Regression test: when dst shares the backing array with annexB,
	// the copy operations in InsertSEIBeforeVCLInto can overwrite annexB
	// before it's fully read, producing corrupted output.
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'C', 'D'}}})

	// Build annexB: SPS + PPS + IDR (typical keyframe layout).
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xC0, 0x1E, // SPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, 0x80, // PPS
		0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, 0xCC, // IDR
	}

	// Compute expected result with a guaranteed-safe separate buffer.
	expected := InsertSEIBeforeVCLInto(sei, annexB, nil)

	// Now create a dst that shares the same backing array as annexB.
	// This simulates the "pass dst[:0] to reuse a buffer" pattern when
	// the previous result was used as the next annexB input.
	// We need a buffer large enough to hold the result that shares
	// backing memory with annexB.
	shared := make([]byte, len(annexB)+len(sei)+64)
	copy(shared, annexB)
	aliasedAnnexB := shared[:len(annexB)]
	aliasedDst := shared[:0]

	result := InsertSEIBeforeVCLInto(sei, aliasedAnnexB, aliasedDst)

	if !bytes.Equal(result, expected) {
		t.Errorf("aliased dst produced corrupted output:\n  got:  %X\n  want: %X", result, expected)
	}
}

func TestInsertSEIBeforeVCLInto_NoSEI_DstAliasesAnnexB(t *testing.T) {
	// When sei is nil/empty, the function copies annexB into dst.
	// If dst aliases annexB, this should still produce correct output.
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, 0xCC,
	}

	expected := make([]byte, len(annexB))
	copy(expected, annexB)

	// Create aliased buffers.
	shared := make([]byte, len(annexB)+64)
	copy(shared, annexB)
	aliasedAnnexB := shared[:len(annexB)]
	aliasedDst := shared[:0]

	result := InsertSEIBeforeVCLInto(nil, aliasedAnnexB, aliasedDst)

	if !bytes.Equal(result, expected) {
		t.Errorf("aliased dst with nil SEI produced corrupted output:\n  got:  %X\n  want: %X", result, expected)
	}
}

func TestExtractCCPairsFromSEI_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"too short", []byte{0x04}},
		{"wrong payload type", []byte{0x05, 0x01, 0xB5}},
		{"wrong country code", []byte{0x04, 0x0A, 0xAA, 0x00, 0x31, 'G', 'A', '9', '4', 0x03, 0xC1, 0xFC, 'A', 'B', 0xFF}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pairs := ExtractCCPairsFromSEI(tt.input); pairs != nil {
				t.Errorf("expected nil, got %v", pairs)
			}
		})
	}
}

func TestFindFirstVCL_TruncatedStartCode(t *testing.T) {
	// Degenerate inputs where start code sits at the end of the buffer
	// with no NALU type byte following. Must return -1 without panicking.
	tests := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"4-byte start code at end", []byte{0x00, 0x00, 0x00, 0x01}},
		{"3-byte start code at end", []byte{0x00, 0x00, 0x01}},
		{"padding then 4-byte start code at end", []byte{0xFF, 0x00, 0x00, 0x00, 0x01}},
		{"padding then 3-byte start code at end", []byte{0xFF, 0x00, 0x00, 0x01}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findFirstVCL(tt.data)
			if got != -1 {
				t.Errorf("findFirstVCL() = %d, want -1", got)
			}
		})
	}
}

func TestInsertSEIBeforeVCLInto_TruncatedStartCode(t *testing.T) {
	// InsertSEIBeforeVCLInto must not panic when annexB contains a start code
	// at the very end with no NALU type byte.
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'A', 'B'}}})
	truncated := []byte{0x00, 0x00, 0x00, 0x01}

	// Must not panic.
	result := InsertSEIBeforeVCLInto(sei, truncated, nil)
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestFindFirstVCL(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{
			"IDR only",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
			0,
		},
		{
			"SPS then IDR",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x01, 0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
			6,
		},
		{
			"no VCL",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x01, 0x02},
			-1,
		},
		{
			"non-IDR slice",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x41, 0xCC},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findFirstVCL(tt.data); got != tt.want {
				t.Errorf("findFirstVCL() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- HEVC tests ---

func TestBuildSEINALU_HEVC_Structure(t *testing.T) {
	pairs := []CCPair{
		{Data: [2]byte{'H', 'i'}},
	}

	nalu := BuildSEINALU(pairs, "h265")
	if nalu == nil {
		t.Fatal("BuildSEINALU(h265) returned nil")
	}

	// Should start with Annex B start code.
	if !bytes.HasPrefix(nalu, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Error("missing Annex B start code")
	}

	// HEVC PREFIX_SEI: 2-byte header 0x4E 0x01.
	if nalu[4] != 0x4E {
		t.Errorf("HEVC NALU header byte 0 = %02X, want 0x4E", nalu[4])
	}
	if nalu[5] != 0x01 {
		t.Errorf("HEVC NALU header byte 1 = %02X, want 0x01", nalu[5])
	}

	// SEI payload type should be 4 (T.35) at byte 6 (after 2-byte header).
	if nalu[6] != seiPayloadTypeT35 {
		t.Errorf("payload type = %02X, want %02X", nalu[6], seiPayloadTypeT35)
	}

	// Should end with RBSP trailing bits 0x80.
	if nalu[len(nalu)-1] != 0x80 {
		t.Errorf("last byte = %02X, want 0x80 (RBSP trailing)", nalu[len(nalu)-1])
	}
}

func TestBuildSEINALU_HEVC_HeaderDiffersFromH264(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}

	h264NALU := BuildSEINALU(pairs)
	hevcNALU := BuildSEINALU(pairs, "h265")

	// H.264: start_code(4) + header(1) = 5 bytes before payload type
	// HEVC:  start_code(4) + header(2) = 6 bytes before payload type
	if len(hevcNALU) != len(h264NALU)+1 {
		t.Errorf("HEVC NALU len=%d, H.264 NALU len=%d, expected HEVC to be 1 byte longer",
			len(hevcNALU), len(h264NALU))
	}

	// H.264 header: 0x06
	if h264NALU[4] != 0x06 {
		t.Errorf("H.264 header = %02X, want 0x06", h264NALU[4])
	}

	// HEVC header: 0x4E 0x01
	if hevcNALU[4] != 0x4E || hevcNALU[5] != 0x01 {
		t.Errorf("HEVC header = %02X %02X, want 0x4E 0x01", hevcNALU[4], hevcNALU[5])
	}
}

func TestBuildSEINALU_HEVC_RoundTrip(t *testing.T) {
	pairs := []CCPair{
		{Data: [2]byte{'H', 'e'}},
		{Data: [2]byte{'l', 'l'}},
		{Data: [2]byte{'o', 0x80}},
	}

	nalu := BuildSEINALU(pairs, "h265")
	if nalu == nil {
		t.Fatal("BuildSEINALU(h265) returned nil")
	}

	// Remove EPB for parsing — strip start code and 2-byte HEVC NALU header.
	body := removeEPB(nalu[6:])

	// Parse back — the SEI payload format is identical for H.264 and HEVC.
	extracted := ExtractCCPairsFromSEI(body)
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

func TestFindFirstVCLHEVC(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{
			// HEVC TRAIL_R (type 1): first byte = (1 << 1) | 0 = 0x02
			"TRAIL_R only",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x02, 0x01, 0xAA},
			0,
		},
		{
			// HEVC IDR_W_RADL (type 19): first byte = (19 << 1) | 0 = 0x26
			"IDR_W_RADL only",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA},
			0,
		},
		{
			// HEVC BLA_W_LP (type 16): first byte = (16 << 1) | 0 = 0x20
			"BLA_W_LP only",
			[]byte{0x00, 0x00, 0x00, 0x01, 0x20, 0x01, 0xAA},
			0,
		},
		{
			// HEVC VPS (type 32): first byte = (32 << 1) | 0 = 0x40
			// HEVC SPS (type 33): first byte = (33 << 1) | 0 = 0x42
			// HEVC IDR_W_RADL (type 19): first byte = 0x26
			"VPS then SPS then IDR",
			[]byte{
				0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0xFF, // VPS (type 32, non-VCL)
				0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0xFF, // SPS (type 33, non-VCL)
				0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, // IDR (type 19, VCL)
			},
			14,
		},
		{
			// Only non-VCL NALUs: VPS (32) + SPS (33) + PPS (34)
			"no VCL",
			[]byte{
				0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0xFF, // VPS (type 32)
				0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0xFF, // SPS (type 33)
				0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0xFF, // PPS (type 34)
			},
			-1,
		},
		{
			// PREFIX_SEI (type 39): first byte = (39 << 1) | 0 = 0x4E — non-VCL
			// Followed by TRAIL_N (type 0): first byte = (0 << 1) | 0 = 0x00
			"SEI then TRAIL_N",
			[]byte{
				0x00, 0x00, 0x00, 0x01, 0x4E, 0x01, 0xFF, // PREFIX_SEI (type 39)
				0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0xAA, // TRAIL_N (type 0, VCL)
			},
			7,
		},
		{
			"nil",
			nil,
			-1,
		},
		{
			"empty",
			[]byte{},
			-1,
		},
		{
			"truncated start code",
			[]byte{0x00, 0x00, 0x00, 0x01},
			-1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findFirstVCLHEVC(tt.data); got != tt.want {
				t.Errorf("findFirstVCLHEVC() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInsertSEIBeforeVCLInto_HEVC_IDR(t *testing.T) {
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'A', 'B'}}}, "h265")

	// HEVC IDR_W_RADL (type 19): first byte = 0x26, second byte = 0x01
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, 0xBB}

	result := InsertSEIBeforeVCLInto(sei, idr, nil, "h265")

	// SEI should come before IDR.
	seiIdx := bytes.Index(result, sei)
	idrIdx := bytes.Index(result, []byte{0x00, 0x00, 0x00, 0x01, 0x26})
	if seiIdx < 0 || idrIdx < 0 {
		t.Fatal("could not find SEI or IDR in result")
	}
	if seiIdx >= idrIdx {
		t.Errorf("SEI at %d should be before IDR at %d", seiIdx, idrIdx)
	}
}

func TestInsertSEIBeforeVCLInto_HEVC_WithVPSSPSPPS(t *testing.T) {
	sei := BuildSEINALU([]CCPair{{Data: [2]byte{'X', 'Y'}}}, "h265")

	// VPS(32) + SPS(33) + PPS(34) + IDR(19)
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0x01, 0x02, // VPS (type 32)
		0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0x03, 0x04, // SPS (type 33)
		0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0x05, 0x06, // PPS (type 34)
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, 0xBB, // IDR (type 19)
	}

	result := InsertSEIBeforeVCLInto(sei, annexB, nil, "h265")

	// VPS, SPS, PPS should remain before SEI.
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

func TestBuildSEINALU_DefaultCodecIsH264(t *testing.T) {
	pairs := []CCPair{{Data: [2]byte{'A', 'B'}}}

	// No codec parameter — should default to H.264.
	nalu := BuildSEINALU(pairs)
	if nalu[4] != 0x06 {
		t.Errorf("default codec header = %02X, want 0x06 (H.264 SEI)", nalu[4])
	}

	// Explicit "h264" — should be identical.
	naluExplicit := BuildSEINALU(pairs, "h264")
	if !bytes.Equal(nalu, naluExplicit) {
		t.Error("default and explicit h264 should produce identical output")
	}
}
