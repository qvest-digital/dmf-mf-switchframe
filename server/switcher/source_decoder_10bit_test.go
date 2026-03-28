package switcher

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// mock10bitDecoder simulates a decoder that returns YUV422P10LE output
// (w*h*4 bytes) to test the source_decoder's format detection.
type mock10bitDecoder struct {
	width  int
	height int
}

func (d *mock10bitDecoder) Decode(data []byte) ([]byte, int, int, error) {
	size := ColorYUV422_10bit.FrameSize(d.width, d.height)
	buf := make([]byte, size)
	// Fill with valid 10-bit values (neutral gray)
	for i := 0; i < d.width*d.height; i++ {
		// Y = 512 (10-bit neutral gray)
		buf[i*2] = 0x00
		buf[i*2+1] = 0x02
	}
	ySize := d.width * d.height * 2
	for i := ySize; i < size; i += 2 {
		// Chroma = 512 (neutral)
		buf[i] = 0x00
		buf[i+1] = 0x02
	}
	return buf, d.width, d.height, nil
}

func (d *mock10bitDecoder) Close() {}

// mock420_8bitDecoder simulates a standard decoder that returns YUV420P 8-bit.
type mock420_8bitDecoder struct {
	width  int
	height int
}

func (d *mock420_8bitDecoder) Decode(data []byte) ([]byte, int, int, error) {
	size := ColorYUV420_8bit.FrameSize(d.width, d.height)
	buf := make([]byte, size)
	for i := 0; i < d.width*d.height; i++ {
		buf[i] = 128 // neutral Y
	}
	for i := d.width * d.height; i < size; i++ {
		buf[i] = 128 // neutral chroma
	}
	return buf, d.width, d.height, nil
}

func (d *mock420_8bitDecoder) Close() {}

// TestSourceDecoder_Native10bit_SkipsConversion verifies that when the
// decoder produces YUV422P10LE output (w*h*4 bytes) and the pipeline is
// in professional mode (10-bit 422), the source decoder uses the data
// directly without upconversion.
func TestSourceDecoder_Native10bit_SkipsConversion(t *testing.T) {
	w, h := 256, 256

	var received *ProcessingFrame
	var receivedKey string

	pipeFmt := &atomic.Pointer[PipelineFormat]{}
	pipeFmt.Store(&PipelineFormat{
		Width:  w,
		Height: h,
		FPSNum: 30,
		FPSDen: 1,
		Color:  PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC},
	})

	sd := &sourceDecoder{
		sourceKey: "test-10bit",
		decoder:   &mock10bitDecoder{width: w, height: h},
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			receivedKey = key
			received = pf
		},
		done:           make(chan struct{}),
		pipelineFormat: pipeFmt,
	}

	go sd.decodeLoop()

	// Send a fake frame
	frame := &media.VideoFrame{
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}, // fake H.264
		IsKeyframe: true,
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		PTS:        90000,
		GroupID:    1,
		Codec:      "h265",
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Close and drain
	close(sd.ch)
	<-sd.done

	require.NotNil(t, received, "should have received a decoded frame")
	assert.Equal(t, "test-10bit", receivedKey)

	// The frame should be 10-bit 422 format
	expected10bitSize := ColorYUV422_10bit.FrameSize(w, h)
	assert.Equal(t, expected10bitSize, len(received.Data),
		"frame data should be YUV422P10LE size (w*h*4)")
	assert.Equal(t, ColorYUV422_10bit, received.Format,
		"frame format should be ColorYUV422_10bit")
}

// TestSourceDecoder_8bitDecoder_Upconverts verifies that when the decoder
// produces standard YUV420P 8-bit output on a 10-bit pipeline, the source
// decoder upconverts to 10-bit 422.
func TestSourceDecoder_8bitDecoder_Upconverts(t *testing.T) {
	w, h := 256, 256

	var received *ProcessingFrame

	pipeFmt := &atomic.Pointer[PipelineFormat]{}
	pipeFmt.Store(&PipelineFormat{
		Width:  w,
		Height: h,
		FPSNum: 30,
		FPSDen: 1,
		Color:  PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC},
	})

	sd := &sourceDecoder{
		sourceKey: "test-8bit-upconvert",
		decoder:   &mock420_8bitDecoder{width: w, height: h},
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			received = pf
		},
		done:           make(chan struct{}),
		pipelineFormat: pipeFmt,
	}

	go sd.decodeLoop()

	frame := &media.VideoFrame{
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
		IsKeyframe: true,
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		PTS:        90000,
		GroupID:    1,
		Codec:      "h264",
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	time.Sleep(100 * time.Millisecond)

	close(sd.ch)
	<-sd.done

	require.NotNil(t, received, "should have received a decoded frame")

	// The frame should be upconverted to 10-bit 422
	expected10bitSize := ColorYUV422_10bit.FrameSize(w, h)
	assert.Equal(t, expected10bitSize, len(received.Data),
		"frame should be upconverted to YUV422P10LE")
	assert.Equal(t, ColorYUV422_10bit, received.Format,
		"frame format should be ColorYUV422_10bit after upconversion")
}

// TestSourceDecoder_8bitPipeline_NoConversion verifies that on an 8-bit
// pipeline, the standard decoder's output is used directly.
func TestSourceDecoder_8bitPipeline_NoConversion(t *testing.T) {
	w, h := 256, 256

	var received *ProcessingFrame

	pipeFmt := &atomic.Pointer[PipelineFormat]{}
	pipeFmt.Store(&PipelineFormat{
		Width:  w,
		Height: h,
		FPSNum: 30,
		FPSDen: 1,
		Color:  PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC},
	})

	sd := &sourceDecoder{
		sourceKey: "test-8bit",
		decoder:   &mock420_8bitDecoder{width: w, height: h},
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			received = pf
		},
		done:           make(chan struct{}),
		pipelineFormat: pipeFmt,
	}

	go sd.decodeLoop()

	frame := &media.VideoFrame{
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
		IsKeyframe: true,
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		PTS:        90000,
		GroupID:    1,
		Codec:      "h264",
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	time.Sleep(100 * time.Millisecond)

	close(sd.ch)
	<-sd.done

	require.NotNil(t, received, "should have received a decoded frame")

	expected420Size := ColorYUV420_8bit.FrameSize(w, h)
	assert.Equal(t, expected420Size, len(received.Data),
		"frame should be standard YUV420P 8-bit")
	assert.Equal(t, ColorYUV420_8bit, received.Format,
		"frame format should be ColorYUV420_8bit")
}

// TestDecoderFactoryForCodec_Native10bit verifies the public factory functions
// that create native 10-bit decoders.
func TestDecoderFactoryForCodec_Native10bit(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return codec.NewVideoDecoderNative10bit()
	}

	dec, err := factory()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}
