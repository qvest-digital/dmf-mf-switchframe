//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t blend_uniform_nv12(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    int pos256, int width, int height, int pitch, cudaStream_t stream);
cudaError_t blend_fade_const_nv12(
    uint8_t* dst, const uint8_t* src,
    uint8_t constY, uint8_t constUV, int pos256,
    int width, int height, int pitch, cudaStream_t stream);
cudaError_t blend_wipe_nv12(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    float position, int direction, int softEdge,
    int width, int height, int pitch,
    uint8_t* maskBuf, cudaStream_t stream);
cudaError_t blend_stinger_nv12(
    uint8_t* dst, const uint8_t* base, const uint8_t* overlay,
    const uint8_t* alpha, int width, int height, int pitch, int alphaPitch,
    uint8_t* uvAlphaBuf, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// WipeDirection matches transition.WipeDirection values.
type WipeDirection int

const (
	WipeHLeft     WipeDirection = 0
	WipeHRight    WipeDirection = 1
	WipeVTop      WipeDirection = 2
	WipeVBottom   WipeDirection = 3
	WipeBoxCenter WipeDirection = 4
	WipeBoxEdges  WipeDirection = 5
)

// BlendMix performs a uniform mix blend between two NV12 frames.
// position is 0.0 (all A) to 1.0 (all B).
func BlendMix(ctx *Context, dst, a, b *GPUFrame, position float64) error {
	if ctx == nil || dst == nil || a == nil || b == nil {
		return ErrGPUNotAvailable
	}
	pos256 := int(position * 256.0)
	if pos256 < 0 {
		pos256 = 0
	} else if pos256 > 256 {
		pos256 = 256
	}

	rc := C.blend_uniform_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(a.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(b.DevPtr))),
		C.int(pos256),
		C.int(a.Width), C.int(a.Height), C.int(a.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend mix failed: %d", rc)
	}
	return ctx.Sync()
}

// BlendFTB fades a frame to black (BT.709 limited range: Y=16, UV=128).
// position is 0.0 (full frame) to 1.0 (full black).
func BlendFTB(ctx *Context, dst, src *GPUFrame, position float64) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	pos256 := int(position * 256.0)
	if pos256 < 0 {
		pos256 = 0
	} else if pos256 > 256 {
		pos256 = 256
	}

	rc := C.blend_fade_const_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.uint8_t(16), C.uint8_t(128), // BT.709 black
		C.int(pos256),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend FTB failed: %d", rc)
	}
	return ctx.Sync()
}

// BlendWipe performs a wipe transition between two frames.
// position is 0.0 to 1.0. dir selects the wipe pattern. softEdge is in pixels.
// maskBuf must be a GPUFrame-sized NV12 buffer used as scratch for the wipe
// alpha mask (Y-plane) and the downsampled UV-width alpha (UV-plane slot).
func BlendWipe(ctx *Context, dst, a, b, maskBuf *GPUFrame, position float64, dir WipeDirection, softEdge int) error {
	if ctx == nil || dst == nil || a == nil || b == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	rc := C.blend_wipe_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(a.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(b.DevPtr))),
		C.float(position), C.int(dir), C.int(softEdge),
		C.int(a.Width), C.int(a.Height), C.int(a.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(maskBuf.DevPtr))),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend wipe failed: %d", rc)
	}
	return ctx.Sync()
}

// BlendStinger composites a stinger overlay onto a base frame using per-pixel alpha.
// alpha is a full NV12-sized GPUFrame whose Y-plane contains the luma-resolution
// alpha mask. The UV-plane slot of alpha is used as scratch space for the
// downsampled UV-width alpha — callers must not use alpha.UV concurrently.
func BlendStinger(ctx *Context, dst, base, overlay, alpha *GPUFrame) error {
	if ctx == nil || dst == nil || base == nil || overlay == nil || alpha == nil {
		return ErrGPUNotAvailable
	}

	// Use the UV-plane slot of the alpha buffer as scratch for the downsampled
	// UV-width alpha. The alpha frame is NV12-sized (pitch * height * 3/2),
	// so offset pitch*height is always valid scratch space.
	uvAlphaBuf := uintptr(alpha.DevPtr) + uintptr(alpha.Pitch*alpha.Height)

	rc := C.blend_stinger_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(base.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(overlay.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(alpha.DevPtr))),
		C.int(base.Width), C.int(base.Height), C.int(base.Pitch),
		C.int(alpha.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uvAlphaBuf)),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend stinger failed: %d", rc)
	}
	return ctx.Sync()
}
