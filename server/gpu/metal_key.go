//go:build cgo && darwin

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

	// Upload LUT to a cached 256-byte Metal buffer (reused across calls).
	mtl.mu.Lock()
	if mtl.lumaKeyLUTBuf == nil {
		var allocErr error
		mtl.lumaKeyLUTBuf, allocErr = mtl.allocBuffer(256)
		if allocErr != nil {
			mtl.mu.Unlock()
			return fmt.Errorf("gpu: luma key: alloc LUT: %w", allocErr)
		}
	}
	lutBuf := mtl.lumaKeyLUTBuf
	mtl.mu.Unlock()
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

// ChromaKeyP210 generates an alpha mask from a P210 (10-bit 4:2:2) frame
// using chroma keying. The 8-bit ChromaKeyConfig values are promoted to
// 10-bit by left-shifting 2 (matches FillRectP210's promotion of 8-bit
// fill colors). simDistSq / totalDistSq are scaled to the 10-bit chroma
// range so the same Similarity / Smoothness floats produce a perceptually
// similar key edge to the 8-bit kernel.
func ChromaKeyP210(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, cfg ChromaKeyConfig) error {
	if ctx == nil || ctx.mtl == nil || frame == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}
	if frame.Format != GPUFormatP210 {
		return fmt.Errorf("gpu: ChromaKeyP210 requires P210 frame, got format %d", frame.Format)
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("chroma_key_p210")
	if err != nil {
		return fmt.Errorf("gpu: chroma key p210: %w", err)
	}

	simDist := cfg.Similarity * 1023.0
	totalDist := (cfg.Similarity + cfg.Smoothness) * 1023.0
	simDistSq := int(simDist * simDist)
	totalDistSq := int(totalDist * totalDist)
	if totalDistSq <= simDistSq {
		totalDistSq = simDistSq + 1
	}

	uvOffsetU16 := uint32(frame.Pitch*frame.Height) / 2

	params := C.MetalChromaKeyP210Params{
		width:          C.uint32_t(frame.Width),
		height:         C.uint32_t(frame.Height),
		pitch:          C.uint32_t(frame.Pitch),
		maskPitch:      C.uint32_t(maskBuf.Pitch),
		uvOffsetU16:    C.uint32_t(uvOffsetU16),
		keyCb:          C.uint16_t(uint16(cfg.KeyCb) << 2),
		keyCr:          C.uint16_t(uint16(cfg.KeyCr) << 2),
		simDistSq:      C.int32_t(simDistSq),
		totalDistSq:    C.int32_t(totalDistSq),
		spillSuppress:  C.float(cfg.SpillSuppress),
		spillReplaceCb: C.uint16_t(uint16(cfg.SpillReplaceCb) << 2),
		spillReplaceCr: C.uint16_t(uint16(cfg.SpillReplaceCr) << 2),
	}

	rc := C.metal_chroma_key_p210(mtl.queue, pipeline, frame.MetalBuf, maskBuf.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: chroma key p210 failed: %d", rc)
	}
	return nil
}

// LumaKeyP210 generates an alpha mask from a P210 frame using luma
// thresholds. The LUT is 256 entries (the existing 8-bit table); the
// kernel shifts the 10-bit Y down to 8 bits to index into it.
func LumaKeyP210(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, lut [256]byte) error {
	if ctx == nil || ctx.mtl == nil || frame == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}
	if frame.Format != GPUFormatP210 {
		return fmt.Errorf("gpu: LumaKeyP210 requires P210 frame, got format %d", frame.Format)
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("luma_key_p210")
	if err != nil {
		return fmt.Errorf("gpu: luma key p210: %w", err)
	}

	mtl.mu.Lock()
	if mtl.lumaKeyLUTBuf == nil {
		var allocErr error
		mtl.lumaKeyLUTBuf, allocErr = mtl.allocBuffer(256)
		if allocErr != nil {
			mtl.mu.Unlock()
			return fmt.Errorf("gpu: luma key p210: alloc LUT: %w", allocErr)
		}
	}
	lutBuf := mtl.lumaKeyLUTBuf
	mtl.mu.Unlock()
	C.memcpy(C.metal_buffer_contents(lutBuf), unsafe.Pointer(&lut[0]), 256)

	params := C.MetalLumaKeyP210Params{
		width:     C.uint32_t(frame.Width),
		height:    C.uint32_t(frame.Height),
		pitch:     C.uint32_t(frame.Pitch),
		maskPitch: C.uint32_t(maskBuf.Pitch),
	}

	rc := C.metal_luma_key_p210(mtl.queue, pipeline, frame.MetalBuf, maskBuf.MetalBuf, lutBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: luma key p210 failed: %d", rc)
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
