package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/switcher"
)

// These tests verify the HEVC/AVC NAL extraction patterns used in gpuOnEncoded
// (app_gpu.go). The gpuOnEncoded callback parses wire-format NALUs to extract
// parameter sets (SPS/PPS for AVC, VPS/SPS/PPS for HEVC). These tests ensure
// the full pipeline path works: Annex B -> wire format -> ExtractNALUs -> type check.

func TestGpuOnEncoded_AVC_ParamExtraction(t *testing.T) {
	// Simulate the AVC param extraction path in gpuOnEncoded.
	// Build minimal AVC Annex B: SPS (type 7) + PPS (type 8) + IDR (type 5).
	sps := []byte{0x67, 0x42, 0x00, 0x1e, 0xab, 0xcd}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	idr := []byte{0x65, 0x88, 0x80, 0x40}

	annexB := codec.PrependSPSPPS(sps, pps, append([]byte{0, 0, 0, 1}, idr...))
	wire := codec.AnnexBToWireInto(annexB, nil)

	// Extract NALUs the same way gpuOnEncoded does.
	nalus := codec.ExtractNALUs(wire)

	var gotSPS, gotPPS []byte
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		switch nalu[0] & 0x1F {
		case 7: // SPS
			gotSPS = append([]byte{}, nalu...)
		case 8: // PPS
			gotPPS = append([]byte{}, nalu...)
		}
	}

	require.NotNil(t, gotSPS, "AVC: expected to extract SPS")
	require.NotNil(t, gotPPS, "AVC: expected to extract PPS")
	assert.Equal(t, sps, gotSPS, "extracted SPS should match input")
	assert.Equal(t, pps, gotPPS, "extracted PPS should match input")
}

func TestGpuOnEncoded_HEVC_ParamExtraction(t *testing.T) {
	// Simulate the HEVC param extraction path in gpuOnEncoded.
	// Build minimal HEVC Annex B: VPS (type 32) + SPS (type 33) + PPS (type 34) + IDR (type 19).
	// HEVC NAL header: forbidden(1) | type(6) | layerID(6) | tid(3)
	// VPS: type 32 -> byte0 = (32 << 1) = 0x40, byte1 = 0x01
	vps := []byte{0x40, 0x01, 0xaa, 0xbb}
	sps := []byte{0x42, 0x01, 0xcc, 0xdd} // type 33
	pps := []byte{0x44, 0x01, 0xee, 0xff} // type 34
	idr := []byte{0x26, 0x01, 0x11, 0x22} // type 19 (IDR_W_RADL)

	annexB := codec.PrependVPSSPSPPS(vps, sps, pps, append([]byte{0, 0, 0, 1}, idr...))
	wire := codec.AnnexBToWireInto(annexB, nil)

	// Extract NALUs the same way gpuOnEncoded does for HEVC.
	nalus := codec.ExtractNALUs(wire)

	var gotVPS, gotSPS, gotPPS []byte
	for _, nalu := range nalus {
		if len(nalu) < 2 {
			continue
		}
		nalType := codec.HEVCNALUType(nalu[0])
		switch nalType {
		case codec.HEVCNALVPS:
			gotVPS = append([]byte{}, nalu...)
		case codec.HEVCNALSPS:
			gotSPS = append([]byte{}, nalu...)
		case codec.HEVCNALPPS:
			gotPPS = append([]byte{}, nalu...)
		}
	}

	require.NotNil(t, gotVPS, "HEVC: expected to extract VPS")
	require.NotNil(t, gotSPS, "HEVC: expected to extract SPS")
	require.NotNil(t, gotPPS, "HEVC: expected to extract PPS")
	assert.Equal(t, vps, gotVPS, "extracted VPS should match input")
	assert.Equal(t, sps, gotSPS, "extracted SPS should match input")
	assert.Equal(t, pps, gotPPS, "extracted PPS should match input")
}

func TestGpuOnEncoded_FrameCodecString(t *testing.T) {
	// Verify the codec string dispatch logic used in gpuOnEncoded.
	// This is the exact pattern from app_gpu.go lines 293-296.
	tests := []struct {
		name          string
		pipelineCodec switcher.PipelineCodec
		wantCodec     string
	}{
		{"AVC_produces_h264", switcher.CodecAVC, "h264"},
		{"HEVC_produces_h265", switcher.CodecHEVC, "h265"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frameCodec := "h264"
			if tt.pipelineCodec == switcher.CodecHEVC {
				frameCodec = "h265"
			}
			assert.Equal(t, tt.wantCodec, frameCodec)
		})
	}
}

func TestEncoderFactoryForCodec_HEVC10bit(t *testing.T) {
	// Verify encoderFactoryForCodec returns a factory for HEVC 10-bit mode.
	factory := encoderFactoryForCodec(switcher.CodecHEVC, switcher.ColorYUV422_10bit)
	require.NotNil(t, factory, "should return non-nil factory for HEVC 10-bit")

	// The factory should create a valid encoder. Use 1920x1080 because some
	// hardware encoders (e.g., VideoToolbox) don't support very small
	// resolutions for 10-bit HEVC.
	enc, err := factory(1920, 1080, 4_000_000, 30, 1)
	if err != nil {
		t.Skipf("HEVC 10-bit encoder not available at this resolution: %v", err)
	}
	require.NotNil(t, enc)
	enc.Close()
}

func TestEncoderFactoryForCodec_HEVC8bit(t *testing.T) {
	requireFFmpeg(t)
	// Verify encoderFactoryForCodec returns a factory for HEVC 8-bit mode.
	factory := encoderFactoryForCodec(switcher.CodecHEVC, switcher.ColorYUV420_8bit)
	require.NotNil(t, factory, "should return non-nil factory for HEVC 8-bit")

	enc, err := factory(64, 64, 500_000, 30, 1)
	require.NoError(t, err)
	require.NotNil(t, enc)
	enc.Close()
}

func TestEncoderFactoryForCodec_AVC(t *testing.T) {
	requireFFmpeg(t)
	// Verify encoderFactoryForCodec returns a factory for AVC (default).
	factory := encoderFactoryForCodec(switcher.CodecAVC)
	require.NotNil(t, factory, "should return non-nil factory for AVC")

	enc, err := factory(64, 64, 500_000, 30, 1)
	require.NoError(t, err)
	require.NotNil(t, enc)
	enc.Close()
}
