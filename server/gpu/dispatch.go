package gpu

// GPU dispatch functions route to NV12 or P210 kernel variants based on the
// GPUFrame's Format field. All callers in the pipeline nodes should use these
// dispatch functions instead of calling NV12-only operations directly, so the
// pipeline transparently handles both 8-bit and 10-bit frames.

// yuv10FrameSize returns the byte size of a YUV422P10LE planar frame.
// Y plane: w*h*2 (uint16) + Cb plane: (w/2)*h*2 + Cr plane: (w/2)*h*2 = w*h*4.
func yuv10FrameSize(w, h int) int {
	return w * h * 4
}

// cpuFrameSize returns the CPU-side planar buffer size for the given format.
// NV12 → YUV420p (w*h*3/2). P210 → YUV422P10LE (w*h*4).
func cpuFrameSize(w, h int, format GPUFrameFormat) int {
	switch format {
	case GPUFormatP210:
		return yuv10FrameSize(w, h)
	default:
		return yuvFrameSize(w, h)
	}
}

// GPUFillBlackDispatch fills a frame with black, routing to the correct format.
func GPUFillBlackDispatch(ctx *Context, frame *GPUFrame) error {
	if frame != nil && frame.Format == GPUFormatP210 {
		return FillBlackP210(ctx, frame)
	}
	return FillBlack(ctx, frame)
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
