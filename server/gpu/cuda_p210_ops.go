//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

// P210 blend
cudaError_t blend_uniform_p210(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    int pos256, int width, int height, int pitch, cudaStream_t stream);
cudaError_t blend_fade_const_p210(
    uint8_t* dst, const uint8_t* src,
    uint16_t constY, uint16_t constUV, int pos256,
    int width, int height, int pitch, cudaStream_t stream);

// P210 scale
cudaError_t scale_bilinear_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream);

// NV12 <-> P210 cross-format conversion + scale
cudaError_t nv12_to_p210(
    uint8_t* dst, int dstPitch,
    const uint8_t* src, int srcPitch,
    int width, int height,
    cudaStream_t stream);
cudaError_t scale_nv12_to_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream);
cudaError_t scale_p210_to_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream);
cudaError_t p210_to_p010_msb(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream);

// P210 composite
cudaError_t pip_composite_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    int rectX, int rectY, int rectW, int rectH,
    int alpha256,
    int cropX, int cropY, int cropW, int cropH,
    cudaStream_t stream);

// P210 V210
cudaError_t v210_to_p210(
    uint8_t* p210, const uint32_t* v210,
    int width, int height, int p210Pitch, int v210StrideBytes,
    cudaStream_t stream);
cudaError_t p210_to_v210(
    uint32_t* v210, const uint8_t* p210,
    int width, int height, int p210Pitch, int v210StrideBytes,
    cudaStream_t stream);

// P210 fill (constant color)
cudaError_t p210_fill(
    uint8_t* p210, int width, int height, int pitch,
    uint16_t yVal, uint16_t cbVal, uint16_t crVal, cudaStream_t stream);

// P210 fill rect (rectangular region)
cudaError_t fill_rect_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    uint16_t colorY, uint16_t colorCb, uint16_t colorCr,
    cudaStream_t stream);

// P210 draw border (border around rectangle)
cudaError_t draw_border_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    int outerX, int outerY, int outerW, int outerH,
    int thickness, uint16_t colorY, uint16_t colorCb, uint16_t colorCr,
    cudaStream_t stream);

// P210 DSK overlay (full-frame + rect)
cudaError_t alpha_blend_rgba_p210(
    uint8_t* p210, const uint8_t* rgba,
    int width, int height, int p210Pitch, int rgbaPitch,
    int alphaScale256, cudaStream_t stream);
cudaError_t alpha_blend_rgba_rect_p210(
    uint8_t* p210,
    const uint8_t* rgba, int overlayW, int overlayH, int rgbaPitch,
    int p210Pitch, int frameW, int frameH,
    int rectX, int rectY, int rectW, int rectH,
    int alphaScale256, cudaStream_t stream);

// ST map upload/free (shared with NV12 path)
cudaError_t stmap_upload(
    float** devS, float** devT, int width, int height,
    const float* hostS, const float* hostT);
void stmap_free(float* devS, float* devT);

// P210 stmap
cudaError_t stmap_warp_p210(
    uint8_t* dst, int dstPitch,
    const uint8_t* src, int srcPitch,
    const float* stS, const float* stT,
    int width, int height, cudaStream_t stream);

// P210 chroma key
cudaError_t chroma_key_p210(
    uint8_t* p210, uint8_t* mask,
    int width, int height, int pitch, int maskPitch,
    uint16_t keyCb, uint16_t keyCr,
    int simDistSq, int totalDistSq,
    float spillSuppress,
    uint16_t spillReplaceCb, uint16_t spillReplaceCr,
    cudaStream_t stream);

// P210 blend wipe + stinger
cudaError_t blend_wipe_p210(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    float position, int direction, int softEdge,
    int width, int height, int pitch,
    uint8_t* maskBuf, cudaStream_t stream);
cudaError_t wipe_map_blend_p210(
    const uint8_t* srcA, const uint8_t* srcB,
    const uint8_t* gradMap, uint8_t* dst,
    int width, int height, int pitch, int gradPitch,
    float position, float softEdge, float borderWidth,
    uint16_t borderColorY, uint16_t borderColorCb, uint16_t borderColorCr,
    int reverse, int multiply, cudaStream_t stream);
cudaError_t blend_stinger_p210(
    uint8_t* dst, const uint8_t* base, const uint8_t* overlay,
    const uint8_t* alpha, int width, int height, int pitch, int alphaPitch,
    uint8_t* uvAlphaBuf, cudaStream_t stream);

// P210 Lanczos-3 scale
cudaError_t scale_lanczos3_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    float* tmpBuf,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// BlendMixP210 performs a uniform mix blend between two P210 frames on CUDA.
func BlendMixP210(ctx *Context, dst, a, b *GPUFrame, position float64) error {
	if ctx == nil || dst == nil || a == nil || b == nil {
		return ErrGPUNotAvailable
	}
	pos256 := int(position * 256.0)
	if pos256 < 0 {
		pos256 = 0
	} else if pos256 > 256 {
		pos256 = 256
	}

	rc := C.blend_uniform_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(a.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(b.DevPtr))),
		C.int(pos256),
		C.int(a.Width), C.int(a.Height), C.int(a.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend mix p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// BlendFTBP210 fades a P210 frame to black (BT.709 10-bit limited range).
// Y plane fades to Y=64, UV plane fades to UV=512.
func BlendFTBP210(ctx *Context, dst, src *GPUFrame, position float64) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	pos256 := int(position * 256.0)
	if pos256 < 0 {
		pos256 = 0
	} else if pos256 > 256 {
		pos256 = 256
	}

	rc := C.blend_fade_const_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.uint16_t(64),  // 10-bit black Y
		C.uint16_t(512), // 10-bit neutral chroma
		C.int(pos256),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend FTB p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// ScaleBilinearP210 scales a P210 GPU frame using bilinear interpolation.
func ScaleBilinearP210(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	rc := C.scale_bilinear_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale bilinear p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// PIPCompositeP210 scales a source P210 frame and composites it into a
// destination P210 frame at the specified rectangle.
func PIPCompositeP210(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	return PIPCompositeP210WithCrop(ctx, dst, src, rect, alpha, 0, 0, 0, 0)
}

// PIPCompositeP210WithCrop scales a source P210 frame (or sub-region) and
// composites it into a destination P210 frame at the specified rectangle.
func PIPCompositeP210WithCrop(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64,
	cropX, cropY, cropW, cropH int) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	alpha256 := int(alpha * 256.0)
	if alpha256 < 0 {
		alpha256 = 0
	} else if alpha256 > 256 {
		alpha256 = 256
	}

	rc := C.pip_composite_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(alpha256),
		C.int(cropX), C.int(cropY), C.int(cropW), C.int(cropH),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: pip composite p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// UploadV210ToP210 uploads a CPU V210 buffer to a GPU P210 frame (lossless 10-bit).
func UploadV210ToP210(ctx *Context, dst *GPUFrame, v210 []byte, width, height int) error {
	if ctx == nil || dst == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 buffer too small: %d < %d", len(v210), expected)
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Lazily allocate or grow the persistent V210 staging buffer.
	if ctx.stagingV210Size < expected {
		if ctx.stagingV210 != nil {
			C.cudaFree(ctx.stagingV210)
			ctx.stagingV210 = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingV210, C.size_t(expected)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: V210->P210 alloc failed: %d", rc)
		}
		ctx.stagingV210Size = expected
	}

	if rc := C.cudaMemcpyAsync(ctx.stagingV210, unsafe.Pointer(&v210[0]), C.size_t(expected), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210->P210 memcpy failed: %d", rc)
	}

	rc := C.v210_to_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint32_t)(ctx.stagingV210),
		C.int(width), C.int(height),
		C.int(dst.Pitch), C.int(v210Stride),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210->P210 kernel failed: %d", rc)
	}
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210->P210 sync failed: %d", rc)
	}
	return nil
}

// DownloadP210ToV210 converts a P210 GPUFrame to V210 and downloads to CPU (lossless 10-bit).
func DownloadP210ToV210(ctx *Context, v210 []byte, src *GPUFrame, width, height int) error {
	if ctx == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 download buffer too small: %d < %d", len(v210), expected)
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if ctx.stagingV210Size < expected {
		if ctx.stagingV210 != nil {
			C.cudaFree(ctx.stagingV210)
			ctx.stagingV210 = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingV210, C.size_t(expected)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: P210->V210 alloc failed: %d", rc)
		}
		ctx.stagingV210Size = expected
	}

	rc := C.p210_to_v210(
		(*C.uint32_t)(ctx.stagingV210),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(width), C.int(height),
		C.int(src.Pitch), C.int(v210Stride),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: P210->V210 kernel failed: %d", rc)
	}
	if rc := C.cudaMemcpyAsync(unsafe.Pointer(&v210[0]), ctx.stagingV210, C.size_t(expected), C.cudaMemcpyDeviceToHost, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: P210->V210 memcpy failed: %d", rc)
	}
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: P210->V210 sync failed: %d", rc)
	}
	return nil
}

// DSKOverlayP210 alpha-blends an RGBA overlay onto a P210 frame.
func DSKOverlayP210(ctx *Context, frame *GPUFrame, rgba []byte, width, height, rgbaPitch, alphaScale256 int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rgbaSize := rgbaPitch * height
	if len(rgba) < rgbaSize {
		return fmt.Errorf("gpu: RGBA buffer too small: %d < %d", len(rgba), rgbaSize)
	}

	// Upload RGBA to GPU staging
	var devRGBA unsafe.Pointer
	if rc := C.cudaMalloc(&devRGBA, C.size_t(rgbaSize)); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: dsk overlay p210 alloc failed: %d", rc)
	}
	defer C.cudaFree(devRGBA)

	if rc := C.cudaMemcpyAsync(devRGBA, unsafe.Pointer(&rgba[0]), C.size_t(rgbaSize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: dsk overlay p210 memcpy failed: %d", rc)
	}

	rc := C.alpha_blend_rgba_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(devRGBA),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(rgbaPitch),
		C.int(alphaScale256),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: dsk overlay p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// DSKCompositeFullFrameP210 composites a full-frame RGBA overlay (already on GPU)
// onto a P210 frame.
func DSKCompositeFullFrameP210(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, alphaScale float64) error {
	if ctx == nil || frame == nil || overlay == nil {
		return ErrGPUNotAvailable
	}

	alphaScale256 := int(alphaScale * 256.0)
	if alphaScale256 < 0 {
		alphaScale256 = 0
	} else if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	rc := C.alpha_blend_rgba_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(overlay.DevPtr),
		C.int(frame.Width), C.int(frame.Height),
		C.int(frame.Pitch), C.int(overlay.Pitch),
		C.int(alphaScale256),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: DSK full-frame p210 composite failed: %d", rc)
	}
	return ctx.Sync()
}

// DSKCompositeRectP210 composites an RGBA overlay into a rectangular region of a P210 frame.
// Uses nearest-neighbor scaling if overlay dimensions differ from rect dimensions.
func DSKCompositeRectP210(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, rect Rect, alphaScale float64) error {
	if ctx == nil || frame == nil || overlay == nil {
		return ErrGPUNotAvailable
	}
	alphaScale256 := int(alphaScale * 256.0)
	if alphaScale256 <= 0 {
		return nil
	}
	if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	rc := C.alpha_blend_rgba_rect_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(overlay.DevPtr),
		C.int(overlay.Width), C.int(overlay.Height), C.int(overlay.Pitch),
		C.int(frame.Pitch), C.int(frame.Width), C.int(frame.Height),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(alphaScale256),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: DSK rect p210 composite failed: %d", rc)
	}
	return ctx.Sync()
}

// ChromaKeyP210 generates an 8-bit alpha mask from a P210 frame's chroma values.
// keyCb/keyCr are the target chroma values to key (10-bit range 0-1023).
// simDistSq is the similarity distance squared, totalDistSq is the total distance squared.
// spillSuppress controls spill suppression strength (0.0 = off, 1.0 = full).
func ChromaKeyP210(ctx *Context, frame *GPUFrame, mask *GPUFrame,
	keyCb, keyCr uint16, simDistSq, totalDistSq int,
	spillSuppress float32, spillReplaceCb, spillReplaceCr uint16) error {
	if ctx == nil || frame == nil || mask == nil {
		return ErrGPUNotAvailable
	}

	rc := C.chroma_key_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(mask.DevPtr))),
		C.int(frame.Width), C.int(frame.Height),
		C.int(frame.Pitch), C.int(mask.Pitch),
		C.uint16_t(keyCb), C.uint16_t(keyCr),
		C.int(simDistSq), C.int(totalDistSq),
		C.float(spillSuppress),
		C.uint16_t(spillReplaceCb), C.uint16_t(spillReplaceCr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: chroma key p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// STMapWarpP210Map warps a P210 frame using a pre-uploaded GPUSTMap.
func STMapWarpP210Map(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	if ctx == nil || dst == nil || src == nil || stmap == nil {
		return ErrGPUNotAvailable
	}
	if src.Width != stmap.Width || src.Height != stmap.Height {
		return fmt.Errorf("gpu: stmap p210: frame %dx%d doesn't match map %dx%d",
			src.Width, src.Height, stmap.Width, stmap.Height)
	}

	rc := C.stmap_warp_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Pitch),
		stmap.DevS, stmap.DevT,
		C.int(stmap.Width), C.int(stmap.Height),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stmap warp p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// STMapWarpP210 warps a P210 frame using float32 ST map arrays.
func STMapWarpP210(ctx *Context, dst, src *GPUFrame, stmapS, stmapT []float32, width, height int) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	stSize := width * height
	if len(stmapS) < stSize || len(stmapT) < stSize {
		return fmt.Errorf("gpu: stmap buffer too small: %d < %d", len(stmapS), stSize)
	}

	// Upload ST maps to GPU
	var devS, devT *C.float
	rc := C.stmap_upload(&devS, &devT, C.int(width), C.int(height),
		(*C.float)(unsafe.Pointer(&stmapS[0])),
		(*C.float)(unsafe.Pointer(&stmapT[0])))
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stmap upload failed: %d", rc)
	}
	defer C.stmap_free(devS, devT)

	rc = C.stmap_warp_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Pitch),
		devS, devT,
		C.int(width), C.int(height),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stmap warp p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// ScaleLanczos3P210 scales a P210 GPU frame using a two-pass separable Lanczos-3
// kernel on CUDA. It allocates (and caches on the Context) a temporary float
// device buffer sized dstW * srcH floats, sufficient for both Y and UV passes.
func ScaleLanczos3P210(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	// Ensure temp buffer is large enough: dstW * srcH floats cover both passes.
	// For P210, UV is full height so the same temp size works.
	needed := dst.Width * src.Height
	if err := ctx.ensureLanczosTemp(needed); err != nil {
		return err
	}

	rc := C.scale_lanczos3_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.lanczosTmp,
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale lanczos3 p210 failed: %d", rc)
	}

	if syncRc := C.cudaStreamSynchronize(ctx.stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale lanczos3 p210 sync failed: %d", syncRc)
	}
	return nil
}

// BlendWipeP210 performs a wipe transition between two P210 frames on CUDA.
// maskBuf must be a GPUFrame-sized P210 buffer used as scratch for the wipe
// alpha mask (Y-plane slot) and the downsampled UV-width alpha (UV-plane slot).
func BlendWipeP210(ctx *Context, dst, a, b, maskBuf *GPUFrame, position float64, dir WipeDirection, softEdge int) error {
	if ctx == nil || dst == nil || a == nil || b == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	rc := C.blend_wipe_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(a.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(b.DevPtr))),
		C.float(position), C.int(dir), C.int(softEdge),
		C.int(a.Width), C.int(a.Height), C.int(a.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(maskBuf.DevPtr))),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: blend wipe p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// BlendStingerP210 composites a stinger overlay onto a base P210 frame on CUDA
// using per-pixel alpha. alpha is a GPUFrame whose Y-plane contains the
// luma-resolution alpha mask. The UV-plane slot of alpha is used as scratch
// space for the downsampled UV-width alpha.
func BlendStingerP210(ctx *Context, dst, base, overlay, alpha *GPUFrame) error {
	if ctx == nil || dst == nil || base == nil || overlay == nil || alpha == nil {
		return ErrGPUNotAvailable
	}

	// Use the UV-plane slot of the alpha buffer as scratch for the downsampled
	// UV-width alpha. The alpha frame is P210-sized (pitch * height * 2),
	// so offset pitch*height is always valid scratch space.
	uvAlphaBuf := uintptr(alpha.DevPtr) + uintptr(alpha.Pitch*alpha.Height)

	rc := C.blend_stinger_p210(
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
		return fmt.Errorf("gpu: blend stinger p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// ScaleP210ToNV12On scales a P210 source to NV12 destination on GPU.
// Combined bilinear scale + 10-bit->8-bit + 4:2:2->4:2:0 in one pass.
// Runs on the specified work queue (nil = default stream).
func ScaleP210ToNV12On(ctx *Context, dst, src *GPUFrame, q *GPUWorkQueue) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	stream := cudaStream(q)
	if stream == nil {
		stream = ctx.stream
	}

	rc := C.scale_p210_to_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: ScaleP210ToNV12On failed: %d", rc)
	}

	if syncRc := C.cudaStreamSynchronize(stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: ScaleP210ToNV12On sync failed: %d", syncRc)
	}
	return nil
}

// ConvertNV12ToP210 converts an NV12 GPU frame to P210 format on CUDA.
// Reads uint8 NV12 (4:2:0), writes uint16 P210 (4:2:2) with <<2 bit promotion.
// UV plane is upsampled from half-height (4:2:0) to full-height (4:2:2).
// Frames must have the same resolution.
func ConvertNV12ToP210(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if dst.Width != src.Width || dst.Height != src.Height {
		return fmt.Errorf("gpu: ConvertNV12ToP210 dimension mismatch: dst %dx%d vs src %dx%d",
			dst.Width, dst.Height, src.Width, src.Height)
	}

	rc := C.nv12_to_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Pitch),
		C.int(src.Width), C.int(src.Height),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: ConvertNV12ToP210 failed: %d", rc)
	}
	return ctx.Sync()
}

// ScaleNV12ToP210 scales an NV12 GPU frame to P210 format with bilinear
// interpolation on CUDA. Combined scale + format convert in one pass.
func ScaleNV12ToP210(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	rc := C.scale_nv12_to_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: ScaleNV12ToP210 failed: %d", rc)
	}
	return ctx.Sync()
}

// FillBlackP210 fills a P210 frame with 10-bit limited-range black (Y=64, Cb=Cr=512).
func FillBlackP210(ctx *Context, frame *GPUFrame) error {
	return FillColorP210(ctx, frame, 64, 512, 512)
}

// FillColorP210 fills a P210 frame with a constant 10-bit YUV color on CUDA.
func FillColorP210(ctx *Context, frame *GPUFrame, y, cb, cr uint16) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rc := C.p210_fill(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.uint16_t(y), C.uint16_t(cb), C.uint16_t(cr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: p210 fill failed: %d", rc)
	}
	return ctx.Sync()
}

// FillRectP210 fills a rectangular region with a constant 10-bit color on a P210 GPU frame.
func FillRectP210(ctx *Context, frame *GPUFrame, rect Rect, colorY, colorCb, colorCr uint16) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rc := C.fill_rect_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.uint16_t(colorY), C.uint16_t(colorCb), C.uint16_t(colorCr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: fill rect p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// DrawBorderP210 draws a colored border outside the given rectangle on a P210 GPU frame.
func DrawBorderP210(ctx *Context, frame *GPUFrame, rect Rect, colorY, colorCb, colorCr uint16, thickness int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	outerX := rect.X - thickness
	outerY := rect.Y - thickness
	outerW := rect.W + thickness*2
	outerH := rect.H + thickness*2

	rc := C.draw_border_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(outerX), C.int(outerY), C.int(outerW), C.int(outerH),
		C.int(thickness),
		C.uint16_t(colorY), C.uint16_t(colorCb), C.uint16_t(colorCr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: draw border p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// BlendWipeMapP210 performs a gradient-map-driven wipe transition between two
// P210 frames on CUDA. The gradient map is stored in the Y plane of a P210
// GPU frame (8-bit values promoted to 10-bit via <<2 during upload). The kernel
// right-shifts gradient values by 2 to recover [0, 255] range for threshold math.
// Border colors in WipeMapBlendConfig are 8-bit; this function promotes them to 10-bit.
func BlendWipeMapP210(ctx *Context, dst, srcA, srcB, gradMap *GPUFrame, cfg WipeMapBlendConfig) error {
	if ctx == nil || dst == nil || srcA == nil || srcB == nil || gradMap == nil {
		return ErrGPUNotAvailable
	}

	reverse := 0
	if cfg.Reverse {
		reverse = 1
	}

	// Promote 8-bit border colors to 10-bit
	borderY := C.uint16_t(uint16(cfg.BorderColorY) << 2)
	borderCb := C.uint16_t(uint16(cfg.BorderColorCb) << 2)
	borderCr := C.uint16_t(uint16(cfg.BorderColorCr) << 2)

	rc := C.wipe_map_blend_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(srcA.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(srcB.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(gradMap.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		C.int(gradMap.Pitch),
		C.float(cfg.Position), C.float(cfg.SoftEdge), C.float(cfg.BorderWidth),
		borderY, borderCb, borderCr,
		C.int(reverse), C.int(cfg.Multiply),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: wipe map blend p210 failed: %d", rc)
	}
	return ctx.Sync()
}

// ConvertP210ToP010MSB converts P210 (LSB-aligned, 4:2:2) to P010 (MSB-aligned, 4:2:0)
// for NVENC encoding. Chroma is downsampled from full-height to half-height by averaging
// adjacent row pairs. All uint16 samples are left-shifted by 6 for MSB alignment.
// This is the only lossy step in the 10-bit pipeline on CUDA — NVENC doesn't support 4:2:2.
func ConvertP210ToP010MSB(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	rc := C.p210_to_p010_msb(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: P210→P010 MSB conversion failed: %d", rc)
	}
	return ctx.Sync()
}
