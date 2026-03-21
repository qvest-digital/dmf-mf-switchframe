//go:build cgo && cuda && tensorrt

package gpu

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const defaultModelPath = "/opt/switchframe/models/u2netp.onnx"

func segModelPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SEGMENTATION_MODEL_PATH")
	if path == "" {
		path = defaultModelPath
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("segmentation model not found at %s (set SEGMENTATION_MODEL_PATH)", path)
	}
	return path
}

// makeTestFrame creates a gray YUV420p frame and uploads it to the GPU pool.
func makeTestFrame(t *testing.T, ctx *Context, pool *FramePool, w, h int) *GPUFrame {
	t.Helper()

	frame, err := pool.Acquire()
	require.NoError(t, err)

	// Build a gray YUV420p frame: Y=128, Cb=128, Cr=128.
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = 128
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	err = Upload(ctx, frame, yuv, w, h)
	require.NoError(t, err)

	return frame
}

func TestSegmentationEngineCreate(t *testing.T) {
	modelPath := segModelPath(t)

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	se, err := NewSegmentationEngine(ctx, modelPath)
	require.NoError(t, err)
	require.NotNil(t, se)
	defer se.Close()

	// Verify the engine has the expected input/output sizes for u2netp.
	assert.Equal(t, 1*3*320*320, se.engine.InputSize(), "input size should be [1,3,320,320]")
	assert.Equal(t, 1*1*320*320, se.engine.OutputSize(), "output size should be [1,1,320,320]")
}

func TestSegmentationEngineCreateNilCtx(t *testing.T) {
	se, err := NewSegmentationEngine(nil, "/some/model.onnx")
	require.ErrorIs(t, err, ErrGPUNotAvailable)
	assert.Nil(t, se)
}

func TestSegmentationEngineCreateEmptyPath(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	se, err := NewSegmentationEngine(ctx, "")
	require.Error(t, err)
	assert.Nil(t, se)
	assert.Contains(t, err.Error(), "modelPath is empty")
}

func TestSegmentationEngineCreateMissingModel(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	se, err := NewSegmentationEngine(ctx, "/nonexistent/model.onnx")
	require.Error(t, err)
	assert.Nil(t, se)
	assert.Contains(t, err.Error(), "ONNX file not found")
}

func TestSegmentationEngineSession(t *testing.T) {
	modelPath := segModelPath(t)

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	require.NoError(t, err)
	defer pool.Close()

	se, err := NewSegmentationEngine(ctx, modelPath)
	require.NoError(t, err)
	defer se.Close()

	// Enable source.
	err = se.EnableSource("cam1", 1920, 1080, 0.5)
	require.NoError(t, err)
	assert.True(t, se.IsEnabled("cam1"))

	// Upload a test frame and run segmentation.
	frame := makeTestFrame(t, ctx, pool, 1920, 1080)
	defer frame.Release()

	se.Segment("cam1", frame)

	// Verify mask is non-nil.
	mask := se.MaskForSource("cam1")
	require.NotNil(t, mask, "mask should be non-nil after segmentation")
	assert.Equal(t, 1920, mask.Width)
	assert.Equal(t, 1080, mask.Height)
}

func TestSegmentationEngineMaskForSource(t *testing.T) {
	modelPath := segModelPath(t)

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	require.NoError(t, err)
	defer pool.Close()

	se, err := NewSegmentationEngine(ctx, modelPath)
	require.NoError(t, err)
	defer se.Close()

	// MaskForSource on non-existent source returns nil.
	assert.Nil(t, se.MaskForSource("nonexistent"))

	// Enable source but don't segment yet — no mask yet.
	err = se.EnableSource("cam1", 1920, 1080, 0.0)
	require.NoError(t, err)
	assert.Nil(t, se.MaskForSource("cam1"))

	// Segment a frame.
	frame := makeTestFrame(t, ctx, pool, 1920, 1080)
	defer frame.Release()

	se.Segment("cam1", frame)

	// Now mask should be available.
	mask := se.MaskForSource("cam1")
	require.NotNil(t, mask, "mask should be non-nil after first segment")
	assert.Equal(t, 1920, mask.Width)
	assert.Equal(t, 1080, mask.Height)

	// Segment again — mask should still be available (updated in place).
	se.Segment("cam1", frame)
	mask2 := se.MaskForSource("cam1")
	require.NotNil(t, mask2, "mask should still be non-nil after second segment")
}

func TestSegmentationEngineDisable(t *testing.T) {
	modelPath := segModelPath(t)

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	se, err := NewSegmentationEngine(ctx, modelPath)
	require.NoError(t, err)
	defer se.Close()

	// Enable and verify.
	err = se.EnableSource("cam1", 1920, 1080, 0.5)
	require.NoError(t, err)
	assert.True(t, se.IsEnabled("cam1"))

	// Disable and verify.
	se.DisableSource("cam1")
	assert.False(t, se.IsEnabled("cam1"))
	assert.Nil(t, se.MaskForSource("cam1"))

	// Double disable should not panic.
	se.DisableSource("cam1")
}

func TestSegmentationEngineNilSafe(t *testing.T) {
	// All methods should be nil-safe on a nil engine.
	var se *SegmentationEngine

	assert.False(t, se.IsEnabled("cam1"))
	assert.Nil(t, se.MaskForSource("cam1"))
	se.Segment("cam1", nil)
	se.DisableSource("cam1")
	se.Close()
}

func TestSegmentationEngineEnableDisableMultipleSources(t *testing.T) {
	modelPath := segModelPath(t)

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	se, err := NewSegmentationEngine(ctx, modelPath)
	require.NoError(t, err)
	defer se.Close()

	// Enable multiple sources.
	require.NoError(t, se.EnableSource("cam1", 1920, 1080, 0.5))
	require.NoError(t, se.EnableSource("cam2", 1280, 720, 0.3))

	assert.True(t, se.IsEnabled("cam1"))
	assert.True(t, se.IsEnabled("cam2"))

	// Disable one.
	se.DisableSource("cam1")
	assert.False(t, se.IsEnabled("cam1"))
	assert.True(t, se.IsEnabled("cam2"))

	// Close cleans up remaining.
	se.Close()
	assert.False(t, se.IsEnabled("cam2"))
}

func TestSegmentationEngineReEnable(t *testing.T) {
	modelPath := segModelPath(t)

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	se, err := NewSegmentationEngine(ctx, modelPath)
	require.NoError(t, err)
	defer se.Close()

	// Enable, then re-enable with different params — should replace cleanly.
	require.NoError(t, se.EnableSource("cam1", 1920, 1080, 0.5))
	require.NoError(t, se.EnableSource("cam1", 1280, 720, 0.3))

	assert.True(t, se.IsEnabled("cam1"))

	// The session should have the new dimensions.
	se.mu.RLock()
	sess := se.sessions["cam1"]
	se.mu.RUnlock()
	require.NotNil(t, sess)
	assert.Equal(t, 1280, sess.srcW)
	assert.Equal(t, 720, sess.srcH)
}
