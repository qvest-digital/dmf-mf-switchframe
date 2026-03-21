package stmap

import "sync"

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
// both be w*h*3/2 bytes. The three planes (Y, Cb, Cr) are warped in
// parallel — one goroutine per plane. Wall-clock time is bounded by the
// Y plane (which has 4x the pixels of each chroma plane).
//
// Previous approach split Y into 4 sub-bands, but pprof showed 7ms/frame
// of goroutine sync overhead (pthread_cond_signal/wait) — nearly as much
// as the chroma planes themselves. 3 goroutines (one per plane) with a
// single WaitGroup eliminates the sub-band overhead and parallelizes chroma.
func (p *Processor) ProcessYUV(dst, src []byte, w, h int) {
	if p.stmap == nil {
		return
	}

	ySize := w * h
	cw := w / 2
	ch := h / 2
	cSize := cw * ch
	cbOff := ySize
	crOff := ySize + cSize

	// Run all 3 planes in parallel: Y on main goroutine, Cb+Cr on workers.
	// This is cheaper than WaitGroup for 3 goroutines: we run Y inline and
	// only spawn 2 goroutines for chroma, halving sync overhead.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		warpPlane(dst[cbOff:cbOff+cSize], src[cbOff:cbOff+cSize], cw, ch, p.lutCSX, p.lutCSY)
	}()

	go func() {
		defer wg.Done()
		warpPlane(dst[crOff:crOff+cSize], src[crOff:crOff+cSize], cw, ch, p.lutCSX, p.lutCSY)
	}()

	// Y plane runs on the calling goroutine (no spawn overhead for the biggest plane).
	warpPlane(dst[:ySize], src[:ySize], w, h, p.lutSX, p.lutSY)

	wg.Wait()
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
	//
	// NOTE: This uses a simple 4-corner average. For H.264's default
	// MPEG-2 chroma siting (left-center), the geometrically correct
	// approach would weight the left column more heavily. The simple
	// average introduces a theoretical 0.25-pixel horizontal chroma
	// shift, which is visually negligible for moderate warps at 1080p
	// but could produce subtle chroma fringing for extreme distortions.
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

// warpPlaneBand applies bilinear interpolation for a band of output pixels.
// dst receives len(lutX) output pixels. src is the FULL source plane (w*h bytes).
// Uses the platform-specific warpBilinearRow kernel (assembly on amd64/arm64,
// Go fallback on other platforms).
func warpPlaneBand(dst, src []byte, w, h int, lutX, lutY []int64) {
	n := len(lutX)
	if n == 0 {
		return
	}
	warpBilinearRow(&dst[0], &src[0], w, h, n, &lutX[0], &lutY[0])
}

// warpPlane applies bilinear interpolation for one complete plane.
func warpPlane(dst, src []byte, w, h int, lutX, lutY []int64) {
	n := w * h
	if n == 0 {
		return
	}
	warpBilinearRow(&dst[0], &src[0], w, h, n, &lutX[0], &lutY[0])
}
