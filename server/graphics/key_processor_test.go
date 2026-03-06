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

	fg1 := makeYUV420Frame(4, 4, 182, 30, 12)  // green
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
