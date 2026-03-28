package switcher

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/stmap"
)

// mock10bitDecoder_CustomRes returns YUV422P10LE at a specified resolution.
type mock10bitDecoder_CustomRes struct {
	width  int
	height int
	yVal   uint16 // Y value to fill
}

func (d *mock10bitDecoder_CustomRes) Decode(data []byte) ([]byte, int, int, error) {
	size := ColorYUV422_10bit.FrameSize(d.width, d.height)
	buf := make([]byte, size)
	// Fill Y plane with custom value
	yPlaneSize := d.width * d.height * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], d.yVal)
	}
	// Fill chroma planes with neutral (512)
	for i := yPlaneSize; i < size; i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], 512)
	}
	return buf, d.width, d.height, nil
}

func (d *mock10bitDecoder_CustomRes) Close() {}

// TestSourceDecoder_10bit_ResolutionScaling verifies that 10-bit frames at the
// wrong resolution are scaled to pipeline dimensions using the 10-bit scaler.
// (Fix 4: Previously 10-bit frames were passed through without scaling.)
func TestSourceDecoder_10bit_ResolutionScaling(t *testing.T) {
	pipeW, pipeH := 256, 256
	srcW, srcH := 128, 128 // Source at different resolution

	var received *ProcessingFrame

	pipeFmt := &atomic.Pointer[PipelineFormat]{}
	pipeFmt.Store(&PipelineFormat{
		Width:  pipeW,
		Height: pipeH,
		FPSNum: 30,
		FPSDen: 1,
		Color:  PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC},
	})

	sd := &sourceDecoder{
		sourceKey: "test-10bit-scale",
		decoder:   &mock10bitDecoder_CustomRes{width: srcW, height: srcH, yVal: 400},
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			received = pf
		},
		done:           make(chan struct{}),
		pipelineFormat: pipeFmt,
	}

	go sd.decodeLoop()

	frame := &media.VideoFrame{
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA},
		IsKeyframe: true,
		SPS:        []byte{0x42, 0x01, 0xFF},
		PPS:        []byte{0x44, 0x01, 0xFF},
		PTS:        90000,
		GroupID:    1,
		Codec:      "h265",
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	time.Sleep(100 * time.Millisecond)

	close(sd.ch)
	<-sd.done

	require.NotNil(t, received, "should have received a decoded frame")

	// Frame should be scaled to pipeline dimensions.
	expectedSize := ColorYUV422_10bit.FrameSize(pipeW, pipeH)
	assert.Equal(t, expectedSize, len(received.Data),
		"10-bit frame should be scaled to pipeline resolution %dx%d", pipeW, pipeH)
	assert.Equal(t, pipeW, received.Width, "frame width should match pipeline")
	assert.Equal(t, pipeH, received.Height, "frame height should match pipeline")
	assert.Equal(t, ColorYUV422_10bit, received.Format, "format should remain 10-bit")
}

// TestSourceDecoder_10bit_STMap verifies that per-source ST map correction
// works for 10-bit frames. (Fix 6: Previously skipped for 10-bit.)
func TestSourceDecoder_10bit_STMap(t *testing.T) {
	w, h := 64, 64

	var received *ProcessingFrame

	pipeFmt := &atomic.Pointer[PipelineFormat]{}
	pipeFmt.Store(&PipelineFormat{
		Width:  w,
		Height: h,
		FPSNum: 30,
		FPSDen: 1,
		Color:  PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC},
	})

	// Create an ST map registry with an identity map for this source.
	registry := stmap.NewRegistry()
	identityMap := stmap.Identity(w, h)
	identityMap.Name = "identity-test"
	err := registry.Store(identityMap)
	require.NoError(t, err)
	err = registry.AssignSource("test-stmap-10bit", "identity-test")
	require.NoError(t, err)

	sd := &sourceDecoder{
		sourceKey: "test-stmap-10bit",
		decoder:   &mock10bitDecoder_CustomRes{width: w, height: h, yVal: 300},
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			received = pf
		},
		done:           make(chan struct{}),
		pipelineFormat: pipeFmt,
		stmapRegistry:  registry,
	}

	go sd.decodeLoop()

	frame := &media.VideoFrame{
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA},
		IsKeyframe: true,
		SPS:        []byte{0x42, 0x01, 0xFF},
		PPS:        []byte{0x44, 0x01, 0xFF},
		PTS:        90000,
		GroupID:    1,
		Codec:      "h265",
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	time.Sleep(100 * time.Millisecond)

	close(sd.ch)
	<-sd.done

	require.NotNil(t, received, "should have received a decoded frame")

	// With an identity ST map, values should be preserved.
	assert.Equal(t, ColorYUV422_10bit, received.Format)
	assert.Equal(t, w, received.Width)
	assert.Equal(t, h, received.Height)

	// Check that Y values are approximately preserved (identity map).
	if len(received.Data) >= 2 {
		firstY := binary.LittleEndian.Uint16(received.Data[:2])
		// Identity map should preserve values within rounding tolerance.
		assert.InDelta(t, 300, int(firstY), 5,
			"identity ST map should preserve Y values (got %d, want ~300)", firstY)
	}
}
