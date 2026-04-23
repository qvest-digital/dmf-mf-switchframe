package graphics

// sampleRGBABilinear samples an 8-bit RGBA overlay at sub-pixel source
// coordinates using bilinear interpolation. srcX and srcY are 16.16
// fixed-point: the upper 16 bits index the source texel and the lower 16 bits
// weight the next-texel neighbour (fx=0 selects the primary texel exactly).
//
// The coordinate convention is top-left-anchored: srcX=0 maps to overlay
// column 0, not column 0.5. This (not center-anchored) is what makes
// matched-resolution sampling reduce to an exact pixel fetch and preserves
// byte-equal identity with the full-frame AlphaBlendRGBA10 fast path.
//
// Returns the interpolated (r, g, b, a) as uint8. The fixed-point arithmetic
// runs in int64 to avoid overflow on the four-tap weighted sum and matches the
// precision convention used by the plane-based scalePlane in server/transition.
//
// Intended as the shared primitive for any compositor that needs to scale an
// RGBA overlay into a sub-region target: the DSK 10-bit path in
// AlphaBlendRGBARect10 is the first caller; the 8-bit sibling
// AlphaBlendRGBARect is a natural next adoption.
func sampleRGBABilinear(rgba []byte, overlayW, overlayH int, srcX, srcY int64) (r, g, b, a uint8) {
	ix := int(srcX >> 16)
	iy := int(srcY >> 16)
	fx := int(srcX & 0xFFFF)
	fy := int(srcY & 0xFFFF)

	if ix < 0 {
		ix = 0
		fx = 0
	}
	if iy < 0 {
		iy = 0
		fy = 0
	}
	if ix >= overlayW-1 {
		ix = overlayW - 1
		fx = 0
	}
	if iy >= overlayH-1 {
		iy = overlayH - 1
		fy = 0
	}

	ix1 := ix + 1
	if ix1 >= overlayW {
		ix1 = ix
	}
	iy1 := iy + 1
	if iy1 >= overlayH {
		iy1 = iy
	}

	p00 := (iy*overlayW + ix) * 4
	p01 := (iy*overlayW + ix1) * 4
	p10 := (iy1*overlayW + ix) * 4
	p11 := (iy1*overlayW + ix1) * 4

	invFx := int64(65536 - fx)
	invFy := int64(65536 - fy)
	fxi := int64(fx)
	fyi := int64(fy)

	sampleChan := func(off int) uint8 {
		c00 := int64(rgba[p00+off])
		c01 := int64(rgba[p01+off])
		c10 := int64(rgba[p10+off])
		c11 := int64(rgba[p11+off])

		v0 := c00*invFx + c01*fxi
		v1 := c10*invFx + c11*fxi

		// Round half-up before dividing by 65536*65536.
		result := v0*invFy + v1*fyi
		return uint8((result + (1 << 31)) >> 32)
	}

	return sampleChan(0), sampleChan(1), sampleChan(2), sampleChan(3)
}
