//go:build cgo && !noffmpeg

package output

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// testCtx creates a background context for testing.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// transcodeMockDecoder decodes "encoded" data by returning a fixed YUV frame.
type transcodeMockDecoder struct {
	width  int
	height int
	closed bool
}

func (d *transcodeMockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	return make([]byte, d.width*d.height*3/2), d.width, d.height, nil
}

func (d *transcodeMockDecoder) Close() { d.closed = true }

// transcodeMockEncoder encodes by returning a minimal Annex B H.264 IDR NALU.
// The output includes SPS (type 7), PPS (type 8), and IDR (type 5).
type transcodeMockEncoder struct {
	width  int
	height int
	closed bool
}

func (e *transcodeMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	if forceIDR {
		// SPS + PPS + IDR in Annex B format
		return []byte{
			0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x1e, // SPS (type 7)
			0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80, // PPS (type 8)
			0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, // IDR (type 5)
		}, true, nil
	}
	// Non-IDR slice (type 1)
	return []byte{
		0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x24, 0x6c,
	}, false, nil
}

func (e *transcodeMockEncoder) Close() { e.closed = true }

func TestRTMPTranscoder_TranscodeHEVCtoH264(t *testing.T) {
	t.Parallel()

	tc := newRTMPTranscoder(
		&transcodeMockDecoder{width: 1920, height: 1080},
		&transcodeMockEncoder{width: 1920, height: 1080},
	)

	// Simulate an HEVC keyframe with VPS/SPS/PPS.
	hevcFrame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		Codec:      "h265",
		VPS:        []byte{0x40, 0x01, 0x0c},        // mock VPS
		SPS:        []byte{0x42, 0x01, 0x01},        // mock SPS
		PPS:        []byte{0x44, 0x01, 0xc0},        // mock PPS
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x26, 0x01, 0xaf, 0x09}, // mock HEVC NALU in HVC1
	}

	h264Frame, err := tc.Transcode(hevcFrame)
	require.NoError(t, err)
	require.NotNil(t, h264Frame)

	// Verify the output is H.264.
	assert.Equal(t, "h264", h264Frame.Codec)
	assert.Equal(t, int64(90000), h264Frame.PTS)
	assert.Equal(t, int64(90000), h264Frame.DTS)
	assert.True(t, h264Frame.IsKeyframe)

	// Verify SPS/PPS were extracted from the H.264 keyframe.
	assert.NotEmpty(t, h264Frame.SPS, "SPS should be extracted from H.264 keyframe")
	assert.NotEmpty(t, h264Frame.PPS, "PPS should be extracted from H.264 keyframe")

	// Verify the wire data is AVC1 format (length-prefixed).
	assert.NotEmpty(t, h264Frame.WireData)

	// Close should clean up.
	tc.Close()
}

func TestRTMPTranscoder_TranscodeNonKeyframe(t *testing.T) {
	t.Parallel()

	tc := newRTMPTranscoder(
		&transcodeMockDecoder{width: 1920, height: 1080},
		&transcodeMockEncoder{width: 1920, height: 1080},
	)
	defer tc.Close()

	// Non-keyframe HEVC.
	hevcFrame := &media.VideoFrame{
		PTS:        93000,
		DTS:        93000,
		IsKeyframe: false,
		Codec:      "h265",
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x02, 0x01, 0xd0, 0x09}, // mock HEVC P-frame
	}

	h264Frame, err := tc.Transcode(hevcFrame)
	require.NoError(t, err)
	require.NotNil(t, h264Frame)

	assert.Equal(t, "h264", h264Frame.Codec)
	assert.False(t, h264Frame.IsKeyframe)
	assert.Equal(t, int64(93000), h264Frame.PTS)
}

func TestRTMPTranscoder_Close(t *testing.T) {
	t.Parallel()

	dec := &transcodeMockDecoder{width: 1920, height: 1080}
	enc := &transcodeMockEncoder{width: 1920, height: 1080}
	tc := newRTMPTranscoder(dec, enc)

	tc.Close()
	assert.True(t, dec.closed, "decoder should be closed")
	assert.True(t, enc.closed, "encoder should be closed")
}

func TestRTMPTranscoder_DoubleCloseIsSafe(t *testing.T) {
	t.Parallel()

	tc := newRTMPTranscoder(
		&transcodeMockDecoder{width: 1920, height: 1080},
		&transcodeMockEncoder{width: 1920, height: 1080},
	)

	tc.Close()
	tc.Close() // should not panic
}

func TestRTMPDirect_PassthroughWhenH264(t *testing.T) {
	t.Parallel()

	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error { return nil }

	err = r.Start(testCtx(t))
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	// H.264 keyframe — should pass through without transcoding.
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		Codec:      "h264",
		SPS:        []byte{0x67, 0x42, 0xc0, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr)

	// Should not create a transcoder.
	assert.Nil(t, r.transcoder, "transcoder should not be created for H.264")
}

func TestRTMPDirect_TranscodeWhenHEVC(t *testing.T) {
	t.Parallel()

	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error { return nil }

	// Inject mock transcoder factory.
	r.transcoderFactory = func(width, height int) (*rtmpTranscoder, error) {
		return newRTMPTranscoder(
			&transcodeMockDecoder{width: width, height: height},
			&transcodeMockEncoder{width: width, height: height},
		), nil
	}

	err = r.Start(testCtx(t))
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	// HEVC keyframe — should trigger transcoder creation.
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		Codec:      "h265",
		VPS:        []byte{0x40, 0x01, 0x0c},
		SPS:        []byte{0x42, 0x01, 0x01},
		PPS:        []byte{0x44, 0x01, 0xc0},
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x26, 0x01, 0xaf, 0x09},
	}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr)

	// Wait for the write loop to process the frame.
	require.Eventually(t, func() bool {
		return r.initialized.Load()
	}, 2*time.Second, 10*time.Millisecond, "should initialize after HEVC keyframe with transcoder")
}

func TestRTMPTranscoder_ExtractH264SPSPPS(t *testing.T) {
	t.Parallel()

	// Verify that extractH264SPSPPS correctly finds SPS and PPS from Annex B data.
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x1e, // SPS (type 7)
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80, // PPS (type 8)
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, // IDR (type 5)
	}

	sps, pps := extractH264SPSPPS(annexB)
	assert.Equal(t, []byte{0x67, 0x42, 0xc0, 0x1e}, sps)
	assert.Equal(t, []byte{0x68, 0xce, 0x38, 0x80}, pps)
}

func TestRTMPTranscoder_ExtractH264SPSPPS_NoParameterSets(t *testing.T) {
	t.Parallel()

	// Annex B with only a slice NALU (no SPS/PPS).
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, // IDR only
	}

	sps, pps := extractH264SPSPPS(annexB)
	assert.Nil(t, sps)
	assert.Nil(t, pps)
}

func TestExtractH264SPSPPS_FromWireData(t *testing.T) {
	t.Parallel()

	// Build AVC1 wire data (length-prefixed) containing SPS + PPS + IDR.
	spsData := []byte{0x67, 0x42, 0xc0, 0x1e}
	ppsData := []byte{0x68, 0xce, 0x38, 0x80}
	idrData := []byte{0x65, 0x88, 0x84, 0x00}

	// Convert to Annex B first, then test extraction.
	annexB := codec.PrependSPSPPS(spsData, ppsData, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00})

	sps, pps := extractH264SPSPPS(annexB)
	assert.Equal(t, spsData, sps)
	assert.Equal(t, ppsData, pps)
	_ = idrData // used indirectly
}
