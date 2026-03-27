//go:build !cgo || !cuda

package gpu

import "unsafe"

// ARCompositorState provides AR data to the pipeline node.
type ARCompositorState interface {
	HasActiveAR() bool
	OcclusionMap() *ARCompositorOcclusion
	RenderedFrame() *ARRenderedFrame
	ProcessFrame(framePtr uintptr, w, h, pitch int)
	TriggerRender()
}

// ARCompositorOcclusion holds occlusion data on GPU.
type ARCompositorOcclusion struct {
	MaskPtr uintptr // uint8 per-pixel foreground confidence (Width x Height)
	Width   int
	Height  int

	// DepthPtr is the CUDA device pointer to the float16 depth map
	// at the depth model's native resolution (DepthW x DepthH).
	// May be 0 if depth estimation is not active.
	DepthPtr uintptr
	DepthW   int
	DepthH   int
}

// ARRenderedFrame holds a 3D-rendered RGBA frame on GPU.
type ARRenderedFrame struct {
	DevPtr uintptr // RGBA uint8 data on GPU (Width * Height * 4 bytes)
	Width  int
	Height int
}

// OcclusionMaskProvider exposes the latest occlusion mask from the AR
// compositor for downstream pipeline nodes.
type OcclusionMaskProvider interface {
	OcclusionMask() (devPtr uintptr, width, height int)
}

// NewGPUARCompositorNode returns nil on non-GPU builds.
// The pipeline filters nil nodes, so this is safely excluded from the chain.
func NewGPUARCompositorNode(ctx *Context, pool *FramePool, state ARCompositorState) GPUPipelineNode {
	return nil
}

// ARComposite returns ErrGPUNotAvailable on non-GPU builds.
func ARComposite(ctx *Context, nv12DevPtr uintptr,
	render uintptr, renderW, renderH int,
	occlusion uintptr,
	width, height, pitch, threshold int) error {
	return ErrGPUNotAvailable
}

// Float4ToRGBA8 returns ErrGPUNotAvailable on non-GPU builds.
func Float4ToRGBA8(output, input unsafe.Pointer, width, height int) error {
	return ErrGPUNotAvailable
}
