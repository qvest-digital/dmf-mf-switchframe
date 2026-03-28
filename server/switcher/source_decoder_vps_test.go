package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// naluCapturingDecoder records the Annex B data passed to Decode so tests
// can verify VPS/SPS/PPS prepend behavior.
type naluCapturingDecoder struct {
	mu       sync.Mutex
	captured [][]byte
	width    int
	height   int
}

func (d *naluCapturingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	cp := make([]byte, len(data))
	copy(cp, data)
	d.mu.Lock()
	d.captured = append(d.captured, cp)
	d.mu.Unlock()

	size := ColorYUV420_8bit.FrameSize(d.width, d.height)
	return make([]byte, size), d.width, d.height, nil
}

func (d *naluCapturingDecoder) Close() {}

func (d *naluCapturingDecoder) getCaptured() [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]byte, len(d.captured))
	for i, v := range d.captured {
		out[i] = make([]byte, len(v))
		copy(out[i], v)
	}
	return out
}

// TestSourceDecoder_HEVC_VPSPrepended verifies that when an HEVC keyframe
// with VPS is received, the source decoder prepends VPS+SPS+PPS before
// sending the Annex B data to the decoder.
//
// This catches Fix 4: source_decoder only used PrependSPSPPSInto (no VPS)
// which breaks HEVC sources that require VPS for decoder initialization.
func TestSourceDecoder_HEVC_VPSPrepended(t *testing.T) {
	w, h := 64, 64
	capDec := &naluCapturingDecoder{width: w, height: h}

	var received *ProcessingFrame
	var receiveMu sync.Mutex

	sd := &sourceDecoder{
		sourceKey: "hevc-vps-test",
		decoder:   capDec,
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			receiveMu.Lock()
			received = pf
			receiveMu.Unlock()
		},
		done: make(chan struct{}),
	}

	go sd.decodeLoop()

	// HEVC IDR NALU (type 19 = IDR_W_RADL, nal_type is in bits[6:1] of first byte)
	// First byte: (19 << 1) = 0x26, second byte: 0x01
	hevcIDRAnnexB := []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA, 0xBB}
	wireData := codec.AnnexBToWireInto(hevcIDRAnnexB, nil)

	// VPS NALU body (type 32, first byte: 32<<1 = 0x40)
	vps := []byte{0x40, 0x01, 0xDE, 0xAD}
	// SPS NALU body (type 33, first byte: 33<<1 = 0x42)
	sps := []byte{0x42, 0x01, 0xBE, 0xEF}
	// PPS NALU body (type 34, first byte: 34<<1 = 0x44)
	pps := []byte{0x44, 0x01, 0xCA, 0xFE}

	frame := &media.VideoFrame{
		WireData:   wireData,
		IsKeyframe: true,
		Codec:      "h265",
		VPS:        vps,
		SPS:        sps,
		PPS:        pps,
		PTS:        90000,
		GroupID:    1,
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	time.Sleep(100 * time.Millisecond)

	close(sd.ch)
	<-sd.done

	receiveMu.Lock()
	defer receiveMu.Unlock()
	require.NotNil(t, received, "should have received a decoded frame")

	// Verify VPS was prepended.
	captured := capDec.getCaptured()
	require.NotEmpty(t, captured, "decoder should have been called")

	data := captured[0]
	// The data should start with VPS NALU (Annex B start code + VPS body).
	// Start code = 00 00 00 01, then VPS body starts with 0x40.
	require.True(t, len(data) > 4, "decoded data should be > 4 bytes")
	assert.Equal(t, byte(0x00), data[0])
	assert.Equal(t, byte(0x00), data[1])
	assert.Equal(t, byte(0x00), data[2])
	assert.Equal(t, byte(0x01), data[3])
	assert.Equal(t, byte(0x40), data[4], "first NALU should be VPS (type 32, byte=0x40)")
}

// TestSourceDecoder_H264_NoVPS_StillWorks verifies that H.264 frames without
// VPS continue to work correctly after the VPS fix.
func TestSourceDecoder_H264_NoVPS_StillWorks(t *testing.T) {
	w, h := 64, 64
	capDec := &naluCapturingDecoder{width: w, height: h}

	var received *ProcessingFrame
	var receiveMu sync.Mutex

	sd := &sourceDecoder{
		sourceKey: "h264-no-vps-test",
		decoder:   capDec,
		ch:        make(chan decoderInput, 2),
		callback: func(key string, pf *ProcessingFrame) {
			receiveMu.Lock()
			received = pf
			receiveMu.Unlock()
		},
		done: make(chan struct{}),
	}

	go sd.decodeLoop()

	// H.264 IDR NALU
	h264IDRAnnexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB}
	wireData := codec.AnnexBToWireInto(h264IDRAnnexB, nil)

	sps := []byte{0x67, 0x42, 0x00, 0x1E}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}

	frame := &media.VideoFrame{
		WireData:   wireData,
		IsKeyframe: true,
		Codec:      "h264",
		SPS:        sps,
		PPS:        pps,
		PTS:        90000,
		GroupID:    1,
	}

	sd.ch <- decoderInput{frame: frame, arrivalNano: time.Now().UnixNano()}

	time.Sleep(100 * time.Millisecond)

	close(sd.ch)
	<-sd.done

	receiveMu.Lock()
	defer receiveMu.Unlock()
	require.NotNil(t, received, "should have received a decoded frame")

	// Verify SPS was prepended (not VPS).
	captured := capDec.getCaptured()
	require.NotEmpty(t, captured)

	data := captured[0]
	require.True(t, len(data) > 4)
	assert.Equal(t, byte(0x00), data[0])
	assert.Equal(t, byte(0x00), data[1])
	assert.Equal(t, byte(0x00), data[2])
	assert.Equal(t, byte(0x01), data[3])
	assert.Equal(t, byte(0x67), data[4], "first NALU should be SPS (type 7, byte=0x67)")
}
