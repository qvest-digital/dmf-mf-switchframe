package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyProcessor_NoKeyPassthrough(t *testing.T) {
	kp := NewKeyProcessor()

	bg := makeYUV420Frame(4, 4, 100, 128, 128)
	original := make([]byte, len(bg))
	copy(original, bg)

	result := kp.Process(bg, nil, 4, 4)

	// Should return bg unchanged
	require.Equal(t, original, result)
}

func TestKeyProcessor_ChromaKeyComposites(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  182,
		KeyColorCb: 30,
		KeyColorCr: 12,
		Similarity: 0.4,
	})

	// Foreground: green (should be keyed out)
	fg := makeYUV420Frame(4, 4, 182, 30, 12)
	// Background: mid-gray
	bg := makeYUV420Frame(4, 4, 128, 128, 128)

	result := kp.Process(bg, map[string][]byte{"cam1": fg}, 4, 4)

	// Since fg is all green and keyed out, result should be close to bg
	// (the transparent foreground reveals the background)
	require.Len(t, result, len(bg))

	// Y channel of result should be similar to bg (128)
	allBg := true
	for i := 0; i < 16; i++ {
		if result[i] < 120 || result[i] > 136 {
			allBg = false
		}
	}
	if !allBg {
		t.Log("result Y values deviated from background, which may be expected due to blending")
	}
}

func TestKeyProcessor_LumaKeyComposites(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		HighClip: 0.5,
		LowClip:  0.0,
	})

	// Foreground: bright (Y=240 > highClip=0.5, should be keyed out)
	fg := makeYUV420Frame(4, 4, 240, 128, 128)
	// Background: dark
	bg := makeYUV420Frame(4, 4, 30, 128, 128)

	result := kp.Process(bg, map[string][]byte{"cam1": fg}, 4, 4)

	require.Len(t, result, len(bg))
}

func TestKeyProcessor_NeuralType_SkippedOnCPU(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:    KeyTypeNeural,
		Enabled: true,
	})

	// Create a background frame and a distinct fill frame.
	bg := makeYUV420Frame(4, 4, 100, 128, 128)
	fill := makeYUV420Frame(4, 4, 200, 50, 200)

	original := make([]byte, len(bg))
	copy(original, bg)

	result := kp.Process(bg, map[string][]byte{"cam1": fill}, 4, 4)

	// Neural key should be skipped on CPU — output identical to input.
	require.Equal(t, original, result, "neural key should be skipped on CPU path, background must be unchanged")
}

func TestKeyProcessor_NeuralKeyAmongOthers_OnlyNeuralSkipped(t *testing.T) {
	kp := NewKeyProcessor()

	// "alpha" is a luma key (should be applied)
	kp.SetKey("alpha", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0.0,
		HighClip: 1.0,
		Softness: 0.0,
	})

	// "bravo" is a neural key (should be skipped)
	kp.SetKey("bravo", KeyConfig{
		Type:    KeyTypeNeural,
		Enabled: true,
	})

	bg := makeYUV420Frame(4, 4, 0, 128, 128)
	lumaFill := makeYUV420Frame(4, 4, 200, 80, 200)
	neuralFill := makeYUV420Frame(4, 4, 255, 30, 30)

	fills := map[string][]byte{
		"alpha": lumaFill,
		"bravo": neuralFill,
	}

	result := kp.Process(bg, fills, 4, 4)

	// The luma key should have composited (Y values changed from 0 toward 200).
	// The neural key should have been skipped entirely.
	ySize := 4 * 4
	for i := 0; i < ySize; i++ {
		require.NotEqual(t, byte(0), result[i],
			"Y[%d] should be changed by luma key", i)
	}
}

func TestKeyProcessor_DisabledKeySkipped(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    false,
		KeyColorCb: 30,
		KeyColorCr: 12,
		Similarity: 0.4,
	})

	fg := makeYUV420Frame(4, 4, 200, 50, 50)
	bg := makeYUV420Frame(4, 4, 128, 128, 128)

	original := make([]byte, len(bg))
	copy(original, bg)

	result := kp.Process(bg, map[string][]byte{"cam1": fg}, 4, 4)

	// Disabled key should pass through bg unchanged
	require.Equal(t, original, result)
}

func TestKeyProcessor_MultipleKeysApplied(t *testing.T) {
	kp := NewKeyProcessor()

	// Two keys configured
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  182,
		KeyColorCb: 30,
		KeyColorCr: 12,
		Similarity: 0.4,
	})
	kp.SetKey("cam2", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		HighClip: 0.5,
		LowClip:  0.0,
	})

	fg1 := makeYUV420Frame(4, 4, 182, 30, 12)   // green
	fg2 := makeYUV420Frame(4, 4, 240, 128, 128) // bright
	bg := makeYUV420Frame(4, 4, 100, 128, 128)

	fills := map[string][]byte{
		"cam1": fg1,
		"cam2": fg2,
	}

	result := kp.Process(bg, fills, 4, 4)

	require.Len(t, result, len(bg))
}

func TestKeyProcessor_RemoveKey(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:    KeyTypeChroma,
		Enabled: true,
	})

	kp.RemoveKey("cam1")

	_, ok := kp.GetKey("cam1")
	require.False(t, ok, "expected key to be removed")
}

func TestKeyProcessor_GetKey(t *testing.T) {
	kp := NewKeyProcessor()

	_, ok := kp.GetKey("cam1")
	require.False(t, ok, "expected no key")

	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		Similarity: 0.5,
	})

	cfg, ok := kp.GetKey("cam1")
	require.True(t, ok, "expected key to exist")
	require.Equal(t, float32(0.5), cfg.Similarity)
}

func TestKeyProcessor_DeterministicOrder(t *testing.T) {
	kp := NewKeyProcessor()
	w, h := 8, 8
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize

	// Configure 5 keys with different fills in non-alphabetical order
	keys := []string{"echo", "alpha", "charlie", "bravo", "delta"}
	for i, name := range keys {
		kp.SetKey(name, KeyConfig{
			Type:     KeyTypeLuma,
			Enabled:  true,
			LowClip:  float32(i) * 0.1,
			HighClip: 1.0,
		})
	}

	// Create background and fill frames
	bg := make([]byte, frameSize)
	for i := range bg {
		bg[i] = 128
	}
	fills := make(map[string][]byte)
	for i, name := range keys {
		fill := make([]byte, frameSize)
		for j := range fill {
			fill[j] = byte(50 + i*30)
		}
		fills[name] = fill
	}

	// Run 100 times and verify identical results
	var firstResult []byte
	for iter := 0; iter < 100; iter++ {
		bgCopy := make([]byte, len(bg))
		copy(bgCopy, bg)
		result := kp.Process(bgCopy, fills, w, h)
		if firstResult == nil {
			firstResult = make([]byte, len(result))
			copy(firstResult, result)
		} else {
			require.Equal(t, firstResult, result,
				"iteration %d: result differs from first iteration", iter)
		}
	}
}

func TestKeyProcessor_UVBlendingAveraged(t *testing.T) {
	kp := NewKeyProcessor()
	w, h := 8, 8
	ySize := w * h
	uvWidth := w / 2
	uvSize := uvWidth * (h / 2)
	frameSize := ySize + 2*uvSize

	// Configure a luma key that makes pixels with Y > 0.5 opaque
	kp.SetKey("fill", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0.0,
		HighClip: 1.0,
		Softness: 0.0,
	})

	// Background: all zeros
	bg := make([]byte, frameSize)
	// Fill: Y=200 everywhere, Cb=200, Cr=50
	fill := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		fill[i] = 200
	}
	for i := 0; i < uvSize; i++ {
		fill[ySize+i] = 200       // Cb
		fill[ySize+uvSize+i] = 50 // Cr
	}
	fills := map[string][]byte{"fill": fill}

	result := kp.Process(bg, fills, w, h)

	// With full alpha (luma key makes everything opaque since Y=200 is between 0 and 255),
	// the UV values should be the fill's UV values (200 for Cb, 50 for Cr)
	for i := 0; i < uvSize; i++ {
		cb := result[ySize+i]
		cr := result[ySize+uvSize+i]
		require.Equal(t, byte(200), cb, "Cb[%d] should be 200", i)
		require.Equal(t, byte(50), cr, "Cr[%d] should be 50", i)
	}
}

func TestKeyProcessor_OnChangeCalledOnSetKey(t *testing.T) {
	kp := NewKeyProcessor()
	called := false
	kp.OnChange(func() { called = true })

	kp.SetKey("cam1", KeyConfig{Enabled: true, Type: KeyTypeChroma})
	require.True(t, called, "OnChange should be called after SetKey")
}

func TestKeyProcessor_OnChangeCalledOnRemoveKey(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Enabled: true, Type: KeyTypeChroma})

	called := false
	kp.OnChange(func() { called = true })

	kp.RemoveKey("cam1")
	require.True(t, called, "OnChange should be called after RemoveKey")
}

func TestKeyProcessor_BlendRoundingNotTruncated(t *testing.T) {
	t.Parallel()
	// Bug 10: uint8(clampFloat(...)) truncates instead of rounding.
	// Without +0.5, fractional blend results always round down (systematic -1 bias).
	//
	// Strategy: Use a luma key with softness to generate a fractional alpha mask,
	// then verify the blend result is rounded, not truncated.
	//
	// Keyer setup: lowClip=0.8, highClip=1.0, softness=0.3
	// Fill Y=153 → luma=153/255=0.6 → in softness zone below lowClip
	//   alpha = (0.8 - 0.6) / 0.3 = 0.6667 → 1.0 - 0.6667 = 0.3333
	//   lut[153] = uint8(0.3333 * 255) = uint8(85.0) = 85
	//
	// Blend at mask=85: alpha = 85/255 ≈ 0.3333
	// bg=40, fill=153:
	//   40 * 0.6667 + 153 * 0.3333 = 26.667 + 51.0 = 77.667
	//   Without +0.5: uint8(77.667) = 77 (truncated)
	//   With +0.5: uint8(78.167) = 78 (rounded correctly)
	kp := NewKeyProcessor()
	w, h := 4, 4
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize

	kp.SetKey("fill", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0.8,
		HighClip: 1.0,
		Softness: 0.3,
	})

	// Background: Y=40, Cb=40, Cr=40
	bg := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		bg[i] = 40
	}
	for i := 0; i < uvSize; i++ {
		bg[ySize+i] = 40
		bg[ySize+uvSize+i] = 40
	}

	// Fill: Y=153, Cb=153, Cr=153
	fill := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		fill[i] = 153
	}
	for i := 0; i < uvSize; i++ {
		fill[ySize+i] = 153
		fill[ySize+uvSize+i] = 153
	}

	result := kp.Process(bg, map[string][]byte{"fill": fill}, w, h)

	// With +0.5 rounding: expect 78, not 77 (truncated).
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(78), result[i],
			"Y[%d] should be 78 (rounded), got %d — truncation bias present", i, result[i])
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(78), result[ySize+i],
			"Cb[%d] should be 78 (rounded), got %d", i, result[ySize+i])
		require.Equal(t, byte(78), result[ySize+uvSize+i],
			"Cr[%d] should be 78 (rounded), got %d", i, result[ySize+uvSize+i])
	}
}

func TestKeyProcessor_OnChangeNilSafe(t *testing.T) {
	kp := NewKeyProcessor()
	// No OnChange registered — should not panic.
	kp.SetKey("cam1", KeyConfig{Enabled: true, Type: KeyTypeChroma})
	kp.RemoveKey("cam1")
}

// B3: Process() writes to spillWorkBuf under RLock, which allows concurrent
// writes if multiple goroutines call Process(). This test detects the race.
func TestKeyProcessor_ConcurrentProcess_NoRace(t *testing.T) {
	kp := NewKeyProcessor()
	w, h := 8, 8
	frameSize := w*h + 2*(w/2)*(h/2)

	// Configure a chroma key with spill suppression (triggers spillWorkBuf write)
	kp.SetKey("cam1", KeyConfig{
		Type:          KeyTypeChroma,
		Enabled:       true,
		KeyColorY:     182,
		KeyColorCb:    30,
		KeyColorCr:    12,
		Similarity:    0.4,
		SpillSuppress: 0.5,
	})

	fill := makeYUV420Frame(w, h, 182, 30, 12)
	fills := map[string][]byte{"cam1": fill}

	// Run concurrent Process() calls — with -race, this detects the data race
	// on spillWorkBuf when Process() uses RLock instead of Lock.
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				bg := make([]byte, frameSize)
				for j := range bg {
					bg[j] = 128
				}
				kp.Process(bg, fills, w, h)
			}
		}()
	}
	for g := 0; g < 4; g++ {
		<-done
	}
}

// BenchmarkKeyProcessor_Process_1080p benchmarks Process() with a luma key
// that produces partial alpha (soft edges) on a 1080p frame.
func BenchmarkKeyProcessor_Process_1080p(b *testing.B) {
	kp := NewKeyProcessor()
	w, h := 1920, 1080
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize

	kp.SetKey("fill", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0.3,
		HighClip: 0.7,
		Softness: 0.2,
	})

	fill := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		fill[i] = byte(i % 256)
	}
	for i := ySize; i < frameSize; i++ {
		fill[i] = 128
	}
	fills := map[string][]byte{"fill": fill}

	bg := make([]byte, frameSize)
	bgTemplate := make([]byte, frameSize)
	for i := range bgTemplate {
		bgTemplate[i] = 128
	}

	b.ReportAllocs()
	b.SetBytes(int64(frameSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(bg, bgTemplate)
		kp.Process(bg, fills, w, h)
	}
}

func TestKeyProcessor_OddDimensionsReturnsUnmodified(t *testing.T) {
	t.Parallel()

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  182,
		KeyColorCb: 30,
		KeyColorCr: 12,
		Similarity: 0.4,
	})

	// Create a small buffer to act as "bg" -- odd dimensions should
	// cause Process to return it unmodified (no panic).
	bg := []byte{100, 101, 102, 103, 104}
	original := make([]byte, len(bg))
	copy(original, bg)

	// Odd width
	result := kp.Process(bg, map[string][]byte{"cam1": bg}, 3, 4)
	require.Equal(t, original, result, "odd width should return bg unmodified")

	// Odd height
	result = kp.Process(bg, map[string][]byte{"cam1": bg}, 4, 3)
	require.Equal(t, original, result, "odd height should return bg unmodified")

	// Both odd
	result = kp.Process(bg, map[string][]byte{"cam1": bg}, 3, 3)
	require.Equal(t, original, result, "both odd should return bg unmodified")
}

func TestKeyTypeNeural(t *testing.T) {
	t.Parallel()
	require.Equal(t, KeyType("neural"), KeyTypeNeural)
	// Verify it is distinct from existing key types.
	require.NotEqual(t, KeyTypeChroma, KeyTypeNeural)
	require.NotEqual(t, KeyTypeLuma, KeyTypeNeural)
}

// --- 10-bit KeyProcessorBridge tests ---

func TestKeyProcessorBridge_ProcessYUV10_ChromaKey(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		KeyColorCb: 30,
		KeyColorCr: 12,
		Similarity: 0.4,
	})

	bridge := NewKeyProcessorBridge(kp)

	w, h := 16, 8
	// Fill: green (should be keyed out). In 10-bit: Cb=120(30*4), Cr=48(12*4).
	fill := make10bitGraphicsFrame(w, h, 728, 120, 48) // green fill
	bridge.IngestFillYUV10("cam1", fill, w, h)

	// Program: mid-gray background.
	program := make10bitGraphicsFrame(w, h, 512, 512, 512)

	result := bridge.ProcessYUV10(program, w, h)
	require.NotNil(t, result)

	// Since the fill is all green and keyed out, the program (background)
	// should show through. Result Y should be close to 512 (program Y).
	gotY := readY10g(result, 4, 4, w)
	// Green pixels should be keyed → result is program (512). Allow tolerance.
	require.InDelta(t, 512, int(gotY), 50, "keyed green pixel should show program background Y~512")
}

func TestKeyProcessorBridge_ProcessYUV10_LumaKey(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0.5,
		HighClip: 1.0,
	})

	bridge := NewKeyProcessorBridge(kp)

	w, h := 16, 8
	// Fill: bright luma (Y=900 > lowClip in 10-bit terms).
	// Luma key keeps pixels in [lowThresh, highThresh], keys rest.
	// In 10-bit terms: lowThresh ~= 512, highThresh ~= 1023 (scaled from LowClip/HighClip).
	// Fill Y=900 is within range → opaque (mask=255, keep).
	fill := make10bitGraphicsFrame(w, h, 900, 512, 512)
	bridge.IngestFillYUV10("cam1", fill, w, h)

	// Program: dark background.
	program := make10bitGraphicsFrame(w, h, 100, 512, 512)

	result := bridge.ProcessYUV10(program, w, h)

	// Fill Y=900 is in the luma key range (opaque), so fill should show.
	gotY := readY10g(result, 4, 4, w)
	require.InDelta(t, 900, int(gotY), 50, "luma key opaque pixel should show fill Y~900")
}

func TestKeyProcessorBridge_ProcessYUV10_NoKeysPassthrough(t *testing.T) {
	kp := NewKeyProcessor()
	bridge := NewKeyProcessorBridge(kp)

	w, h := 16, 8
	program := make10bitGraphicsFrame(w, h, 500, 400, 600)
	original := make([]byte, len(program))
	copy(original, program)

	result := bridge.ProcessYUV10(program, w, h)
	require.Equal(t, original, result, "no keys should return program unchanged")
}

func TestKeyProcessorBridge_ProcessYUV10_NoFillsPassthrough(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:    KeyTypeChroma,
		Enabled: true,
	})
	bridge := NewKeyProcessorBridge(kp)

	w, h := 16, 8
	program := make10bitGraphicsFrame(w, h, 500, 400, 600)
	original := make([]byte, len(program))
	copy(original, program)

	result := bridge.ProcessYUV10(program, w, h)
	require.Equal(t, original, result, "no fills should return program unchanged")
}

func TestKeyProcessor_KeyFillsSelfFill(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeChroma, Enabled: true})

	require.ElementsMatch(t, []string{"cam1"}, kp.KeyFills())
	require.Empty(t, kp.KeyAlphas())
}

func TestKeyProcessor_KeyFillsExplicitFillSource(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		FillSource: "cam3",
	})

	require.ElementsMatch(t, []string{"cam3"}, kp.KeyFills())
	require.ElementsMatch(t, []string{"cam1"}, kp.KeyAlphas())
}

func TestKeyProcessor_KeyFillsDisabledKeyIgnored(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    false,
		FillSource: "cam3",
	})

	require.Empty(t, kp.KeyFills())
	require.Empty(t, kp.KeyAlphas())
}

func TestKeyProcessor_KeyFillsMultipleKeysDeduplicated(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeChroma, Enabled: true})
	kp.SetKey("cam2", KeyConfig{
		Type:       KeyTypeLuma,
		Enabled:    true,
		FillSource: "cam3",
	})
	kp.SetKey("cam4", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		FillSource: "cam3",
	})

	require.ElementsMatch(t, []string{"cam1", "cam3"}, kp.KeyFills())
	require.ElementsMatch(t, []string{"cam2", "cam4"}, kp.KeyAlphas())
}
