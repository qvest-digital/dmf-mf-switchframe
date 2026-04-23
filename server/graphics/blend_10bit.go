package graphics

import (
	"encoding/binary"
	"fmt"
	"image"

	"github.com/zsiec/switchframe/server/internal/video"
)

// AlphaBlendRGBA10 composites an 8-bit RGBA overlay onto a YUV422P10LE frame in-place.
//
// The RGBA overlay uses standard 8-bit channels (as produced by browser/HTML5 graphics).
// The YUV buffer contains uint16 LE samples in 10-bit range (0-1023).
//
// For each pixel with non-zero alpha:
//  1. Convert RGBA to 10-bit BT.709 limited-range YCbCr
//  2. Alpha-blend the converted overlay onto the existing YUV sample
//
// The Y plane is blended per-pixel. Chroma planes are half-width, full-height (4:2:2),
// so overlay chroma is computed from even-column RGBA pixels.
//
// RGB to 10-bit YCbCr conversion uses BT.709 8-bit coefficients then scales by 4:
//
//	Y8  = (66*R + 129*G + 25*B + 128) >> 8 + 16   → Y10 = Y8 * 4
//	Cb8 = (-38*R - 74*G + 112*B + 128) >> 8 + 128  → Cb10 = Cb8 * 4
//	Cr8 = (112*R - 94*G - 18*B + 128) >> 8 + 128   → Cr10 = Cr8 * 4
//
// This maps 8-bit limited range (Y: 16-235, C: 16-240) to 10-bit limited range
// (Y: 64-940, C: 64-960) with neutral chroma at 512.
func AlphaBlendRGBA10(yuv []byte, rgba []byte, frameW, frameH int) error {
	if frameW%2 != 0 {
		return fmt.Errorf("YUV422 requires even width: got %d", frameW)
	}
	if frameW <= 0 || frameH <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", frameW, frameH)
	}
	needYUV := video.YUV422_10bitFrameSize(frameW, frameH)
	if len(yuv) < needYUV {
		return fmt.Errorf("YUV buffer too small: got %d, need %d", len(yuv), needYUV)
	}
	needRGBA := frameW * frameH * 4
	if len(rgba) < needRGBA {
		return fmt.Errorf("RGBA buffer too small: got %d, need %d", len(rgba), needRGBA)
	}

	ySize := frameW * frameH
	chromaW := frameW / 2
	chromaSize := chromaW * frameH
	cbByteOff := ySize * 2
	crByteOff := cbByteOff + chromaSize*2

	// Process Y plane: per-pixel alpha blend.
	for py := 0; py < frameH; py++ {
		for px := 0; px < frameW; px++ {
			rgbaIdx := (py*frameW + px) * 4
			a := int32(rgba[rgbaIdx+3])
			if a == 0 {
				continue
			}

			r := int32(rgba[rgbaIdx+0])
			g := int32(rgba[rgbaIdx+1])
			b := int32(rgba[rgbaIdx+2])

			// BT.709 8-bit Y, then scale to 10-bit
			y8 := int32((66*r+129*g+25*b+128)>>8) + 16
			overlayY := y8 * 4

			yOff := (py*frameW + px) * 2
			existingY := int32(binary.LittleEndian.Uint16(yuv[yOff:]))

			// Alpha blend: result = (overlay * alpha + existing * (255 - alpha) + 128) >> 8
			inv := int32(255) - a
			result := (overlayY*a + existingY*inv + 128) >> 8
			if result < 0 {
				result = 0
			}
			if result > 1023 {
				result = 1023
			}
			binary.LittleEndian.PutUint16(yuv[yOff:], uint16(result))
		}
	}

	// Process Cb/Cr planes: half-width, full-height (4:2:2).
	// Use the left (even-column) RGBA pixel for each chroma sample.
	for py := 0; py < frameH; py++ {
		for cx := 0; cx < chromaW; cx++ {
			// Use the even-column pixel (left of the 2-pixel pair)
			px := cx * 2
			rgbaIdx := (py*frameW + px) * 4
			a := int32(rgba[rgbaIdx+3])
			if a == 0 {
				continue
			}

			r := int32(rgba[rgbaIdx+0])
			g := int32(rgba[rgbaIdx+1])
			b := int32(rgba[rgbaIdx+2])

			// BT.709 8-bit Cb/Cr, then scale to 10-bit
			cb8 := int32((-38*r-74*g+112*b+128)>>8) + 128
			cr8 := int32((112*r-94*g-18*b+128)>>8) + 128
			overlayCb := cb8 * 4
			overlayCr := cr8 * 4

			inv := int32(255) - a

			// Cb
			cbOff := cbByteOff + (py*chromaW+cx)*2
			existingCb := int32(binary.LittleEndian.Uint16(yuv[cbOff:]))
			resultCb := (overlayCb*a + existingCb*inv + 128) >> 8
			if resultCb < 0 {
				resultCb = 0
			}
			if resultCb > 1023 {
				resultCb = 1023
			}
			binary.LittleEndian.PutUint16(yuv[cbOff:], uint16(resultCb))

			// Cr
			crOff := crByteOff + (py*chromaW+cx)*2
			existingCr := int32(binary.LittleEndian.Uint16(yuv[crOff:]))
			resultCr := (overlayCr*a + existingCr*inv + 128) >> 8
			if resultCr < 0 {
				resultCr = 0
			}
			if resultCr > 1023 {
				resultCr = 1023
			}
			binary.LittleEndian.PutUint16(yuv[crOff:], uint16(resultCr))
		}
	}

	return nil
}

// AlphaBlendRGBARect10 composites an 8-bit RGBA overlay into a sub-region of a
// YUV422P10LE frame in-place. The overlay is nearest-neighbor scaled to fit the
// given rect. The rect is clipped to frame bounds and even-aligned for YUV422
// compatibility.
//
// Parameters:
//   - yuv: destination YUV422P10LE frame (Y[w*h*2] + Cb[w/2*h*2] + Cr[w/2*h*2])
//   - rgba: source RGBA overlay (overlayW * overlayH * 4 bytes)
//   - frameW, frameH: dimensions of the YUV frame
//   - overlayW, overlayH: dimensions of the RGBA overlay
//   - rect: target region within the YUV frame
//   - alphaScale: global alpha modulator (0.0-1.0)
func AlphaBlendRGBARect10(yuv []byte, rgba []byte, frameW, frameH, overlayW, overlayH int,
	rect image.Rectangle, alphaScale float64) error {

	if frameW%2 != 0 {
		return fmt.Errorf("YUV422 requires even width: got %d", frameW)
	}
	if frameW <= 0 || frameH <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", frameW, frameH)
	}
	needYUV := video.YUV422_10bitFrameSize(frameW, frameH)
	if len(yuv) < needYUV {
		return fmt.Errorf("YUV buffer too small: got %d, need %d", len(yuv), needYUV)
	}
	if len(rgba) < overlayW*overlayH*4 {
		return fmt.Errorf("RGBA buffer too small: got %d, need %d", len(rgba), overlayW*overlayH*4)
	}

	// Even-align the rect for YUV422 compatibility.
	rect.Min.X = rect.Min.X &^ 1
	rect.Min.Y = rect.Min.Y &^ 1
	rect.Max.X = rect.Max.X &^ 1
	rect.Max.Y = rect.Max.Y &^ 1

	// Clip to frame bounds.
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > frameW {
		rect.Max.X = frameW
	}
	if rect.Max.Y > frameH {
		rect.Max.Y = frameH
	}

	rectW := rect.Dx()
	rectH := rect.Dy()
	if rectW <= 0 || rectH <= 0 {
		return nil
	}

	alphaScale256 := int32(alphaScale*256 + 0.5)
	if alphaScale256 <= 0 {
		return nil
	}
	if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	ySize := frameW * frameH
	chromaW := frameW / 2
	chromaSize := chromaW * frameH
	cbByteOff := ySize * 2
	crByteOff := cbByteOff + chromaSize*2

	// Precompute 16.16 fixed-point source X coordinates for every destination
	// column. Using (dx*(overlayW-1)<<16)/(rectW-1) gives exact corner mapping
	// (dx=0→srcX=0, dx=rectW-1→srcX=overlayW-1<<16) and — at matched resolution
	// — fractional part zero for every column, reducing bilinear to identity.
	dstWm1 := int64(rectW - 1)
	dstHm1 := int64(rectH - 1)
	srcWm1 := int64(overlayW - 1)
	srcHm1 := int64(overlayH - 1)

	xCoords := make([]int64, rectW)
	if dstWm1 > 0 {
		for dx := 0; dx < rectW; dx++ {
			xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
		}
	}

	// Y plane: bilinear-sample RGBA overlay and alpha blend per pixel.
	for dy := 0; dy < rectH; dy++ {
		frameRow := rect.Min.Y + dy
		var srcY int64
		if dstHm1 > 0 {
			srcY = (int64(dy) * srcHm1 << 16) / dstHm1
		}

		for dx := 0; dx < rectW; dx++ {
			r8, g8, b8, a8 := sampleRGBABilinear(rgba, overlayW, overlayH, xCoords[dx], srcY)
			if a8 == 0 {
				continue
			}
			a := int32(a8)

			// Apply global alpha scale.
			a = (a*alphaScale256 + 128) >> 8
			if a <= 0 {
				continue
			}
			if a > 255 {
				a = 255
			}

			r := int32(r8)
			g := int32(g8)
			b := int32(b8)

			// BT.709 8-bit Y, then scale to 10-bit.
			y8 := int32((66*r+129*g+25*b+128)>>8) + 16
			overlayY := y8 * 4

			yOff := (frameRow*frameW + rect.Min.X + dx) * 2
			existingY := int32(binary.LittleEndian.Uint16(yuv[yOff:]))

			inv := int32(255) - a
			result := (overlayY*a + existingY*inv + 128) >> 8
			if result < 0 {
				result = 0
			}
			if result > 1023 {
				result = 1023
			}
			binary.LittleEndian.PutUint16(yuv[yOff:], uint16(result))
		}
	}

	// Chroma planes: half-width, full-height (4:2:2).
	// Each chroma sample is co-sited with the even (left) Y pixel of the
	// 2-pixel pair, so reuse xCoords[dxFull] for bilinear source lookup.
	halfRectW := rectW / 2
	chromaRectX := rect.Min.X / 2

	for dy := 0; dy < rectH; dy++ {
		frameRow := rect.Min.Y + dy
		var srcY int64
		if dstHm1 > 0 {
			srcY = (int64(dy) * srcHm1 << 16) / dstHm1
		}

		for dcx := 0; dcx < halfRectW; dcx++ {
			dxFull := dcx * 2
			r8, g8, b8, a8 := sampleRGBABilinear(rgba, overlayW, overlayH, xCoords[dxFull], srcY)
			if a8 == 0 {
				continue
			}
			a := int32(a8)

			a = (a*alphaScale256 + 128) >> 8
			if a <= 0 {
				continue
			}
			if a > 255 {
				a = 255
			}

			r := int32(r8)
			g := int32(g8)
			b := int32(b8)

			cb8 := int32((-38*r-74*g+112*b+128)>>8) + 128
			cr8 := int32((112*r-94*g-18*b+128)>>8) + 128
			overlayCb := cb8 * 4
			overlayCr := cr8 * 4

			inv := int32(255) - a

			// Cb
			cbOff := cbByteOff + (frameRow*chromaW+chromaRectX+dcx)*2
			existingCb := int32(binary.LittleEndian.Uint16(yuv[cbOff:]))
			resultCb := (overlayCb*a + existingCb*inv + 128) >> 8
			if resultCb < 0 {
				resultCb = 0
			}
			if resultCb > 1023 {
				resultCb = 1023
			}
			binary.LittleEndian.PutUint16(yuv[cbOff:], uint16(resultCb))

			// Cr
			crOff := crByteOff + (frameRow*chromaW+chromaRectX+dcx)*2
			existingCr := int32(binary.LittleEndian.Uint16(yuv[crOff:]))
			resultCr := (overlayCr*a + existingCr*inv + 128) >> 8
			if resultCr < 0 {
				resultCr = 0
			}
			if resultCr > 1023 {
				resultCr = 1023
			}
			binary.LittleEndian.PutUint16(yuv[crOff:], uint16(resultCr))
		}
	}

	return nil
}
