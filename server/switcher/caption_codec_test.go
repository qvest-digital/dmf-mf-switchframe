package switcher

import (
	"bytes"
	"testing"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
)

// TestCaptionSEI_HEVC_BuildsCorrectNALU verifies that when the pipeline codec
// is HEVC, the caption SEI injection produces an HEVC PREFIX_SEI (type 39,
// header 0x4E 0x01) rather than an H.264 SEI (type 6, header 0x06).
func TestCaptionSEI_HEVC_BuildsCorrectNALU(t *testing.T) {
	// This test validates the codec string derivation that broadcastWithCaptions
	// must use. When pipelineCodec == CodecHEVC, the codec string should be "h265".
	codecStr := codecStringFromPipelineCodec(CodecHEVC)
	if codecStr != "h265" {
		t.Fatalf("codecStringFromPipelineCodec(HEVC) = %q, want %q", codecStr, "h265")
	}

	pairs := []caption.CCPair{{Data: [2]byte{'H', 'i'}}}
	seiNALU := caption.BuildSEINALU(pairs, codecStr)

	// HEVC PREFIX_SEI: start_code(4) + 0x4E 0x01
	if len(seiNALU) < 6 {
		t.Fatalf("SEI NALU too short: %d bytes", len(seiNALU))
	}
	if seiNALU[4] != 0x4E || seiNALU[5] != 0x01 {
		t.Errorf("HEVC SEI header = %02X %02X, want 0x4E 0x01", seiNALU[4], seiNALU[5])
	}
}

// TestCaptionSEI_H264_BuildsCorrectNALU verifies the H.264 path remains correct.
func TestCaptionSEI_H264_BuildsCorrectNALU(t *testing.T) {
	codecStr := codecStringFromPipelineCodec(CodecAVC)
	if codecStr != "h264" {
		t.Fatalf("codecStringFromPipelineCodec(AVC) = %q, want %q", codecStr, "h264")
	}

	pairs := []caption.CCPair{{Data: [2]byte{'H', 'i'}}}
	seiNALU := caption.BuildSEINALU(pairs, codecStr)

	// H.264 SEI: start_code(4) + 0x06
	if len(seiNALU) < 5 {
		t.Fatalf("SEI NALU too short: %d bytes", len(seiNALU))
	}
	if seiNALU[4] != 0x06 {
		t.Errorf("H.264 SEI header = %02X, want 0x06", seiNALU[4])
	}
}

// TestCaptionSEI_HEVC_VCLDetection verifies that InsertSEIBeforeVCLInto with
// codec "h265" uses HEVC VCL detection (types 0-31) rather than H.264 (types 1,5).
func TestCaptionSEI_HEVC_VCLDetection(t *testing.T) {
	pairs := []caption.CCPair{{Data: [2]byte{'T', 'X'}}}
	seiNALU := caption.BuildSEINALU(pairs, "h265")

	// HEVC stream: VPS + SPS + PPS + IDR
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0xFF, // VPS (type 32)
		0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0xFF, // SPS (type 33)
		0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0xFF, // PPS (type 34)
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, // IDR (type 19)
	}

	inserted := caption.InsertSEIBeforeVCLInto(seiNALU, annexB, nil, "h265")

	// SEI should be after PPS but before IDR.
	seiIdx := bytes.Index(inserted, seiNALU)
	idrIdx := bytes.Index(inserted, []byte{0x00, 0x00, 0x00, 0x01, 0x26})
	ppsIdx := bytes.Index(inserted, []byte{0x00, 0x00, 0x00, 0x01, 0x44})

	if seiIdx < 0 {
		t.Fatal("SEI not found in output")
	}
	if seiIdx <= ppsIdx {
		t.Errorf("SEI at %d should be after PPS at %d", seiIdx, ppsIdx)
	}
	if seiIdx >= idrIdx {
		t.Errorf("SEI at %d should be before IDR at %d", seiIdx, idrIdx)
	}
}

// TestCaptionSEI_WireFormatRoundTrip_HEVC verifies that the codec-agnostic
// wire format functions (WireToAnnexBInto / AnnexBToWireInto) work correctly
// for HEVC content, replacing the AVC1-specific functions previously used.
func TestCaptionSEI_WireFormatRoundTrip_HEVC(t *testing.T) {
	// Build HEVC content in Annex B format.
	hevcAnnexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0xFF, // VPS
		0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0xFF, // SPS
		0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0xFF, // PPS
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, // IDR
	}

	// Wire → Annex B → Wire should round-trip.
	wire := codec.AnnexBToWireInto(hevcAnnexB, nil)
	annexB := codec.WireToAnnexBInto(wire, nil)
	wire2 := codec.AnnexBToWireInto(annexB, nil)

	if !bytes.Equal(wire, wire2) {
		t.Error("wire format round-trip failed for HEVC")
	}
}

// codecStringFromPipelineCodec derives the caption codec string from PipelineCodec.
// This is the helper that broadcastWithCaptions should use.
func codecStringFromPipelineCodec(pc PipelineCodec) string {
	if pc == CodecHEVC {
		return "h265"
	}
	return "h264"
}
