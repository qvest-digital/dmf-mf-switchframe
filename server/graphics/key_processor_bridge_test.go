package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBridge_HasEnabledKeys(t *testing.T) {
	kp := NewKeyProcessor()

	require.False(t, kp.HasEnabledKeys(), "expected no enabled keys on empty processor")

	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: false})
	require.False(t, kp.HasEnabledKeys(), "expected no enabled keys when all disabled")

	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true})
	require.True(t, kp.HasEnabledKeys(), "expected enabled keys")
}

func TestBridge_EnabledSources(t *testing.T) {
	kp := NewKeyProcessor()

	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0.1})
	kp.SetKey("cam2", KeyConfig{Type: KeyTypeChroma, Enabled: false})
	kp.SetKey("cam3", KeyConfig{Type: KeyTypeChroma, Enabled: true, Similarity: 0.5})

	enabled := kp.EnabledSources()
	require.Len(t, enabled, 2)
	require.Contains(t, enabled, "cam1")
	require.Contains(t, enabled, "cam3")
	require.NotContains(t, enabled, "cam2")
}

func TestBridge_IngestFillYUV(t *testing.T) {
	w, h := 8, 8
	yuvSize := w * h * 3 / 2
	fillYUV := make([]byte, yuvSize)
	for i := range fillYUV {
		fillYUV[i] = 200
	}

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0,
		HighClip: 1.0,
		Softness: 0,
	})

	bridge := NewKeyProcessorBridge(kp)

	bridge.IngestFillYUV("cam1", fillYUV, w, h)

	// Verify fill was cached
	bridge.mu.Lock()
	require.Contains(t, bridge.fills, "cam1", "expected fill to be cached for cam1")
	require.Equal(t, yuvSize, len(bridge.fills["cam1"].yuv))
	require.Equal(t, w, bridge.fills["cam1"].width)
	require.Equal(t, h, bridge.fills["cam1"].height)
	bridge.mu.Unlock()
}

func TestBridge_RemoveFillSource(t *testing.T) {
	w, h := 4, 4
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	bridge.IngestFillYUV("cam1", yuv, w, h)

	// Verify fill was cached
	bridge.mu.Lock()
	require.Contains(t, bridge.fills, "cam1", "expected fill YUV to be cached for cam1")
	bridge.mu.Unlock()

	bridge.RemoveFillSource("cam1")

	// Verify fill was removed
	bridge.mu.Lock()
	require.NotContains(t, bridge.fills, "cam1", "expected fill YUV to be removed for cam1")
	bridge.mu.Unlock()
}

func TestBridge_CleanupOnClose(t *testing.T) {
	w, h := 4, 4
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	bridge.IngestFillYUV("cam1", yuv, w, h)

	bridge.mu.Lock()
	require.Contains(t, bridge.fills, "cam1")
	bridge.mu.Unlock()

	bridge.Close()

	bridge.mu.Lock()
	require.NotContains(t, bridge.fills, "cam1", "expected fillYUV cleared after Close")
	bridge.mu.Unlock()
}
