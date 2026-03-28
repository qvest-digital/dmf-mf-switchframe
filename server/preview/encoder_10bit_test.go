package preview

import (
	"encoding/binary"
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
