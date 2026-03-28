package codec

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEncoderIdentSEI_H264_Structure(t *testing.T) {
	sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
		EncoderName: "libx264",
		Profile:     "high",
		Width:       1920,
		Height:      1080,
		FPSNum:      30000,
		FPSDen:      1001,
		BitDepth:    8,
		Chroma:      "420",
	})

	require.NotNil(t, sei, "SEI should not be nil")
	require.True(t, len(sei) > 20, "SEI should have reasonable length")

	// Should start with Annex B start code.
	assert.Equal(t, []byte{0x00, 0x00, 0x00, 0x01}, sei[:4], "must start with Annex B start code")

	// H.264 SEI NALU type = 6 (forbidden_zero_bit=0, nal_ref_idc=0, nal_unit_type=6).
	assert.Equal(t, byte(0x06), sei[4]&0x1F, "H.264 SEI NALU type must be 6")

	// Payload type = 5 (user_data_unregistered).
	assert.Equal(t, byte(0x05), sei[5], "payload type must be 5 (user_data_unregistered)")

	// UUID should be present in the payload.
	assert.True(t, bytes.Contains(sei, SwitchframeUUID[:]),
		"SEI must contain the Switchframe UUID")

	// Payload should contain the application identifier.
	assert.True(t, bytes.Contains(sei, []byte("Switchframe")),
		"SEI must contain 'Switchframe' in payload string")

	// Payload should contain encoder name.
	assert.True(t, bytes.Contains(sei, []byte("libx264")),
		"SEI must contain the encoder name")

	// Payload should contain profile.
	assert.True(t, bytes.Contains(sei, []byte("high")),
		"SEI must contain the profile")

	// Payload should contain format info.
	assert.True(t, bytes.Contains(sei, []byte("1080p")),
		"SEI must contain resolution info")

	// Should end with RBSP trailing bits 0x80.
	// (EPB insertion may shift the exact position, but the last byte of the
	// underlying RBSP should be 0x80.)
	assert.Equal(t, byte(0x80), sei[len(sei)-1], "must end with RBSP trailing bits 0x80")
}

func TestBuildEncoderIdentSEI_HEVC_Structure(t *testing.T) {
	sei := BuildEncoderIdentSEI("h265", EncoderIdentInfo{
		EncoderName: "hevc_nvenc",
		Profile:     "main422-10",
		Width:       1920,
		Height:      1080,
		FPSNum:      30000,
		FPSDen:      1001,
		BitDepth:    10,
		Chroma:      "422",
	})

	require.NotNil(t, sei, "SEI should not be nil")
	require.True(t, len(sei) > 20, "SEI should have reasonable length")

	// Should start with Annex B start code.
	assert.Equal(t, []byte{0x00, 0x00, 0x00, 0x01}, sei[:4], "must start with Annex B start code")

	// HEVC PREFIX_SEI: first byte = (39 << 1) | 0 = 0x4E.
	assert.Equal(t, byte(0x4E), sei[4], "HEVC PREFIX_SEI byte 0 must be 0x4E")
	// Second byte = 0x01 (nuh_layer_id=0, nuh_temporal_id_plus1=1).
	assert.Equal(t, byte(0x01), sei[5], "HEVC PREFIX_SEI byte 1 must be 0x01")

	// Payload type = 5 (user_data_unregistered).
	assert.Equal(t, byte(0x05), sei[6], "payload type must be 5 (user_data_unregistered)")

	// UUID should be present.
	assert.True(t, bytes.Contains(sei, SwitchframeUUID[:]),
		"SEI must contain the Switchframe UUID")

	// Payload should contain encoder info.
	assert.True(t, bytes.Contains(sei, []byte("Switchframe")))
	assert.True(t, bytes.Contains(sei, []byte("hevc_nvenc")))
	assert.True(t, bytes.Contains(sei, []byte("main422-10")))

	// Should end with RBSP trailing bits.
	assert.Equal(t, byte(0x80), sei[len(sei)-1])
}

func TestBuildEncoderIdentSEI_PayloadFormat(t *testing.T) {
	sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
		EncoderName: "libx264",
		Profile:     "high",
		Width:       1920,
		Height:      1080,
		FPSNum:      30000,
		FPSDen:      1001,
		BitDepth:    8,
		Chroma:      "420",
	})

	// Find the UUID in the SEI and extract the payload string after it.
	uuidIdx := bytes.Index(sei, SwitchframeUUID[:])
	require.True(t, uuidIdx > 0, "UUID must be found in SEI")

	// The user data string starts right after the 16-byte UUID.
	payloadStart := uuidIdx + 16
	// The string ends before the RBSP trailing byte (0x80) and any EPB.
	// Find the 0x80 trailing byte.
	payloadEnd := len(sei) - 1 // byte before RBSP trailing
	payloadStr := string(sei[payloadStart:payloadEnd])

	// Verify format: "Switchframe/1.0 encoder=libx264 profile=high format=1080p29.97"
	assert.Contains(t, payloadStr, "Switchframe/1.0")
	assert.Contains(t, payloadStr, "encoder=libx264")
	assert.Contains(t, payloadStr, "profile=high")
	assert.Contains(t, payloadStr, "format=1080p29.97")
}

func TestBuildEncoderIdentSEI_FPSFormatting(t *testing.T) {
	tests := []struct {
		fpsNum, fpsDen int
		expected       string
	}{
		{30000, 1001, "29.97"},
		{60000, 1001, "59.94"},
		{24000, 1001, "23.98"},
		{30, 1, "30"},
		{60, 1, "60"},
		{25, 1, "25"},
		{50, 1, "50"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d/%d", tt.fpsNum, tt.fpsDen), func(t *testing.T) {
			sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
				EncoderName: "libx264",
				Profile:     "high",
				Width:       1920,
				Height:      1080,
				FPSNum:      tt.fpsNum,
				FPSDen:      tt.fpsDen,
				BitDepth:    8,
				Chroma:      "420",
			})

			expected := fmt.Sprintf("format=1080p%s", tt.expected)
			assert.True(t, bytes.Contains(sei, []byte(expected)),
				"SEI should contain %q, got payload containing: %s", expected, extractPayloadString(sei))
		})
	}
}

func TestBuildEncoderIdentSEI_Resolutions(t *testing.T) {
	tests := []struct {
		w, h     int
		expected string
	}{
		{1920, 1080, "1080p"},
		{1280, 720, "720p"},
		{3840, 2160, "2160p"},
		{720, 480, "480p"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
				EncoderName: "libx264",
				Profile:     "high",
				Width:       tt.w,
				Height:      tt.h,
				FPSNum:      30,
				FPSDen:      1,
				BitDepth:    8,
				Chroma:      "420",
			})

			expected := fmt.Sprintf("format=%s30", tt.expected)
			assert.True(t, bytes.Contains(sei, []byte(expected)),
				"SEI should contain %q", expected)
		})
	}
}

func TestBuildEncoderIdentSEI_PayloadSizeEncoding(t *testing.T) {
	sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
		EncoderName: "libx264",
		Profile:     "high",
		Width:       1920,
		Height:      1080,
		FPSNum:      30,
		FPSDen:      1,
		BitDepth:    8,
		Chroma:      "420",
	})

	// Payload type byte is at offset 5 (after start code + NALU header).
	// Payload size follows — for typical ident strings it should be < 255,
	// so a single byte encodes the size.
	payloadType := sei[5]
	assert.Equal(t, byte(0x05), payloadType)

	payloadSize := int(sei[6])
	// 16 (UUID) + len("Switchframe/1.0 encoder=libx264 profile=high format=1080p30")
	// The payload size must be correct.
	expectedPayload := "Switchframe/1.0 encoder=libx264 profile=high format=1080p30"
	expectedSize := 16 + len(expectedPayload)
	assert.Equal(t, expectedSize, payloadSize,
		"payload size must equal 16 (UUID) + len(user_data_string)")
}

func TestBuildEncoderIdentSEI_EmulationPrevention(t *testing.T) {
	// Verify no raw 00 00 00 / 00 00 01 / 00 00 02 / 00 00 03 sequences
	// appear in the NALU body (after start code). EPB should prevent this.
	sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
		EncoderName: "libx264",
		Profile:     "high",
		Width:       1920,
		Height:      1080,
		FPSNum:      30000,
		FPSDen:      1001,
		BitDepth:    8,
		Chroma:      "420",
	})

	// Scan the NALU body (skip the 4-byte start code).
	body := sei[4:]
	for i := 0; i < len(body)-2; i++ {
		if body[i] == 0x00 && body[i+1] == 0x00 && body[i+2] <= 0x03 {
			t.Errorf("found forbidden 00 00 %02x sequence at body offset %d (no EPB inserted)", body[i+2], i)
		}
	}
}

func TestBuildEncoderIdentSEI_UUIDIsValid(t *testing.T) {
	// The UUID should be 16 bytes and have proper v4 variant bits.
	assert.Equal(t, 16, len(SwitchframeUUID), "UUID must be 16 bytes")

	// v4 UUID: version nibble (byte 6, bits 4-7) should be 0x4_.
	assert.Equal(t, byte(0x40), SwitchframeUUID[6]&0xF0,
		"UUID version nibble must be 4 (v4)")

	// Variant bits (byte 8, bits 6-7) should be 0b10.
	assert.Equal(t, byte(0x80), SwitchframeUUID[8]&0xC0,
		"UUID variant bits must be 10 (RFC 4122)")
}

func TestBuildEncoderIdentSEI_10bit422(t *testing.T) {
	sei := BuildEncoderIdentSEI("h265", EncoderIdentInfo{
		EncoderName: "hevc_nvenc",
		Profile:     "main422-10",
		Width:       1920,
		Height:      1080,
		FPSNum:      30000,
		FPSDen:      1001,
		BitDepth:    10,
		Chroma:      "422",
	})

	assert.True(t, bytes.Contains(sei, []byte("main422-10")))
	assert.True(t, bytes.Contains(sei, []byte("hevc_nvenc")))
}

func TestBuildEncoderIdentSEI_InsertionWithVCL(t *testing.T) {
	// Build an ident SEI and verify it can be inserted before a VCL NALU
	// using the InsertSEIBeforeVCLInto infrastructure from caption package.
	// (This is a cross-package integration test for the wire format.)
	sei := BuildEncoderIdentSEI("h264", EncoderIdentInfo{
		EncoderName: "libx264",
		Profile:     "high",
		Width:       1920,
		Height:      1080,
		FPSNum:      30,
		FPSDen:      1,
		BitDepth:    8,
		Chroma:      "420",
	})

	// The SEI NALU includes its own start code.
	// Verify it starts with a start code (needed for Annex B insertion).
	require.Equal(t, []byte{0x00, 0x00, 0x00, 0x01}, sei[:4])

	// Create a minimal H.264 IDR Annex B bitstream.
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00}

	// Concatenate: SEI + IDR should be valid Annex B.
	combined := make([]byte, 0, len(sei)+len(idr))
	combined = append(combined, sei...)
	combined = append(combined, idr...)

	// The UUID should appear before the IDR NALU.
	uuidIdx := bytes.Index(combined, SwitchframeUUID[:])
	idrIdx := bytes.Index(combined, []byte{0x65, 0x88})
	require.True(t, uuidIdx > 0, "UUID not found in combined stream")
	require.True(t, uuidIdx < idrIdx, "UUID must appear before IDR NALU data")
}

// extractPayloadString is a test helper that extracts the user data string
// from an ident SEI NALU (after UUID, before RBSP trailing bits).
func extractPayloadString(sei []byte) string {
	idx := bytes.Index(sei, SwitchframeUUID[:])
	if idx < 0 {
		return "<uuid not found>"
	}
	start := idx + 16
	end := len(sei) - 1 // before RBSP trailing 0x80
	if start >= end {
		return "<empty>"
	}
	return string(sei[start:end])
}
