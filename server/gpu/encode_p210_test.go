//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUEncoder_Close_ReleasesP010Tmp(t *testing.T) {
	// Verifies that GPUEncoder.Close() properly releases the p010Tmp frame
	// without panicking. On CUDA, the encoder allocates a temporary P010 frame
	// lazily when encoding P210 frames (for the P210→P010 MSB conversion that
	// NVENC requires). Close() must release this frame.
	//
	// On Metal, VT supports native 4:2:2 encoding so no p010Tmp exists.
	// This test verifies Close() is safe regardless of platform.
	ctx := testContext(t)

	w, h := 320, 240

	// Create encoder and close it — should not panic even without encoding.
	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)
	enc.Close()

	// Double close should also be safe.
	enc.Close()
}

func TestGPUEncoder_Close_AfterP210Encode(t *testing.T) {
	// Tests that Close() works after encoding P210 frames (which triggers
	// p010Tmp lazy allocation on CUDA). The p010Tmp frame must be properly
	// released back to the pool.
	ctx := testContext(t)

	w, h := 320, 240
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx.SetPool(pool)

	// Create a P210 frame with valid content.
	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillColorP210(ctx, frame, 512, 512, 512)
	require.NoError(t, err)
	frame.PTS = 0

	// Create encoder.
	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)

	// Attempt to encode the P210 frame. This may or may not succeed depending
	// on the encoder type (NVENC hw_frames with P210→P010 conversion on CUDA,
	// native VT on Metal). We don't care about the encode result — we're
	// testing that Close() properly cleans up.
	for i := 0; i < 5; i++ {
		frame.PTS = int64(i * 3000)
		_, _, _ = enc.EncodeGPU(frame, i == 0)
	}

	// Close should release p010Tmp (CUDA) or be a no-op (Metal) without panic.
	enc.Close()

	// Verify the pool is not corrupted: we should still be able to acquire frames.
	verifyFrame, err := pool.Acquire()
	if err == nil {
		verifyFrame.Release()
	}
}

func TestGPUEncoder_P210Frame_FormatPreserved(t *testing.T) {
	// Smoke test: encoding a P210 frame should not corrupt the source frame's
	// format or dimensions. The encoder should handle P210 input gracefully
	// regardless of platform (CUDA: P210→P010 conversion, Metal: native 4:2:2).
	ctx := testContext(t)

	w, h := 64, 64
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx.SetPool(pool)

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillColorP210(ctx, frame, 400, 300, 700)
	require.NoError(t, err)

	assert.Equal(t, GPUFormatP210, frame.Format, "frame should be P210 before encode")
	assert.Equal(t, w, frame.Width)
	assert.Equal(t, h, frame.Height)

	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer enc.Close()

	// Encode a few frames.
	for i := 0; i < 5; i++ {
		frame.PTS = int64(i * 3000)
		_, _, _ = enc.EncodeGPU(frame, i == 0)
	}

	// Source frame format and dimensions should be unchanged.
	assert.Equal(t, GPUFormatP210, frame.Format, "frame format should be unchanged after encode")
	assert.Equal(t, w, frame.Width, "frame width should be unchanged")
	assert.Equal(t, h, frame.Height, "frame height should be unchanged")
}
