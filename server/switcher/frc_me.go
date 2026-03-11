package switcher

import (
	"runtime"
	"sync"

	"github.com/zsiec/switchframe/server/switcher/frcasm"
)

// Compile-time assertion: diamondSearch's computeSAD closure calls
// frcasm.SadBlock16x16, which is hardcoded to 16x16. This must match.
var _ [frcMEBlockSize - 16]struct{} // compile error if frcMEBlockSize < 16
var _ [16 - frcMEBlockSize]struct{} // compile error if frcMEBlockSize > 16

// motionVectorField holds per-block motion vectors for forward and backward estimation.
type motionVectorField struct {
	blockSize int // 16
	cols      int // width / blockSize
	rows      int // height / blockSize

	// Sub-pixel resolution: 1 = integer pel, 2 = half-pel.
	// MVs are stored in sub-pel units: value 17 with subPel=2 means 8.5 pixels.
	subPel int

	// Forward vectors: displacement from F0 to F1 (in sub-pel units)
	fwdX   []int16
	fwdY   []int16
	fwdSAD []uint32 // matching cost per block

	// Backward vectors: displacement from F1 to F0 (in pixels)
	bwdX   []int16
	bwdY   []int16
	bwdSAD []uint32

	// Occlusion: 0=valid, 1=fwd-occluded, 2=bwd-occluded, 3=both
	occlusion []byte

	// Scratch buffers for median filter (avoids per-call allocation)
	medFwdX []int16
	medFwdY []int16
	medBwdX []int16
	medBwdY []int16
}

// newMotionVectorField allocates a motion vector field for the given frame dimensions.
func newMotionVectorField(width, height, blockSize int) *motionVectorField {
	cols := width / blockSize
	rows := height / blockSize
	n := cols * rows

	return &motionVectorField{
		blockSize: blockSize,
		cols:      cols,
		rows:      rows,
		subPel:    1,
		fwdX:      make([]int16, n),
		fwdY:      make([]int16, n),
		fwdSAD:    make([]uint32, n),
		bwdX:      make([]int16, n),
		bwdY:      make([]int16, n),
		bwdSAD:    make([]uint32, n),
		occlusion: make([]byte, n),
	}
}

// reset zeros all vectors, SAD values, and occlusion flags without reallocating.
func (mvf *motionVectorField) reset() {
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 0
		mvf.fwdY[i] = 0
		mvf.fwdSAD[i] = 0
		mvf.bwdX[i] = 0
		mvf.bwdY[i] = 0
		mvf.bwdSAD[i] = 0
		mvf.occlusion[i] = 0
	}
}

// staticBlockSADThreshold is the SAD below which a block is considered static
// and diamond search is skipped entirely. 256 = average 1 per pixel in a 16x16
// block, catching sensor noise and compression artifacts.
const staticBlockSADThreshold = 256

// Large diamond search pattern: 8 points at distance 2
var largeDiamond = [8][2]int{
	{0, -2}, {0, 2}, {-2, 0}, {2, 0},
	{-1, -1}, {1, -1}, {-1, 1}, {1, 1},
}

// Small diamond search pattern: 4 points at distance 1
var smallDiamond = [4][2]int{
	{0, -1}, {0, 1}, {-1, 0}, {1, 0},
}

// diamondSearch finds the best matching block for position (bx, by) in the reference frame.
// initMVX, initMVY provide the initial search center (predictor from neighboring blocks).
// Returns (mvx, mvy, sad).
//
// Algorithm:
//  1. Start at predictor (initMVX, initMVY) — compute SAD
//  2. Also test (0, 0) and keep the better starting point
//  3. Large diamond pattern (8 neighbors at distance 2)
//  4. If best is center, switch to small diamond
//  5. Else move center to best point, repeat large diamond
//  6. Small diamond (4 neighbors at distance 1)
//  7. If best is center, done (converged)
//  8. Else move center, repeat small diamond
//  9. Max 25 total iterations to guarantee termination
func diamondSearch(
	cur []byte, curStride int,
	ref []byte, refStride int,
	bx, by int,
	width, height int,
	searchRange int,
	blockSize int,
	initMVX, initMVY int,
) (mvx, mvy int16, sad uint32) {
	return diamondSearchAbsRange(cur, curStride, ref, refStride, bx, by, width, height,
		searchRange, blockSize, initMVX, initMVY, searchRange)
}

// diamondSearchAbsRange is the core diamond search with an explicit absolute
// range limit. searchRange controls the window around the predictor;
// absRange caps the maximum absolute MV magnitude to prevent runaway propagation.
func diamondSearchAbsRange(
	cur []byte, curStride int,
	ref []byte, refStride int,
	bx, by int,
	width, height int,
	searchRange int,
	blockSize int,
	initMVX, initMVY int,
	absRange int,
) (mvx, mvy int16, sad uint32) {
	// Compute SAD for a candidate motion vector (dx, dy).
	// Returns (sad, ok) where ok is false if the block would read out of bounds.
	computeSAD := func(dx, dy int) (uint32, bool) {
		// Reference block position
		rx := bx + dx
		ry := by + dy
		if rx < 0 || ry < 0 || rx+blockSize > width || ry+blockSize > height {
			return 0, false
		}

		curOffset := by*curStride + bx
		refOffset := ry*refStride + rx
		return frcasm.SadBlock16x16(&cur[curOffset], &ref[refOffset], curStride, refStride), true
	}

	// Check that MV is within search range of the predictor AND within
	// the absolute range limit. The absolute limit prevents runaway MV
	// propagation through neighbor chains in single-level ME.
	inRange := func(dx, dy int) bool {
		if dx-initMVX > searchRange || dx-initMVX < -searchRange {
			return false
		}
		if dy-initMVY > searchRange || dy-initMVY < -searchRange {
			return false
		}
		if dx > absRange || dx < -absRange {
			return false
		}
		if dy > absRange || dy < -absRange {
			return false
		}
		return true
	}

	// Clamp predictor to absolute range
	cx, cy := initMVX, initMVY
	if cx > absRange {
		cx = absRange
	} else if cx < -absRange {
		cx = -absRange
	}
	if cy > absRange {
		cy = absRange
	} else if cy < -absRange {
		cy = -absRange
	}

	bestSAD := ^uint32(0)
	bestDX, bestDY := 0, 0

	// Test predictor
	if s, ok := computeSAD(cx, cy); ok {
		bestSAD = s
		bestDX = cx
		bestDY = cy
	}

	// Always also test (0, 0) and keep the better starting point
	if s, ok := computeSAD(0, 0); ok && s < bestSAD {
		bestSAD = s
		bestDX = 0
		bestDY = 0
	}

	if bestSAD == ^uint32(0) {
		return 0, 0, ^uint32(0) // block itself is out of bounds
	}

	// Static block early exit: if the best SAD so far is below the noise floor,
	// skip diamond search entirely. Threshold 256 = average 1 per pixel in a
	// 16x16 block, catching sensor noise and compression artifacts. In typical
	// studio content 50-70% of blocks are static (backgrounds, graphics, lower
	// thirds), so this avoids 8-point large diamond + 4-point small diamond
	// iterations for the majority of blocks with zero quality loss.
	if bestSAD < staticBlockSADThreshold {
		return int16(bestDX), int16(bestDY), bestSAD
	}

	cx = bestDX
	cy = bestDY

	iterations := 0
	const maxIterations = 25

	curOffset := by*curStride + bx

	// Phase 1: Large diamond search (8 candidates → 2 batches of 4)
	for iterations < maxIterations {
		iterations++
		moved := false

		// Process large diamond in 2 batches of 4.
		// Batch 1: axis-aligned {0,-2}, {0,2}, {-2,0}, {2,0}
		// Batch 2: diagonals   {-1,-1}, {1,-1}, {-1,1}, {1,1}
		for batch := 0; batch < 2; batch++ {
			var refs [4]*byte
			var candDX, candDY [4]int
			n := 0
			start := batch * 4
			for i := 0; i < 4; i++ {
				d := largeDiamond[start+i]
				nx := cx + d[0]
				ny := cy + d[1]
				if !inRange(nx, ny) {
					continue
				}
				rx := bx + nx
				ry := by + ny
				if rx < 0 || ry < 0 || rx+blockSize > width || ry+blockSize > height {
					continue
				}
				refOffset := ry*refStride + rx
				refs[n] = &ref[refOffset]
				candDX[n] = nx
				candDY[n] = ny
				n++
			}
			if n == 0 {
				continue
			}
			// Fill unused slots with cur (self-SAD=0, never wins since bestSAD is real)
			for i := n; i < 4; i++ {
				refs[i] = &cur[curOffset]
			}
			sads := frcasm.SadBlock16x16x4(&cur[curOffset], refs, curStride, refStride)
			for i := 0; i < n; i++ {
				if sads[i] < bestSAD {
					bestSAD = sads[i]
					bestDX = candDX[i]
					bestDY = candDY[i]
					moved = true
				}
			}
		}

		if !moved {
			break
		}
		cx = bestDX
		cy = bestDY
	}

	// Phase 2: Small diamond search (4 candidates → 1 batch)
	for iterations < maxIterations {
		iterations++
		moved := false

		var refs [4]*byte
		var candDX, candDY [4]int
		n := 0
		for _, d := range smallDiamond {
			nx := cx + d[0]
			ny := cy + d[1]
			if !inRange(nx, ny) {
				continue
			}
			rx := bx + nx
			ry := by + ny
			if rx < 0 || ry < 0 || rx+blockSize > width || ry+blockSize > height {
				continue
			}
			refOffset := ry*refStride + rx
			refs[n] = &ref[refOffset]
			candDX[n] = nx
			candDY[n] = ny
			n++
		}
		if n == 0 {
			break
		}
		for i := n; i < 4; i++ {
			refs[i] = &cur[curOffset]
		}
		sads := frcasm.SadBlock16x16x4(&cur[curOffset], refs, curStride, refStride)
		for i := 0; i < n; i++ {
			if sads[i] < bestSAD {
				bestSAD = sads[i]
				bestDX = candDX[i]
				bestDY = candDY[i]
				moved = true
			}
		}

		if !moved {
			break
		}
		cx = bestDX
		cy = bestDY
	}

	return int16(bestDX), int16(bestDY), bestSAD
}

// estimateMotionField computes forward and backward motion vectors between
// prev (F0) and curr (F1) using diamond search block matching.
// Results are written into the provided mvf (which is reused across frame pairs).
//
// Each block's search is initialized from the best predictor among its already-computed
// neighbors (left, top, top-right), following the standard H.264-style ME approach.
// This enables diamond search to handle large motions by propagating good MVs from
// neighboring blocks rather than always starting from (0, 0).
func estimateMotionField(prev, curr *ProcessingFrame, mvf *motionVectorField, searchRange int) {
	mvf.reset()

	w := prev.Width
	h := prev.Height
	bs := mvf.blockSize

	prevY := prev.YUV[:w*h]
	currY := curr.YUV[:w*h]

	// Forward ME pass: raster scan with neighbor predictors
	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			bx := col * bs
			by := row * bs

			// Collect predictors from already-computed neighbors
			predX, predY := collectPredictors(mvf.fwdX, mvf.fwdY, mvf.fwdSAD, mvf.cols, col, row)
			fx, fy, fSAD := diamondSearch(prevY, w, currY, w, bx, by, w, h, searchRange, bs, predX, predY)
			mvf.fwdX[idx] = fx
			mvf.fwdY[idx] = fy
			mvf.fwdSAD[idx] = fSAD
		}
	}

	// Backward ME pass: raster scan with neighbor predictors
	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			bx := col * bs
			by := row * bs

			predX, predY := collectPredictors(mvf.bwdX, mvf.bwdY, mvf.bwdSAD, mvf.cols, col, row)
			bxv, byv, bSAD := diamondSearch(currY, w, prevY, w, bx, by, w, h, searchRange, bs, predX, predY)
			mvf.bwdX[idx] = bxv
			mvf.bwdY[idx] = byv
			mvf.bwdSAD[idx] = bSAD
		}
	}
}

// collectPredictors picks the best MV predictor from already-computed neighbors.
// Checks left, top, and top-right neighbors (available in raster scan order).
// Returns (0, 0) if no neighbors are available.
func collectPredictors(mvX, mvY []int16, mvSAD []uint32, cols, col, row int) (int, int) {
	bestSAD := ^uint32(0)
	bestX, bestY := 0, 0

	// Left neighbor
	if col > 0 {
		idx := row*cols + (col - 1)
		if mvSAD[idx] < bestSAD {
			bestSAD = mvSAD[idx]
			bestX = int(mvX[idx])
			bestY = int(mvY[idx])
		}
	}

	// Top neighbor
	if row > 0 {
		idx := (row-1)*cols + col
		if mvSAD[idx] < bestSAD {
			bestSAD = mvSAD[idx]
			bestX = int(mvX[idx])
			bestY = int(mvY[idx])
		}
	}

	// Top-right neighbor
	if row > 0 && col < cols-1 {
		idx := (row-1)*cols + (col + 1)
		if mvSAD[idx] < bestSAD {
			bestX = int(mvX[idx])
			bestY = int(mvY[idx])
		}
	}

	return bestX, bestY
}

// medianFilterMVField applies a 3x3 weighted median filter to smooth outlier motion vectors.
// For each block, collects X and Y components from the 3x3 neighborhood separately,
// computes the median of each, and replaces the center value.
// Uses insertion sort for the small arrays (faster than quickselect for <=9 elements).
func medianFilterMVField(mvf *motionVectorField) {
	n := mvf.cols * mvf.rows

	// Reuse struct scratch buffers to avoid per-call allocation
	if cap(mvf.medFwdX) < n {
		mvf.medFwdX = make([]int16, n)
		mvf.medFwdY = make([]int16, n)
		mvf.medBwdX = make([]int16, n)
		mvf.medBwdY = make([]int16, n)
	}
	origFwdX := mvf.medFwdX[:n]
	origFwdY := mvf.medFwdY[:n]
	origBwdX := mvf.medBwdX[:n]
	origBwdY := mvf.medBwdY[:n]
	copy(origFwdX, mvf.fwdX)
	copy(origFwdY, mvf.fwdY)
	copy(origBwdX, mvf.bwdX)
	copy(origBwdY, mvf.bwdY)

	var buf [9]int16

	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col

			// Collect forward X neighbors
			count := 0
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					nr := row + dr
					nc := col + dc
					if nr < 0 || nr >= mvf.rows || nc < 0 || nc >= mvf.cols {
						continue
					}
					buf[count] = origFwdX[nr*mvf.cols+nc]
					count++
				}
			}
			mvf.fwdX[idx] = medianInt16(buf[:count])

			// Collect forward Y neighbors
			count = 0
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					nr := row + dr
					nc := col + dc
					if nr < 0 || nr >= mvf.rows || nc < 0 || nc >= mvf.cols {
						continue
					}
					buf[count] = origFwdY[nr*mvf.cols+nc]
					count++
				}
			}
			mvf.fwdY[idx] = medianInt16(buf[:count])

			// Collect backward X neighbors
			count = 0
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					nr := row + dr
					nc := col + dc
					if nr < 0 || nr >= mvf.rows || nc < 0 || nc >= mvf.cols {
						continue
					}
					buf[count] = origBwdX[nr*mvf.cols+nc]
					count++
				}
			}
			mvf.bwdX[idx] = medianInt16(buf[:count])

			// Collect backward Y neighbors
			count = 0
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					nr := row + dr
					nc := col + dc
					if nr < 0 || nr >= mvf.rows || nc < 0 || nc >= mvf.cols {
						continue
					}
					buf[count] = origBwdY[nr*mvf.cols+nc]
					count++
				}
			}
			mvf.bwdY[idx] = medianInt16(buf[:count])
		}
	}
}

// medianInt16 returns the median of the provided slice using insertion sort.
// The input slice is modified in place (sorted).
func medianInt16(vals []int16) int16 {
	n := len(vals)
	if n == 0 {
		return 0
	}

	// Insertion sort — optimal for n <= 9
	for i := 1; i < n; i++ {
		key := vals[i]
		j := i - 1
		for j >= 0 && vals[j] > key {
			vals[j+1] = vals[j]
			j--
		}
		vals[j+1] = key
	}

	return vals[n/2]
}

// hierarchicalME holds reusable state for multi-level hierarchical motion estimation.
// Downsamples frames in a 3-level Gaussian pyramid and propagates motion vectors
// from coarse to fine levels, extending the effective search range from ±32px to ±128px.
type hierarchicalME struct {
	// Downscaled Y planes (level 0 = full res, level 1 = half, level 2 = quarter)
	prevPyr [3][]byte
	currPyr [3][]byte

	// Per-level MV fields (levels 1 and 2 only; level 0 uses the caller's mvf)
	mvfL1 *motionVectorField
	mvfL2 *motionVectorField

	// Temporal prediction: previous frame's MV field for seeding
	temporalMVF *motionVectorField
}

func newHierarchicalME() *hierarchicalME {
	return &hierarchicalME{}
}

// estimate runs hierarchical motion estimation: L2 → L1 → L0.
// Results are written into mvf (the full-resolution MV field).
// searchRange applies at each level (typically 32), giving ±128px total at L0.
func (h *hierarchicalME) estimate(prev, curr *ProcessingFrame, mvf *motionVectorField, searchRange int) {
	w := prev.Width
	h0 := prev.Height
	bs := mvf.blockSize

	prevY := prev.YUV[:w*h0]
	currY := curr.YUV[:w*h0]

	// Level 0: full resolution (pointers, no copy)
	h.prevPyr[0] = prevY
	h.currPyr[0] = currY

	// Level 1: half resolution
	w1, h1 := w/2, h0/2
	if len(h.prevPyr[1]) < w1*h1 {
		h.prevPyr[1] = make([]byte, w1*h1)
		h.currPyr[1] = make([]byte, w1*h1)
	}
	downsampleY2x(h.prevPyr[1][:w1*h1], prevY, w, h0)
	downsampleY2x(h.currPyr[1][:w1*h1], currY, w, h0)

	// Level 2: quarter resolution
	w2, h2 := w1/2, h1/2
	if len(h.prevPyr[2]) < w2*h2 {
		h.prevPyr[2] = make([]byte, w2*h2)
		h.currPyr[2] = make([]byte, w2*h2)
	}
	downsampleY2x(h.prevPyr[2][:w2*h2], h.prevPyr[1][:w1*h1], w1, h1)
	downsampleY2x(h.currPyr[2][:w2*h2], h.currPyr[1][:w1*h1], w1, h1)

	// --- Level 2 (coarsest): full search, no predictor from above ---
	cols2 := w2 / bs
	rows2 := h2 / bs
	if h.mvfL2 == nil || h.mvfL2.cols != cols2 || h.mvfL2.rows != rows2 {
		h.mvfL2 = newMotionVectorField(w2, h2, bs)
	}
	pfL2prev := &ProcessingFrame{YUV: h.prevPyr[2][:w2*h2], Width: w2, Height: h2}
	pfL2curr := &ProcessingFrame{YUV: h.currPyr[2][:w2*h2], Width: w2, Height: h2}
	estimateMotionField(pfL2prev, pfL2curr, h.mvfL2, searchRange)

	// --- Level 1 (half res): seed from upscaled L2 MVs ---
	cols1 := w1 / bs
	rows1 := h1 / bs
	if h.mvfL1 == nil || h.mvfL1.cols != cols1 || h.mvfL1.rows != rows1 {
		h.mvfL1 = newMotionVectorField(w1, h1, bs)
	}
	h.mvfL1.reset()

	prevL1 := h.prevPyr[1][:w1*h1]
	currL1 := h.currPyr[1][:w1*h1]
	estimateWithSeeds(prevL1, currL1, w1, h1, h.mvfL1, h.mvfL2, nil, searchRange, bs)

	// --- Level 0 (full res): seed from upscaled L1 MVs + temporal prediction ---
	mvf.reset()
	estimateWithSeeds(prevY, currY, w, h0, mvf, h.mvfL1, h.temporalMVF, searchRange, bs)

	// Save current MV field as temporal predictor for next frame pair
	// (stored in integer-pel units before half-pel refinement)
	if h.temporalMVF == nil || h.temporalMVF.cols != mvf.cols || h.temporalMVF.rows != mvf.rows {
		h.temporalMVF = newMotionVectorField(w, h0, bs)
	}
	n := mvf.cols * mvf.rows
	copy(h.temporalMVF.fwdX[:n], mvf.fwdX[:n])
	copy(h.temporalMVF.fwdY[:n], mvf.fwdY[:n])
	copy(h.temporalMVF.fwdSAD[:n], mvf.fwdSAD[:n])
	copy(h.temporalMVF.bwdX[:n], mvf.bwdX[:n])
	copy(h.temporalMVF.bwdY[:n], mvf.bwdY[:n])
	copy(h.temporalMVF.bwdSAD[:n], mvf.bwdSAD[:n])

	// Half-pel refinement: test 8 sub-pixel positions around each integer-pel result.
	// After this, MVs are in half-pel units (×2) and mvf.subPel = 2.
	halfPelRefineMVField(mvf, prevY, currY, w, h0)
}

// estimateWithSeeds runs diamond search ME, seeding each block's initial predictor
// from the corresponding block in the parent (coarser) MV field. Parent MVs are
// scaled 2× since each parent pixel represents 2 pixels at this level.
//
// temporal is an optional MV field from the previous frame pair (same resolution).
// When non-nil, the temporal MV is tested as an additional starting candidate
// within a single diamond search (no extra full search).
//
// The absolute range for each block is seed_magnitude + searchRange, allowing
// refinement around the parent's estimate without unbounded MV propagation.
func estimateWithSeeds(
	prevY, currY []byte,
	w, h int,
	mvf *motionVectorField,
	parent *motionVectorField,
	temporal *motionVectorField,
	searchRange, bs int,
) {
	hasTemporal := temporal != nil && temporal.cols == mvf.cols && temporal.rows == mvf.rows

	// Forward ME pass
	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			bx := col * bs
			by := row * bs

			// Get seed from parent level (halved coordinates, 2× MV)
			seedX, seedY := parentPredictor(parent.fwdX, parent.fwdY, parent.cols, parent.rows, col, row)
			neighX, neighY := collectPredictors(mvf.fwdX, mvf.fwdY, mvf.fwdSAD, mvf.cols, col, row)

			// Pick the best seed among: parent, neighbor, temporal
			bestSeedX, bestSeedY := pickBestSeed(
				prevY, currY, w, h, bx, by, bs,
				seedX, seedY, neighX, neighY,
				hasTemporal, temporal, idx, true,
			)

			absRange := absRangeForSeed(bestSeedX, bestSeedY, searchRange)
			fx, fy, fSAD := diamondSearchAbsRange(prevY, w, currY, w, bx, by, w, h, searchRange, bs, bestSeedX, bestSeedY, absRange)

			mvf.fwdX[idx] = fx
			mvf.fwdY[idx] = fy
			mvf.fwdSAD[idx] = fSAD
		}
	}

	// Backward ME pass
	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			bx := col * bs
			by := row * bs

			seedX, seedY := parentPredictor(parent.bwdX, parent.bwdY, parent.cols, parent.rows, col, row)
			neighX, neighY := collectPredictors(mvf.bwdX, mvf.bwdY, mvf.bwdSAD, mvf.cols, col, row)

			bestSeedX, bestSeedY := pickBestSeed(
				currY, prevY, w, h, bx, by, bs,
				seedX, seedY, neighX, neighY,
				hasTemporal, temporal, idx, false,
			)

			absRange := absRangeForSeed(bestSeedX, bestSeedY, searchRange)
			bxv, byv, bSAD := diamondSearchAbsRange(currY, w, prevY, w, bx, by, w, h, searchRange, bs, bestSeedX, bestSeedY, absRange)

			mvf.bwdX[idx] = bxv
			mvf.bwdY[idx] = byv
			mvf.bwdSAD[idx] = bSAD
		}
	}
}

// pickBestSeed evaluates up to 3 candidate seeds (parent, neighbor, temporal)
// by computing a single SAD for each and returning the one with lowest cost.
// This replaces running multiple full diamond searches — just one SAD per candidate.
func pickBestSeed(
	cur, ref []byte,
	w, h, bx, by, bs int,
	parentX, parentY int,
	neighX, neighY int,
	hasTemporal bool,
	temporal *motionVectorField,
	idx int,
	forward bool,
) (int, int) {
	bestX, bestY := parentX, parentY
	bestSAD := sadForMV(cur, ref, w, h, bx, by, bs, parentX, parentY)

	nSAD := sadForMV(cur, ref, w, h, bx, by, bs, neighX, neighY)
	if nSAD < bestSAD {
		bestSAD = nSAD
		bestX = neighX
		bestY = neighY
	}

	if hasTemporal {
		var tx, ty int
		if forward {
			tx = int(temporal.fwdX[idx])
			ty = int(temporal.fwdY[idx])
		} else {
			tx = int(temporal.bwdX[idx])
			ty = int(temporal.bwdY[idx])
		}
		tSAD := sadForMV(cur, ref, w, h, bx, by, bs, tx, ty)
		if tSAD < bestSAD {
			bestX = tx
			bestY = ty
		}
	}

	return bestX, bestY
}

// sadForMV computes SAD for a single MV candidate. Returns max uint32 if out of bounds.
func sadForMV(cur, ref []byte, w, h, bx, by, bs, mvx, mvy int) uint32 {
	rx := bx + mvx
	ry := by + mvy
	if rx < 0 || ry < 0 || rx+bs > w || ry+bs > h {
		return ^uint32(0)
	}
	curOff := by*w + bx
	refOff := ry*w + rx
	return frcasm.SadBlock16x16(&cur[curOff], &ref[refOff], w, w)
}

// absRangeForSeed computes the absolute MV range needed to search around a seed.
// Returns max(abs(seedX), abs(seedY)) + searchRange.
func absRangeForSeed(seedX, seedY, searchRange int) int {
	ax := seedX
	if ax < 0 {
		ax = -ax
	}
	ay := seedY
	if ay < 0 {
		ay = -ay
	}
	m := ax
	if ay > m {
		m = ay
	}
	return m + searchRange
}

// parentPredictor returns the upscaled MV from the parent level for position (col, row).
// Parent coordinates are col/2, row/2. MVs are scaled 2× since one parent pixel = 2 child pixels.
func parentPredictor(mvX, mvY []int16, parentCols, parentRows, col, row int) (int, int) {
	if parentCols <= 0 || parentRows <= 0 {
		return 0, 0
	}
	pc := col / 2
	pr := row / 2
	if pc >= parentCols {
		pc = parentCols - 1
	}
	if pr >= parentRows {
		pr = parentRows - 1
	}
	idx := pr*parentCols + pc
	return int(mvX[idx]) * 2, int(mvY[idx]) * 2
}

// downsampleY2x downsamples a Y plane by 2× using box filter (average of 2×2 blocks).
// dst must be at least (w/2)*(h/2) bytes. src must be at least w*h bytes.
func downsampleY2x(dst, src []byte, w, h int) {
	dstW := w / 2
	for row := 0; row < h/2; row++ {
		srcRow0 := row * 2 * w
		srcRow1 := srcRow0 + w
		dstOff := row * dstW
		for col := 0; col < dstW; col++ {
			sc := col * 2
			a := int(src[srcRow0+sc])
			b := int(src[srcRow0+sc+1])
			c := int(src[srcRow1+sc])
			d := int(src[srcRow1+sc+1])
			dst[dstOff+col] = byte((a + b + c + d + 2) / 4)
		}
	}
}

// halfPelRefine performs half-pixel refinement on all MVs in the field.
// For each block, tests 8 half-pel positions around the integer-pel result
// using bilinear-interpolated reference pixels. If a half-pel position has
// lower SAD, the MV is updated. After refinement, MVs are stored in half-pel
// units (original integer values ×2, half-pel adds ±1) and subPel is set to 2.
//
func halfPelRefine(mvf *motionVectorField, cur, ref []byte, w, h int, forward bool) {
	bs := mvf.blockSize

	var mvX, mvY []int16
	var mvSAD []uint32
	if forward {
		mvX, mvY, mvSAD = mvf.fwdX, mvf.fwdY, mvf.fwdSAD
	} else {
		mvX, mvY, mvSAD = mvf.bwdX, mvf.bwdY, mvf.bwdSAD
	}

	cols := mvf.cols
	rows := mvf.rows

	// Row-parallel: each block row is independent
	numWorkers := runtime.NumCPU()
	if numWorkers > rows {
		numWorkers = rows
	}
	if numWorkers <= 1 {
		halfPelRefineRows(mvX, mvY, mvSAD, cur, ref, w, h, bs, cols, 0, rows)
		return
	}

	var wg sync.WaitGroup
	rowsPerWorker := rows / numWorkers
	for g := 0; g < numWorkers; g++ {
		startRow := g * rowsPerWorker
		endRow := startRow + rowsPerWorker
		if g == numWorkers-1 {
			endRow = rows
		}
		wg.Add(1)
		go func(sr, er int) {
			defer wg.Done()
			halfPelRefineRows(mvX, mvY, mvSAD, cur, ref, w, h, bs, cols, sr, er)
		}(startRow, endRow)
	}
	wg.Wait()
}

// halfPelRefineRows processes block rows [startRow, endRow) for half-pel refinement.
// halfPelSkipSADThreshold: blocks with integer-pel SAD above this are
// occluded/mismatched and won't benefit from sub-pixel refinement.
// Just convert their MVs to half-pel units and move on.
const halfPelSkipSADThreshold = uint32(4096)

func halfPelRefineRows(mvX, mvY []int16, mvSAD []uint32, cur, ref []byte, w, h, bs, cols, startRow, endRow int) {
	for row := startRow; row < endRow; row++ {
		by := row * bs
		rowBase := row * cols

		for col := 0; col < cols; col++ {
			bx := col * bs
			i := rowBase + col

			// Current integer MV in half-pel units
			bestHpX := int(mvX[i]) * 2
			bestHpY := int(mvY[i]) * 2
			bestSAD := mvSAD[i]

			// Skip half-pel refinement for high-SAD blocks — they're
			// occluded or mismatched and won't benefit from sub-pixel search.
			if bestSAD > halfPelSkipSADThreshold {
				mvX[i] = int16(bestHpX)
				mvY[i] = int16(bestHpY)
				continue
			}

			// Phase 1: Test 4 axis-aligned half-pel positions
			axisImproved := false
			for _, off := range [][2]int{{0, -1}, {-1, 0}, {1, 0}, {0, 1}} {
				hpX := bestHpX + off[0]
				hpY := bestHpY + off[1]

				sad, ok := sadHalfPelFused(cur, ref, w, h, bx, by, bs, hpX, hpY)
				if ok && sad < bestSAD {
					bestSAD = sad
					bestHpX = hpX
					bestHpY = hpY
					axisImproved = true
				}
			}

			// Phase 2: Only test diagonals if axis-aligned search found improvement
			if axisImproved {
				for _, off := range [][2]int{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
					hpX := bestHpX + off[0]
					hpY := bestHpY + off[1]

					sad, ok := sadHalfPelFused(cur, ref, w, h, bx, by, bs, hpX, hpY)
					if ok && sad < bestSAD {
						bestSAD = sad
						bestHpX = hpX
						bestHpY = hpY
					}
				}
			}

			mvX[i] = int16(bestHpX)
			mvY[i] = int16(bestHpY)
			mvSAD[i] = bestSAD
		}
	}
}

// sadHalfPelFused computes SAD between a current block at (bx, by) and a
// half-pel interpolated reference, fusing interpolation and SAD in one pass
// (no intermediate buffer). hpX, hpY are in half-pel units.
func sadHalfPelFused(cur, ref []byte, w, h, bx, by, bs, hpX, hpY int) (uint32, bool) {
	intX := hpX >> 1
	intY := hpY >> 1
	fracX := hpX & 1
	fracY := hpY & 1

	rx := bx + intX
	ry := by + intY

	maxRX := rx + bs
	maxRY := ry + bs
	if fracX != 0 {
		maxRX++
	}
	if fracY != 0 {
		maxRY++
	}
	if rx < 0 || ry < 0 || maxRX > w || maxRY > h {
		return 0, false
	}

	if fracX == 0 && fracY == 0 {
		curOff := by*w + bx
		refOff := ry*w + rx
		return frcasm.SadBlock16x16(&cur[curOff], &ref[refOff], w, w), true
	}

	// SIMD fused interpolation + SAD via platform-specific kernels.
	// Uses PAVGB/URHADD rounding: (a+b+1)>>1 for H/V, cascaded for diagonal.
	curOff := by*w + bx
	refOff := ry*w + rx
	if fracX != 0 && fracY != 0 {
		return frcasm.SadBlock16x16HpelD(&cur[curOff], &ref[refOff], w, w), true
	} else if fracX != 0 {
		return frcasm.SadBlock16x16HpelH(&cur[curOff], &ref[refOff], w, w), true
	}
	return frcasm.SadBlock16x16HpelV(&cur[curOff], &ref[refOff], w, w), true
}

// halfPelRefineMVField applies half-pel refinement to both forward and backward
// MV fields concurrently. After this call, all MVs are in half-pel units and
// subPel is set to 2.
func halfPelRefineMVField(mvf *motionVectorField, prevY, currY []byte, w, h int) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		halfPelRefine(mvf, prevY, currY, w, h, true) // forward: prev→curr
	}()
	go func() {
		defer wg.Done()
		halfPelRefine(mvf, currY, prevY, w, h, false) // backward: curr→prev
	}()
	wg.Wait()
	mvf.subPel = 2
}

// checkConsistency marks blocks as occluded where forward and backward vectors disagree.
// A block at (col, row) with forward vector (fx, fy) should have a backward vector near (-fx, -fy)
// at the destination block (col + fx/blockSize, row + fy/blockSize). If the L1 disagreement
// exceeds threshold pixels, the block is marked occluded.
func checkConsistency(mvf *motionVectorField, threshold int) {
	bs := mvf.blockSize
	sp := mvf.subPel
	if sp < 1 {
		sp = 1
	}
	// Threshold in sub-pel units
	thresh16 := int16(threshold * sp)
	// Block stride in sub-pel units
	bsSP := bs * sp

	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col

			// Check forward -> backward consistency
			fx := mvf.fwdX[idx]
			fy := mvf.fwdY[idx]

			// Destination block in F1 (MVs in sub-pel units, divide by blockSize*subPel)
			dstCol := col + int(fx)/bsSP
			dstRow := row + int(fy)/bsSP

			fwdOccluded := false
			if dstCol < 0 || dstCol >= mvf.cols || dstRow < 0 || dstRow >= mvf.rows {
				fwdOccluded = true
			} else {
				dstIdx := dstRow*mvf.cols + dstCol
				bx := mvf.bwdX[dstIdx]
				by := mvf.bwdY[dstIdx]

				// Forward + backward should cancel out
				diffX := fx + bx
				diffY := fy + by
				if diffX < 0 {
					diffX = -diffX
				}
				if diffY < 0 {
					diffY = -diffY
				}
				if diffX+diffY > thresh16 {
					fwdOccluded = true
				}
			}

			// Check backward -> forward consistency
			bkx := mvf.bwdX[idx]
			bky := mvf.bwdY[idx]

			srcCol := col + int(bkx)/bsSP
			srcRow := row + int(bky)/bsSP

			bwdOccluded := false
			if srcCol < 0 || srcCol >= mvf.cols || srcRow < 0 || srcRow >= mvf.rows {
				bwdOccluded = true
			} else {
				srcIdx := srcRow*mvf.cols + srcCol
				rfx := mvf.fwdX[srcIdx]
				rfy := mvf.fwdY[srcIdx]

				diffX := bkx + rfx
				diffY := bky + rfy
				if diffX < 0 {
					diffX = -diffX
				}
				if diffY < 0 {
					diffY = -diffY
				}
				if diffX+diffY > thresh16 {
					bwdOccluded = true
				}
			}

			// Encode occlusion flags
			var occ byte
			if fwdOccluded {
				occ |= 1
			}
			if bwdOccluded {
				occ |= 2
			}
			mvf.occlusion[idx] = occ
		}
	}
}
