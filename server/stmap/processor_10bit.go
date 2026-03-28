package stmap

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Errors for 10-bit warp validation.
var (
	ErrInactive        = errors.New("stmap: processor is inactive (no ST map loaded)")
	ErrBufferTooSmall  = errors.New("stmap: buffer too small for given dimensions")
	ErrOddWidth        = errors.New("stmap: width must be even for YUV422")
	ErrZeroDimensions  = errors.New("stmap: width and height must be positive")
)

// yuv422p10Size returns the byte size of a YUV422P10LE frame.
// Y: w*h*2 bytes, Cb: (w/2)*h*2 bytes, Cr: (w/2)*h*2 bytes.
func yuv422p10Size(w, h int) int {
	return w*h*2 + 2*(w/2)*h*2
}

// Warp10 applies the ST map warp to a YUV422P10LE frame using bilinear
// interpolation. dst and src must be yuv422p10Size(w, h) bytes.
// LUTs are precomputed 16.16 fixed-point coordinates.
//
// Unlike the 8-bit YUV420 path, chroma planes here are full-height (4:2:2)
// with half-width. Dedicated 422 chroma LUTs are built lazily on first call.
func (p *Processor) Warp10(dst, src []byte, w, h int) error {
	if err := validateWarp10Params(p, dst, src, w, h); err != nil {
		return err
	}

	// Ensure 422 chroma LUTs are built.
	p.ensure422ChromaLUTs(w, h)

	ySize := w * h * 2
	chromaW := w / 2
	chromaPlaneSize := chromaW * h * 2

	// Y plane (full resolution) — uses platform-optimized assembly dispatch.
	warpPlane10Dispatch(dst[:ySize], src[:ySize], w, h, p.lutSX, p.lutSY)

	// Cb plane (half-width, full-height)
	cbOff := ySize
	warpPlane10Dispatch(dst[cbOff:cbOff+chromaPlaneSize], src[cbOff:cbOff+chromaPlaneSize],
		chromaW, h, p.lut422CSX, p.lut422CSY)

	// Cr plane (half-width, full-height)
	crOff := ySize + chromaPlaneSize
	warpPlane10Dispatch(dst[crOff:crOff+chromaPlaneSize], src[crOff:crOff+chromaPlaneSize],
		chromaW, h, p.lut422CSX, p.lut422CSY)

	return nil
}

// validateWarp10Params checks parameters for Warp10.
func validateWarp10Params(p *Processor, dst, src []byte, w, h int) error {
	if len(p.lutSX) == 0 {
		return ErrInactive
	}
	if w <= 0 || h <= 0 {
		return ErrZeroDimensions
	}
	if w%2 != 0 {
		return ErrOddWidth
	}
	needed := yuv422p10Size(w, h)
	if len(dst) < needed {
		return fmt.Errorf("%w: dst has %d bytes, need %d", ErrBufferTooSmall, len(dst), needed)
	}
	if len(src) < needed {
		return fmt.Errorf("%w: src has %d bytes, need %d", ErrBufferTooSmall, len(src), needed)
	}
	return nil
}

// lut422CSX and lut422CSY are chroma LUTs for 4:2:2 (half-width, full-height).
// They are built lazily on first Warp10 call because the standard 8-bit path
// uses 4:2:0 chroma LUTs which are quarter-resolution.
//
// Added to Processor struct below via the ensure method.

// ensure422ChromaLUTs builds the 422 chroma LUTs if not already built.
// For 422, chroma is half-width but full-height. We average the two
// horizontally adjacent Y-plane LUT entries for each chroma column.
func (p *Processor) ensure422ChromaLUTs(w, h int) {
	p.lut422Once.Do(func() {
		chromaW := w / 2
		n := chromaW * h
		p.lut422CSX = make([]int32, n)
		p.lut422CSY = make([]int32, n)

		for y := 0; y < h; y++ {
			for cx := 0; cx < chromaW; cx++ {
				// Average the two horizontally adjacent luma LUT entries.
				lx0 := cx * 2
				lx1 := lx0 + 1
				li0 := y*w + lx0
				li1 := y*w + lx1

				// Average in 32-bit to avoid overflow.
				avgSX := (int64(p.lutSX[li0]) + int64(p.lutSX[li1])) / 2
				avgSY := (int64(p.lutSY[li0]) + int64(p.lutSY[li1])) / 2

				// Scale from full-width to half-width coordinate space.
				// The luma LUTs are in full-resolution pixel coords (16.16).
				// For chroma at half-width, divide X by 2.
				ci := y*chromaW + cx
				p.lut422CSX[ci] = int32(avgSX / 2)
				p.lut422CSY[ci] = int32(avgSY)
			}
		}
	})
}

// warpPlane10 applies bilinear interpolation for one plane of a 10-bit frame.
// Each sample is a uint16 LE (2 bytes). The LUTs are in 16.16 fixed-point
// coordinates within the plane's coordinate space.
func warpPlane10(dst, src []byte, w, h int, lutX, lutY []int32) {
	n := w * h
	if n == 0 {
		return
	}

	maxX := int32((w - 1) << 16)
	maxY := int32((h - 1) << 16)
	lastCol := w - 1
	lastRow := h - 1

	for i := 0; i < n; i++ {
		sx := lutX[i]
		sy := lutY[i]

		// Clamp to valid range.
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

		ix := int(sx >> 16)
		iy := int(sy >> 16)
		fx := int32(sx & 0xFFFF)
		fy := int32(sy & 0xFFFF)

		ix1 := ix + 1
		if ix1 > lastCol {
			ix1 = lastCol
		}
		iy1 := iy + 1
		if iy1 > lastRow {
			iy1 = lastRow
		}

		// Read 4 neighboring uint16 LE samples.
		p00 := int32(binary.LittleEndian.Uint16(src[(iy*w+ix)*2:]))
		p10 := int32(binary.LittleEndian.Uint16(src[(iy*w+ix1)*2:]))
		p01 := int32(binary.LittleEndian.Uint16(src[(iy1*w+ix)*2:]))
		p11 := int32(binary.LittleEndian.Uint16(src[(iy1*w+ix1)*2:]))

		// Bilinear interpolation with 16.16 fractional parts.
		// For 10-bit samples (max 1023), sample * frac (max 65536) fits in int32.
		// But top/bot can be up to 1023*65536 = 67M, and the vertical pass
		// multiplies that by 65536, which overflows int32. Use int64 for the
		// vertical accumulation.
		invFx := 65536 - fx
		top := p00*invFx + p10*fx          // max ~67M, fits int32
		bot := p01*invFx + p11*fx          // max ~67M, fits int32
		invFy := int64(65536) - int64(fy)
		val := (int64(top)*invFy + int64(bot)*int64(fy) + (1 << 31)) >> 32

		// Clamp to 10-bit range.
		if val < 0 {
			val = 0
		} else if val > 1023 {
			val = 1023
		}

		binary.LittleEndian.PutUint16(dst[i*2:], uint16(val))
	}
}
