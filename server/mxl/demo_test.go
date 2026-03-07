package mxl

import (
	"testing"
)

func TestDemoVideoReader_GeneratesValidV210(t *testing.T) {
	// 360x240 is the resolution used in demo mode.
	reader := NewDemoVideoReader(360, 240, 30, 0)
	defer reader.Close()

	data, info, err := reader.ReadGrain(0, 0)
	if err != nil {
		t.Fatalf("ReadGrain: %v", err)
	}

	if info.Index != 1 {
		t.Fatalf("expected index 1, got %d", info.Index)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty V210 data")
	}

	// Verify the V210 data can be decoded back to YUV420p.
	yuv, err := V210ToYUV420p(data, 360, 240)
	if err != nil {
		t.Fatalf("V210ToYUV420p: %v", err)
	}

	expectedSize := 360*240 + 180*120 + 180*120
	if len(yuv) != expectedSize {
		t.Fatalf("YUV420p size: got %d, want %d", len(yuv), expectedSize)
	}

	t.Logf("Demo frame: %d bytes V210 → %d bytes YUV420p (360x240)", len(data), len(yuv))
}

func TestDemoVideoReader_DifferentColors(t *testing.T) {
	// Verify different colorIdx produces different patterns.
	reader0 := NewDemoVideoReader(12, 2, 30, 0)
	reader1 := NewDemoVideoReader(12, 2, 30, 1)
	defer reader0.Close()
	defer reader1.Close()

	data0, _, _ := reader0.ReadGrain(0, 0)
	data1, _, _ := reader1.ReadGrain(0, 0)

	// Convert to YUV420p to compare.
	yuv0, _ := V210ToYUV420p(data0, 12, 2)
	yuv1, _ := V210ToYUV420p(data1, 12, 2)

	// They should differ (different color tints).
	same := true
	for i := range yuv0 {
		if yuv0[i] != yuv1[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("expected different patterns for different colorIdx")
	}
}

func TestDemoVideoReader_Closes(t *testing.T) {
	reader := NewDemoVideoReader(12, 2, 30, 0)

	// First read should work.
	_, _, err := reader.ReadGrain(0, 0)
	if err != nil {
		t.Fatalf("ReadGrain before close: %v", err)
	}

	reader.Close()

	// After close, should error.
	_, _, err = reader.ReadGrain(0, 0)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestDemoAudioReader_GeneratesSilence(t *testing.T) {
	reader := NewDemoAudioReader(48000, 2)
	defer reader.Close()

	channels, err := reader.ReadSamples(0, 1024, 0)
	if err != nil {
		t.Fatalf("ReadSamples: %v", err)
	}

	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
	if len(channels[0]) != 1024 {
		t.Fatalf("expected 1024 samples, got %d", len(channels[0]))
	}

	// Verify silence (all zeros).
	for ch := 0; ch < 2; ch++ {
		for i, v := range channels[ch] {
			if v != 0 {
				t.Fatalf("channel %d sample %d = %f, want 0", ch, i, v)
			}
		}
	}
}

func TestDemoAudioReader_Closes(t *testing.T) {
	reader := NewDemoAudioReader(48000, 2)

	_, err := reader.ReadSamples(0, 1024, 0)
	if err != nil {
		t.Fatalf("ReadSamples before close: %v", err)
	}

	reader.Close()

	_, err = reader.ReadSamples(0, 1024, 0)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestGenerateDemoYUV420p_ValidSize(t *testing.T) {
	yuv := generateDemoYUV420p(360, 240, 0, 1)
	expectedSize := 360*240 + 180*120 + 180*120
	if len(yuv) != expectedSize {
		t.Fatalf("YUV420p size: got %d, want %d", len(yuv), expectedSize)
	}

	// Verify Y plane has non-zero values (gradient pattern).
	hasNonZero := false
	for i := 0; i < 360*240; i++ {
		if yuv[i] != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Fatal("Y plane is all zeros — expected gradient pattern")
	}
}
