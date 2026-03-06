package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

// mockBridgeDecoder implements transition.VideoDecoder for testing.
type mockBridgeDecoder struct {
	yuv    []byte
	w, h   int
	closed bool
}

func (d *mockBridgeDecoder) Decode(annexB []byte) ([]byte, int, int, error) {
	return d.yuv, d.w, d.h, nil
}

func (d *mockBridgeDecoder) Close() { d.closed = true }

func TestBridge_IngestFillFrame(t *testing.T) {
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

	bridge.SetDecoderFactory(
		func() (transition.VideoDecoder, error) {
			return &mockBridgeDecoder{yuv: fillYUV, w: w, h: h}, nil
		},
	)

	// Ingest a fill frame for cam1
	fillFrame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	bridge.IngestFillFrame("cam1", fillFrame)

	// Verify fill was cached
	bridge.mu.Lock()
	require.Contains(t, bridge.fillYUV, "cam1", "expected fill YUV to be cached for cam1")
	require.Equal(t, yuvSize, len(bridge.fillYUV["cam1"]))
	bridge.mu.Unlock()
}

func TestBridge_CleanupOnClose(t *testing.T) {
	w, h := 4, 4
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	var fillDec *mockBridgeDecoder

	bridge.SetDecoderFactory(
		func() (transition.VideoDecoder, error) {
			fillDec = &mockBridgeDecoder{yuv: yuv, w: w, h: h}
			return fillDec, nil
		},
	)

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	bridge.IngestFillFrame("cam1", frame)
	bridge.Close()

	if fillDec != nil {
		require.True(t, fillDec.closed, "fill decoder not closed")
	}
}

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

func TestBridge_RemoveFillSource(t *testing.T) {
	w, h := 4, 4
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	var fillDec *mockBridgeDecoder
	bridge.SetDecoderFactory(
		func() (transition.VideoDecoder, error) {
			fillDec = &mockBridgeDecoder{yuv: yuv, w: w, h: h}
			return fillDec, nil
		},
	)

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	bridge.IngestFillFrame("cam1", frame)

	// Verify fill was cached
	bridge.mu.Lock()
	require.Contains(t, bridge.fillYUV, "cam1", "expected fill YUV to be cached for cam1")
	bridge.mu.Unlock()

	bridge.RemoveFillSource("cam1")

	// Verify fill was removed
	bridge.mu.Lock()
	require.NotContains(t, bridge.fillYUV, "cam1", "expected fill YUV to be removed for cam1")
	require.NotContains(t, bridge.fillDecoders, "cam1", "expected fill decoder to be removed for cam1")
	bridge.mu.Unlock()

	if fillDec != nil {
		require.True(t, fillDec.closed, "fill decoder should be closed after RemoveFillSource")
	}
}
