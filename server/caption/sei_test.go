package caption

import (
	"bytes"
	"testing"
)

func TestBuildSEINALU_NilPairs(t *testing.T) {
	if got := BuildSEINALU(nil); got != nil {
		t.Errorf("BuildSEINALU(nil) = %v, want nil", got)
	}
}

func TestBuildSEINALU_EmptyPairs(t *testing.T) {
	if got := BuildSEINALU([]CCPair{}); got != nil {
		t.Errorf("BuildSEINALU([]) = %v, want nil", got)
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
