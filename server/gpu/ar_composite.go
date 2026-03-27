//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t launch_ar_composite(
    uint8_t* nv12,
    const uint8_t* render,
    const uint8_t* occlusion,
    int render_w, int render_h,
    int width, int height, int pitch,
    int threshold,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
	"unsafe"
)

// ARCompositorState provides AR data to the pipeline node.
type ARCompositorState interface {
	// HasActiveAR returns true if any AR feature is active (occlusion, 3D render, etc.)
	HasActiveAR() bool
	// OcclusionMap returns the latest AI-generated occlusion data. May be nil.
	OcclusionMap() *ARCompositorOcclusion
	// RenderedFrame returns the latest 3D-rendered RGBA frame. May be nil.
	// The RGBA data is uint8 packed (R,G,B,A per pixel), Width*Height*4 bytes.
	// Returns a device pointer and dimensions. The pointer is valid until
	// the next call to RenderedFrame().
	RenderedFrame() *ARRenderedFrame
	// ProcessFrame feeds the current GPU frame to the inference engine
	// (depth estimation + SAM segmentation). Called by the AR compositor
	// node at the start of ProcessGPU so occlusion data is refreshed
	// each frame. Implementations that don't support inference should no-op.
	ProcessFrame(framePtr uintptr, w, h, pitch int)
	// TriggerRender is called every frame. If the scene has changed,
	// it re-renders the 3D scene via OptiX. This is a no-op if the
	// scene is clean or no renderer is available.
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
// compositor for downstream pipeline nodes (e.g., the DSK compositor)
// to read. The DSK compositor uses the mask to prevent 2D overlays from
// drawing on top of real-world foreground objects (people, desks).
type OcclusionMaskProvider interface {
	// OcclusionMask returns the latest occlusion mask device pointer and
	// dimensions. Returns (0, 0, 0) if no occlusion is active.
	// The mask is uint8 per-pixel: 255 = foreground, 0 = background.
	OcclusionMask() (devPtr uintptr, width, height int)
}

// gpuARCompositorNode applies AR content overlays onto the NV12 program frame
// with depth-aware occlusion. It reads state from an ARCompositorState interface
// and dispatches the ar_composite CUDA kernel.
//
// It also stores the latest occlusion mask and exposes it via the
// OcclusionMaskProvider interface so the downstream DSK compositor can
// mask 2D graphics behind real-world objects.
type gpuARCompositorNode struct {
	ctx     *Context
	pool    *FramePool
	arState ARCompositorState
	lastErr atomic.Value
	width   int
	height  int
	pitch   int

	// Occlusion threshold: occlusion values above this cause the real-world
	// object to occlude the virtual content. Range 0-255, default 128.
	occlusionThreshold int

	// Latest occlusion mask, stored each frame for the DSK compositor to read.
	// Updated atomically via sync/atomic so concurrent reads are safe.
	occMaskPtr    atomic.Uintptr
	occMaskWidth  atomic.Int32
	occMaskHeight atomic.Int32
}

// NewGPUARCompositorNode creates a GPU AR compositor pipeline node.
// The state parameter provides rendered AR content and occlusion maps.
// Returns nil if state is nil (no AR engine configured).
func NewGPUARCompositorNode(ctx *Context, pool *FramePool, state ARCompositorState) GPUPipelineNode {
	if state == nil {
		return nil
	}
	return &gpuARCompositorNode{
		ctx:                ctx,
		pool:               pool,
		arState:            state,
		occlusionThreshold: 128,
	}
}

func (n *gpuARCompositorNode) Name() string { return "gpu_ar_composite" }

func (n *gpuARCompositorNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	n.pitch = pitch
	return nil
}

// Active returns true when an AR state provider is attached.
// The HasActiveAR() check is a fast-path optimization inside ProcessGPU.
func (n *gpuARCompositorNode) Active() bool {
	return n.arState != nil
}

func (n *gpuARCompositorNode) Latency() time.Duration { return 200 * time.Microsecond }

func (n *gpuARCompositorNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuARCompositorNode) Close() error {
	return nil
}

// OcclusionMask returns the latest occlusion mask device pointer and dimensions.
// Implements OcclusionMaskProvider. Returns (0, 0, 0) if no occlusion is active.
// Safe for concurrent reads — all fields are atomic.
func (n *gpuARCompositorNode) OcclusionMask() (devPtr uintptr, width, height int) {
	ptr := n.occMaskPtr.Load()
	if ptr == 0 {
		return 0, 0, 0
	}
	return ptr, int(n.occMaskWidth.Load()), int(n.occMaskHeight.Load())
}

// ProcessGPU feeds the program frame to the inference engine, stores the
// occlusion mask for downstream nodes, and composites AR content.
//
//  1. Feed the frame to the inference engine (refreshes depth + SAM data).
//  2. If no AR features are active, clear stored occlusion and return.
//  3. Store the occlusion mask for the DSK compositor (even without 3D render).
//  4. If a 3D rendered frame exists, launch the ar_composite CUDA kernel.
func (n *gpuARCompositorNode) ProcessGPU(frame *GPUFrame) error {
	if n.arState == nil {
		return nil
	}

	// Feed the current program frame to the inference engine so depth/SAM
	// data is refreshed each frame. This runs on a dedicated CUDA stream
	// inside the engine — the cost is amortized across frames (previous
	// frame's results are used if inference hasn't completed yet).
	n.arState.ProcessFrame(uintptr(frame.DevPtr), frame.Width, frame.Height, frame.Pitch)

	// Trigger an OptiX render if the scene has changed (object placed/moved).
	// This is a no-op when the scene is clean (~1ns atomic load). When dirty,
	// it renders once and clears the flag, so cost is amortized.
	n.arState.TriggerRender()

	// Fast path: no AR features active — clear stored occlusion.
	if !n.arState.HasActiveAR() {
		n.occMaskPtr.Store(0)
		n.occMaskWidth.Store(0)
		n.occMaskHeight.Store(0)
		return nil
	}

	// Get occlusion map (optional). Store it for the DSK compositor
	// regardless of whether a 3D render exists — the DSK compositor
	// uses it to mask 2D overlays behind real-world objects.
	var occPtr uintptr
	occlusion := n.arState.OcclusionMap()
	if occlusion != nil && occlusion.MaskPtr != 0 {
		occPtr = occlusion.MaskPtr
		n.occMaskPtr.Store(occlusion.MaskPtr)
		n.occMaskWidth.Store(int32(occlusion.Width))
		n.occMaskHeight.Store(int32(occlusion.Height))
	} else {
		n.occMaskPtr.Store(0)
		n.occMaskWidth.Store(0)
		n.occMaskHeight.Store(0)
	}

	// Get the rendered AR frame. If no 3D render exists, we still stored
	// the occlusion mask above for the DSK compositor — just skip the
	// 3D composite kernel.
	rendered := n.arState.RenderedFrame()
	if rendered == nil || rendered.DevPtr == 0 {
		return nil // No 3D content to composite — occlusion stored for DSK
	}

	// Launch the AR composite kernel.
	rc := C.launch_ar_composite(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(rendered.DevPtr)),
		(*C.uint8_t)(unsafe.Pointer(occPtr)),
		C.int(rendered.Width), C.int(rendered.Height),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(n.occlusionThreshold),
		n.ctx.stream,
	)
	if rc != C.cudaSuccess {
		err := fmt.Errorf("gpu: ar_composite kernel failed: %d", rc)
		slog.Warn("gpu_ar_composite: kernel failed", "error", err)
		n.lastErr.Store(err)
		// Don't fail the pipeline — pass through unmodified frame.
		return nil
	}

	if err := n.ctx.Sync(); err != nil {
		n.lastErr.Store(err)
		return nil
	}

	return nil
}

// ARComposite is the low-level GPU function that composites a rendered RGBA
// layer onto an NV12 frame with occlusion awareness. Called by the pipeline
// node, but also usable standalone.
//
// Parameters:
//   - nv12: NV12 frame device pointer (modified in place)
//   - render: RGBA uint8 device pointer (may be 0 for passthrough)
//   - occlusion: uint8 occlusion mask device pointer (may be 0 for no occlusion)
//   - renderW, renderH: rendered frame dimensions
//   - width, height, pitch: NV12 frame dimensions
//   - threshold: occlusion confidence threshold (0-255)
func ARComposite(ctx *Context, nv12DevPtr uintptr,
	render uintptr, renderW, renderH int,
	occlusion uintptr,
	width, height, pitch, threshold int) error {
	if ctx == nil {
		return ErrGPUNotAvailable
	}
	if nv12DevPtr == 0 {
		return fmt.Errorf("gpu: ARComposite: nv12 pointer is nil")
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("gpu: ARComposite: invalid dimensions %dx%d", width, height)
	}

	rc := C.launch_ar_composite(
		(*C.uint8_t)(unsafe.Pointer(nv12DevPtr)),
		(*C.uint8_t)(unsafe.Pointer(render)),
		(*C.uint8_t)(unsafe.Pointer(occlusion)),
		C.int(renderW), C.int(renderH),
		C.int(width), C.int(height), C.int(pitch),
		C.int(threshold),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: ARComposite: kernel failed: %d", rc)
	}
	return ctx.Sync()
}
