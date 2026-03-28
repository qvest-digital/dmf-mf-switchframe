//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeUniform422P10 creates a YUV422P10LE buffer with a uniform value.
// Layout: Y (w*h uint16) + Cb (w/2*h uint16) + Cr (w/2*h uint16).
func makeUniform422P10(w, h int, val uint16) []byte {
	buf := make([]byte, yuv10FrameSize(w, h))
	for i := 0; i < len(buf)/2; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], val)
	}
	return buf
}

func TestPreviewEncoder_P210Input_ProducesOutput(t *testing.T) {
	// Verify that the PreviewEncoder handles P210 input frames correctly,
	// dispatching through ScaleP210ToNV12On instead of ScaleBilinearOn.
	ctx := testContext(t)

	srcW, srcH := 64, 64
	dstW, dstH := 32, 32

	// Create a P210 source frame.
	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	// Fill with valid P210 data (10-bit value 400).
	require.NoError(t, FillColorP210(ctx, src, 400, 512, 512))

	// Create preview encoder (64x64 -> 32x32).
	enc, err := NewPreviewEncoder(ctx, srcW, srcH, dstW, dstH, 500000, 30, 1)
	require.NoError(t, err)
	defer enc.Close()

	// Encode P210 source frame — should use ScaleP210ToNV12On internally.
	// Hardware encoders may buffer the first few frames before producing output.
	var encoded int
	var totalBytes int
	for i := 0; i < 30; i++ {
		src.PTS = int64(i * 3000)
		data, _, err := enc.Encode(src, i == 0)
		require.NoError(t, err, "preview encode of P210 frame %d failed", i)
		if len(data) > 0 {
			encoded++
			totalBytes += len(data)
		}
	}

	assert.Greater(t, encoded, 0,
		"preview encoder should produce output from P210 input")
	t.Logf("P210 preview encode: %d/30 frames, %d total bytes", encoded, totalBytes)
}

func TestPreviewEncoder_P210Input_1080pTo480p(t *testing.T) {
	// Production-scale test: P210 1080p source to 480p preview.
	ctx := testContext(t)

	srcW, srcH := 1920, 1080
	dstW, dstH := 854, 480

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	// Fill with a mid-range color.
	require.NoError(t, FillColorP210(ctx, src, 512, 512, 512))

	enc, err := NewPreviewEncoder(ctx, srcW, srcH, dstW, dstH, 1_000_000, 30, 1)
	require.NoError(t, err)
	defer enc.Close()

	var encoded int
	for i := 0; i < 10; i++ {
		src.PTS = int64(i * 3000)
		data, _, err := enc.Encode(src, i == 0)
		require.NoError(t, err, "1080p P210 preview encode frame %d failed", i)
		if len(data) > 0 {
			encoded++
		}
	}

	assert.Greater(t, encoded, 0,
		"1080p P210 preview should produce at least one frame")
	t.Logf("P210 1080p->480p preview: %d/10 frames produced", encoded)
}

func TestScaleP210ToNV12On_UniformValue(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 64, 64
	dstW, dstH := 32, 32

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload uniform P210 with 10-bit value 512 (= 128 in 8-bit after >>2)
	srcData := makeUniform422P10(srcW, srcH, 512)
	require.NoError(t, Upload10(ctx, src, srcData, srcW, srcH))

	err = ScaleP210ToNV12On(ctx, dst, src, nil)
	require.NoError(t, err)

	dstYUV := make([]byte, yuvFrameSize(dstW, dstH))
	require.NoError(t, Download(ctx, dstYUV, dst, dstW, dstH))

	// Y values should be ~128 (512 >> 2)
	for i := 0; i < dstW*dstH; i++ {
		assert.InDelta(t, 128, int(dstYUV[i]), 2, "Y[%d] = %d, want ~128", i, dstYUV[i])
	}

	// U and V values should also be ~128
	uvStart := dstW * dstH
	for i := uvStart; i < len(dstYUV); i++ {
		assert.InDelta(t, 128, int(dstYUV[i]), 2, "UV[%d] = %d, want ~128", i-uvStart, dstYUV[i])
	}
}

func TestScaleP210ToNV12On_MaxValue(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 32, 32
	dstW, dstH := 16, 16

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Fill with 10-bit max value 1023 (should become 255 in 8-bit)
	srcData := makeUniform422P10(srcW, srcH, 1023)
	require.NoError(t, Upload10(ctx, src, srcData, srcW, srcH))

	require.NoError(t, ScaleP210ToNV12On(ctx, dst, src, nil))

	dstYUV := make([]byte, yuvFrameSize(dstW, dstH))
	require.NoError(t, Download(ctx, dstYUV, dst, dstW, dstH))

	for i := 0; i < len(dstYUV); i++ {
		assert.InDelta(t, 255, int(dstYUV[i]), 2, "dst[%d] = %d, want ~255", i, dstYUV[i])
	}
}

func TestScaleP210ToNV12On_ZeroValue(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 32, 32
	dstW, dstH := 16, 16

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Pre-fill NV12 dst with 0xFF to verify it gets overwritten
	fillData := make([]byte, yuvFrameSize(dstW, dstH))
	for i := range fillData {
		fillData[i] = 0xFF
	}
	require.NoError(t, Upload(ctx, dst, fillData, dstW, dstH))

	// P210 source is zero-filled
	srcData := makeUniform422P10(srcW, srcH, 0)
	require.NoError(t, Upload10(ctx, src, srcData, srcW, srcH))

	require.NoError(t, ScaleP210ToNV12On(ctx, dst, src, nil))

	dstYUV := make([]byte, yuvFrameSize(dstW, dstH))
	require.NoError(t, Download(ctx, dstYUV, dst, dstW, dstH))

	for i := 0; i < len(dstYUV); i++ {
		assert.Equal(t, byte(0), dstYUV[i], "dst[%d] = %d, want 0", i, dstYUV[i])
	}
}

func TestScaleP210ToNV12On_1080pTo480p(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 1920, 1080
	dstW, dstH := 854, 480

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Fill with 10-bit value 400 (8-bit 100)
	srcData := makeUniform422P10(srcW, srcH, 400)
	require.NoError(t, Upload10(ctx, src, srcData, srcW, srcH))

	require.NoError(t, ScaleP210ToNV12On(ctx, dst, src, nil))

	dstYUV := make([]byte, yuvFrameSize(dstW, dstH))
	require.NoError(t, Download(ctx, dstYUV, dst, dstW, dstH))

	// Spot-check Y values (should be 100 = 400 >> 2)
	for i := 0; i < 100; i++ {
		assert.InDelta(t, 100, int(dstYUV[i]), 2, "Y[%d] = %d, want ~100", i, dstYUV[i])
	}
	// Spot-check a UV value
	uvStart := dstW * dstH
	assert.InDelta(t, 100, int(dstYUV[uvStart]), 2, "U[0] = %d, want ~100", dstYUV[uvStart])
}

func TestScaleP210ToNV12On_ChromaSeparation(t *testing.T) {
	// Verify that Cb and Cr channels don't get mixed during cross-format scale.
	ctx := testContext(t)

	srcW, srcH := 64, 64
	dstW, dstH := 32, 32

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Create P210 with Y=512, Cb=200, Cr=800 (distinct Cb and Cr values)
	srcData := make([]byte, yuv10FrameSize(srcW, srcH))
	// Y plane: 512
	for i := 0; i < srcW*srcH; i++ {
		binary.LittleEndian.PutUint16(srcData[i*2:], 512)
	}
	// Cb plane: 200
	chromaW := srcW / 2
	cbOffset := srcW * srcH * 2
	for i := 0; i < chromaW*srcH; i++ {
		binary.LittleEndian.PutUint16(srcData[cbOffset+i*2:], 200)
	}
	// Cr plane: 800
	crOffset := cbOffset + chromaW*srcH*2
	for i := 0; i < chromaW*srcH; i++ {
		binary.LittleEndian.PutUint16(srcData[crOffset+i*2:], 800)
	}

	require.NoError(t, Upload10(ctx, src, srcData, srcW, srcH))

	require.NoError(t, ScaleP210ToNV12On(ctx, dst, src, nil))

	dstYUV := make([]byte, yuvFrameSize(dstW, dstH))
	require.NoError(t, Download(ctx, dstYUV, dst, dstW, dstH))

	// Cb should be ~50 (200>>2), Cr should be ~200 (800>>2).
	// If channels are mixed, both would converge toward ~125.
	dstChromaW := dstW / 2
	dstChromaH := dstH / 2
	cbStart := dstW * dstH
	crStart := cbStart + dstChromaW*dstChromaH

	cbMid := dstYUV[cbStart+dstChromaW*(dstChromaH/2)+dstChromaW/2]
	crMid := dstYUV[crStart+dstChromaW*(dstChromaH/2)+dstChromaW/2]
	assert.InDelta(t, 50, int(cbMid), 5, "Cb should be ~50 after scale, got %d", cbMid)
	assert.InDelta(t, 200, int(crMid), 5, "Cr should be ~200 after scale, got %d", crMid)

	t.Logf("Chroma separation: Cb=%d (want ~50), Cr=%d (want ~200)", cbMid, crMid)
}

func TestScaleP210ToNV12On_NilArgs(t *testing.T) {
	err := ScaleP210ToNV12On(nil, nil, nil, nil)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}
