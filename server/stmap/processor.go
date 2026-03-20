package stmap

// Processor applies an ST map warp to YUV420 frames using bilinear
// interpolation. It precomputes 16.16 fixed-point lookup tables from the
// float32 ST map coordinates so the per-pixel hot path is integer-only.
type Processor struct {
	stmap *STMap
	// LUTs for the Y plane (full resolution).
	lutSX []int64
	lutSY []int64
	// LUTs for chroma planes (half resolution).
	lutCSX []int64
	lutCSY []int64
}

// NewProcessor creates a Processor that warps frames according to the
// given ST map. If stmap is nil the processor is inactive and ProcessYUV
// is a no-op.
func NewProcessor(stmap *STMap) *Processor {
	p := &Processor{stmap: stmap}
	if stmap != nil {
		p.buildLUT()
	}
	return p
}

// Active returns true if a map is loaded and the processor will warp frames.
func (p *Processor) Active() bool {
	return p.stmap != nil
}

// ProcessYUV applies the ST map warp to a YUV420 frame. src and dst must
// both be w*h*3/2 bytes. The three planes (Y, Cb, Cr) are warped
// independently using precomputed lookup tables.
func (p *Processor) ProcessYUV(dst, src []byte, w, h int) {
	if p.stmap == nil {
		return
	}

	ySize := w * h
	cw := w / 2
	ch := h / 2
	cSize := cw * ch

	// Y plane
	warpPlane(dst[:ySize], src[:ySize], w, h, p.lutSX, p.lutSY)

	// Cb plane
	cbOff := ySize
	warpPlane(dst[cbOff:cbOff+cSize], src[cbOff:cbOff+cSize], cw, ch, p.lutCSX, p.lutCSY)

	// Cr plane
	crOff := ySize + cSize
	warpPlane(dst[crOff:crOff+cSize], src[crOff:crOff+cSize], cw, ch, p.lutCSX, p.lutCSY)
}

// buildLUT precomputes the 16.16 fixed-point source coordinate lookup
// tables for both luma (full res) and chroma (half res) planes.
func (p *Processor) buildLUT() {
	m := p.stmap
	w := m.Width
	h := m.Height
	n := w * h
	fw := float64(w)
	fh := float64(h)

	p.lutSX = make([]int64, n)
	p.lutSY = make([]int64, n)

	for i := 0; i < n; i++ {
		sx := float64(m.S[i])*fw - 0.5
		sy := float64(m.T[i])*fh - 0.5
		p.lutSX[i] = int64(sx * 65536)
		p.lutSY[i] = int64(sy * 65536)
	}

	// Chroma LUT: average the 4 luma values in each 2x2 block, then
	// convert to half-resolution pixel coordinates.
	cw := w / 2
	ch := h / 2
	cn := cw * ch
	p.lutCSX = make([]int64, cn)
	p.lutCSY = make([]int64, cn)

	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			// Indices of the 4 luma pixels in this 2x2 block.
			ly := cy * 2
			lx := cx * 2
			i00 := ly*w + lx
			i10 := ly*w + lx + 1
			i01 := (ly+1)*w + lx
			i11 := (ly+1)*w + lx + 1

			// Average normalized ST values from the 4 luma positions.
			avgS := (float64(m.S[i00]) + float64(m.S[i10]) + float64(m.S[i01]) + float64(m.S[i11])) / 4.0
			avgT := (float64(m.T[i00]) + float64(m.T[i10]) + float64(m.T[i01]) + float64(m.T[i11])) / 4.0

			// Convert to half-res pixel coords with center offset.
			csx := avgS*float64(cw) - 0.5
			csy := avgT*float64(ch) - 0.5

			ci := cy*cw + cx
			p.lutCSX[ci] = int64(csx * 65536)
			p.lutCSY[ci] = int64(csy * 65536)
		}
	}
}

// warpPlane applies bilinear interpolation for one plane using 16.16
// fixed-point source coordinates from the precomputed LUT.
func warpPlane(dst, src []byte, w, h int, lutX, lutY []int64) {
	maxX := int64(w-1) << 16
	maxY := int64(h-1) << 16
	lastCol := w - 1
	lastRow := h - 1

	for i := 0; i < w*h; i++ {
		sx := lutX[i]
		sy := lutY[i]

		// Clamp to valid source range.
		if sx < 0 {
			sx = 0
		} else if sx > maxX {
			sx = maxX
		}
		if sy < 0 {
			sy = 0
		} else if sy > maxY {
			sy = maxY
		}

		// Split into integer and fractional parts.
		ix := int(sx >> 16)
		iy := int(sy >> 16)
		fx := int(sx & 0xFFFF)
		fy := int(sy & 0xFFFF)

		// Neighbor pixel coordinates, clamped.
		ix1 := ix + 1
		if ix1 > lastCol {
			ix1 = lastCol
		}
		iy1 := iy + 1
		if iy1 > lastRow {
			iy1 = lastRow
		}

		// Sample 4 source pixels.
		p00 := int(src[iy*w+ix])
		p10 := int(src[iy*w+ix1])
		p01 := int(src[iy1*w+ix])
		p11 := int(src[iy1*w+ix1])

		// Bilinear interpolation using 16.16 fixed-point.
		invFx := 65536 - fx
		invFy := 65536 - fy
		val := ((p00*invFx+p10*fx)*invFy + (p01*invFx+p11*fx)*fy + (1 << 31)) >> 32

		// Clamp to [0, 255].
		if val < 0 {
			val = 0
		} else if val > 255 {
			val = 255
		}

		dst[i] = byte(val)
	}
}
