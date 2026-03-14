package switcher

import (
	"runtime"
	"sync"
)

// Fixed-point 16.16 constants for the fast warp pipeline.
const (
	fpShift = 16
	fpOne   = 1 << fpShift       // 65536
	fpHalf  = 1 << (fpShift - 1) // 32768
	fpMask  = fpOne - 1          // 0xFFFF
)

// mcfiInterpolateFast produces an interpolated frame using 16.16 fixed-point
// arithmetic and row-parallel processing. This is a performance-optimized
// replacement for mcfiInterpolateSmooth that produces output within +/-1 LSB
// of the float64 reference.
//
// dst, srcA, srcB are YUV420 frames (width * height * 3/2 bytes).
// mvf contains per-16x16-block motion vectors.
// alpha is the interpolation position (0.0=srcA, 1.0=srcB).
func mcfiInterpolateFast(dst, srcA, srcB []byte, width, height int, mvf *motionVectorField, alpha float64) {
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crOff := ySize + cbSize

	// Run all 3 planes concurrently.
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		fastWarpPlane(dst[:ySize], srcA[:ySize], srcB[:ySize],
			width, height, mvf, alpha, 1, 0)
	}()

	go func() {
		defer wg.Done()
		fastWarpPlane(dst[ySize:ySize+cbSize], srcA[ySize:ySize+cbSize], srcB[ySize:ySize+cbSize],
			width/2, height/2, mvf, alpha, 2, 0)
	}()

	go func() {
		defer wg.Done()
		fastWarpPlane(dst[crOff:crOff+cbSize], srcA[crOff:crOff+cbSize], srcB[crOff:crOff+cbSize],
			width/2, height/2, mvf, alpha, 2, 0)
	}()

	wg.Wait()
}

// fastWarpPlane warps and blends a single YUV plane using 16.16 fixed-point
// arithmetic with optional row-parallel processing.
//
// chromaScale is 1 for luma, 2 for 4:2:0 chroma.
// maxGoroutines: 0 = auto, 1 = serial, >1 = that many goroutines.
func fastWarpPlane(dst, srcA, srcB []byte, planeW, planeH int, mvf *motionVectorField, alpha float64, chromaScale int, maxGoroutines int) {
	if maxGoroutines == 0 {
		n := runtime.NumCPU()
		maxRows := planeH / 16
		if maxRows < 1 {
			maxRows = 1
		}
		if n > maxRows {
			n = maxRows
		}
		maxGoroutines = n
	}

	alphaFP := int64(alpha*float64(fpOne) + 0.5)
	if alphaFP < 0 {
		alphaFP = 0
	}
	if alphaFP > fpOne {
		alphaFP = fpOne
	}
	invAlphaFP := int64(fpOne) - alphaFP

	if maxGoroutines <= 1 || planeH < 2 {
		fastWarpRows(dst, srcA, srcB, planeW, planeH, mvf, 0, planeH, alphaFP, invAlphaFP, chromaScale)
		return
	}

	numWorkers := maxGoroutines
	if numWorkers > planeH {
		numWorkers = planeH
	}

	var wg sync.WaitGroup
	rowsPerWorker := planeH / numWorkers

	for g := 0; g < numWorkers; g++ {
		startRow := g * rowsPerWorker
		endRow := startRow + rowsPerWorker
		if g == numWorkers-1 {
			endRow = planeH
		}

		wg.Add(1)
		go func(sr, er int) {
			defer wg.Done()
			fastWarpRows(dst, srcA, srcB, planeW, planeH, mvf, sr, er, alphaFP, invAlphaFP, chromaScale)
		}(startRow, endRow)
	}

	wg.Wait()
}

// fastWarpRows processes rows [startRow, endRow) using 16.16 fixed-point
// arithmetic with fully inlined bilinear sampling for maximum performance.
func fastWarpRows(
	dst, srcA, srcB []byte,
	planeW, planeH int,
	mvf *motionVectorField,
	startRow, endRow int,
	alphaFP, invAlphaFP int64,
	chromaScale int,
) {
	bs := mvf.blockSize
	halfBSFP := int64(bs) << (fpShift - 1)
	invBSFP := int64(fpOne) / int64(bs)

	maxPxX := int64(planeW-1) << fpShift
	maxPxY := int64(planeH-1) << fpShift

	mvCols := mvf.cols
	mvRows := mvf.rows

	fwdXSlice := mvf.fwdX
	fwdYSlice := mvf.fwdY
	bwdXSlice := mvf.bwdX
	bwdYSlice := mvf.bwdY
	relSlice := mvf.reliability
	hasReliability := len(relSlice) == mvf.cols*mvf.rows

	subPel := mvf.subPel
	if subPel < 1 {
		subPel = 1
	}
	mvScaleFP := int64(fpOne) / int64(chromaScale*subPel)

	lastRow := planeH - 1
	lastCol := planeW - 1

	for py := startRow; py < endRow; py++ {
		lumaYFP := int64(py*chromaScale) << fpShift
		byFP := ((lumaYFP - halfBSFP) * invBSFP) >> fpShift
		by0 := int(byFP >> fpShift)
		if by0 < 0 {
			by0 = 0
		}
		by1 := by0 + 1
		if by1 >= mvRows {
			by1 = mvRows - 1
		}

		fyFP := byFP & fpMask
		if int(byFP>>fpShift) < 0 {
			fyFP = 0
		}
		invFyFP := int64(fpOne) - fyFP

		rowOff := py * planeW
		by0Off := by0 * mvCols
		by1Off := by1 * mvCols

		dstRow := dst[rowOff : rowOff+planeW]

		for px := 0; px < planeW; px++ {
			lumaXFP := int64(px*chromaScale) << fpShift

			bxFP := ((lumaXFP - halfBSFP) * invBSFP) >> fpShift
			bx0 := int(bxFP >> fpShift)
			if bx0 < 0 {
				bx0 = 0
			}
			bx1 := bx0 + 1
			if bx1 >= mvCols {
				bx1 = mvCols - 1
			}

			fxFP := bxFP & fpMask
			if int(bxFP>>fpShift) < 0 {
				fxFP = 0
			}
			invFxFP := int64(fpOne) - fxFP

			i00 := by0Off + bx0
			i10 := by0Off + bx1
			i01 := by1Off + bx0
			i11 := by1Off + bx1

			// Forward MV bilinear interpolation
			fmvxFP := ((int64(fwdXSlice[i00])*invFxFP+int64(fwdXSlice[i10])*fxFP)*invFyFP +
				(int64(fwdXSlice[i01])*invFxFP+int64(fwdXSlice[i11])*fxFP)*fyFP + fpHalf) >> fpShift
			fmvyFP := ((int64(fwdYSlice[i00])*invFxFP+int64(fwdYSlice[i10])*fxFP)*invFyFP +
				(int64(fwdYSlice[i01])*invFxFP+int64(fwdYSlice[i11])*fxFP)*fyFP + fpHalf) >> fpShift

			// Backward MV bilinear interpolation
			bmvxFP := ((int64(bwdXSlice[i00])*invFxFP+int64(bwdXSlice[i10])*fxFP)*invFyFP +
				(int64(bwdXSlice[i01])*invFxFP+int64(bwdXSlice[i11])*fxFP)*fyFP + fpHalf) >> fpShift
			bmvyFP := ((int64(bwdYSlice[i00])*invFxFP+int64(bwdYSlice[i10])*fxFP)*invFyFP +
				(int64(bwdYSlice[i01])*invFxFP+int64(bwdYSlice[i11])*fxFP)*fyFP + fpHalf) >> fpShift

			// Displaced positions
			pxFP := int64(px) << fpShift
			pyFP := int64(py) << fpShift

			sAxFP := pxFP - ((fmvxFP*alphaFP>>fpShift)*mvScaleFP)>>fpShift
			sAyFP := pyFP - ((fmvyFP*alphaFP>>fpShift)*mvScaleFP)>>fpShift
			sBxFP := pxFP - ((bmvxFP*invAlphaFP>>fpShift)*mvScaleFP)>>fpShift
			sByFP := pyFP - ((bmvyFP*invAlphaFP>>fpShift)*mvScaleFP)>>fpShift

			// Clamp all positions
			if sAxFP < 0 {
				sAxFP = 0
			} else if sAxFP > maxPxX {
				sAxFP = maxPxX
			}
			if sAyFP < 0 {
				sAyFP = 0
			} else if sAyFP > maxPxY {
				sAyFP = maxPxY
			}
			if sBxFP < 0 {
				sBxFP = 0
			} else if sBxFP > maxPxX {
				sBxFP = maxPxX
			}
			if sByFP < 0 {
				sByFP = 0
			} else if sByFP > maxPxY {
				sByFP = maxPxY
			}

			// ---- Inline bilinear sample srcA ----
			ax0 := int(sAxFP >> fpShift)
			ay0 := int(sAyFP >> fpShift)
			afx := sAxFP & fpMask
			afy := sAyFP & fpMask
			ax1 := ax0 + 1
			ay1 := ay0 + 1
			if ax1 > lastCol {
				ax1 = lastCol
			}
			if ay1 > lastRow {
				ay1 = lastRow
			}
			aInvFx := int64(fpOne) - afx
			aInvFy := int64(fpOne) - afy
			sA := (int64(srcA[ay0*planeW+ax0])*aInvFx*aInvFy +
				int64(srcA[ay0*planeW+ax1])*afx*aInvFy +
				int64(srcA[ay1*planeW+ax0])*aInvFx*afy +
				int64(srcA[ay1*planeW+ax1])*afx*afy +
				int64(fpOne)*fpHalf) >> (2 * fpShift)

			// ---- Inline bilinear sample srcB ----
			bx0v := int(sBxFP >> fpShift)
			by0v := int(sByFP >> fpShift)
			bfx := sBxFP & fpMask
			bfy := sByFP & fpMask
			bx1v := bx0v + 1
			by1v := by0v + 1
			if bx1v > lastCol {
				bx1v = lastCol
			}
			if by1v > lastRow {
				by1v = lastRow
			}
			bInvFx := int64(fpOne) - bfx
			bInvFy := int64(fpOne) - bfy
			sB := (int64(srcB[by0v*planeW+bx0v])*bInvFx*bInvFy +
				int64(srcB[by0v*planeW+bx1v])*bfx*bInvFy +
				int64(srcB[by1v*planeW+bx0v])*bInvFx*bfy +
				int64(srcB[by1v*planeW+bx1v])*bfx*bfy +
				int64(fpOne)*fpHalf) >> (2 * fpShift)

			// MCFI warp blend
			mcfi := (sA*invAlphaFP + sB*alphaFP + fpHalf) >> fpShift

			// Reliability-weighted fallback: blend between MCFI warp and
			// simple linear interpolation based on per-block reliability.
			// This eliminates visible blocking at boundaries between
			// well-matched and poorly-matched blocks.
			if hasReliability {
				// Bilinear interpolation of reliability from 4 nearest blocks
				r00 := int64(relSlice[i00])
				r10 := int64(relSlice[i10])
				r01 := int64(relSlice[i01])
				r11 := int64(relSlice[i11])
				relFP := ((r00*invFxFP+r10*fxFP)*invFyFP +
					(r01*invFxFP+r11*fxFP)*fyFP + fpHalf) >> fpShift
				// relFP is in [0, 255]; scale to [0, fpOne]
				relFP = relFP * 257

				if relFP < fpOne {
					// Linear blend fallback: srcA[px,py] and srcB[px,py] at
					// the output position (no motion compensation).
					linearIdx := py*planeW + px
					linearA := int64(srcA[linearIdx])
					linearB := int64(srcB[linearIdx])
					linear := (linearA*invAlphaFP + linearB*alphaFP + fpHalf) >> fpShift

					mcfi = (mcfi*relFP + linear*(int64(fpOne)-relFP) + fpHalf) >> fpShift
				}
			}

			if mcfi < 0 {
				mcfi = 0
			} else if mcfi > 255 {
				mcfi = 255
			}

			dstRow[px] = byte(mcfi)
		}
	}
}
