//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestFFmpegDecoderCreate(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestFFmpegDecoderDoubleClose(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	dec.Close()
	dec.Close() // should not panic
}

func TestFFmpegDecoderEmptyInput(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// nil input
	_, _, _, err = dec.Decode(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")

	// empty slice
	_, _, _, err = dec.Decode([]byte{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestFFmpegDecoderCorruptedInput(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// Random bytes should produce an error, not a crash.
	garbage := make([]byte, 256)
	for i := range garbage {
		garbage[i] = byte(i * 37 % 256)
	}
	_, _, _, err = dec.Decode(garbage)
	require.Error(t, err)
}

func TestFFmpegDecoderClosedDecode(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	dec.Close()

	_, _, _, err = dec.Decode([]byte{0x00, 0x00, 0x00, 0x01, 0x65})
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
}

func TestFFmpegDecoderInterface(t *testing.T) {
	// Verify FFmpegDecoder implements transition.VideoDecoder.
	var dec transition.VideoDecoder
	d, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	dec = d
	require.NotNil(t, dec)
	dec.Close()
}

func TestFFmpegEncodeDecodeRoundTrip(t *testing.T) {
	w, h := 320, 240

	enc, err := NewFFmpegEncoder("libx264", w, h, 500000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// Build a YUV420 frame with a recognizable pattern.
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = byte((i * 7) % 256)
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Without zerolatency, the encoder may buffer initial frames
	// (frame-level threading fills the pipeline before producing output).
	// Feed frames until we get encoded output.
	var encoded []byte
	for i := 0; i < 30; i++ {
		forceIDR := i == 0
		encoded, _, err = enc.Encode(yuv, int64(i*3000), forceIDR)
		require.NoError(t, err)
		if encoded != nil {
			break
		}
	}
	require.NotEmpty(t, encoded, "encoder should produce output within 30 frames")

	// Decode it back. With multi-threaded decode, the decoder may need
	// a few packets before producing output. Feed remaining encoded frames.
	var decoded []byte
	var dw, dh int
	decoded, dw, dh, err = dec.Decode(encoded)
	if err != nil {
		// Decoder is buffering — feed more frames to flush it.
		for i := 0; i < 30; i++ {
			var moreEncoded []byte
			moreEncoded, _, err = enc.Encode(yuv, int64((30+i)*3000), false)
			require.NoError(t, err)
			if moreEncoded == nil {
				continue
			}
			decoded, dw, dh, err = dec.Decode(moreEncoded)
			if err == nil {
				break
			}
		}
	}
	require.NoError(t, err, "decoder should produce output")
	require.Equal(t, w, dw)
	require.Equal(t, h, dh)
	require.Equal(t, ySize+2*uvSize, len(decoded))
}

func TestFFmpegMultiFrameDecodeSequence(t *testing.T) {
	w, h := 160, 120

	enc, err := NewFFmpegEncoder("libx264", w, h, 200000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	successCount := 0
	for i := 0; i < 60; i++ {
		// Vary Y plane each frame.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*13) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		forceIDR := i == 0
		encoded, _, err := enc.Encode(yuv, int64(i*3000), forceIDR)
		require.NoError(t, err, "encode frame %d", i)
		// Without zerolatency, initial frames may return nil (EAGAIN).
		if encoded == nil {
			continue
		}

		decoded, dw, dh, err := dec.Decode(encoded)
		if err == nil {
			require.Equal(t, w, dw, "frame %d width", i)
			require.Equal(t, h, dh, "frame %d height", i)
			require.Equal(t, ySize+2*uvSize, len(decoded), "frame %d YUV size", i)
			successCount++
		}
	}

	// At least some frames should decode successfully.
	require.Greater(t, successCount, 0, "at least one frame should decode successfully")
}

func TestFFmpegDecoderPacketReuse(t *testing.T) {
	// Regression test: the decoder reuses a single AVPacket across Decode() calls.
	// av_packet_unref must be called before each reuse to free side_data and buf
	// references from the previous call. Without it, side_data accumulates and
	// the packet's internal buffers leak. This test decodes 200 frames through
	// a single decoder instance — enough iterations to surface memory issues
	// from missing cleanup under AddressSanitizer or valgrind, and to verify
	// correct output under the race detector.
	w, h := 160, 120

	enc, err := NewFFmpegEncoder("libx264", w, h, 200000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize
	yuv := make([]byte, frameSize)

	const totalFrames = 200
	successCount := 0

	for i := 0; i < totalFrames; i++ {
		// Vary the pattern each frame to generate different encoded payloads.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*3 + i*17) % 256)
		}
		for j := ySize; j < frameSize; j++ {
			yuv[j] = byte(128 + (i % 10))
		}

		forceIDR := (i % 30) == 0 // periodic IDR frames
		encoded, _, encErr := enc.Encode(yuv, int64(i*3000), forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if encoded == nil {
			continue
		}

		decoded, dw, dh, decErr := dec.Decode(encoded)
		if decErr == nil {
			require.Equal(t, w, dw, "frame %d width", i)
			require.Equal(t, h, dh, "frame %d height", i)
			require.Equal(t, frameSize, len(decoded), "frame %d YUV size", i)
			successCount++
		}
	}

	// With 200 frames and periodic IDRs, we should decode many frames.
	require.Greater(t, successCount, 10,
		"expected many decoded frames from %d encoded frames", totalFrames)
}

func TestFFmpegDecoderBufferAliasing(t *testing.T) {
	// Regression test: adoptOrCopy must return a deep copy, not an alias
	// of the decoder's internal yuvBuf. If the returned slice aliases
	// yuvBuf, decoding a second frame overwrites the first frame's data.
	w, h := 160, 120

	// Use single-threaded encoder+decoder so each Encode produces output
	// and each Decode produces output without extra buffering.
	enc, err := NewFFmpegEncoder("libx264", w, h, 200000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize

	// Build two visually distinct YUV frames.
	yuvA := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		yuvA[i] = 32 // dark Y
	}
	for i := ySize; i < frameSize; i++ {
		yuvA[i] = 128
	}

	yuvB := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		yuvB[i] = 220 // bright Y
	}
	for i := ySize; i < frameSize; i++ {
		yuvB[i] = 128
	}

	// Encode enough frames to prime the encoder pipeline, then encode our
	// two distinct frames as IDRs so each decode produces immediate output.
	// Prime with frame A.
	var encodedFrames [][]byte
	for i := 0; i < 30; i++ {
		encoded, _, encErr := enc.Encode(yuvA, int64(i*3000), i == 0)
		require.NoError(t, encErr)
		if encoded != nil {
			encodedFrames = append(encodedFrames, append([]byte(nil), encoded...))
		}
	}
	// Encode frame B as IDR.
	encodedB, _, err := enc.Encode(yuvB, int64(30*3000), true)
	require.NoError(t, err)
	if encodedB != nil {
		encodedFrames = append(encodedFrames, append([]byte(nil), encodedB...))
	}

	require.GreaterOrEqual(t, len(encodedFrames), 2,
		"need at least 2 encoded frames for aliasing test")

	// Decode all frames, keeping references to the last two decoded outputs.
	var decodedPrev, decodedCurr []byte
	for _, ef := range encodedFrames {
		decoded, dw, dh, decErr := dec.Decode(ef)
		if decErr != nil {
			continue // buffering
		}
		require.Equal(t, w, dw)
		require.Equal(t, h, dh)
		decodedPrev = decodedCurr
		decodedCurr = decoded
	}

	require.NotNil(t, decodedPrev, "need at least two decoded frames")
	require.NotNil(t, decodedCurr, "need at least two decoded frames")

	// The bug: if adoptOrCopy returns an alias, decodedPrev and decodedCurr
	// point to the same underlying buffer. Both would contain the last
	// decoded frame's data (Y≈220), making their luminance identical.
	// With the fix, each decode returns an independent copy.
	// Verify the two frames have different Y data (32 vs 220).
	var sumPrev, sumCurr int64
	for i := 0; i < ySize; i++ {
		sumPrev += int64(decodedPrev[i])
		sumCurr += int64(decodedCurr[i])
	}
	avgPrev := float64(sumPrev) / float64(ySize)
	avgCurr := float64(sumCurr) / float64(ySize)

	// The dark frame (Y=32) and bright frame (Y=220) should produce
	// noticeably different averages even after lossy encode/decode.
	require.Greater(t, avgCurr-avgPrev, 50.0,
		"decoded frames should have different luminance (prev=%.1f, curr=%.1f)", avgPrev, avgCurr)
}
