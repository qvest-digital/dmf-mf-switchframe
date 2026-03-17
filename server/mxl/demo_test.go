package mxl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDemoVideoReader_GeneratesValidV210(t *testing.T) {
	// 360x240 is the resolution used in demo mode.
	reader := NewDemoVideoReader(360, 240, 30, 0)
	defer func() { _ = reader.Close() }()

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
	defer func() { _ = reader0.Close() }()
	defer func() { _ = reader1.Close() }()

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

	_ = reader.Close()

	// After close, should error.
	_, _, err = reader.ReadGrain(0, 0)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestDemoAudioReader_GeneratesTone(t *testing.T) {
	reader := NewDemoAudioReader(48000, 2)
	defer func() { _ = reader.Close() }()

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

	// Verify tone is present (has non-zero samples).
	hasNonZero := false
	for _, v := range channels[0] {
		if v != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Fatal("expected non-zero samples (440 Hz tone), got silence")
	}

	// Both channels should be identical (mono tone on both).
	for i := range channels[0] {
		if channels[0][i] != channels[1][i] {
			t.Fatalf("channels differ at sample %d: L=%f R=%f", i, channels[0][i], channels[1][i])
		}
	}
}

func TestDemoAudioReader_Closes(t *testing.T) {
	reader := NewDemoAudioReader(48000, 2)

	_, err := reader.ReadSamples(0, 1024, 0)
	if err != nil {
		t.Fatalf("ReadSamples before close: %v", err)
	}

	_ = reader.Close()

	_, err = reader.ReadSamples(0, 1024, 0)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestGreenScreenPattern_ValidSize(t *testing.T) {
	const w, h = 360, 240
	yuv := generateGreenScreenYUV420p(w, h, 1)
	expectedSize := w*h + w*h/4 + w*h/4
	require.Equal(t, expectedSize, len(yuv), "YUV420p buffer size mismatch")
}

func TestGreenScreenPattern_GreenBackground(t *testing.T) {
	const w, h = 360, 240
	yuv := generateGreenScreenYUV420p(w, h, 0)

	cw, ch := w/2, h/2
	cbPlane := yuv[w*h : w*h+cw*ch]
	crPlane := yuv[w*h+cw*ch:]

	// Sample a background pixel (top-left corner should be green).
	require.Equal(t, byte(173), yuv[0], "Y[0,0] should be green Y=173")
	require.Equal(t, byte(42), cbPlane[0], "Cb[0,0] should be green Cb=42")
	require.Equal(t, byte(26), crPlane[0], "Cr[0,0] should be green Cr=26")
}

func TestGreenScreenPattern_ForegroundPresent(t *testing.T) {
	const w, h = 360, 240
	yuv := generateGreenScreenYUV420p(w, h, 1)

	yPlane := yuv[:w*h]

	// Count white pixels (Y=235) — should have some from logo and lower third.
	whiteCount := 0
	for _, y := range yPlane {
		if y == 235 {
			whiteCount++
		}
	}
	require.Greater(t, whiteCount, 0,
		"should have white foreground pixels (logo and/or lower third)")

	// Logo should be in top-right corner.
	logoSize := w / 8
	logoX0 := w - logoSize - 4
	logoY0 := 4
	require.Equal(t, byte(235), yPlane[logoY0*w+logoX0],
		"logo top-left corner should be white")
}

func TestGreenScreenPattern_AnimatesBetweenFrames(t *testing.T) {
	const w, h = 360, 240
	yuv1 := generateGreenScreenYUV420p(w, h, 0)
	yuv2 := generateGreenScreenYUV420p(w, h, 10) // 10 frames later

	// The lower third sweeps, so the Y planes should differ.
	y1 := yuv1[:w*h]
	y2 := yuv2[:w*h]

	diffs := 0
	for i := range y1 {
		if y1[i] != y2[i] {
			diffs++
		}
	}
	require.Greater(t, diffs, 0,
		"green screen pattern should animate between frames")
}

func TestGreenScreenReader_GeneratesValidV210(t *testing.T) {
	reader := NewDemoVideoReaderWithPattern(360, 240, 30, 0, PatternGreenScreen)
	defer func() { _ = reader.Close() }()

	data, info, err := reader.ReadGrain(0, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), info.Index)
	require.NotEmpty(t, data)

	// V210 should round-trip back to YUV420p.
	yuv, err := V210ToYUV420p(data, 360, 240)
	require.NoError(t, err)

	expectedSize := 360*240 + 180*120 + 180*120
	require.Equal(t, expectedSize, len(yuv))
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

// BenchmarkDemoVideoReader_ReadGrain benchmarks the hot path for MXL demo
// video generation. The Into variants reuse buffers across frames.
func BenchmarkDemoVideoReader_ReadGrain(b *testing.B) {
	reader := NewDemoVideoReaderWithPattern(360, 240, 30, 0, PatternColorBars)
	reader.interval = 0 // disable sleep for benchmark
	defer func() { _ = reader.Close() }()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, err := reader.ReadGrain(0, 0)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDemoVideoReader_ReadGrain_Allocating benchmarks the old path
// (allocates fresh YUV + V210 buffers each frame) for comparison.
func BenchmarkDemoVideoReader_ReadGrain_Allocating(b *testing.B) {
	w, h := 360, 240
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		yuv := generateDemoYUV420p(w, h, 0, uint64(i))
		_, err := YUV420pToV210(yuv, w, h)
		if err != nil {
			b.Fatal(err)
		}
	}
}
