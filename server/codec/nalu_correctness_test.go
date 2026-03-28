//go:build correctness

package codec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestAVC1AnnexBRoundTrip converts known NALUs from AVC1 -> Annex B -> AVC1
// and verifies byte-exact match.
func TestAVC1AnnexBRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		nalus [][]byte // raw NALU bodies (no prefix)
	}{
		{
			name:  "single_small_nalu",
			nalus: [][]byte{{0x65, 0x88, 0x84, 0x00, 0xFF}},
		},
		{
			name:  "sps_pps_idr",
			nalus: [][]byte{{0x67, 0x64, 0x00, 0x28, 0xAC, 0xD9}, {0x68, 0xEE, 0x3C, 0x80}, {0x65, 0x88, 0x84, 0x00, 0x0A, 0xFF, 0xFE}},
		},
		{
			name:  "single_byte_nalu",
			nalus: [][]byte{{0x06}},
		},
		{
			name: "large_nalu",
			nalus: func() [][]byte {
				nalu := make([]byte, 4096)
				for i := range nalu {
					nalu[i] = byte(i & 0xFF)
				}
				return [][]byte{nalu}
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build AVC1 format: [4-byte big-endian length][NALU body]...
			var avc1 []byte
			for _, nalu := range tc.nalus {
				lenBuf := make([]byte, 4)
				binary.BigEndian.PutUint32(lenBuf, uint32(len(nalu)))
				avc1 = append(avc1, lenBuf...)
				avc1 = append(avc1, nalu...)
			}

			// AVC1 -> Annex B
			annexB := AVC1ToAnnexB(avc1)
			if annexB == nil {
				t.Fatal("AVC1ToAnnexB returned nil")
			}

			// Verify Annex B has 4-byte start codes
			pos := 0
			for i := range tc.nalus {
				if pos+4 > len(annexB) {
					t.Fatalf("NALU %d: Annex B data too short at offset %d", i, pos)
				}
				if !bytes.Equal(annexB[pos:pos+4], annexBStartCode) {
					t.Fatalf("NALU %d: missing start code at offset %d, got %X", i, pos, annexB[pos:pos+4])
				}
				pos += 4 + len(tc.nalus[i])
			}
			if pos != len(annexB) {
				t.Fatalf("Annex B length mismatch: expected %d, got %d", pos, len(annexB))
			}

			// Annex B -> AVC1
			recovered := AnnexBToAVC1(annexB)
			if recovered == nil {
				t.Fatal("AnnexBToAVC1 returned nil")
			}

			if !bytes.Equal(avc1, recovered) {
				t.Fatalf("round-trip mismatch:\n  original: %X\n  recovered: %X", avc1, recovered)
			}
		})
	}
}

// TestAVC1AnnexBRoundTripWithInto verifies that the *Into variants produce
// identical results to the allocating variants.
func TestAVC1AnnexBRoundTripWithInto(t *testing.T) {
	nalus := [][]byte{
		{0x67, 0x64, 0x00, 0x28}, // SPS
		{0x68, 0xEE, 0x3C, 0x80}, // PPS
		{0x65, 0x88, 0x84},       // IDR
	}

	var avc1 []byte
	for _, nalu := range nalus {
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, uint32(len(nalu)))
		avc1 = append(avc1, lenBuf...)
		avc1 = append(avc1, nalu...)
	}

	// Standard conversion
	annexB := AVC1ToAnnexB(avc1)

	// Into variant with pre-allocated buffer
	dst := make([]byte, 0, len(avc1)*2)
	annexBInto := AVC1ToAnnexBInto(avc1, dst)

	if !bytes.Equal(annexB, annexBInto) {
		t.Fatalf("AVC1ToAnnexBInto result differs from AVC1ToAnnexB:\n  standard: %X\n  into: %X", annexB, annexBInto)
	}

	// AnnexB -> AVC1 Into variant
	avc1Recovered := AnnexBToAVC1(annexB)
	dst2 := make([]byte, 0, len(annexB)*2)
	avc1RecoveredInto := AnnexBToAVC1Into(annexB, dst2)

	if !bytes.Equal(avc1Recovered, avc1RecoveredInto) {
		t.Fatalf("AnnexBToAVC1Into result differs from AnnexBToAVC1:\n  standard: %X\n  into: %X", avc1Recovered, avc1RecoveredInto)
	}
}

// TestAnnexBThreeByteStartCode verifies that AnnexBToAVC1 handles 3-byte
// start codes (0x000001) as well as 4-byte (0x00000001).
func TestAnnexBThreeByteStartCode(t *testing.T) {
	naluBody := []byte{0x65, 0x88, 0x84, 0x00, 0xFF}

	// Build Annex B with 3-byte start code
	var annexB3 []byte
	annexB3 = append(annexB3, 0x00, 0x00, 0x01)
	annexB3 = append(annexB3, naluBody...)

	avc1 := AnnexBToAVC1(annexB3)
	if avc1 == nil {
		t.Fatal("AnnexBToAVC1 returned nil for 3-byte start code")
	}

	// Verify the length prefix is correct
	gotLen := binary.BigEndian.Uint32(avc1[:4])
	if gotLen != uint32(len(naluBody)) {
		t.Fatalf("NALU length mismatch: expected %d, got %d", len(naluBody), gotLen)
	}

	// Verify the NALU body matches
	if !bytes.Equal(avc1[4:], naluBody) {
		t.Fatalf("NALU body mismatch: expected %X, got %X", naluBody, avc1[4:])
	}
}

// TestADTSHeaderConstruction verifies ADTS header fields for known parameters.
func TestADTSHeaderConstruction(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		frameLen   int // raw AAC payload size (excluding header)
	}{
		{"48kHz_stereo_256", 48000, 2, 256},
		{"44100_stereo_512", 44100, 2, 512},
		{"48kHz_mono_128", 48000, 1, 128},
		{"96kHz_stereo_1024", 96000, 2, 1024},
		{"8kHz_mono_64", 8000, 1, 64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := BuildADTS(tc.sampleRate, tc.channels, tc.frameLen)

			if len(header) != 7 {
				t.Fatalf("expected 7-byte header, got %d bytes", len(header))
			}

			// Verify sync word (0xFFF)
			if header[0] != 0xFF {
				t.Fatalf("byte 0: expected 0xFF, got 0x%02X", header[0])
			}
			if header[1]&0xF0 != 0xF0 {
				t.Fatalf("byte 1 high nibble: expected 0xF, got 0x%X", header[1]>>4)
			}

			// Verify ID=0 (MPEG-4)
			if header[1]&0x08 != 0 {
				t.Fatalf("ID bit should be 0 (MPEG-4), got 1")
			}

			// Verify protection_absent=1 (no CRC)
			if header[1]&0x01 != 1 {
				t.Fatalf("protection_absent should be 1, got 0")
			}

			// Verify profile = 1 (AAC-LC, objectType-1)
			profile := (header[2] >> 6) & 0x03
			if profile != 1 {
				t.Fatalf("profile: expected 1 (AAC-LC), got %d", profile)
			}

			// Verify sample rate index
			srIdx := (header[2] >> 2) & 0x0F
			expectedSrIdx := byte(sampleRateIndex(tc.sampleRate))
			if srIdx != expectedSrIdx {
				t.Fatalf("sample rate index: expected %d, got %d", expectedSrIdx, srIdx)
			}

			// Verify channel configuration
			chHigh := header[2] & 0x01
			chLow := (header[3] >> 6) & 0x03
			gotChannels := int(chHigh<<2) | int(chLow)
			if gotChannels != tc.channels {
				t.Fatalf("channels: expected %d, got %d", tc.channels, gotChannels)
			}

			// Verify frame length (13-bit field spanning bytes 3-5)
			totalLen := tc.frameLen + 7
			gotFrameLen := ADTSFrameLen(header)
			if gotFrameLen != totalLen {
				t.Fatalf("frame length: expected %d, got %d", totalLen, gotFrameLen)
			}

			// Verify IsADTS recognizes it
			if !IsADTS(header) {
				t.Fatal("IsADTS returned false for constructed header")
			}

			// Verify ADTSHeaderLen returns 7 (no CRC)
			if got := ADTSHeaderLen(header); got != 7 {
				t.Fatalf("ADTSHeaderLen: expected 7, got %d", got)
			}

			// Verify ParseADTSInfo round-trips the parameters
			gotSR, gotCh := ParseADTSInfo(header)
			if gotSR != tc.sampleRate {
				t.Fatalf("ParseADTSInfo sample rate: expected %d, got %d", tc.sampleRate, gotSR)
			}
			if gotCh != tc.channels {
				t.Fatalf("ParseADTSInfo channels: expected %d, got %d", tc.channels, gotCh)
			}
		})
	}
}

// TestEnsureADTSIdempotent verifies that EnsureADTS is idempotent: wrapping
// already-wrapped data returns the same bytes.
func TestEnsureADTSIdempotent(t *testing.T) {
	rawPayload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	wrapped := EnsureADTS(rawPayload, 48000, 2)

	if !IsADTS(wrapped) {
		t.Fatal("wrapped data is not ADTS")
	}

	// Second call should be a no-op
	doubleWrapped := EnsureADTS(wrapped, 48000, 2)
	if !bytes.Equal(wrapped, doubleWrapped) {
		t.Fatalf("EnsureADTS is not idempotent:\n  first: %X\n  second: %X", wrapped, doubleWrapped)
	}
}

// TestSplitADTSFramesRoundTrip builds concatenated ADTS frames and verifies
// they can be split and the payloads match.
func TestSplitADTSFramesRoundTrip(t *testing.T) {
	payloads := [][]byte{
		{0x01, 0x02, 0x03, 0x04, 0x05},
		{0x0A, 0x0B, 0x0C, 0x0D},
		{0xFF, 0xFE, 0xFD},
	}

	var concatenated []byte
	for _, p := range payloads {
		header := BuildADTS(48000, 2, len(p))
		concatenated = append(concatenated, header...)
		concatenated = append(concatenated, p...)
	}

	frames := SplitADTSFrames(concatenated)
	if len(frames) != len(payloads) {
		t.Fatalf("expected %d frames, got %d", len(payloads), len(frames))
	}

	for i, expected := range payloads {
		if !bytes.Equal(frames[i], expected) {
			t.Fatalf("frame %d: expected %X, got %X", i, expected, frames[i])
		}
	}
}
