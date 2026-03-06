package graphics

import (
	"testing"

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

// mockBridgeEncoder implements transition.VideoEncoder for testing.
type mockBridgeEncoder struct {
	lastInput []byte
	closed    bool
}

func (e *mockBridgeEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	e.lastInput = make([]byte, len(yuv))
	copy(e.lastInput, yuv)
	// Return minimal Annex B data: start code + SPS-like NALU
	if forceIDR {
		return []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a,
			0x00, 0x00, 0x00, 0x01, 0x68, 0x42, 0x00,
			0x00, 0x00, 0x00, 0x01, 0x65, 0x88}, true, nil
	}
	return []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a}, false, nil
}

func (e *mockBridgeEncoder) Close() { e.closed = true }

func TestBridge_NoKeysPassthrough(t *testing.T) {
	kp := NewKeyProcessor()
	bridge := NewKeyProcessorBridge(kp)

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
	}

	result := bridge.ProcessFrame(frame)
	if result != frame {
		t.Fatal("expected passthrough when no keys configured")
	}
}

func TestBridge_NoFillsPassthrough(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
	}

	result := bridge.ProcessFrame(frame)
	if result != frame {
		t.Fatal("expected passthrough when no fills cached")
	}
}

func TestBridge_IngestAndProcess(t *testing.T) {
	w, h := 8, 8
	yuvSize := w * h * 3 / 2
	bgYUV := make([]byte, yuvSize)
	for i := range bgYUV {
		bgYUV[i] = 100
	}
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

	var capturedEncoder *mockBridgeEncoder
	decoderCount := 0

	bridge.SetCodecFactories(
		func() (transition.VideoDecoder, error) {
			decoderCount++
			if decoderCount == 1 {
				// First decoder is for fill
				return &mockBridgeDecoder{yuv: fillYUV, w: w, h: h}, nil
			}
			// Second decoder is for program
			return &mockBridgeDecoder{yuv: bgYUV, w: w, h: h}, nil
		},
		func(ew, eh, bitrate int, fps float32) (transition.VideoEncoder, error) {
			enc := &mockBridgeEncoder{}
			capturedEncoder = enc
			return enc, nil
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

	// Now process a program frame
	programFrame := &media.VideoFrame{
		PTS:        2000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	result := bridge.ProcessFrame(programFrame)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result == programFrame {
		t.Fatal("expected processed frame, not passthrough")
	}
	if capturedEncoder == nil {
		t.Fatal("expected encoder to be initialized")
	}
	// Verify the encoder received YUV data (the process method was called)
	if capturedEncoder.lastInput == nil {
		t.Fatal("expected encoder to receive YUV data")
	}
}

func TestBridge_CleanupOnClose(t *testing.T) {
	w, h := 4, 4
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	var fillDec, progDec *mockBridgeDecoder
	var enc *mockBridgeEncoder
	decCount := 0

	bridge.SetCodecFactories(
		func() (transition.VideoDecoder, error) {
			decCount++
			d := &mockBridgeDecoder{yuv: yuv, w: w, h: h}
			if decCount == 1 {
				fillDec = d
			} else {
				progDec = d
			}
			return d, nil
		},
		func(ew, eh, bitrate int, fps float32) (transition.VideoEncoder, error) {
			enc = &mockBridgeEncoder{}
			return enc, nil
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
	bridge.ProcessFrame(frame)
	bridge.Close()

	if fillDec != nil && !fillDec.closed {
		t.Error("fill decoder not closed")
	}
	if progDec != nil && !progDec.closed {
		t.Error("program decoder not closed")
	}
	if enc != nil && !enc.closed {
		t.Error("encoder not closed")
	}
}

func TestBridge_HasEnabledKeys(t *testing.T) {
	kp := NewKeyProcessor()

	if kp.HasEnabledKeys() {
		t.Fatal("expected no enabled keys on empty processor")
	}

	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: false})
	if kp.HasEnabledKeys() {
		t.Fatal("expected no enabled keys when all disabled")
	}

	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true})
	if !kp.HasEnabledKeys() {
		t.Fatal("expected enabled keys")
	}
}

func TestBridge_EnabledSources(t *testing.T) {
	kp := NewKeyProcessor()

	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0.1})
	kp.SetKey("cam2", KeyConfig{Type: KeyTypeChroma, Enabled: false})
	kp.SetKey("cam3", KeyConfig{Type: KeyTypeChroma, Enabled: true, Similarity: 0.5})

	enabled := kp.EnabledSources()
	if len(enabled) != 2 {
		t.Fatalf("expected 2 enabled sources, got %d", len(enabled))
	}
	if _, ok := enabled["cam1"]; !ok {
		t.Error("expected cam1 in enabled sources")
	}
	if _, ok := enabled["cam3"]; !ok {
		t.Error("expected cam3 in enabled sources")
	}
	if _, ok := enabled["cam2"]; ok {
		t.Error("cam2 should not be in enabled sources")
	}
}

func TestBridge_RemoveFillSource(t *testing.T) {
	w, h := 4, 4
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)

	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true, LowClip: 0, HighClip: 1})

	bridge := NewKeyProcessorBridge(kp)

	var fillDec *mockBridgeDecoder
	bridge.SetCodecFactories(
		func() (transition.VideoDecoder, error) {
			fillDec = &mockBridgeDecoder{yuv: yuv, w: w, h: h}
			return fillDec, nil
		},
		func(ew, eh, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return &mockBridgeEncoder{}, nil
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
	if _, ok := bridge.fillYUV["cam1"]; !ok {
		t.Fatal("expected fill YUV to be cached for cam1")
	}
	bridge.mu.Unlock()

	bridge.RemoveFillSource("cam1")

	// Verify fill was removed
	bridge.mu.Lock()
	if _, ok := bridge.fillYUV["cam1"]; ok {
		t.Fatal("expected fill YUV to be removed for cam1")
	}
	if _, ok := bridge.fillDecoders["cam1"]; ok {
		t.Fatal("expected fill decoder to be removed for cam1")
	}
	bridge.mu.Unlock()

	if fillDec != nil && !fillDec.closed {
		t.Error("fill decoder should be closed after RemoveFillSource")
	}
}
