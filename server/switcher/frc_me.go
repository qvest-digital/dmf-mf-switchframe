package switcher

import (
	"github.com/zsiec/switchframe/server/switcher/frcasm"
)

// motionVectorField holds per-block motion vectors for forward and backward estimation.
type motionVectorField struct {
	blockSize int // 16
	cols      int // width / blockSize
	rows      int // height / blockSize

	// Forward vectors: displacement from F0 to F1 (in pixels)
	fwdX   []int16
	fwdY   []int16
	fwdSAD []uint32 // matching cost per block

	// Backward vectors: displacement from F1 to F0 (in pixels)
	bwdX   []int16
	bwdY   []int16
	bwdSAD []uint32

	// Occlusion: 0=valid, 1=fwd-occluded, 2=bwd-occluded, 3=both
	occlusion []byte
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

	// Check that motion vector is within search range
	inRange := func(dx, dy int) bool {
		if dx > searchRange || dx < -searchRange {
			return false
		}
		if dy > searchRange || dy < -searchRange {
			return false
		}
		return true
	}

	// Start with the predictor
	cx, cy := initMVX, initMVY

	// Clamp predictor to search range
	if cx > searchRange {
		cx = searchRange
	} else if cx < -searchRange {
		cx = -searchRange
	}
	if cy > searchRange {
		cy = searchRange
	} else if cy < -searchRange {
		cy = -searchRange
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

	cx = bestDX
	cy = bestDY

	iterations := 0
	const maxIterations = 25

	// Phase 1: Large diamond search
	for iterations < maxIterations {
		iterations++
		moved := false

		for _, d := range largeDiamond {
			nx := cx + d[0]
			ny := cy + d[1]
			if !inRange(nx, ny) {
				continue
			}
			s, valid := computeSAD(nx, ny)
			if !valid {
				continue
			}
			if s < bestSAD {
				bestSAD = s
				bestDX = nx
				bestDY = ny
				moved = true
			}
		}

		if !moved {
			// Best is center, switch to small diamond
			break
		}
		cx = bestDX
		cy = bestDY
	}

	// Phase 2: Small diamond search
	for iterations < maxIterations {
		iterations++
		moved := false

		for _, d := range smallDiamond {
			nx := cx + d[0]
			ny := cy + d[1]
			if !inRange(nx, ny) {
				continue
			}
			s, valid := computeSAD(nx, ny)
			if !valid {
				continue
			}
			if s < bestSAD {
				bestSAD = s
				bestDX = nx
				bestDY = ny
				moved = true
			}
		}

		if !moved {
			// Converged
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

	// Work on copies to avoid read-after-write dependency
	origFwdX := make([]int16, n)
	origFwdY := make([]int16, n)
	origBwdX := make([]int16, n)
	origBwdY := make([]int16, n)
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

// checkConsistency marks blocks as occluded where forward and backward vectors disagree.
// A block at (col, row) with forward vector (fx, fy) should have a backward vector near (-fx, -fy)
// at the destination block (col + fx/blockSize, row + fy/blockSize). If the L1 disagreement
// exceeds threshold pixels, the block is marked occluded.
func checkConsistency(mvf *motionVectorField, threshold int) {
	bs := mvf.blockSize
	thresh16 := int16(threshold)

	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col

			// Check forward -> backward consistency
			fx := mvf.fwdX[idx]
			fy := mvf.fwdY[idx]

			// Destination block in F1
			dstCol := col + int(fx)/bs
			dstRow := row + int(fy)/bs

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

			srcCol := col + int(bkx)/bs
			srcRow := row + int(bky)/bs

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
