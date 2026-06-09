package graphics

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/zsiec/switchframe/server/internal/video"
)

// alphaBlendRGBA10Dispatch composites an 8-bit RGBA overlay onto a YUV422P10LE frame
// using SIMD-accelerated row kernels. Bit-identical to AlphaBlendRGBA10.
func alphaBlendRGBA10Dispatch(yuv []byte, rgba []byte, frameW, frameH int) error {
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

	// Y plane: per-pixel alpha blend using SIMD row kernel
	for py := 0; py < frameH; py++ {
		yOff := py * frameW * 2
		rgbaOff := py * frameW * 4
		alphaBlendRGBA10YRow(&yuv[yOff], &rgba[rgbaOff], frameW)
	}

	// Cb/Cr planes: half-width, full-height
	for py := 0; py < frameH; py++ {
		cbOff := cbByteOff + py*chromaW*2
		crOff := crByteOff + py*chromaW*2
		rgbaOff := py * frameW * 4
		alphaBlendRGBA10UVRow(&yuv[cbOff], &yuv[crOff], &rgba[rgbaOff], chromaW)
	}

	return nil
}

// alphaBlendRGBA10YRowGo is the pure Go reference for Y plane alpha blending.
// For each pixel (BT.709 coefficients, matching the 8-bit blend path):
//
//	A' = A + (A >> 7)  (map 0-255 to 0-256, opaque = exact replacement)
//	overlayY = ((47*R + 157*G + 16*B + 128) >> 8 + 16) * 4
//	result = (overlayY * A' + existingY * (256-A') + 128) >> 8
//	clamp to [0, 1023]
func alphaBlendRGBA10YRowGo(yPlane *byte, rgba *byte, count int) {
	if count <= 0 {
		return
	}

	yS := unsafe.Slice(yPlane, count*2)
	rS := unsafe.Slice(rgba, count*4)

	for i := 0; i < count; i++ {
		a := int32(rS[i*4+3])
		if a == 0 {
			continue
		}
		a += a >> 7 // map 0-255 to 0-256

		r := int32(rS[i*4+0])
		g := int32(rS[i*4+1])
		b := int32(rS[i*4+2])

		y8 := int32((47*r+157*g+16*b+128)>>8) + 16
		overlayY := y8 * 4

		existingY := int32(binary.LittleEndian.Uint16(yS[i*2:]))

		inv := int32(256) - a
		result := (overlayY*a + existingY*inv + 128) >> 8
		if result < 0 {
			result = 0
		}
		if result > 1023 {
			result = 1023
		}
		binary.LittleEndian.PutUint16(yS[i*2:], uint16(result))
	}
}

// alphaBlendRGBA10UVRowGo is the pure Go reference for Cb/Cr plane alpha blending.
// For each chroma sample (covers 2 horizontal luma pixels), uses the left pixel's RGBA.
func alphaBlendRGBA10UVRowGo(cbPlane, crPlane *byte, rgba *byte, chromaCount int) {
	if chromaCount <= 0 {
		return
	}

	cbS := unsafe.Slice(cbPlane, chromaCount*2)
	crS := unsafe.Slice(crPlane, chromaCount*2)
	rS := unsafe.Slice(rgba, chromaCount*2*4)

	for cx := 0; cx < chromaCount; cx++ {
		px := cx * 2
		rgbaIdx := px * 4
		a := int32(rS[rgbaIdx+3])
		if a == 0 {
			continue
		}
		a += a >> 7 // map 0-255 to 0-256

		r := int32(rS[rgbaIdx+0])
		g := int32(rS[rgbaIdx+1])
		b := int32(rS[rgbaIdx+2])

		cb8 := int32((-26*r-86*g+112*b+128)>>8) + 128
		cr8 := int32((112*r-102*g-10*b+128)>>8) + 128
		overlayCb := cb8 * 4
		overlayCr := cr8 * 4

		inv := int32(256) - a

		existingCb := int32(binary.LittleEndian.Uint16(cbS[cx*2:]))
		resultCb := (overlayCb*a + existingCb*inv + 128) >> 8
		if resultCb < 0 {
			resultCb = 0
		}
		if resultCb > 1023 {
			resultCb = 1023
		}
		binary.LittleEndian.PutUint16(cbS[cx*2:], uint16(resultCb))

		existingCr := int32(binary.LittleEndian.Uint16(crS[cx*2:]))
		resultCr := (overlayCr*a + existingCr*inv + 128) >> 8
		if resultCr < 0 {
			resultCr = 0
		}
		if resultCr > 1023 {
			resultCr = 1023
		}
		binary.LittleEndian.PutUint16(crS[cx*2:], uint16(resultCr))
	}
}
