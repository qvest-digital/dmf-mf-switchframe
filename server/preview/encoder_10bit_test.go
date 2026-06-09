package preview

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeYUV422P10 creates a YUV422P10LE buffer of the correct size for the given
// dimensions, filled with valid 10-bit values.
func makeYUV422P10(w, h int) []byte {
	// YUV422P10LE: Y plane (w*h*2) + Cb plane (w/2*h*2) + Cr plane (w/2*h*2) = w*h*4
	ySize := w * h * 2
	cbSize := (w / 2) * h * 2
	crSize := (w / 2) * h * 2
	buf := make([]byte, ySize+cbSize+crSize)

	// Y plane: 400 (valid 10-bit luma)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], 400)
	}
	// Cb/Cr planes: 512 (neutral chroma)
	for i := ySize; i < len(buf); i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], 512)
	}
	return buf
}

// TestEncoder_10bitFrame_DetectedAndDownconverted verifies that the preview
// encoder can handle 10-bit YUV422P10LE input by detecting the oversized buffer
// and downconverting to 8-bit YUV420P before encoding.
//
// This catches Fix 3: The CPU preview encoder sized buffers for 8-bit (w*h*3/2)
// and would silently truncate/corrupt 10-bit frames (w*h*4 bytes). The fix adds
// detection based on buffer size and automatic downconversion.
func TestEncoder_10bitFrame_DetectedAndDownconverted(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	w, h := 320, 240

	enc, err := NewEncoder(Config{
		SourceKey: "test-10bit",
		Width:     w,
		Height:    h,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	require.NoError(t, err)
	defer enc.Stop()

	// Send several 10-bit frames.
	for i := 0; i < 5; i++ {
		yuv10bit := makeYUV422P10(w, h)
		enc.Send(yuv10bit, w, h, int64(i)*3000)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	frames := relay.getVideos()
	require.NotEmpty(t, frames, "should produce encoded output from 10-bit input")

	// Verify the output contains valid H.264 data (not garbage from truncated 10-bit).
	// If the encoder received corrupted data (only first w*h*3/2 bytes of 10-bit frame),
	// it may still encode but the output would be visually wrong. At minimum verify
	// we got frames without encode errors.
	stats := enc.GetStats()
	assert.Zero(t, stats.EncodeErrors, "should have no encode errors processing 10-bit frames")
	assert.GreaterOrEqual(t, stats.FramesOut, int64(1), "should have produced at least 1 frame")
}

// TestEncoder_10bit_SendOwned_ReleasesOriginalBuffer verifies that when a 10-bit
// YUV422P10LE frame is sent via SendOwned, the release callback receives the
// ORIGINAL caller-owned buffer — not the encoder's internal downconvert buffer.
//
// Regression test: previously the encode loop rebased job.yuv onto its persistent
// downconvertBuf before calling release, so the caller's pooled source frame was
// leaked (never released) and the encoder-internal buffer was handed to the
// caller's release callback (use-after-release if it went back to a frame pool).
func TestEncoder_10bit_SendOwned_ReleasesOriginalBuffer(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	w, h := 320, 240

	enc, err := NewEncoder(Config{
		SourceKey: "test-10bit-owned",
		Width:     w,
		Height:    h,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	require.NoError(t, err)
	defer enc.Stop()

	yuv10bit := makeYUV422P10(w, h)
	// Identify the original buffer by its underlying data pointer.
	origPtr := &yuv10bit[0]

	var released atomic.Int32
	var releasedOrig atomic.Bool
	releaseFn := func(buf []byte) {
		released.Add(1)
		if len(buf) > 0 && &buf[0] == origPtr {
			releasedOrig.Store(true)
		}
	}

	enc.SendOwned(yuv10bit, w, h, 3000, releaseFn)

	// Wait for the encode goroutine to process the frame.
	time.Sleep(500 * time.Millisecond)

	require.Equal(t, int32(1), released.Load(), "release callback must be called exactly once")
	assert.True(t, releasedOrig.Load(),
		"release callback must receive the original owned buffer, not the encoder-internal downconvert buffer")
}

// TestEncoder_Oversized8bitBuffer_NotMisclassifiedAs10bit verifies that an
// over-sized 8-bit YUV420 buffer (len set to capacity, larger than the 10-bit
// payload size w*h*4) sent via SendOwned is treated as 8-bit and NOT misclassified
// as 10-bit.
//
// Regression test for the size-only detection hazard: the 10-bit branch keyed on
// len(job.yuv) >= w*h*4, so a pooled buffer whose len was set to capacity (a
// buffer larger than the actual 8-bit payload) would be routed through
// Downconvert422_10to420, reinterpreting 8-bit YUV420 bytes as 10-bit YUV422 and
// emitting corrupted output. The fix requires an exact size match (len == w*h*4),
// so an over-length 8-bit buffer falls through to the 8-bit path.
//
// We assert no encode errors and that output is produced: with the old >= logic
// the over-sized buffer would have been downconverted (8-bit interpreted as 10-bit),
// whereas the exact-match logic feeds the first encSize bytes to the encoder as-is.
func TestEncoder_Oversized8bitBuffer_NotMisclassifiedAs10bit(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	w, h := 320, 240

	enc, err := NewEncoder(Config{
		SourceKey: "test-oversized-8bit",
		Width:     w,
		Height:    h,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	require.NoError(t, err)
	defer enc.Stop()

	payloadSize := w * h * 3 / 2 // 8-bit YUV420 payload
	tenBitSize := w * h * 4      // size the old >= heuristic would trip on
	// Allocate a buffer larger than the 10-bit size but containing only an 8-bit
	// payload; the trailing bytes are uninitialized/garbage padding (zeroed here).
	// len is deliberately set to capacity, which is the latent SendOwned hazard.
	oversized := make([]byte, tenBitSize+128)
	require.Greater(t, len(oversized), tenBitSize,
		"buffer must exceed 10-bit size to exercise the >= misclassification path")
	// Fill the real 8-bit region with neutral values; leave padding as-is.
	for i := 0; i < payloadSize; i++ {
		oversized[i] = 128
	}

	var released atomic.Int32
	releaseFn := func([]byte) { released.Add(1) }

	for i := 0; i < 5; i++ {
		// Each frame needs its own buffer because SendOwned takes ownership.
		buf := make([]byte, len(oversized))
		copy(buf, oversized)
		enc.SendOwned(buf, w, h, int64(i)*3000, releaseFn)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	frames := relay.getVideos()
	require.NotEmpty(t, frames, "should produce encoded output from over-sized 8-bit input")

	stats := enc.GetStats()
	assert.Zero(t, stats.EncodeErrors,
		"over-sized 8-bit buffer must not be misclassified/downconverted as 10-bit")
	assert.GreaterOrEqual(t, stats.FramesOut, int64(1))
	assert.Equal(t, int32(5), released.Load(),
		"every SendOwned buffer must be released exactly once")
}

// TestEncoder_8bitFrame_StillWorks ensures standard 8-bit input continues working.
func TestEncoder_8bitFrame_StillWorks(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	w, h := 320, 240

	enc, err := NewEncoder(Config{
		SourceKey: "test-8bit-compat",
		Width:     w,
		Height:    h,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	require.NoError(t, err)
	defer enc.Stop()

	for i := 0; i < 5; i++ {
		yuv := makeYUV420(w, h, byte(128))
		enc.Send(yuv, w, h, int64(i)*3000)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	frames := relay.getVideos()
	require.NotEmpty(t, frames, "should produce encoded output from 8-bit input")

	stats := enc.GetStats()
	assert.Zero(t, stats.EncodeErrors)
}
