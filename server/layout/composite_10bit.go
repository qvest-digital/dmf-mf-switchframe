package layout

import "encoding/binary"

// ComposePIPOpaque10 copies a scaled PIP source into dst at the given position.
// Both src and dst are YUV422P10LE format.
// dstX must be even for chroma alignment.
// Safe when src extends past frame bounds — clips to destination dimensions.
func ComposePIPOpaque10(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, dstX, dstY int) {
	// Clip copy dimensions to what fits in the destination frame.
	copyW := srcW
	copyH := srcH
	if dstX+copyW > dstW {
		copyW = dstW - dstX
	}
	if dstY+copyH > dstH {
		copyH = dstH - dstY
	}
	if copyW <= 0 || copyH <= 0 || dstX < 0 || dstY < 0 {
		return
	}

	dstYSize := dstW * dstH * 2
	srcYSize := srcW * srcH * 2

	// Y plane: row-by-row copy (uint16 LE samples, 2 bytes each)
	for y := 0; y < copyH; y++ {
		dstOff := ((dstY+y)*dstW + dstX) * 2
		srcOff := (y * srcW) * 2
		copy(dst[dstOff:dstOff+copyW*2], src[srcOff:srcOff+copyW*2])
	}

	// Chroma planes: 4:2:2 = half-width, FULL height
	chromaDstW := dstW / 2
	chromaSrcW := srcW / 2
	chromaCopyW := copyW / 2
	chromaX := dstX / 2

	dstCbBase := dstYSize
	srcCbBase := srcYSize
	dstChromaPlaneSize := chromaDstW * dstH * 2
	srcChromaPlaneSize := chromaSrcW * srcH * 2
	dstCrBase := dstCbBase + dstChromaPlaneSize
	srcCrBase := srcCbBase + srcChromaPlaneSize

	// Cb plane: full height
	for y := 0; y < copyH; y++ {
		dstOff := dstCbBase + ((dstY+y)*chromaDstW+chromaX)*2
		srcOff := srcCbBase + (y*chromaSrcW)*2
		copy(dst[dstOff:dstOff+chromaCopyW*2], src[srcOff:srcOff+chromaCopyW*2])
	}

	// Cr plane: full height
	for y := 0; y < copyH; y++ {
		dstOff := dstCrBase + ((dstY+y)*chromaDstW+chromaX)*2
		srcOff := srcCrBase + (y*chromaSrcW)*2
		copy(dst[dstOff:dstOff+chromaCopyW*2], src[srcOff:srcOff+chromaCopyW*2])
	}
}

// FillRectBlack10 fills a rectangle in a YUV422P10LE frame with 10-bit BT.709 black.
// Y=64, Cb=512, Cr=512 (limited range).
// Clips to frame bounds. x must be even for chroma alignment.
func FillRectBlack10(dst []byte, dstW, dstH int, x, y, w, h int) {
	fillRect10(dst, dstW, dstH, x, y, w, h, 64, 512, 512)
}

// fillRect10 fills a rectangle in a YUV422P10LE frame with the given Y/Cb/Cr values.
func fillRect10(dst []byte, dstW, dstH int, x, y, w, h int, valY, valCb, valCr uint16) {
	// Clip to frame bounds
	fillW := w
	fillH := h
	if x+fillW > dstW {
		fillW = dstW - x
	}
	if y+fillH > dstH {
		fillH = dstH - y
	}
	if fillW <= 0 || fillH <= 0 || x < 0 || y < 0 {
		return
	}

	ySize := dstW * dstH * 2
	chromaDstW := dstW / 2

	// Y plane: fill with valY (uint16 LE)
	var yBytes [2]byte
	binary.LittleEndian.PutUint16(yBytes[:], valY)
	for row := 0; row < fillH; row++ {
		off := ((y+row)*dstW + x) * 2
		for col := 0; col < fillW; col++ {
			dst[off+col*2] = yBytes[0]
			dst[off+col*2+1] = yBytes[1]
		}
	}

	// Chroma: 4:2:2 = half-width, full height
	chromaFillW := fillW / 2
	chromaX := x / 2
	chromaPlaneSize := chromaDstW * dstH * 2
	cbBase := ySize
	crBase := ySize + chromaPlaneSize

	var cbBytes, crBytes [2]byte
	binary.LittleEndian.PutUint16(cbBytes[:], valCb)
	binary.LittleEndian.PutUint16(crBytes[:], valCr)

	for row := 0; row < fillH; row++ {
		cbOff := cbBase + ((y+row)*chromaDstW+chromaX)*2
		crOff := crBase + ((y+row)*chromaDstW+chromaX)*2
		for col := 0; col < chromaFillW; col++ {
			dst[cbOff+col*2] = cbBytes[0]
			dst[cbOff+col*2+1] = cbBytes[1]
			dst[crOff+col*2] = crBytes[0]
			dst[crOff+col*2+1] = crBytes[1]
		}
	}
}

// DrawBorderYUV10 draws a colored border around a rectangle in YUV422P10LE.
// The border is drawn OUTSIDE the rectangle (expanding outward), clamped to frame bounds.
// borderWidth is in luma pixels. All coordinates should be even for chroma alignment.
func DrawBorderYUV10(dst []byte, dstW, dstH int, x, y, w, h, borderWidth int, borderY, borderCb, borderCr uint16) {
	if borderWidth <= 0 {
		return
	}

	// Expand rect outward by borderWidth, clamp to frame bounds.
	outerX0 := max10(x-borderWidth, 0)
	outerY0 := max10(y-borderWidth, 0)
	outerX1 := min10(x+w+borderWidth, dstW)
	outerY1 := min10(y+h+borderWidth, dstH)

	// Top strip: full outer width, rows from outerY0 to y
	topH := min10(y, outerY1) - outerY0
	if topH > 0 {
		fillRect10Strip(dst, dstW, dstH, outerX0, outerY0, outerX1-outerX0, topH, borderY, borderCb, borderCr)
	}

	// Bottom strip: full outer width, rows from y+h to outerY1
	botY := max10(y+h, outerY0)
	botH := outerY1 - botY
	if botH > 0 {
		fillRect10Strip(dst, dstW, dstH, outerX0, botY, outerX1-outerX0, botH, borderY, borderCb, borderCr)
	}

	// Left strip: only inner height
	innerTop := max10(y, outerY0)
	innerBot := min10(y+h, outerY1)
	leftW := min10(x, outerX1) - outerX0
	if leftW > 0 && innerBot > innerTop {
		fillRect10Strip(dst, dstW, dstH, outerX0, innerTop, leftW, innerBot-innerTop, borderY, borderCb, borderCr)
	}

	// Right strip
	rightX := max10(x+w, outerX0)
	rightW := outerX1 - rightX
	if rightW > 0 && innerBot > innerTop {
		fillRect10Strip(dst, dstW, dstH, rightX, innerTop, rightW, innerBot-innerTop, borderY, borderCb, borderCr)
	}
}

// fillRect10Strip fills a rectangle region with constant Y/Cb/Cr values.
// This is the internal helper that does no bounds clipping — callers must pre-clip.
func fillRect10Strip(dst []byte, dstW, dstH int, x, y, w, h int, valY, valCb, valCr uint16) {
	if w <= 0 || h <= 0 {
		return
	}
	fillRect10(dst, dstW, dstH, x, y, w, h, valY, valCb, valCr)
}

func max10(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min10(a, b int) int {
	if a < b {
		return a
	}
	return b
}
