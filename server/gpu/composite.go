//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t pip_composite_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    int rectX, int rectY, int rectW, int rectH,
    int alpha256,
    int cropX, int cropY, int cropW, int cropH,
    cudaStream_t stream);
cudaError_t draw_border_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    int thickness, uint8_t colorY, uint8_t colorCb, uint8_t colorCr,
    cudaStream_t stream);
cudaError_t fill_rect_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    uint8_t colorY, uint8_t colorCb, uint8_t colorCr,
    cudaStream_t stream);

// DVE composite structs and functions
typedef struct {
    int dstW, dstH, dstPitch;
    int srcW, srcH, srcPitch;
    float m00, m01, m02, m10, m11, m12, m20, m21, m22;
    int slotMinX, slotMinY, slotMaxX, slotMaxY;
    int alpha256;
    float cropTop, cropBottom, cropLeft, cropRight, cropSoftness;
    float borderWidth;
    int borderColorY;
    float borderCornerRadius, borderSoftness;
    int shadowEnabled;
    float shadowOffsetX, shadowOffsetY, shadowBlur;
    int shadowAlpha256;
    int shadowColorY;
    int mirrorH;
    int mirrorV;
    int mosaicSize;
    float brightness;
    float contrast;
} DVECompositeYParams;

typedef struct {
    int dstW, dstChromaH, dstPitch;
    int srcW, srcChromaH, srcPitch;
    float m00, m01, m02, m10, m11, m12, m20, m21, m22;
    int slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int alpha256;
    float cropTop, cropBottom, cropLeft, cropRight, cropSoftness;
    float borderWidth;
    int borderColorCb, borderColorCr;
    float borderCornerRadius, borderSoftness;
    int shadowEnabled;
    float shadowOffsetX, shadowOffsetY, shadowBlur;
    int shadowAlpha256;
    int shadowColorCb, shadowColorCr;
    int mirrorH;
    int mirrorV;
    int mosaicSize;
    float saturation;
} DVECompositeUVParams;

cudaError_t dve_composite_y(
    uint8_t* dst, const uint8_t* src,
    DVECompositeYParams params, cudaStream_t stream);
cudaError_t dve_composite_uv(
    uint8_t* dstUV, const uint8_t* srcUV,
    DVECompositeUVParams params, cudaStream_t stream);

// 3D DVE composite structs and functions
typedef struct {
    int dstW, dstH, dstPitch;
    int srcW, srcH, srcPitch;
    float invMVP[16];
    int slotMinX, slotMinY, slotMaxX, slotMaxY;
    int alpha256;
    int shape;
    int lightEnabled;
    float lightX, lightY, lightZ;
    float ambient, specular;
    float srcAspect;
    int reflectionEnabled;
    float reflectionOpacity, reflectionFade;
    float curlProgress;
    float curlRadius;
} DVE3DParams;

typedef struct {
    int dstW, dstChromaH, dstPitch;
    int srcW, srcChromaH, srcPitch;
    float invMVP[16];
    int slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int alpha256;
    int shape;
    int lightEnabled;
    float lightX, lightY, lightZ;
    float ambient, specular;
    float srcAspect;
    int reflectionEnabled;
    float reflectionOpacity, reflectionFade;
    float curlProgress;
    float curlRadius;
} DVE3DUVParams;

cudaError_t dve3d_composite_y(
    uint8_t* dst, const uint8_t* src,
    DVE3DParams params, cudaStream_t stream);
cudaError_t dve3d_composite_uv(
    uint8_t* dstUV, const uint8_t* srcUV,
    DVE3DUVParams params, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/zsiec/switchframe/server/dve"
)

// Rect defines a rectangle for PIP compositing.
type Rect struct {
	X, Y, W, H int
}

// YUVColor defines a color in YCbCr space.
type YUVColor struct {
	Y, Cb, Cr uint8
}

// ColorBlack is BT.709 limited-range black.
var ColorBlack = YUVColor{16, 128, 128}

// PIPComposite scales a source GPU frame and composites it into a destination
// frame at the specified rectangle. alpha is 0.0 (transparent) to 1.0 (opaque).
// Equivalent to PIPCompositeWithCrop with zero crop rect (full source).
func PIPComposite(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	return PIPCompositeWithCrop(ctx, dst, src, rect, alpha, 0, 0, 0, 0)
}

// PIPCompositeWithCrop scales a source GPU frame (or a sub-region of it) and
// composites it into a destination frame at the specified rectangle.
// cropX/cropY/cropW/cropH define the source crop region; 0,0,0,0 means use
// the full source (backward compatible with PIPComposite).
// All crop coordinates must be even-aligned for YUV420 correctness.
func PIPCompositeWithCrop(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64,
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

	rc := C.pip_composite_nv12(
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
		return fmt.Errorf("gpu: pip composite failed: %d", rc)
	}
	return ctx.Sync()
}

// DrawBorder draws a colored border outside the given rectangle on a GPU frame.
func DrawBorder(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor, thickness int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rc := C.draw_border_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(thickness),
		C.uint8_t(color.Y), C.uint8_t(color.Cb), C.uint8_t(color.Cr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: draw border failed: %d", rc)
	}
	return ctx.Sync()
}

// DVEComposite composites a single DVE slot onto the destination GPU frame
// using the CUDA DVE kernel. The kernel performs inverse projective transform,
// bilinear sampling, SDF border/shadow rendering, crop with soft edges,
// and alpha compositing — all in a single dispatch per plane.
func DVEComposite(ctx *Context, dst, src *GPUFrame, transform dve.Transform,
	invMatrix dve.Matrix3x3, srcW, srcH int, aabb image.Rectangle) error {
	if ctx == nil || dst == nil || src == nil {
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
	shadowEnabled := 0
	if transform.Shadow.Enabled {
		shadowEnabled = 1
		shadowAlpha256 = int(transform.Shadow.Opacity * 256.0)
		if shadowAlpha256 < 0 {
			shadowAlpha256 = 0
		} else if shadowAlpha256 > 256 {
			shadowAlpha256 = 256
		}
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

	mirrorH := 0
	if transform.MirrorH {
		mirrorH = 1
	}
	mirrorV := 0
	if transform.MirrorV {
		mirrorV = 1
	}

	// --- Y plane ---
	yParams := C.DVECompositeYParams{
		dstW: C.int(dst.Width), dstH: C.int(dst.Height), dstPitch: C.int(dst.Pitch),
		srcW: C.int(srcW), srcH: C.int(srcH), srcPitch: C.int(src.Pitch),

		m00: C.float(invMatrix[0]), m01: C.float(invMatrix[1]), m02: C.float(invMatrix[2]),
		m10: C.float(invMatrix[3]), m11: C.float(invMatrix[4]), m12: C.float(invMatrix[5]),
		m20: C.float(invMatrix[6]), m21: C.float(invMatrix[7]), m22: C.float(invMatrix[8]),

		slotMinX: C.int(aabb.Min.X), slotMinY: C.int(aabb.Min.Y),
		slotMaxX: C.int(aabb.Max.X), slotMaxY: C.int(aabb.Max.Y),

		alpha256: C.int(alpha256),

		cropTop: C.float(transform.CropTop), cropBottom: C.float(transform.CropBottom),
		cropLeft: C.float(transform.CropLeft), cropRight: C.float(transform.CropRight),
		cropSoftness: C.float(transform.CropSoftness),

		borderWidth:        C.float(transform.Border.Width),
		borderColorY:       C.int(transform.Border.ColorY),
		borderCornerRadius: C.float(transform.Border.CornerRadius),
		borderSoftness:     C.float(transform.Border.Softness),

		shadowEnabled:  C.int(shadowEnabled),
		shadowOffsetX:  C.float(transform.Shadow.OffsetX),
		shadowOffsetY:  C.float(transform.Shadow.OffsetY),
		shadowBlur:     C.float(transform.Shadow.Blur),
		shadowAlpha256: C.int(shadowAlpha256),
		shadowColorY:   C.int(transform.Shadow.ColorY),

		mirrorH:    C.int(mirrorH),
		mirrorV:    C.int(mirrorV),
		mosaicSize: C.int(transform.MosaicSize),
		brightness: C.float(transform.Brightness),
		contrast:   C.float(contrast),
	}

	rc := C.dve_composite_y(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		yParams,
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: dve composite Y failed: %d", rc)
	}

	// --- UV plane ---
	dstUVPtr := uintptr(dst.DevPtr) + uintptr(dst.Pitch*dst.Height)
	srcUVPtr := uintptr(src.DevPtr) + uintptr(src.Pitch*src.Height)

	// Chroma AABB: halved coordinates with 1px expansion to avoid off-by-1 at edges.
	chromaW := dst.Width / 2
	chromaH := dst.Height / 2
	chromaMinX := max(aabb.Min.X/2-1, 0)
	chromaMinY := max(aabb.Min.Y/2-1, 0)
	chromaMaxX := min(aabb.Max.X/2+1, chromaW)
	chromaMaxY := min(aabb.Max.Y/2+1, chromaH)

	uvParams := C.DVECompositeUVParams{
		dstW: C.int(dst.Width), dstChromaH: C.int(dst.Height / 2), dstPitch: C.int(dst.Pitch),
		srcW: C.int(srcW), srcChromaH: C.int(srcH / 2), srcPitch: C.int(src.Pitch),

		m00: C.float(invMatrix[0]), m01: C.float(invMatrix[1]), m02: C.float(invMatrix[2]),
		m10: C.float(invMatrix[3]), m11: C.float(invMatrix[4]), m12: C.float(invMatrix[5]),
		m20: C.float(invMatrix[6]), m21: C.float(invMatrix[7]), m22: C.float(invMatrix[8]),

		slotMinCX: C.int(chromaMinX), slotMinCY: C.int(chromaMinY),
		slotMaxCX: C.int(chromaMaxX), slotMaxCY: C.int(chromaMaxY),

		alpha256: C.int(alpha256),

		cropTop: C.float(transform.CropTop), cropBottom: C.float(transform.CropBottom),
		cropLeft: C.float(transform.CropLeft), cropRight: C.float(transform.CropRight),
		cropSoftness: C.float(transform.CropSoftness),

		borderWidth:        C.float(transform.Border.Width),
		borderColorCb:      C.int(transform.Border.ColorCb),
		borderColorCr:      C.int(transform.Border.ColorCr),
		borderCornerRadius: C.float(transform.Border.CornerRadius),
		borderSoftness:     C.float(transform.Border.Softness),

		shadowEnabled:  C.int(shadowEnabled),
		shadowOffsetX:  C.float(transform.Shadow.OffsetX),
		shadowOffsetY:  C.float(transform.Shadow.OffsetY),
		shadowBlur:     C.float(transform.Shadow.Blur),
		shadowAlpha256: C.int(shadowAlpha256),
		shadowColorCb:  C.int(128), // achromatic shadow: neutral chroma
		shadowColorCr:  C.int(128), // neutral Cr for shadow

		mirrorH:    C.int(mirrorH),
		mirrorV:    C.int(mirrorV),
		mosaicSize: C.int(transform.MosaicSize),
		saturation: C.float(saturation),
	}

	rc = C.dve_composite_uv(
		(*C.uint8_t)(unsafe.Pointer(dstUVPtr)),
		(*C.uint8_t)(unsafe.Pointer(srcUVPtr)),
		uvParams,
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: dve composite UV failed: %d", rc)
	}

	return ctx.Sync()
}

// FillRect fills a rectangular region with a constant color on a GPU frame.
func FillRect(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rc := C.fill_rect_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.uint8_t(color.Y), C.uint8_t(color.Cb), C.uint8_t(color.Cr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: fill rect failed: %d", rc)
	}
	return ctx.Sync()
}

// DVE3DComposite composites a 3D DVE slot using CUDA ray-casting kernels.
func DVE3DComposite(ctx *Context, dst, src *GPUFrame, t3d *dve.Transform3D,
	invMVP dve.Matrix4x4, srcW, srcH int, slotRect image.Rectangle, alpha256 int) error {
	if ctx == nil || dst == nil || src == nil || t3d == nil {
		return ErrGPUNotAvailable
	}

	shape := 0
	if t3d.Shape == "cube" {
		shape = 1
	} else if t3d.Shape == "page_turn" {
		shape = 2
	}
	ambient := t3d.Ambient
	if ambient <= 0 {
		ambient = 0.3
	}
	lightZ := 1.0

	var cInvMVP [16]C.float
	for i := 0; i < 16; i++ {
		cInvMVP[i] = C.float(invMVP[i])
	}

	yParams := C.DVE3DParams{
		dstW: C.int(dst.Width), dstH: C.int(dst.Height), dstPitch: C.int(dst.Pitch),
		srcW: C.int(srcW), srcH: C.int(srcH), srcPitch: C.int(src.Pitch),
		slotMinX: C.int(slotRect.Min.X), slotMinY: C.int(slotRect.Min.Y),
		slotMaxX: C.int(slotRect.Max.X), slotMaxY: C.int(slotRect.Max.Y),
		alpha256:     C.int(alpha256),
		shape:        C.int(shape),
		lightEnabled: boolToInt(t3d.LightEnabled),
		lightX:       C.float(t3d.LightX), lightY: C.float(t3d.LightY), lightZ: C.float(lightZ),
		ambient: C.float(ambient), specular: C.float(t3d.Specular),
		srcAspect:         C.float(float32(srcW) / float32(srcH)),
		reflectionEnabled: boolToInt(t3d.ReflectionEnabled),
		reflectionOpacity: C.float(t3d.ReflectionOpacity),
		reflectionFade:    C.float(t3d.ReflectionFade),
		curlProgress:      C.float(t3d.CurlProgress),
		curlRadius:        C.float(t3d.CurlRadius),
	}
	copy(yParams.invMVP[:], cInvMVP[:])

	rc := C.dve3d_composite_y(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		yParams, ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: DVE3D composite Y failed: %d", rc)
	}

	dstUVPtr := uintptr(dst.DevPtr) + uintptr(dst.Pitch*dst.Height)
	srcUVPtr := uintptr(src.DevPtr) + uintptr(src.Pitch*srcH)

	uvParams := C.DVE3DUVParams{
		dstW: C.int(dst.Width), dstChromaH: C.int(dst.Height / 2), dstPitch: C.int(dst.Pitch),
		srcW: C.int(srcW), srcChromaH: C.int(srcH / 2), srcPitch: C.int(src.Pitch),
		slotMinCX:    C.int(max(slotRect.Min.X/2-1, 0)),
		slotMinCY:    C.int(max(slotRect.Min.Y/2-1, 0)),
		slotMaxCX:    C.int(min(slotRect.Max.X/2+1, dst.Width/2)),
		slotMaxCY:    C.int(min(slotRect.Max.Y/2+1, dst.Height/2)),
		alpha256:     C.int(alpha256),
		shape:        C.int(shape),
		lightEnabled: boolToInt(t3d.LightEnabled),
		lightX:       C.float(t3d.LightX), lightY: C.float(t3d.LightY), lightZ: C.float(lightZ),
		ambient: C.float(ambient), specular: C.float(t3d.Specular),
		srcAspect:         C.float(float32(srcW) / float32(srcH)),
		reflectionEnabled: boolToInt(t3d.ReflectionEnabled),
		reflectionOpacity: C.float(t3d.ReflectionOpacity),
		reflectionFade:    C.float(t3d.ReflectionFade),
		curlProgress:      C.float(t3d.CurlProgress),
		curlRadius:        C.float(t3d.CurlRadius),
	}
	copy(uvParams.invMVP[:], cInvMVP[:])

	rc = C.dve3d_composite_uv(
		(*C.uint8_t)(unsafe.Pointer(dstUVPtr)),
		(*C.uint8_t)(unsafe.Pointer(srcUVPtr)),
		uvParams, ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: DVE3D composite UV failed: %d", rc)
	}

	return ctx.Sync()
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

func boolToInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
