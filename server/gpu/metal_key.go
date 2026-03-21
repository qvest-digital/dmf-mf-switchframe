//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// ChromaKey generates an alpha mask from an NV12 frame using chroma keying.
func ChromaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, cfg ChromaKeyConfig) error {
	if ctx == nil || ctx.mtl == nil || frame == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("chroma_key_nv12")
	if err != nil {
		return fmt.Errorf("gpu: chroma key: %w", err)
	}

	simDist := cfg.Similarity * 255.0
	totalDist := (cfg.Similarity + cfg.Smoothness) * 255.0
	simDistSq := int(simDist * simDist)
	totalDistSq := int(totalDist * totalDist)
	if totalDistSq <= simDistSq {
		totalDistSq = simDistSq + 1
	}

	params := C.MetalChromaKeyParams{
		width:          C.uint32_t(frame.Width),
		height:         C.uint32_t(frame.Height),
		pitch:          C.uint32_t(frame.Pitch),
		keyCb:          C.uint8_t(cfg.KeyCb),
		keyCr:          C.uint8_t(cfg.KeyCr),
		simDistSq:      C.int32_t(simDistSq),
		totalDistSq:    C.int32_t(totalDistSq),
		spillSuppress:  C.float(cfg.SpillSuppress),
		spillReplaceCb: C.uint8_t(cfg.SpillReplaceCb),
		spillReplaceCr: C.uint8_t(cfg.SpillReplaceCr),
	}

	rc := C.metal_chroma_key(mtl.queue, pipeline, frame.MetalBuf, maskBuf.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: chroma key failed: %d", rc)
	}
	return nil
}

// LumaKey generates an alpha mask from an NV12 frame using luma keying.
// No lock needed — Metal command queues are thread-safe.
func LumaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, lut [256]byte) error {
	if ctx == nil || ctx.mtl == nil || frame == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	pipeline, err := mtl.getPipeline("luma_key_nv12")
	if err != nil {
		return fmt.Errorf("gpu: luma key: %w", err)
	}

	// Upload LUT as a Metal buffer (256 bytes).
	// TODO: Cache this buffer on the context to avoid per-call allocation.
	lutBuf, err := mtl.allocBuffer(256)
	if err != nil {
		return fmt.Errorf("gpu: luma key: alloc LUT: %w", err)
	}
	defer C.metal_buffer_free(lutBuf)
	C.memcpy(C.metal_buffer_contents(lutBuf), unsafe.Pointer(&lut[0]), 256)

	params := C.MetalLumaKeyParams{
		width:  C.uint32_t(frame.Width),
		height: C.uint32_t(frame.Height),
		pitch:  C.uint32_t(frame.Pitch),
	}

	rc := C.metal_luma_key(mtl.queue, pipeline, frame.MetalBuf, maskBuf.MetalBuf, lutBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: luma key failed: %d", rc)
	}
	return nil
}

// BuildLumaKeyLUT creates a 256-byte lookup table for luma keying.
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
