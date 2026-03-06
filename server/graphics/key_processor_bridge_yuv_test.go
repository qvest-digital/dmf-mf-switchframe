package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestBridgeProcessYUV_NoKeys(t *testing.T) {
	kp := NewKeyProcessor()
	bridge := NewKeyProcessorBridge(kp)

	yuv := make([]byte, 4*4*3/2)
	yuv[0] = 42

	result := bridge.ProcessYUV(yuv, 4, 4)
	require.Equal(t, yuv, result, "no keys → passthrough")
}

func TestBridgeProcessYUV_WithFill(t *testing.T) {
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
	bridge.SetCodecFactories(
		func() (transition.VideoDecoder, error) {
			return &mockBridgeDecoder{yuv: fillYUV, w: w, h: h}, nil
		},
		func(ew, eh, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return &mockBridgeEncoder{}, nil
		},
	)

	// Ingest fill frame
	bridge.IngestFillFrame("cam1", &media.VideoFrame{
		PTS: 1000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0x42, 0x00},
	})

	// Process YUV directly (no decode/encode of program frame)
	result := bridge.ProcessYUV(bgYUV, w, h)

	// Luma key with LowClip=0, HighClip=1.0 → full alpha → fill replaces bg
	require.NotNil(t, result)
	require.Equal(t, yuvSize, len(result))
}

func TestBridgeProcessYUV_NoFills(t *testing.T) {
	kp := NewKeyProcessor()
	kp.SetKey("cam1", KeyConfig{Type: KeyTypeLuma, Enabled: true})

	bridge := NewKeyProcessorBridge(kp)

	yuv := make([]byte, 4*4*3/2)
	yuv[0] = 42

	result := bridge.ProcessYUV(yuv, 4, 4)
	require.Equal(t, yuv, result, "no fills cached → passthrough")
}
