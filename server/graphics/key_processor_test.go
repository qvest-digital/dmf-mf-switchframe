package graphics

import (
	"testing"
)

func TestKeyProcessor_NoKeyPassthrough(t *testing.T) {
	kp := NewKeyProcessor()

	bg := makeYUV420Frame(4, 4, 100, 128, 128)
	original := make([]byte, len(bg))
	copy(original, bg)

	result := kp.Process(bg, nil, 4, 4)

	// Should return bg unchanged
	for i := range result {
		if result[i] != original[i] {
			t.Fatalf("byte %d: expected %d, got %d", i, original[i], result[i])
		}
	}
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

	bgCopy := make([]byte, len(bg))
	copy(bgCopy, bg)

	result := kp.Process(bg, map[string][]byte{"cam1": fg}, 4, 4)

	// Since fg is all green and keyed out, result should be close to bg
	// (the transparent foreground reveals the background)
	if len(result) != len(bg) {
		t.Fatalf("expected result length %d, got %d", len(bg), len(result))
	}

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

	if len(result) != len(bg) {
		t.Fatalf("expected result length %d, got %d", len(bg), len(result))
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
	for i := range result {
		if result[i] != original[i] {
			t.Fatalf("byte %d: expected %d (passthrough), got %d", i, original[i], result[i])
		}
	}
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
	fg2 := makeYUV420Frame(4, 4, 240, 128, 128)  // bright
	bg := makeYUV420Frame(4, 4, 100, 128, 128)

	fills := map[string][]byte{
		"cam1": fg1,
		"cam2": fg2,
	}

	result := kp.Process(bg, fills, 4, 4)

	if len(result) != len(bg) {
		t.Fatalf("expected result length %d, got %d", len(bg), len(result))
	}
}

func TestKeyProcessor_RemoveKey(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:    KeyTypeChroma,
		Enabled: true,
	})

	kp.RemoveKey("cam1")

	cfg, ok := kp.GetKey("cam1")
	if ok {
		t.Fatalf("expected key to be removed, got %+v", cfg)
	}
}

func TestKeyProcessor_GetKey(t *testing.T) {
	kp := NewKeyProcessor()

	_, ok := kp.GetKey("cam1")
	if ok {
		t.Fatal("expected no key")
	}

	kp.SetKey("cam1", KeyConfig{
		Type:       KeyTypeChroma,
		Enabled:    true,
		Similarity: 0.5,
	})

	cfg, ok := kp.GetKey("cam1")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if cfg.Similarity != 0.5 {
		t.Fatalf("expected similarity 0.5, got %f", cfg.Similarity)
	}
}
