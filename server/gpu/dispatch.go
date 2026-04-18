package gpu

import (
	"image"

	"github.com/zsiec/switchframe/server/dve"
)

// GPU dispatch functions route to NV12 or P210 kernel variants based on the
// GPUFrame's Format field. All callers in the pipeline nodes should use these
// dispatch functions instead of calling NV12-only operations directly, so the
// pipeline transparently handles both 8-bit and 10-bit frames.

// GPUFillBlackDispatch fills a frame with black, routing to the correct format.
func GPUFillBlackDispatch(ctx *Context, frame *GPUFrame) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return FillBlackP210(ctx, frame)
	}
	return FillBlack(ctx, frame)
}

// GPUFillColorDispatch fills a frame with a constant YUV color, routing to the
// correct format. For P210, promotes 8-bit YUVColor values to 10-bit (<<2).
func GPUFillColorDispatch(ctx *Context, frame *GPUFrame, color YUVColor) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return FillColorP210(ctx, frame, uint16(color.Y)<<2, uint16(color.Cb)<<2, uint16(color.Cr)<<2)
	}
	return FillColor(ctx, frame, color)
}

// GPUUploadDispatch uploads a CPU planar buffer to a GPU frame, selecting the
// correct kernel based on the frame's format.
//   - NV12: expects YUV420p (w*h*3/2 bytes), calls Upload.
//   - P210: expects YUV422P10LE (w*h*4 bytes), calls Upload10.
func GPUUploadDispatch(ctx *Context, frame *GPUFrame, data []byte, width, height int) error {
	switch frame.Format {
	case GPUFormatP210:
		return Upload10(ctx, frame, data, width, height)
	default:
		return Upload(ctx, frame, data, width, height)
	}
}

// GPUDownloadDispatch downloads a GPU frame to a CPU planar buffer, selecting
// the correct kernel based on the frame's format.
//   - NV12: writes YUV420p, calls Download.
//   - P210: writes YUV422P10LE, calls Download10.
func GPUDownloadDispatch(ctx *Context, frame *GPUFrame, data []byte, width, height int) error {
	switch frame.Format {
	case GPUFormatP210:
		return Download10(ctx, frame, data, width, height)
	default:
		return Download(ctx, data, frame, width, height)
	}
}

// GPUBlendMixDispatch performs a uniform mix blend, dispatching to the
// P210 or NV12 variant based on the destination frame's format.
func GPUBlendMixDispatch(ctx *Context, dst, a, b *GPUFrame, position float64) error {
	switch dst.Format {
	case GPUFormatP210:
		return BlendMixP210(ctx, dst, a, b, position)
	default:
		return BlendMix(ctx, dst, a, b, position)
	}
}

// GPUBlendFTBDispatch performs a fade-to-black, dispatching to the
// P210 or NV12 variant based on the destination frame's format.
func GPUBlendFTBDispatch(ctx *Context, dst, src *GPUFrame, position float64) error {
	switch dst.Format {
	case GPUFormatP210:
		return BlendFTBP210(ctx, dst, src, position)
	default:
		return BlendFTB(ctx, dst, src, position)
	}
}

// GPUBlendWipeDispatch performs a wipe transition, dispatching to the
// P210 or NV12 variant based on the destination frame's format.
func GPUBlendWipeDispatch(ctx *Context, dst, a, b, maskBuf *GPUFrame, position float64, dir WipeDirection, softEdge int) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return BlendWipeP210(ctx, dst, a, b, maskBuf, position, dir, softEdge)
	}
	return BlendWipe(ctx, dst, a, b, maskBuf, position, dir, softEdge)
}

// GPUBlendStingerDispatch composites a stinger overlay, dispatching to the
// P210 or NV12 variant based on the destination frame's format.
func GPUBlendStingerDispatch(ctx *Context, dst, base, overlay, alpha *GPUFrame) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return BlendStingerP210(ctx, dst, base, overlay, alpha)
	}
	return BlendStinger(ctx, dst, base, overlay, alpha)
}

// GPUScaleDispatch scales a GPU frame using bilinear interpolation,
// dispatching to the P210 or NV12 variant based on the source frame's format.
func GPUScaleDispatch(ctx *Context, dst, src *GPUFrame) error {
	switch src.Format {
	case GPUFormatP210:
		return ScaleBilinearP210(ctx, dst, src)
	default:
		return ScaleBilinear(ctx, dst, src)
	}
}

// GPUScaleLanczos3Dispatch scales a GPU frame using Lanczos-3 interpolation,
// dispatching to the P210 or NV12 variant based on the destination frame's format.
func GPUScaleLanczos3Dispatch(ctx *Context, dst, src *GPUFrame) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return ScaleLanczos3P210(ctx, dst, src)
	}
	return ScaleLanczos3(ctx, dst, src)
}

// GPUPIPCompositeDispatch composites a source into a destination rectangle,
// dispatching to the P210 or NV12 variant based on the destination frame's format.
func GPUPIPCompositeDispatch(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	switch dst.Format {
	case GPUFormatP210:
		return PIPCompositeP210(ctx, dst, src, rect, alpha)
	default:
		return PIPComposite(ctx, dst, src, rect, alpha)
	}
}

// GPUPIPCompositeWithCropDispatch composites a source sub-region into a
// destination rectangle, dispatching based on the destination frame's format.
func GPUPIPCompositeWithCropDispatch(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64,
	cropX, cropY, cropW, cropH int) error {
	switch dst.Format {
	case GPUFormatP210:
		return PIPCompositeP210WithCrop(ctx, dst, src, rect, alpha, cropX, cropY, cropW, cropH)
	default:
		return PIPCompositeWithCrop(ctx, dst, src, rect, alpha, cropX, cropY, cropW, cropH)
	}
}

// GPUChromaKeyDispatch generates an alpha mask, picking the chroma key
// kernel by frame format. P210 program frames go through the 10-bit
// kernel; everything else through the legacy NV12 kernel.
func GPUChromaKeyDispatch(ctx *Context, frame, maskBuf *GPUFrame, cfg ChromaKeyConfig) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return ChromaKeyP210(ctx, frame, maskBuf, cfg)
	}
	return ChromaKey(ctx, frame, maskBuf, cfg)
}

// GPULumaKeyDispatch generates an alpha mask, picking the luma key
// kernel by frame format.
func GPULumaKeyDispatch(ctx *Context, frame, maskBuf *GPUFrame, lut [256]byte) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return LumaKeyP210(ctx, frame, maskBuf, lut)
	}
	return LumaKey(ctx, frame, maskBuf, lut)
}

// GPUSTMapWarpDispatch applies an ST map warp, dispatching to the P210 or NV12
// variant based on the source frame's format.
func GPUSTMapWarpDispatch(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	switch src.Format {
	case GPUFormatP210:
		return STMapWarpP210Map(ctx, dst, src, stmap)
	default:
		return STMapWarp(ctx, dst, src, stmap)
	}
}

// GPUFillRectDispatch fills a rectangular region with a constant color,
// dispatching to the P210 or NV12 variant based on the frame's format.
// For P210, the 8-bit YUVColor is promoted to 10-bit (<<2).
func GPUFillRectDispatch(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return FillRectP210(ctx, frame, rect,
			uint16(color.Y)<<2, uint16(color.Cb)<<2, uint16(color.Cr)<<2)
	}
	return FillRect(ctx, frame, rect, color)
}

// GPUBlendWipeMapDispatch performs a gradient-map-driven wipe transition,
// dispatching to the P210 or NV12 variant based on the destination frame's format.
func GPUBlendWipeMapDispatch(ctx *Context, dst, srcA, srcB, gradMap *GPUFrame, cfg WipeMapBlendConfig) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return BlendWipeMapP210(ctx, dst, srcA, srcB, gradMap, cfg)
	}
	return BlendWipeMap(ctx, dst, srcA, srcB, gradMap, cfg)
}

// GPUDrawBorderDispatch draws a colored border outside the given rectangle,
// dispatching to the P210 or NV12 variant based on the frame's format.
// For P210, the 8-bit YUVColor is promoted to 10-bit (<<2).
func GPUDrawBorderDispatch(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor, thickness int) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return DrawBorderP210(ctx, frame, rect,
			uint16(color.Y)<<2, uint16(color.Cb)<<2, uint16(color.Cr)<<2, thickness)
	}
	return DrawBorder(ctx, frame, rect, color, thickness)
}

// GPUDVECompositeDispatch composites a DVE slot onto the destination frame,
// dispatching to the P210 or NV12 variant based on the destination frame's format.
func GPUDVECompositeDispatch(ctx *Context, dst, src *GPUFrame, transform dve.Transform,
	invMatrix dve.Matrix3x3, srcW, srcH int, aabb image.Rectangle) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return DVECompositeP210(ctx, dst, src, transform, invMatrix, srcW, srcH, aabb)
	}
	return DVEComposite(ctx, dst, src, transform, invMatrix, srcW, srcH, aabb)
}

// GPUDVE3DCompositeDispatch composites a 3D DVE slot onto the destination frame,
// dispatching to the P210 or NV12 variant based on the destination frame's format.
func GPUDVE3DCompositeDispatch(ctx *Context, dst, src *GPUFrame, t3d *dve.Transform3D,
	invMVP dve.Matrix4x4, srcW, srcH int, slotRect image.Rectangle, alpha256 int) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return DVE3DCompositeP210(ctx, dst, src, t3d, invMVP, srcW, srcH, slotRect, alpha256)
	}
	return DVE3DComposite(ctx, dst, src, t3d, invMVP, srcW, srcH, slotRect, alpha256)
}
