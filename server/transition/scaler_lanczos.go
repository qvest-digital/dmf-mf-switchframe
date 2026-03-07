package transition

import "math"

// ScaleQuality selects the scaling algorithm.
type ScaleQuality int

const (
	// ScaleQualityHigh uses Lanczos-3 interpolation for broadcast-quality
	// scaling. Produces sharper output than bilinear, especially on downscales,
	// at the cost of ~3-4x more computation.
	ScaleQualityHigh ScaleQuality = iota

	// ScaleQualityFast uses bilinear interpolation. Suitable for real-time
	// preview or when CPU budget is tight.
	ScaleQualityFast
)

// ScaleYUV420WithQuality scales a YUV420 planar frame using the selected algorithm.
// Same buffer layout as ScaleYUV420: src and dst must be w*h*3/2 bytes.
func ScaleYUV420WithQuality(src []byte, srcW, srcH int, dst []byte, dstW, dstH int, quality ScaleQuality) {
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return
	}
	switch quality {
	case ScaleQualityHigh:
		ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
	default:
		ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
	}
}

// ScaleYUV420Lanczos scales a YUV420 planar frame from (srcW x srcH) to
// (dstW x dstH) using Lanczos-3 interpolation. Each plane is scaled at its
// native resolution: full for Y, half for Cb/Cr.
//
// The scaler uses separable filtering (horizontal pass then vertical pass)
// for performance. The Lanczos-3 kernel has a support radius of 3 pixels
// and produces sharper output than bilinear, particularly on downscales.
func ScaleYUV420Lanczos(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	srcYSize := srcW * srcH
	srcUVW := srcW / 2
	srcUVH := srcH / 2
	srcUVSize := srcUVW * srcUVH

	dstYSize := dstW * dstH
	dstUVW := dstW / 2
	dstUVH := dstH / 2
	dstUVSize := dstUVW * dstUVH

	// Y plane: full resolution
	scalePlaneLanczos(
		src[:srcYSize], srcW, srcH,
		dst[:dstYSize], dstW, dstH,
	)

	// Cb plane: half resolution
	srcCbOff := srcYSize
	dstCbOff := dstYSize
	scalePlaneLanczos(
		src[srcCbOff:srcCbOff+srcUVSize], srcUVW, srcUVH,
		dst[dstCbOff:dstCbOff+dstUVSize], dstUVW, dstUVH,
	)

	// Cr plane: half resolution
	srcCrOff := srcYSize + srcUVSize
	dstCrOff := dstYSize + dstUVSize
	scalePlaneLanczos(
		src[srcCrOff:srcCrOff+srcUVSize], srcUVW, srcUVH,
		dst[dstCrOff:dstCrOff+dstUVSize], dstUVW, dstUVH,
	)
}

// lanczos3 computes the Lanczos-3 kernel value:
//
//	L(x) = sinc(x) * sinc(x/3)   for |x| < 3
//	L(x) = 0                      for |x| >= 3
//
// where sinc(x) = sin(pi*x) / (pi*x), sinc(0) = 1.
func lanczos3(x float64) float64 {
	if x == 0 {
		return 1.0
	}
	ax := math.Abs(x)
	if ax >= 3.0 {
		return 0.0
	}
	pix := math.Pi * x
	return (math.Sin(pix) / pix) * (math.Sin(pix/3.0) / (pix / 3.0))
}

// scalePlaneLanczos performs Lanczos-3 interpolation on a single plane using
// separable filtering: horizontal pass into a temp buffer, then vertical pass
// to produce the final output.
//
// Fast paths:
//   - Same dimensions: plain copy
//   - 1x1 source: fill destination with single value
func scalePlaneLanczos(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	// Fast path: same dimensions
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return
	}

	// Fast path: 1x1 source
	if srcW == 1 && srcH == 1 {
		val := src[0]
		for i := range dst {
			dst[i] = val
		}
		return
	}

	// Separable filtering: horizontal then vertical.
	// Temp buffer has dstW columns and srcH rows.
	tmp := make([]float64, dstW*srcH)

	// --- Horizontal pass: resample each row from srcW to dstW ---
	xRatio := float64(srcW) / float64(dstW)

	// When downscaling, widen the filter window proportionally to
	// avoid aliasing. The effective radius becomes a * xRatio (clamped
	// to at least 3) and the kernel is evaluated at x/xRatio.
	hScale := 1.0
	if xRatio > 1.0 {
		hScale = xRatio
	}
	hRadius := 3.0 * hScale

	for y := 0; y < srcH; y++ {
		srcRow := y * srcW
		tmpRow := y * dstW
		for dx := 0; dx < dstW; dx++ {
			// Map destination center to source coordinate
			sx := (float64(dx)+0.5)*xRatio - 0.5

			// Determine tap range
			minX := int(math.Floor(sx - hRadius))
			maxX := int(math.Ceil(sx + hRadius))

			var sum, wsum float64
			for ix := minX; ix <= maxX; ix++ {
				// Clamp to valid source range
				cix := ix
				if cix < 0 {
					cix = 0
				} else if cix >= srcW {
					cix = srcW - 1
				}

				w := lanczos3((float64(ix) - sx) / hScale)
				sum += w * float64(src[srcRow+cix])
				wsum += w
			}

			if wsum != 0 {
				tmp[tmpRow+dx] = sum / wsum
			}
		}
	}

	// --- Vertical pass: resample each column from srcH to dstH ---
	yRatio := float64(srcH) / float64(dstH)

	vScale := 1.0
	if yRatio > 1.0 {
		vScale = yRatio
	}
	vRadius := 3.0 * vScale

	for dx := 0; dx < dstW; dx++ {
		for dy := 0; dy < dstH; dy++ {
			sy := (float64(dy)+0.5)*yRatio - 0.5

			minY := int(math.Floor(sy - vRadius))
			maxY := int(math.Ceil(sy + vRadius))

			var sum, wsum float64
			for iy := minY; iy <= maxY; iy++ {
				ciy := iy
				if ciy < 0 {
					ciy = 0
				} else if ciy >= srcH {
					ciy = srcH - 1
				}

				w := lanczos3((float64(iy) - sy) / vScale)
				sum += w * tmp[ciy*dstW+dx]
				wsum += w
			}

			var val float64
			if wsum != 0 {
				val = sum / wsum
			}

			// Clamp to [0, 255]
			if val < 0 {
				val = 0
			} else if val > 255 {
				val = 255
			}

			dst[dy*dstW+dx] = byte(val + 0.5) // round to nearest
		}
	}
}
