package transition

import (
	"math"
	"sync"
	"sync/atomic"
)

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
// with precomputed kernel weights for performance. The Lanczos-3 kernel has
// a support radius of 3 pixels and produces sharper output than bilinear.
func ScaleYUV420Lanczos(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	srcYSize := srcW * srcH
	srcUVW := srcW / 2
	srcUVH := srcH / 2
	srcUVSize := srcUVW * srcUVH

	dstYSize := dstW * dstH
	dstUVW := dstW / 2
	dstUVH := dstH / 2
	dstUVSize := dstUVW * dstUVH

	srcCbOff := srcYSize
	dstCbOff := dstYSize
	srcCrOff := srcYSize + srcUVSize
	dstCrOff := dstYSize + dstUVSize

	// Scale all three planes concurrently. Y is 4x the work of each
	// chroma plane so it dominates; Cb and Cr finish in parallel.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scalePlaneLanczos(
			src[srcCbOff:srcCbOff+srcUVSize], srcUVW, srcUVH,
			dst[dstCbOff:dstCbOff+dstUVSize], dstUVW, dstUVH,
		)
	}()
	go func() {
		defer wg.Done()
		scalePlaneLanczos(
			src[srcCrOff:srcCrOff+srcUVSize], srcUVW, srcUVH,
			dst[dstCrOff:dstCrOff+dstUVSize], dstUVW, dstUVH,
		)
	}()

	// Y plane on calling goroutine (largest plane, dominates runtime)
	scalePlaneLanczos(
		src[:srcYSize], srcW, srcH,
		dst[:dstYSize], dstW, dstH,
	)

	wg.Wait()
}

// lanczosKernel holds precomputed weights for one dimension of Lanczos-3 scaling.
// For a given (srcSize, dstSize) pair, weights are constant across all frames.
type lanczosKernel struct {
	size    int       // number of destination positions
	maxTaps int       // max taps per position (typically 7 for upscale, ~13 for 2x down)
	offsets []int32   // [size] first source index per destination pixel
	weights []float32 // [size * maxTaps] packed weight array (zero-padded)
}

// kernelCacheEntry wraps a kernel with its key.
type kernelCacheEntry struct {
	srcSize, dstSize int
	kernel           *lanczosKernel
}

// kernelCache holds recently-used kernels. Sized for typical YUV420 usage:
// Y horizontal, Y vertical, chroma horizontal, chroma vertical = 4 entries.
const kernelCacheSize = 8

var kernelCache [kernelCacheSize]atomic.Pointer[kernelCacheEntry]

// getLanczosKernel returns a precomputed kernel, using a small cache for repeated calls.
func getLanczosKernel(srcSize, dstSize int) *lanczosKernel {
	// Search cache
	for i := range kernelCache {
		if c := kernelCache[i].Load(); c != nil && c.srcSize == srcSize && c.dstSize == dstSize {
			return c.kernel
		}
	}
	// Compute and store in first empty or overwrite slot 0
	k := precomputeLanczosKernel(srcSize, dstSize)
	entry := &kernelCacheEntry{srcSize: srcSize, dstSize: dstSize, kernel: k}
	for i := range kernelCache {
		if kernelCache[i].Load() == nil {
			kernelCache[i].Store(entry)
			return k
		}
	}
	// Cache full — overwrite slot based on hash to spread evictions
	slot := (srcSize*31 + dstSize) % kernelCacheSize
	kernelCache[slot].Store(entry)
	return k
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

// precomputeLanczosKernel builds the weight table for one dimension.
// For each destination position, it evaluates the Lanczos-3 kernel at the
// original (unclamped) tap positions, then folds weights of out-of-bounds
// taps onto the nearest edge pixel. This exactly matches the reference
// implementation's edge-clamping behavior.
func precomputeLanczosKernel(srcSize, dstSize int) *lanczosKernel {
	ratio := float64(srcSize) / float64(dstSize)

	// When downscaling, widen the filter to avoid aliasing
	scale := 1.0
	if ratio > 1.0 {
		scale = ratio
	}
	radius := 3.0 * scale

	// Compute max number of taps. We store weights indexed by clamped
	// source position relative to the start offset. The window spans
	// [0, srcSize-1] at most, but typically only a few pixels.
	rawTaps := int(math.Ceil(radius))*2 + 2 // generous upper bound
	if rawTaps > srcSize {
		rawTaps = srcSize
	}

	offsets := make([]int32, dstSize)
	weights := make([]float32, dstSize*rawTaps)

	for d := 0; d < dstSize; d++ {
		// Map destination center to source coordinate
		center := (float64(d)+0.5)*ratio - 0.5

		// Determine unclamped tap range (matching reference)
		minX := int(math.Floor(center - radius))
		maxX := int(math.Ceil(center + radius))

		// The clamped window starts at max(0, minX)
		startX := minX
		if startX < 0 {
			startX = 0
		}
		offsets[d] = int32(startX)

		// Evaluate kernel: for each original tap position ix in [minX, maxX],
		// compute its weight and add it to the clamped position.
		wBase := d * rawTaps
		var wsum float64
		for ix := minX; ix <= maxX; ix++ {
			w := lanczos3((float64(ix) - center) / scale)
			wsum += w

			// Clamp to [0, srcSize-1], matching reference behavior
			cix := ix
			if cix < 0 {
				cix = 0
			} else if cix >= srcSize {
				cix = srcSize - 1
			}

			// Map to local tap index relative to startX
			t := cix - startX
			if t >= 0 && t < rawTaps {
				weights[wBase+t] += float32(w)
			}
		}

		// Normalize so weights sum to 1.0
		if wsum != 0 {
			invW := float32(1.0 / wsum)
			for t := 0; t < rawTaps; t++ {
				weights[wBase+t] *= invW
			}
		}
	}

	// Trim trailing zero-weight taps to reduce the inner loop iteration count.
	// We only trim trailing zeros (not leading) because offset adjustment
	// would cause the vertical pass to read beyond the temp buffer when
	// startRow + maxTaps exceeds srcH.
	trimmedMaxTaps := 0
	for d := 0; d < dstSize; d++ {
		wBase := d * rawTaps
		lastNZ := rawTaps - 1
		for lastNZ >= 0 && weights[wBase+lastNZ] == 0 {
			lastNZ--
		}
		needed := lastNZ + 1
		if needed > trimmedMaxTaps {
			trimmedMaxTaps = needed
		}
	}

	// Pad maxTaps up to a multiple of 4 so the NEON 4-tap inner loop
	// processes all taps without a scalar tail. Extra positions are
	// zero-weight (no effect on output).
	paddedMaxTaps := (trimmedMaxTaps + 3) &^ 3
	if paddedMaxTaps > rawTaps {
		paddedMaxTaps = rawTaps // don't exceed original allocation
	}
	if paddedMaxTaps == 0 {
		paddedMaxTaps = rawTaps
	}

	// Repack weights with the padded stride.
	if paddedMaxTaps != rawTaps {
		compact := make([]float32, dstSize*paddedMaxTaps)
		for d := 0; d < dstSize; d++ {
			copy(compact[d*paddedMaxTaps:d*paddedMaxTaps+trimmedMaxTaps],
				weights[d*rawTaps:d*rawTaps+trimmedMaxTaps])
		}
		weights = compact
		rawTaps = paddedMaxTaps
	}

	return &lanczosKernel{
		size:    dstSize,
		maxTaps: rawTaps,
		offsets: offsets,
		weights: weights,
	}
}

// lanczosIntermPool recycles float32 intermediate buffers.
var lanczosIntermPool = sync.Pool{
	New: func() any {
		// Pre-allocate for common 1080p horizontal pass: 1920 * 1080
		buf := make([]float32, 1920*1080)
		return &buf
	},
}

func getLanczosTempBuf(n int) []float32 {
	bp := lanczosIntermPool.Get().(*[]float32)
	buf := *bp
	if cap(buf) < n {
		buf = make([]float32, n)
		*bp = buf
		return buf
	}
	return buf[:n]
}

func putLanczosTempBuf(buf []float32) {
	buf = buf[:cap(buf)]
	lanczosIntermPool.Put(&buf)
}

// boxShrinkPlane performs fast integer box-averaging to shrink a plane by
// an integer factor. Each output pixel is the average of a factorW×factorH
// block. Used as a pre-pass before Lanczos for large downscales (>2x) to
// keep the Lanczos kernel narrow (libvips "shrink then reduce" strategy).
func boxShrinkPlane(src []byte, srcW, srcH int, dst []byte, dstW, dstH, factorW, factorH int) {
	for dy := 0; dy < dstH; dy++ {
		sy := dy * factorH
		for dx := 0; dx < dstW; dx++ {
			sx := dx * factorW
			var sum int
			count := 0
			for fy := 0; fy < factorH && sy+fy < srcH; fy++ {
				row := (sy + fy) * srcW
				for fx := 0; fx < factorW && sx+fx < srcW; fx++ {
					sum += int(src[row+sx+fx])
					count++
				}
			}
			if count > 0 {
				dst[dy*dstW+dx] = byte(sum / count)
			}
		}
	}
}

// boxShrinkPool recycles byte buffers for box pre-shrink intermediate results.
var boxShrinkPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 1920*1080) // common 1080p
		return &buf
	},
}

// scalePlaneLanczos performs Lanczos-3 interpolation on a single plane using
// separable filtering with precomputed kernel weights: horizontal pass into
// a float32 intermediate buffer, then vertical pass to produce final output.
//
// For downscales >2x, uses box pre-shrink (libvips strategy) to get within
// 2x of the target, then Lanczos for the final precise resize. This keeps
// the Lanczos kernel narrow (7 taps vs ~20 for 3x downscale).
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

	// Box pre-shrink for very large downscales (>4x in either dimension).
	// Uses the libvips "shrink then reduce" strategy: fast integer box
	// averaging gets within 2x of target, then Lanczos for precise resize.
	// Only triggered above 4x because our NEON assembly handles moderate
	// kernel widths efficiently; the naive Go box shrink overhead exceeds
	// the savings from narrower kernels at 2-4x ratios.
	if srcW > dstW*4 || srcH > dstH*4 {
		// Choose factors so that midW/dstW ≤ 2 and midH/dstH ≤ 2.
		factorW := 1
		if srcW > dstW*4 {
			factorW = (srcW + 2*dstW - 1) / (2 * dstW)
			if factorW < 2 {
				factorW = 2
			}
		}
		factorH := 1
		if srcH > dstH*4 {
			factorH = (srcH + 2*dstH - 1) / (2 * dstH)
			if factorH < 2 {
				factorH = 2
			}
		}

		if factorW > 1 || factorH > 1 {
			midW := srcW / factorW
			midH := srcH / factorH
			midSize := midW * midH

			bp := boxShrinkPool.Get().(*[]byte)
			mid := *bp
			if cap(mid) < midSize {
				mid = make([]byte, midSize)
				*bp = mid
			} else {
				mid = mid[:midSize]
			}

			boxShrinkPlane(src, srcW, srcH, mid, midW, midH, factorW, factorH)

			// Recurse with the smaller intermediate
			scalePlaneLanczos(mid, midW, midH, dst, dstW, dstH)

			mid = mid[:cap(mid)]
			boxShrinkPool.Put(bp)
			return
		}
	}

	// Precompute kernels (cached for repeated same-ratio calls)
	hKernel := getLanczosKernel(srcW, dstW)
	vKernel := getLanczosKernel(srcH, dstH)

	// Get float32 intermediate buffer from pool (dstW × srcH)
	temp := getLanczosTempBuf(dstW * srcH)
	defer putLanczosTempBuf(temp)
	// No zero-fill needed: the horizontal pass writes every position in
	// temp[y*dstW .. (y+1)*dstW) for all y in [0, srcH), so no stale
	// values remain from previous uses of the pooled buffer.

	// Horizontal pass: resample each source row from srcW to dstW.
	// Each row is independent, so parallelize when there are enough rows
	// to amortize goroutine overhead.
	const horizChunkSize = 64
	if srcH > 128 {
		var hwg sync.WaitGroup
		for start := 0; start < srcH; start += horizChunkSize {
			end := start + horizChunkSize
			if end > srcH {
				end = srcH
			}
			if start == 0 {
				// First chunk runs on calling goroutine to avoid overhead
				for y := start; y < end; y++ {
					lanczosHorizRow(
						temp[y*dstW:(y+1)*dstW],
						src[y*srcW:(y+1)*srcW],
						hKernel.offsets,
						hKernel.weights,
						hKernel.maxTaps,
					)
				}
				continue
			}
			hwg.Add(1)
			go func(s, e int) {
				defer hwg.Done()
				for y := s; y < e; y++ {
					lanczosHorizRow(
						temp[y*dstW:(y+1)*dstW],
						src[y*srcW:(y+1)*srcW],
						hKernel.offsets,
						hKernel.weights,
						hKernel.maxTaps,
					)
				}
			}(start, end)
		}
		hwg.Wait()
	} else {
		for y := 0; y < srcH; y++ {
			lanczosHorizRow(
				temp[y*dstW:(y+1)*dstW],
				src[y*srcW:(y+1)*srcW],
				hKernel.offsets,
				hKernel.weights,
				hKernel.maxTaps,
			)
		}
	}

	// Vertical pass: resample each column from srcH to dstH
	for dy := 0; dy < dstH; dy++ {
		lanczosVertRow(
			dst[dy*dstW:(dy+1)*dstW],
			temp,
			dstW,
			vKernel.offsets[dy],
			vKernel.weights[dy*vKernel.maxTaps:(dy+1)*vKernel.maxTaps],
			vKernel.maxTaps,
		)
	}
}
