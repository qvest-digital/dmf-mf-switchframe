//go:build darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

func TestGPUUploadDownloadBridgeRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	upload := NewUploadNode(ctx, pool)
	download := NewDownloadNode(ctx)

	// Verify interface compliance.
	assert.Equal(t, "gpu-upload", upload.Name())
	assert.Equal(t, "gpu-download", download.Name())
	assert.True(t, upload.Active())
	assert.True(t, download.Active())

	// Configure with a PipelineFormat.
	format := switcher.PipelineFormat{Width: w, Height: h, FPSNum: 30, FPSDen: 1}
	require.NoError(t, upload.Configure(format))
	require.NoError(t, download.Configure(format))

	// Create a test frame with a known Y pattern.
	yuvSize := w*h + w*h/4 + w*h/4
	yuv := make([]byte, yuvSize)
	for i := 0; i < w*h; i++ {
		yuv[i] = 180 // Y plane
	}
	for i := w * h; i < yuvSize; i++ {
		yuv[i] = 128 // Cb/Cr planes
	}

	frame := &switcher.ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
		PTS:    90000,
	}

	// Upload: CPU YUV420p → GPU NV12.
	result := upload.Process(nil, frame)
	require.NotNil(t, result)
	require.NotNil(t, result.GPUData, "GPUData should be set after upload")

	gpuFrame, ok := result.GPUData.(*GPUFrame)
	require.True(t, ok, "GPUData should be *GPUFrame")
	assert.Equal(t, int64(90000), gpuFrame.PTS)

	// Download: GPU NV12 → CPU YUV420p.
	result = download.Process(nil, result)
	require.NotNil(t, result)
	assert.Nil(t, result.GPUData, "GPUData should be nil after download")

	// Verify Y plane center pixel is approximately correct (NV12 round-trip).
	centerIdx := h/2*w + w/2
	assert.InDelta(t, 180, int(result.YUV[centerIdx]), 2,
		"Y value should survive upload/download round-trip")
}

func TestGPUUploadFallbackOnNilPool(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// Upload with nil pool should still work (fails gracefully).
	upload := NewUploadNode(ctx, nil)
	assert.False(t, upload.Active(), "upload with nil pool should be inactive")
}

func TestGPUDownloadNoGPUData(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	download := NewDownloadNode(ctx)

	// Download with no GPUData should pass through.
	frame := &switcher.ProcessingFrame{
		YUV:    make([]byte, 320*240*3/2),
		Width:  320,
		Height: 240,
	}

	result := download.Process(nil, frame)
	require.NotNil(t, result)
	assert.Same(t, frame, result, "should pass through when no GPUData")
}

func TestGPUBridgeNodeNames(t *testing.T) {
	tests := []struct {
		node switcher.PipelineNode
		name string
	}{
		{NewKeyNode(), "gpu-key"},
		{NewLayoutNode(), "gpu-layout"},
		{NewCompositorNode(), "gpu-compositor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, tt.node.Name())
			assert.False(t, tt.node.Active(), "placeholder nodes should be inactive")
			assert.NoError(t, tt.node.Close())
		})
	}
}

func TestGPUFullPipelineBridge(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 640, 480
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	format := switcher.PipelineFormat{Width: w, Height: h, FPSNum: 30, FPSDen: 1}

	// Build a full GPU bridge pipeline.
	nodes := []switcher.PipelineNode{
		NewUploadNode(ctx, pool),
		NewKeyNode(),
		NewLayoutNode(),
		NewCompositorNode(),
		NewDownloadNode(ctx),
	}

	// Build a switcher Pipeline with these nodes.
	p := &switcher.Pipeline{}
	err = p.Build(format, nil, nodes)
	require.NoError(t, err)
	defer p.Close()

	// Create test frame.
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 200
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}

	frame := &switcher.ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
		PTS:    0,
	}

	// Run through the pipeline.
	result := p.Run(frame)
	require.NotNil(t, result)
	assert.Nil(t, result.GPUData, "GPUData should be cleared after download")

	// Y plane should survive the round-trip.
	assert.InDelta(t, 200, int(result.YUV[h/2*w+w/2]), 2)
}

func TestContextBackendDeviceName(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	assert.Equal(t, "metal", ctx.Backend())
	assert.NotEmpty(t, ctx.DeviceName(), "device name should not be empty")
	t.Logf("GPU: backend=%s, device=%s", ctx.Backend(), ctx.DeviceName())
}
