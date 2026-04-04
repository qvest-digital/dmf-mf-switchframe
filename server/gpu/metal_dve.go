//go:build cgo && darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/zsiec/switchframe/server/dve"
)

// DVEComposite composites a single DVE slot onto the destination GPU frame
// using the Metal DVE shader. The shader performs inverse projective transform,
// bilinear sampling, SDF border/shadow rendering, crop with soft edges,
// and alpha compositing — all in a single dispatch per plane.
//
// Parameters:
//   - dst: destination NV12 frame (composited in-place)
//   - src: source NV12 frame
//   - transform: DVE transform parameters (opacity, crop, border, shadow)
//   - invMatrix: pre-computed inverse of the forward transform matrix
//   - srcW, srcH: source frame dimensions
//   - aabb: pre-computed axis-aligned bounding box in output space
func DVEComposite(ctx *Context, dst, src *GPUFrame, transform dve.Transform,
	invMatrix dve.Matrix3x3, srcW, srcH int, aabb image.Rectangle) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	// Compute alpha256 from effective opacity
	alpha256 := int(transform.EffectiveOpacity() * 256.0)
	if alpha256 < 0 {
		alpha256 = 0
	} else if alpha256 > 256 {
		alpha256 = 256
	}

	// Shadow alpha256
	shadowAlpha256 := 0
	if transform.Shadow.Enabled {
		shadowAlpha256 = int(transform.Shadow.Opacity * 256.0)
		if shadowAlpha256 < 0 {
			shadowAlpha256 = 0
		} else if shadowAlpha256 > 256 {
			shadowAlpha256 = 256
		}
	}

	mtl := ctx.mtl

	// --- Y plane ---
	yPipeline, err := mtl.getPipeline("dve_composite_y")
	if err != nil {
		return fmt.Errorf("gpu: dve composite Y: %w", err)
	}

	// Default contrast/saturation to 1.0 when zero-valued
	contrast := transform.Contrast
	if contrast == 0 {
		contrast = 1.0
	}
	saturation := transform.Saturation
	if saturation == 0 {
		saturation = 1.0
	}

	yParams := C.MetalDVECompositeYParams{
		dstW: C.uint32_t(dst.Width), dstH: C.uint32_t(dst.Height), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(srcW), srcH: C.uint32_t(srcH), srcPitch: C.uint32_t(src.Pitch),

		m00: C.float(invMatrix[0]), m01: C.float(invMatrix[1]), m02: C.float(invMatrix[2]),
		m10: C.float(invMatrix[3]), m11: C.float(invMatrix[4]), m12: C.float(invMatrix[5]),
		m20: C.float(invMatrix[6]), m21: C.float(invMatrix[7]), m22: C.float(invMatrix[8]),

		slotMinX: C.int32_t(aabb.Min.X), slotMinY: C.int32_t(aabb.Min.Y),
		slotMaxX: C.int32_t(aabb.Max.X), slotMaxY: C.int32_t(aabb.Max.Y),

		alpha256: C.int32_t(alpha256),

		cropTop: C.float(transform.CropTop), cropBottom: C.float(transform.CropBottom),
		cropLeft: C.float(transform.CropLeft), cropRight: C.float(transform.CropRight),
		cropSoftness: C.float(transform.CropSoftness),

		borderWidth:        C.float(transform.Border.Width),
		borderColorY:       C.uint32_t(transform.Border.ColorY),
		borderCornerRadius: C.float(transform.Border.CornerRadius),
		borderSoftness:     C.float(transform.Border.Softness),

		shadowEnabled:  boolToInt32(transform.Shadow.Enabled),
		shadowOffsetX:  C.float(transform.Shadow.OffsetX),
		shadowOffsetY:  C.float(transform.Shadow.OffsetY),
		shadowBlur:     C.float(transform.Shadow.Blur),
		shadowAlpha256: C.int32_t(shadowAlpha256),
		shadowColorY:   C.uint32_t(transform.Shadow.ColorY),

		mirrorH:    boolToInt32(transform.MirrorH),
		mirrorV:    boolToInt32(transform.MirrorV),
		mosaicSize: C.int32_t(transform.MosaicSize),
		brightness: C.float(transform.Brightness),
		contrast:   C.float(contrast),
	}

	rc := C.metal_dve_composite_y(mtl.queue, yPipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: dve composite Y failed: %d", rc)
	}

	// --- UV plane ---
	uvPipeline, err := mtl.getPipeline("dve_composite_uv")
	if err != nil {
		return fmt.Errorf("gpu: dve composite UV: %w", err)
	}

	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	srcUVOffset := C.int64_t(src.Pitch * src.Height)

	// Chroma AABB: halved coordinates with 1px expansion to avoid off-by-1 at edges.
	chromaW := int(dst.Width) / 2
	chromaH := int(dst.Height) / 2
	chromaMinX := max(aabb.Min.X/2-1, 0)
	chromaMinY := max(aabb.Min.Y/2-1, 0)
	chromaMaxX := min(aabb.Max.X/2+1, chromaW)
	chromaMaxY := min(aabb.Max.Y/2+1, chromaH)

	uvParams := C.MetalDVECompositeUVParams{
		dstW: C.uint32_t(dst.Width), dstChromaH: C.uint32_t(dst.Height / 2), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(srcW), srcChromaH: C.uint32_t(srcH / 2), srcPitch: C.uint32_t(src.Pitch),

		m00: C.float(invMatrix[0]), m01: C.float(invMatrix[1]), m02: C.float(invMatrix[2]),
		m10: C.float(invMatrix[3]), m11: C.float(invMatrix[4]), m12: C.float(invMatrix[5]),
		m20: C.float(invMatrix[6]), m21: C.float(invMatrix[7]), m22: C.float(invMatrix[8]),

		slotMinCX: C.int32_t(chromaMinX), slotMinCY: C.int32_t(chromaMinY),
		slotMaxCX: C.int32_t(chromaMaxX), slotMaxCY: C.int32_t(chromaMaxY),

		alpha256: C.int32_t(alpha256),

		cropTop: C.float(transform.CropTop), cropBottom: C.float(transform.CropBottom),
		cropLeft: C.float(transform.CropLeft), cropRight: C.float(transform.CropRight),
		cropSoftness: C.float(transform.CropSoftness),

		borderWidth:        C.float(transform.Border.Width),
		borderColorCb:      C.uint32_t(transform.Border.ColorCb),
		borderColorCr:      C.uint32_t(transform.Border.ColorCr),
		borderCornerRadius: C.float(transform.Border.CornerRadius),
		borderSoftness:     C.float(transform.Border.Softness),

		shadowEnabled:  boolToInt32(transform.Shadow.Enabled),
		shadowOffsetX:  C.float(transform.Shadow.OffsetX),
		shadowOffsetY:  C.float(transform.Shadow.OffsetY),
		shadowBlur:     C.float(transform.Shadow.Blur),
		shadowAlpha256: C.int32_t(shadowAlpha256),
		shadowColorCb:  C.uint32_t(128), // achromatic shadow: neutral chroma
		shadowColorCr:  C.uint32_t(128), // neutral Cr for shadow

		mirrorH:    boolToInt32(transform.MirrorH),
		mirrorV:    boolToInt32(transform.MirrorV),
		mosaicSize: C.int32_t(transform.MosaicSize),
		saturation: C.float(saturation),
	}

	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(dst.Width/2), C.uint32_t(dst.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: dve composite UV failed: %d", rc)
	}

	return nil
}

// boolToInt32 converts a Go bool to a C int32_t (0 or 1).
func boolToInt32(b bool) C.int32_t {
	if b {
		return 1
	}
	return 0
}

// DVE3DComposite composites a 3D DVE slot onto the destination GPU frame.
// The shader ray-casts through each output pixel to intersect with the specified
// geometry (plane or cube), samples the video at the intersection UV, applies
// optional lighting, and alpha-composites onto the destination.
func DVE3DComposite(ctx *Context, dst, src *GPUFrame, t3d *dve.Transform3D,
	invMVP dve.Matrix4x4, srcW, srcH int, slotRect image.Rectangle, alpha256 int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil || t3d == nil {
		return fmt.Errorf("gpu: DVE3DComposite: nil context, frame, or transform")
	}
	mtl := ctx.mtl

	shape := int32(0) // plane
	if t3d.Shape == "cube" {
		shape = 1
	} else if t3d.Shape == "page_turn" {
		shape = 2
	}

	lightZ := float32(1.0) // default light pointing at camera
	ambient := float32(t3d.Ambient)
	if ambient <= 0 {
		ambient = 0.3
	}

	// Pack inverse MVP into C struct
	var cInvMVP [16]C.float
	for i := 0; i < 16; i++ {
		cInvMVP[i] = C.float(invMVP[i])
	}

	yPipeline, err := ctx.mtl.getPipeline("dve3d_composite_y")
	if err != nil {
		return fmt.Errorf("gpu: DVE3DComposite Y: %w", err)
	}

	yParams := C.MetalDVE3DYParams{
		dstW: C.uint32_t(dst.Width), dstH: C.uint32_t(dst.Height), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(srcW), srcH: C.uint32_t(srcH), srcPitch: C.uint32_t(src.Pitch),
		slotMinX: C.int32_t(slotRect.Min.X), slotMinY: C.int32_t(slotRect.Min.Y),
		slotMaxX: C.int32_t(slotRect.Max.X), slotMaxY: C.int32_t(slotRect.Max.Y),
		alpha256:     C.int32_t(alpha256),
		shape:        C.int32_t(shape),
		lightEnabled: boolToInt32(t3d.LightEnabled),
		lightX:       C.float(t3d.LightX), lightY: C.float(t3d.LightY), lightZ: C.float(lightZ),
		ambient: C.float(ambient), specular: C.float(t3d.Specular),
		srcAspect:         C.float(float32(srcW) / float32(srcH)),
		reflectionEnabled: boolToInt32(t3d.ReflectionEnabled),
		reflectionOpacity: C.float(t3d.ReflectionOpacity),
		reflectionFade:    C.float(t3d.ReflectionFade),
		curlProgress:      C.float(t3d.CurlProgress),
		curlRadius:        C.float(t3d.CurlRadius),
	}
	copy(yParams.invMVP[:], cInvMVP[:])

	rc := C.metal_dve3d_composite_y(
		mtl.queue, yPipeline,
		dst.MetalBuf, src.MetalBuf,
		&yParams,
	)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: DVE3DComposite Y plane failed: %d", rc)
	}

	// UV plane
	uvPipeline, err := ctx.mtl.getPipeline("dve3d_composite_uv")
	if err != nil {
		return fmt.Errorf("gpu: DVE3DComposite UV: %w", err)
	}

	uvParams := C.MetalDVE3DUVParams{
		dstW: C.uint32_t(dst.Width), dstChromaH: C.uint32_t(dst.Height / 2), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(srcW), srcChromaH: C.uint32_t(srcH / 2), srcPitch: C.uint32_t(src.Pitch),
		slotMinCX:    C.int32_t(max(slotRect.Min.X/2-1, 0)),
		slotMinCY:    C.int32_t(max(slotRect.Min.Y/2-1, 0)),
		slotMaxCX:    C.int32_t(min(slotRect.Max.X/2+1, int(dst.Width)/2)),
		slotMaxCY:    C.int32_t(min(slotRect.Max.Y/2+1, int(dst.Height)/2)),
		alpha256:     C.int32_t(alpha256),
		shape:        C.int32_t(shape),
		lightEnabled: boolToInt32(t3d.LightEnabled),
		lightX:       C.float(t3d.LightX), lightY: C.float(t3d.LightY), lightZ: C.float(lightZ),
		ambient: C.float(ambient), specular: C.float(t3d.Specular),
		srcAspect:         C.float(float32(srcW) / float32(srcH)),
		reflectionEnabled: boolToInt32(t3d.ReflectionEnabled),
		reflectionOpacity: C.float(t3d.ReflectionOpacity),
		reflectionFade:    C.float(t3d.ReflectionFade),
		curlProgress:      C.float(t3d.CurlProgress),
		curlRadius:        C.float(t3d.CurlRadius),
	}
	copy(uvParams.invMVP[:], cInvMVP[:])

	dstUVOffset := C.int64_t(int64(dst.Pitch) * int64(dst.Height))
	srcUVOffset := C.int64_t(int64(src.Pitch) * int64(srcH))

	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(dst.Width/2), C.uint32_t(dst.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: DVE3DComposite UV plane failed: %d", rc)
	}

	return nil
}

// DVEDefocusBlur applies a Gaussian blur to a GPU frame when the DVE transform
// specifies a non-zero defocus radius. This should be called after DVEComposite
// or DVE3DComposite to implement the defocus/depth-of-field effect.
// The radius is clamped to [1, 50] as required by GaussianBlur.
func DVEDefocusBlur(ctx *Context, frame *GPUFrame, defocusRadius float64) error {
	if defocusRadius <= 0 {
		return nil // no defocus
	}
	radius := int(defocusRadius + 0.5)
	if radius < 1 {
		radius = 1
	} else if radius > 50 {
		radius = 50
	}
	return GaussianBlur(ctx, frame, frame, radius)
}
