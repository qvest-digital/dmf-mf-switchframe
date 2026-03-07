package mxl

import (
	"testing"
)

func TestParseFlowDef_Video(t *testing.T) {
	json := `{
		"id": "5fbec3b1-1b0f-417d-9059-8b94a47197ed",
		"format": "urn:x-nmos:format:video",
		"media_type": "video/v210",
		"label": "Camera 1",
		"grain_rate": {"numerator": 30000, "denominator": 1001},
		"frame_width": 1920,
		"frame_height": 1080,
		"colorspace": "BT709"
	}`

	info, err := parseFlowDef([]byte(json), "5fbec3b1-1b0f-417d-9059-8b94a47197ed")
	if err != nil {
		t.Fatalf("parseFlowDef error: %v", err)
	}

	if info.ID != "5fbec3b1-1b0f-417d-9059-8b94a47197ed" {
		t.Fatalf("ID = %q, want UUID", info.ID)
	}
	if info.Name != "Camera 1" {
		t.Fatalf("Name = %q, want 'Camera 1'", info.Name)
	}
	if info.Format != DataFormatVideo {
		t.Fatalf("Format = %d, want DataFormatVideo", info.Format)
	}
	if info.MediaType != "video/v210" {
		t.Fatalf("MediaType = %q, want 'video/v210'", info.MediaType)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("dimensions = %dx%d, want 1920x1080", info.Width, info.Height)
	}
	if info.GrainRate.Numerator != 30000 || info.GrainRate.Denominator != 1001 {
		t.Fatalf("GrainRate = %d/%d, want 30000/1001",
			info.GrainRate.Numerator, info.GrainRate.Denominator)
	}
}

func TestParseFlowDef_Audio(t *testing.T) {
	json := `{
		"id": "b3bb5be7-9fe9-4324-a5bb-4c70e1084449",
		"format": "urn:x-nmos:format:audio",
		"media_type": "audio/float32",
		"label": "Audio Mix",
		"sample_rate": {"numerator": 48000},
		"channel_count": 2,
		"bit_depth": 32
	}`

	info, err := parseFlowDef([]byte(json), "b3bb5be7-9fe9-4324-a5bb-4c70e1084449")
	if err != nil {
		t.Fatalf("parseFlowDef error: %v", err)
	}

	if info.Format != DataFormatAudio {
		t.Fatalf("Format = %d, want DataFormatAudio", info.Format)
	}
	if info.MediaType != "audio/float32" {
		t.Fatalf("MediaType = %q, want 'audio/float32'", info.MediaType)
	}
	if info.SampleRate != 48000 {
		t.Fatalf("SampleRate = %d, want 48000", info.SampleRate)
	}
	if info.Channels != 2 {
		t.Fatalf("Channels = %d, want 2", info.Channels)
	}
	if info.GrainRate.Numerator != 48000 {
		t.Fatalf("GrainRate.Numerator = %d, want 48000", info.GrainRate.Numerator)
	}
}

func TestParseFlowDef_Data(t *testing.T) {
	json := `{
		"id": "test-data-flow",
		"format": "urn:x-nmos:format:data",
		"media_type": "video/smpte291",
		"label": "Ancillary Data"
	}`

	info, err := parseFlowDef([]byte(json), "test-data-flow")
	if err != nil {
		t.Fatalf("parseFlowDef error: %v", err)
	}

	if info.Format != DataFormatData {
		t.Fatalf("Format = %d, want DataFormatData", info.Format)
	}
}

func TestParseFlowDef_UnknownFormat(t *testing.T) {
	json := `{
		"id": "test",
		"format": "urn:x-nmos:format:mux",
		"label": "Unknown"
	}`

	info, err := parseFlowDef([]byte(json), "test")
	if err != nil {
		t.Fatalf("parseFlowDef error: %v", err)
	}

	if info.Format != DataFormatUnspecified {
		t.Fatalf("Format = %d, want DataFormatUnspecified", info.Format)
	}
}

func TestParseFlowDef_InvalidJSON(t *testing.T) {
	_, err := parseFlowDef([]byte("not json"), "test")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseFlowDef_AudioWithDenominator(t *testing.T) {
	json := `{
		"id": "test",
		"format": "urn:x-nmos:format:audio",
		"media_type": "audio/float32",
		"label": "Audio",
		"sample_rate": {"numerator": 48000, "denominator": 1},
		"channel_count": 8
	}`

	info, err := parseFlowDef([]byte(json), "test")
	if err != nil {
		t.Fatalf("parseFlowDef error: %v", err)
	}

	if info.Channels != 8 {
		t.Fatalf("Channels = %d, want 8", info.Channels)
	}
	if info.GrainRate.Denominator != 1 {
		t.Fatalf("GrainRate.Denominator = %d, want 1", info.GrainRate.Denominator)
	}
}

func TestDiscoverStub_ReturnsError2(t *testing.T) {
	// In non-mxl builds, Discover should return ErrMXLNotAvailable.
	_, err := Discover("/dev/shm/mxl")
	if err != ErrMXLNotAvailable {
		t.Fatalf("expected ErrMXLNotAvailable, got: %v", err)
	}
}
