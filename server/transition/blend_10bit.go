package transition

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/zsiec/switchframe/server/internal/video"
)

// 10-bit YUV422P10LE color constants
const (
	blackY10      = 64   // limited-range black Y
	blackYFull10  = 0    // full-range black Y
	neutralC10    = 512  // neutral chroma (both limited and full range)
	maxValue10bit = 1023 // maximum 10-bit sample value
)

// errOddWidth10 is returned when 10-bit YUV422 operations receive odd width.
// YUV422 chroma subsampling requires even width.
var errOddWidth10 = errors.New("YUV422 requires even width")

// validate10 checks dimensions and buffer sizes for 10-bit blend operations.
func validate10(dst, a, b []byte, w, h int) error {
	if w%2 != 0 {
		return fmt.Errorf("%w: got width %d", errOddWidth10, w)
	}
	if w <= 0 || h <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", w, h)
	}
	need := video.YUV422_10bitFrameSize(w, h)
	if len(dst) < need {
		return fmt.Errorf("dst buffer too small: got %d, need %d", len(dst), need)
	}
	if len(a) < need {
		return fmt.Errorf("source A buffer too small: got %d, need %d", len(a), need)
	}
	if len(b) < need {
		return fmt.Errorf("source B buffer too small: got %d, need %d", len(b), need)
	}
	return nil
}

// validate10Single checks dimensions and buffer sizes for single-source 10-bit operations.
func validate10Single(dst, src []byte, w, h int) error {
	if w%2 != 0 {
		return fmt.Errorf("%w: got width %d", errOddWidth10, w)
	}
	if w <= 0 || h <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", w, h)
	}
	need := video.YUV422_10bitFrameSize(w, h)
	if len(dst) < need {
		return fmt.Errorf("dst buffer too small: got %d, need %d", len(dst), need)
	}
	if len(src) < need {
		return fmt.Errorf("src buffer too small: got %d, need %d", len(src), need)
	}
	return nil
}

// clamp10 clamps a value to the 10-bit range [0, 1023].
func clamp10(v int32) uint16 {
	if v < 0 {
		return 0
	}
	if v > maxValue10bit {
		return maxValue10bit
	}
	return uint16(v)
}

// BlendMix10 performs linear interpolation between two YUV422P10LE frames.
// pos256 ranges from 0 (100% A) to 256 (100% B).
// SIMD-accelerated on amd64 (AVX2) and arm64 (NEON), ~15x faster than pure Go.
func BlendMix10(dst, a, b []byte, width, height, pos256 int) error {
	return blendMix10Dispatch(dst, a, b, width, height, pos256)
}

// BlendDip10 performs a two-phase dip-to-black transition between two YUV422P10LE frames.
// pos256 ranges from 0 (100% A) to 256 (100% B), with midpoint (128) being full black.
// Phase 1 (0-128): A fades to black. Phase 2 (128-256): black fades to B.
// limitedRange controls the black level: true uses Y=64, false uses Y=0.
func BlendDip10(dst, a, b []byte, width, height, pos256 int, limitedRange bool) error {
	if err := validate10(dst, a, b, width, height); err != nil {
		return err
	}
	if pos256 < 0 {
		pos256 = 0
	}
	if pos256 > 256 {
		pos256 = 256
	}

	blackY := int32(blackYFull10)
	if limitedRange {
		blackY = int32(blackY10)
	}
	neutralChroma := int32(neutralC10)

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	if pos256 <= 128 {
		// Phase 1: fade A to black. gain goes from 256 at pos=0 to 0 at pos=128
		gain := int32(256 - pos256*2)
		invGain := int32(256) - gain

		// Y plane
		for i := 0; i < ySize; i++ {
			off := i * 2
			sa := int32(binary.LittleEndian.Uint16(a[off:]))
			result := (sa*gain + blackY*invGain + 128) >> 8
			binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
		}
		// Cb plane
		for i := 0; i < chromaSize; i++ {
			off := cbOff + i*2
			sa := int32(binary.LittleEndian.Uint16(a[off:]))
			result := (sa*gain + neutralChroma*invGain + 128) >> 8
			binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
		}
		// Cr plane
		for i := 0; i < chromaSize; i++ {
			off := crOff + i*2
			sa := int32(binary.LittleEndian.Uint16(a[off:]))
			result := (sa*gain + neutralChroma*invGain + 128) >> 8
			binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
		}
	} else {
		// Phase 2: fade black to B. gain goes from 0 at pos=128 to 256 at pos=256
		gain := int32((pos256 - 128) * 2)
		invGain := int32(256) - gain

		// Y plane
		for i := 0; i < ySize; i++ {
			off := i * 2
			sb := int32(binary.LittleEndian.Uint16(b[off:]))
			result := (sb*gain + blackY*invGain + 128) >> 8
			binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
		}
		// Cb plane
		for i := 0; i < chromaSize; i++ {
			off := cbOff + i*2
			sb := int32(binary.LittleEndian.Uint16(b[off:]))
			result := (sb*gain + neutralChroma*invGain + 128) >> 8
			binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
		}
		// Cr plane
		for i := 0; i < chromaSize; i++ {
			off := crOff + i*2
			sb := int32(binary.LittleEndian.Uint16(b[off:]))
			result := (sb*gain + neutralChroma*invGain + 128) >> 8
			binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
		}
	}

	return nil
}

// BlendWipe10 performs a per-pixel alpha wipe between two YUV422P10LE frames.
// alpha is an 8-bit per-luma-pixel alpha mask (0 = A, 255 = B), same dimensions as Y plane.
// For chroma planes, alpha is downsampled 2:1 horizontally only (4:2:2 subsampling).
func BlendWipe10(dst, a, b []byte, width, height int, alpha []byte) error {
	if err := validate10(dst, a, b, width, height); err != nil {
		return err
	}
	if len(alpha) < width*height {
		return fmt.Errorf("alpha buffer too small: got %d, need %d", len(alpha), width*height)
	}

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	// Y plane: per-pixel alpha blend
	for i := 0; i < ySize; i++ {
		off := i * 2
		al := int32(alpha[i])
		inv := int32(256) - al
		sa := int32(binary.LittleEndian.Uint16(a[off:]))
		sb := int32(binary.LittleEndian.Uint16(b[off:]))
		result := (sa*inv + sb*al + 128) >> 8
		binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
	}

	// Chroma planes: downsample alpha 2:1 horizontally (4:2:2: half width, full height)
	for y := 0; y < height; y++ {
		for x := 0; x < chromaW; x++ {
			// Average 2 horizontal luma alpha samples for this chroma sample
			a0 := int32(alpha[y*width+x*2])
			a1 := int32(alpha[y*width+x*2+1])
			chromaAlpha := (a0 + a1 + 1) >> 1
			inv := int32(256) - chromaAlpha

			ci := y*chromaW + x

			// Cb
			offCb := cbOff + ci*2
			saCb := int32(binary.LittleEndian.Uint16(a[offCb:]))
			sbCb := int32(binary.LittleEndian.Uint16(b[offCb:]))
			resultCb := (saCb*inv + sbCb*chromaAlpha + 128) >> 8
			binary.LittleEndian.PutUint16(dst[offCb:], clamp10(resultCb))

			// Cr
			offCr := crOff + ci*2
			saCr := int32(binary.LittleEndian.Uint16(a[offCr:]))
			sbCr := int32(binary.LittleEndian.Uint16(b[offCr:]))
			resultCr := (saCr*inv + sbCr*chromaAlpha + 128) >> 8
			binary.LittleEndian.PutUint16(dst[offCr:], clamp10(resultCr))
		}
	}

	return nil
}

// BlendFTB10 fades a single YUV422P10LE source to black.
// pos256 ranges from 0 (full source) to 256 (fully black).
// limitedRange controls the black level: true uses Y=64, false uses Y=0.
// Chroma fades to neutral (512) in both ranges.
func BlendFTB10(dst, src []byte, width, height, pos256 int, limitedRange bool) error {
	if err := validate10Single(dst, src, width, height); err != nil {
		return err
	}
	if pos256 < 0 {
		pos256 = 0
	}
	if pos256 > 256 {
		pos256 = 256
	}

	blackY := int32(blackYFull10)
	if limitedRange {
		blackY = int32(blackY10)
	}
	neutralChroma := int32(neutralC10)

	inv := int32(256 - pos256)
	pos := int32(pos256)

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	// Y plane: fade toward black
	for i := 0; i < ySize; i++ {
		off := i * 2
		s := int32(binary.LittleEndian.Uint16(src[off:]))
		result := (s*inv + blackY*pos + 128) >> 8
		binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
	}

	// Cb plane: fade toward neutral chroma
	for i := 0; i < chromaSize; i++ {
		off := cbOff + i*2
		s := int32(binary.LittleEndian.Uint16(src[off:]))
		result := (s*inv + neutralChroma*pos + 128) >> 8
		binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
	}

	// Cr plane: fade toward neutral chroma
	for i := 0; i < chromaSize; i++ {
		off := crOff + i*2
		s := int32(binary.LittleEndian.Uint16(src[off:]))
		result := (s*inv + neutralChroma*pos + 128) >> 8
		binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
	}

	return nil
}

// BlendStinger10 composites a stinger overlay onto a base YUV422P10LE frame using per-pixel alpha.
// alpha is an 8-bit per-luma-pixel alpha mask (0 = base, 255 = overlay), same dimensions as Y plane.
// For chroma planes, alpha is downsampled 2:1 horizontally only (4:2:2 subsampling).
func BlendStinger10(dst, base, overlay []byte, width, height int, alpha []byte) error {
	if err := validate10(dst, base, overlay, width, height); err != nil {
		return err
	}
	if len(alpha) < width*height {
		return fmt.Errorf("alpha buffer too small: got %d, need %d", len(alpha), width*height)
	}

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	// Y plane: per-pixel alpha composite
	for i := 0; i < ySize; i++ {
		off := i * 2
		al := int32(alpha[i])
		inv := int32(256) - al
		sb := int32(binary.LittleEndian.Uint16(base[off:]))
		so := int32(binary.LittleEndian.Uint16(overlay[off:]))
		result := (sb*inv + so*al + 128) >> 8
		binary.LittleEndian.PutUint16(dst[off:], clamp10(result))
	}

	// Chroma planes: downsample alpha 2:1 horizontally (4:2:2)
	for y := 0; y < height; y++ {
		for x := 0; x < chromaW; x++ {
			a0 := int32(alpha[y*width+x*2])
			a1 := int32(alpha[y*width+x*2+1])
			chromaAlpha := (a0 + a1 + 1) >> 1
			inv := int32(256) - chromaAlpha

			ci := y*chromaW + x

			// Cb
			offCb := cbOff + ci*2
			sbCb := int32(binary.LittleEndian.Uint16(base[offCb:]))
			soCb := int32(binary.LittleEndian.Uint16(overlay[offCb:]))
			resultCb := (sbCb*inv + soCb*chromaAlpha + 128) >> 8
			binary.LittleEndian.PutUint16(dst[offCb:], clamp10(resultCb))

			// Cr
			offCr := crOff + ci*2
			sbCr := int32(binary.LittleEndian.Uint16(base[offCr:]))
			soCr := int32(binary.LittleEndian.Uint16(overlay[offCr:]))
			resultCr := (sbCr*inv + soCr*chromaAlpha + 128) >> 8
			binary.LittleEndian.PutUint16(dst[offCr:], clamp10(resultCr))
		}
	}

	return nil
}
