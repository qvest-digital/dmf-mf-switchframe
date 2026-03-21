//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t chroma_key_nv12(
    uint8_t* nv12, uint8_t* mask,
    int width, int height, int pitch,
    uint8_t keyCb, uint8_t keyCr,
    int simDistSq, int totalDistSq,
    float spillSuppress,
    uint8_t spillReplaceCb, uint8_t spillReplaceCr,
    cudaStream_t stream);
cudaError_t luma_key_upload_lut(const uint8_t* lut);
cudaError_t luma_key_nv12(
    const uint8_t* nv12, uint8_t* mask,
    int width, int height, int pitch,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// ChromaKeyConfig holds parameters for GPU chroma keying.
type ChromaKeyConfig struct {
	KeyCb, KeyCr   uint8   // Key color in YCbCr space
	Similarity     float32 // 0-1, inner threshold radius
	Smoothness     float32 // 0-1, feathering zone width
	SpillSuppress  float32 // 0-1, spill suppression strength
	SpillReplaceCb uint8   // Replacement Cb for spill region
	SpillReplaceCr uint8   // Replacement Cr for spill region
}

// ChromaKey generates an alpha mask from an NV12 frame using chroma keying.
// The mask is written to maskBuf at luma resolution (width x height).
// The source frame's UV plane may be modified in-place for spill suppression.
func ChromaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, cfg ChromaKeyConfig) error {
	if ctx == nil || frame == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	// Convert similarity/smoothness to squared distance thresholds (matching CPU algorithm)
	simDist := cfg.Similarity * 255.0
	totalDist := (cfg.Similarity + cfg.Smoothness) * 255.0
	simDistSq := int(simDist * simDist)
	totalDistSq := int(totalDist * totalDist)
	if totalDistSq <= simDistSq {
		totalDistSq = simDistSq + 1
	}

	rc := C.chroma_key_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(maskBuf.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.uint8_t(cfg.KeyCb), C.uint8_t(cfg.KeyCr),
		C.int(simDistSq), C.int(totalDistSq),
		C.float(cfg.SpillSuppress),
		C.uint8_t(cfg.SpillReplaceCb), C.uint8_t(cfg.SpillReplaceCr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: chroma key failed: %d", rc)
	}
	return ctx.Sync()
}

// LumaKey generates an alpha mask from an NV12 frame using luma keying.
// lut is a 256-byte lookup table mapping Y values to alpha values.
// The LUT is uploaded to CUDA constant memory (cached, broadcast to all threads).
//
// ctx.mu is held for the duration because cudaMemcpyToSymbol writes to
// device-global constant memory — concurrent LumaKey calls on the same
// Context would race and corrupt the LUT mid-kernel.
func LumaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, lut [256]byte) error {
	if ctx == nil || frame == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Upload LUT to constant memory (device-global — must be serialized).
	rc := C.luma_key_upload_lut((*C.uint8_t)(unsafe.Pointer(&lut[0])))
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: luma key LUT upload failed: %d", rc)
	}

	rc = C.luma_key_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(maskBuf.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: luma key failed: %d", rc)
	}

	// Sync inside the lock so the kernel completes before we release the mutex.
	// This prevents a second LumaKey call from overwriting the LUT while
	// the first call's kernel is still running.
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: luma key sync failed: %d", rc)
	}
	return nil
}

// BuildLumaKeyLUT creates a 256-byte lookup table for luma keying.
// lowClip: Y values below this are fully transparent (alpha=0)
// highClip: Y values above this are fully opaque (alpha=255)
// softness: transition zone width (0 = hard edge)
func BuildLumaKeyLUT(lowClip, highClip, softness float32) [256]byte {
	var lut [256]byte
	for i := 0; i < 256; i++ {
		y := float32(i)
		if y <= lowClip-softness {
			lut[i] = 0
		} else if y >= highClip+softness {
			lut[i] = 255
		} else if y < lowClip+softness {
			t := (y - (lowClip - softness)) / (2.0 * softness)
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			lut[i] = uint8(t * 255.0)
		} else if y > highClip-softness {
			t := (y - (highClip - softness)) / (2.0 * softness)
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			lut[i] = uint8(t * 255.0)
		} else {
			lut[i] = 255
		}
	}
	return lut
}
