package layout

import (
	"image"

	"github.com/zsiec/switchframe/server/transition"
)

// ComposePIPOpaque copies a scaled PIP source YUV420 buffer into a sub-region
// of the destination frame. All three planes are copied at their native resolutions.
// rect.Min must be even-aligned for YUV420 correctness.
// Safe when rect extends past frame bounds — clips to destination dimensions.
func ComposePIPOpaque(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, rect image.Rectangle) {
	// Clip copy dimensions to what fits in the destination frame.
	copyW := srcW
	copyH := srcH
	if rect.Min.X+copyW > dstW {
		copyW = dstW - rect.Min.X
	}
	if rect.Min.Y+copyH > dstH {
		copyH = dstH - rect.Min.Y
	}
	if copyW <= 0 || copyH <= 0 || rect.Min.X < 0 || rect.Min.Y < 0 {
		return
	}

	// Y plane: row-by-row copy
	for y := 0; y < copyH; y++ {
		dstOff := (rect.Min.Y+y)*dstW + rect.Min.X
		srcOff := y * srcW
		copy(dst[dstOff:dstOff+copyW], src[srcOff:srcOff+copyW])
	}

	// Chroma planes
	dstYSize := dstW * dstH
	srcYSize := srcW * srcH
	chromaDstW := dstW / 2
	chromaSrcW := srcW / 2
	chromaCopyW := copyW / 2
	chromaCopyH := copyH / 2
	chromaX := rect.Min.X / 2
	chromaY := rect.Min.Y / 2

	for plane := 0; plane < 2; plane++ {
		dstBase := dstYSize + plane*(chromaDstW*(dstH/2))
		srcBase := srcYSize + plane*(chromaSrcW*(srcH/2))
		for y := 0; y < chromaCopyH; y++ {
			dstOff := dstBase + (chromaY+y)*chromaDstW + chromaX
			srcOff := srcBase + y*chromaSrcW
			copy(dst[dstOff:dstOff+chromaCopyW], src[srcOff:srcOff+chromaCopyW])
		}
	}
}

// DrawBorderYUV draws a solid border around a rectangle in the YUV420 frame.
// borderColor is {Y, Cb, Cr}. thickness is in luma pixels.
// The border is drawn OUTSIDE the rectangle (expanding outward).
func DrawBorderYUV(dst []byte, dstW, dstH int, rect image.Rectangle, borderColor [3]byte, thickness int) {
	if thickness <= 0 {
		return
	}
	ySize := dstW * dstH
	chromaW := dstW / 2
	chromaH := dstH / 2

	// Expand rect outward by thickness, clamp to frame bounds.
	outer := image.Rect(
		max(rect.Min.X-thickness, 0),
		max(rect.Min.Y-thickness, 0),
		min(rect.Max.X+thickness, dstW),
		min(rect.Max.Y+thickness, dstH),
	)

	// Draw 4 border strips (no per-pixel branch for interior skip).
	// Top strip: full outer width, rows from outer.Min.Y to rect.Min.Y
	fillYRows(dst, dstW, borderColor[0], outer.Min.X, outer.Max.X, outer.Min.Y, min(rect.Min.Y, outer.Max.Y))
	// Bottom strip: full outer width, rows from rect.Max.Y to outer.Max.Y
	fillYRows(dst, dstW, borderColor[0], outer.Min.X, outer.Max.X, max(rect.Max.Y, outer.Min.Y), outer.Max.Y)
	// Left strip: only the inner height (between top and bottom strips)
	innerTop := max(rect.Min.Y, outer.Min.Y)
	innerBot := min(rect.Max.Y, outer.Max.Y)
	fillYRows(dst, dstW, borderColor[0], outer.Min.X, min(rect.Min.X, outer.Max.X), innerTop, innerBot)
	// Right strip
	fillYRows(dst, dstW, borderColor[0], max(rect.Max.X, outer.Min.X), outer.Max.X, innerTop, innerBot)

	// Chroma planes: same 4-strip approach at half resolution.
	chromaOuter := image.Rect(outer.Min.X/2, outer.Min.Y/2, outer.Max.X/2, outer.Max.Y/2)
	chromaInner := image.Rect(rect.Min.X/2, rect.Min.Y/2, rect.Max.X/2, rect.Max.Y/2)

	fillChromaStrips(dst, ySize, chromaW, chromaH, borderColor[1], borderColor[2], chromaOuter, chromaInner)
}

// fillYRows fills a rectangular region of the Y plane with a constant value.
func fillYRows(dst []byte, stride int, val byte, x0, x1, y0, y1 int) {
	w := x1 - x0
	if w <= 0 || y1 <= y0 {
		return
	}
	for y := y0; y < y1; y++ {
		off := y*stride + x0
		row := dst[off : off+w]
		for i := range row {
			row[i] = val
		}
	}
}

// fillChromaStrips draws 4 border strips on both Cb and Cr chroma planes.
func fillChromaStrips(dst []byte, ySize, chromaW, chromaH int, cb, cr byte, outer, inner image.Rectangle) {
	cbBase := ySize
	crBase := ySize + chromaW*chromaH

	// Top strip
	fillChromaRows(dst, cbBase, crBase, chromaW, cb, cr, outer.Min.X, outer.Max.X, outer.Min.Y, min(inner.Min.Y, outer.Max.Y))
	// Bottom strip
	fillChromaRows(dst, cbBase, crBase, chromaW, cb, cr, outer.Min.X, outer.Max.X, max(inner.Max.Y, outer.Min.Y), outer.Max.Y)
	// Left strip (inner rows only)
	innerTop := max(inner.Min.Y, outer.Min.Y)
	innerBot := min(inner.Max.Y, outer.Max.Y)
	fillChromaRows(dst, cbBase, crBase, chromaW, cb, cr, outer.Min.X, min(inner.Min.X, outer.Max.X), innerTop, innerBot)
	// Right strip
	fillChromaRows(dst, cbBase, crBase, chromaW, cb, cr, max(inner.Max.X, outer.Min.X), outer.Max.X, innerTop, innerBot)
}

// fillChromaRows fills a rectangular region of both Cb and Cr planes.
func fillChromaRows(dst []byte, cbBase, crBase, stride int, cb, cr byte, x0, x1, y0, y1 int) {
	w := x1 - x0
	if w <= 0 || y1 <= y0 {
		return
	}
	for y := y0; y < y1; y++ {
		off := y*stride + x0
		cbRow := dst[cbBase+off : cbBase+off+w]
		crRow := dst[crBase+off : crBase+off+w]
		for i := range cbRow {
			cbRow[i] = cb
		}
		for i := range crRow {
			crRow[i] = cr
		}
	}
}

// FillRectBlack fills a rectangular region with BT.709 limited-range black
// (Y=16, Cb=128, Cr=128). Used for "no signal" slots to avoid scaling a
// uniform gray buffer through the scaler. Clips to frame bounds.
func FillRectBlack(dst []byte, dstW, dstH int, rect image.Rectangle) {
	fillW := rect.Dx()
	fillH := rect.Dy()
	if rect.Min.X+fillW > dstW {
		fillW = dstW - rect.Min.X
	}
	if rect.Min.Y+fillH > dstH {
		fillH = dstH - rect.Min.Y
	}
	if fillW <= 0 || fillH <= 0 || rect.Min.X < 0 || rect.Min.Y < 0 {
		return
	}

	// Y plane: BT.709 limited-range black
	for y := 0; y < fillH; y++ {
		off := (rect.Min.Y+y)*dstW + rect.Min.X
		row := dst[off : off+fillW]
		for i := range row {
			row[i] = 16
		}
	}

	// Chroma planes: neutral
	ySize := dstW * dstH
	chromaDstW := dstW / 2
	chromaFillW := fillW / 2
	chromaFillH := fillH / 2
	chromaX := rect.Min.X / 2
	chromaY := rect.Min.Y / 2

	for plane := 0; plane < 2; plane++ {
		base := ySize + plane*(chromaDstW*(dstH/2))
		for y := 0; y < chromaFillH; y++ {
			off := base + (chromaY+y)*chromaDstW + chromaX
			row := dst[off : off+chromaFillW]
			for i := range row {
				row[i] = 128
			}
		}
	}
}

// BlendRegion alpha-blends src onto dst for a rectangular region.
// alpha is 0.0 (fully transparent) to 1.0 (fully opaque).
// Used for dissolve transitions on PIP slots.
// Safe when rect extends past frame bounds — clips to destination dimensions.
func BlendRegion(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, rect image.Rectangle, alpha float64) {
	if alpha <= 0 {
		return
	}
	if alpha >= 1.0 {
		ComposePIPOpaque(dst, dstW, dstH, src, srcW, srcH, rect)
		return
	}

	// Clip to destination bounds.
	copyW := srcW
	copyH := srcH
	if rect.Min.X+copyW > dstW {
		copyW = dstW - rect.Min.X
	}
	if rect.Min.Y+copyH > dstH {
		copyH = dstH - rect.Min.Y
	}
	if copyW <= 0 || copyH <= 0 || rect.Min.X < 0 || rect.Min.Y < 0 {
		return
	}

	pos := int(alpha * 256)

	// Y plane: dispatch SIMD kernel per-row (each row is contiguous in memory).
	// BlendUniformBytes computes: dst[i] = (a[i]*(256-pos) + b[i]*pos) >> 8
	// With a=dst and b=src, this gives: dst[i] = (dst[i]*inv + src[i]*pos) >> 8
	for y := 0; y < copyH; y++ {
		dstOff := (rect.Min.Y+y)*dstW + rect.Min.X
		srcOff := y * srcW
		dstRow := dst[dstOff : dstOff+copyW]
		srcRow := src[srcOff : srcOff+copyW]
		transition.BlendUniformBytes(dstRow, dstRow, srcRow, pos)
	}

	// Chroma planes
	dstYSize := dstW * dstH
	srcYSize := srcW * srcH
	chromaDstW := dstW / 2
	chromaSrcW := srcW / 2
	chromaCopyW := copyW / 2
	chromaCopyH := copyH / 2
	chromaX := rect.Min.X / 2
	chromaY := rect.Min.Y / 2

	for plane := 0; plane < 2; plane++ {
		dstBase := dstYSize + plane*(chromaDstW*(dstH/2))
		srcBase := srcYSize + plane*(chromaSrcW*(srcH/2))
		for y := 0; y < chromaCopyH; y++ {
			dstOff := dstBase + (chromaY+y)*chromaDstW + chromaX
			srcOff := srcBase + y*chromaSrcW
			dstRow := dst[dstOff : dstOff+chromaCopyW]
			srcRow := src[srcOff : srcOff+chromaCopyW]
			transition.BlendUniformBytes(dstRow, dstRow, srcRow, pos)
		}
	}
}
