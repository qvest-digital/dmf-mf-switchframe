package switcher

import (
	"github.com/zsiec/switchframe/server/transition"
)

// mcfiInterpolate produces an interpolated frame at fractional position alpha (0.0-1.0)
// between f0 and f1 using the precomputed motion vector field.
// alpha=0.0 produces f0, alpha=1.0 produces f1.
// Output is written to dst (must be at least w*h*3/2 bytes).
// warpA and warpB are scratch buffers (same size as dst) for the two warped intermediates.
func mcfiInterpolate(dst, warpA, warpB []byte, f0, f1 *ProcessingFrame, mvf *motionVectorField, alpha float64) {
	w := f0.Width
	h := f0.Height
	bs := mvf.blockSize

	// Step 1: Initialize scratch buffers with source frames for uncovered region fill.
	// Any pixel not written by the warp loop retains the source frame value.
	copy(warpA, f0.YUV)
	copy(warpB, f1.YUV)

	ySize := w * h
	cbSize := (w / 2) * (h / 2)

	// Step 2: Forward warp — move blocks from F0 by scaled forward MV into warpA.
	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			srcX := col * bs
			srcY := row * bs

			// Scale MV by alpha
			mvx := int(float64(mvf.fwdX[idx]) * alpha)
			mvy := int(float64(mvf.fwdY[idx]) * alpha)

			// Warp Y plane
			warpBlockY(warpA, f0.YUV, w, w, srcX, srcY, mvx, mvy, bs, w, h)

			// Warp Cb plane (4:2:0 subsampled)
			chromaSrcX := srcX / 2
			chromaSrcY := srcY / 2
			chromaMVX := mvx / 2
			chromaMVY := mvy / 2
			chromaBS := bs / 2
			chromaW := w / 2
			chromaH := h / 2
			warpBlockY(
				warpA[ySize:ySize+cbSize],
				f0.YUV[ySize:ySize+cbSize],
				chromaW, chromaW,
				chromaSrcX, chromaSrcY,
				chromaMVX, chromaMVY,
				chromaBS, chromaW, chromaH,
			)

			// Warp Cr plane
			warpBlockY(
				warpA[ySize+cbSize:],
				f0.YUV[ySize+cbSize:],
				chromaW, chromaW,
				chromaSrcX, chromaSrcY,
				chromaMVX, chromaMVY,
				chromaBS, chromaW, chromaH,
			)
		}
	}

	// Step 3: Backward warp — move blocks from F1 by scaled backward MV into warpB.
	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			srcX := col * bs
			srcY := row * bs

			// Scale MV by (1 - alpha)
			invAlpha := 1.0 - alpha
			mvx := int(float64(mvf.bwdX[idx]) * invAlpha)
			mvy := int(float64(mvf.bwdY[idx]) * invAlpha)

			// Warp Y plane
			warpBlockY(warpB, f1.YUV, w, w, srcX, srcY, mvx, mvy, bs, w, h)

			// Warp Cb plane
			chromaSrcX := srcX / 2
			chromaSrcY := srcY / 2
			chromaMVX := mvx / 2
			chromaMVY := mvy / 2
			chromaBS := bs / 2
			chromaW := w / 2
			chromaH := h / 2
			warpBlockY(
				warpB[ySize:ySize+cbSize],
				f1.YUV[ySize:ySize+cbSize],
				chromaW, chromaW,
				chromaSrcX, chromaSrcY,
				chromaMVX, chromaMVY,
				chromaBS, chromaW, chromaH,
			)

			// Warp Cr plane
			warpBlockY(
				warpB[ySize+cbSize:],
				f1.YUV[ySize+cbSize:],
				chromaW, chromaW,
				chromaSrcX, chromaSrcY,
				chromaMVX, chromaMVY,
				chromaBS, chromaW, chromaH,
			)
		}
	}

	// Step 4: Bidirectional blend with occlusion-aware fallback.
	// Process each block according to its occlusion flag.
	totalSize := ySize + 2*cbSize

	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			occ := mvf.occlusion[idx]

			// Blend Y-plane block
			blendBlockWithOcclusion(
				dst, warpA, warpB, f0.YUV, f1.YUV,
				col*bs, row*bs, bs, w, h, occ, alpha,
			)

			// Blend Cb-plane block
			chromaBS := bs / 2
			chromaW := w / 2
			chromaH := h / 2
			blendBlockWithOcclusion(
				dst[ySize:ySize+cbSize],
				warpA[ySize:ySize+cbSize],
				warpB[ySize:ySize+cbSize],
				f0.YUV[ySize:ySize+cbSize],
				f1.YUV[ySize:ySize+cbSize],
				col*chromaBS, row*chromaBS,
				chromaBS, chromaW, chromaH, occ, alpha,
			)

			// Blend Cr-plane block
			blendBlockWithOcclusion(
				dst[ySize+cbSize:totalSize],
				warpA[ySize+cbSize:totalSize],
				warpB[ySize+cbSize:totalSize],
				f0.YUV[ySize+cbSize:totalSize],
				f1.YUV[ySize+cbSize:totalSize],
				col*chromaBS, row*chromaBS,
				chromaBS, chromaW, chromaH, occ, alpha,
			)
		}
	}
}

// warpBlockY copies a blockSize x blockSize region from src to dst with motion vector offset.
// Clips to frame boundaries. srcX/srcY is the source block top-left, mvx/mvy is the displacement.
// dstStride and srcStride are the row strides for the destination and source planes respectively.
func warpBlockY(dst, src []byte, dstStride, srcStride, srcX, srcY, mvx, mvy, blockSize, width, height int) {
	dstX := srcX + mvx
	dstY := srcY + mvy

	// Compute the overlap region after clipping to both source and destination bounds.
	// Source region: [srcX, srcX+blockSize) x [srcY, srcY+blockSize)
	// Destination region: [dstX, dstX+blockSize) x [dstY, dstY+blockSize)
	// Both must be within [0, width) x [0, height).

	// Clipping offsets — how many pixels to skip from the start of the block
	startOffX := 0
	startOffY := 0
	if dstX < 0 {
		startOffX = -dstX
	}
	if dstY < 0 {
		startOffY = -dstY
	}
	if srcX+startOffX < 0 {
		if -srcX > startOffX {
			startOffX = -srcX
		}
	}
	if srcY+startOffY < 0 {
		if -srcY > startOffY {
			startOffY = -srcY
		}
	}

	// Effective copy width and height after clipping
	copyW := blockSize - startOffX
	copyH := blockSize - startOffY

	// Clip right/bottom edges against destination bounds
	if dstX+startOffX+copyW > width {
		copyW = width - (dstX + startOffX)
	}
	if dstY+startOffY+copyH > height {
		copyH = height - (dstY + startOffY)
	}

	// Clip right/bottom edges against source bounds
	if srcX+startOffX+copyW > width {
		copyW = width - (srcX + startOffX)
	}
	if srcY+startOffY+copyH > height {
		copyH = height - (srcY + startOffY)
	}

	if copyW <= 0 || copyH <= 0 {
		return
	}

	// Copy the clipped block row by row
	for dy := 0; dy < copyH; dy++ {
		dRow := (dstY + startOffY + dy) * dstStride
		sRow := (srcY + startOffY + dy) * srcStride
		dOff := dRow + dstX + startOffX
		sOff := sRow + srcX + startOffX
		copy(dst[dOff:dOff+copyW], src[sOff:sOff+copyW])
	}
}

// blendBlockWithOcclusion blends a single block from warpA and warpB into dst,
// selecting the blend strategy based on the occlusion flag.
//
// Occlusion values:
//
//	0 = valid: 50/50 blend of warpA and warpB
//	1 = forward-occluded: use warpB only
//	2 = backward-occluded: use warpA only
//	3 = both occluded: use nearest source frame (alpha < 0.5 -> f0, else f1)
func blendBlockWithOcclusion(dst, warpA, warpB, f0, f1 []byte, bx, by, blockSize, width, height int, occlusion byte, alpha float64) {
	// Clip block to plane boundaries
	copyW := blockSize
	copyH := blockSize
	if bx+copyW > width {
		copyW = width - bx
	}
	if by+copyH > height {
		copyH = height - by
	}
	if copyW <= 0 || copyH <= 0 {
		return
	}

	switch occlusion {
	case 0:
		// Valid: blend warpA and warpB at 50/50 (pos=128 out of 256)
		for dy := 0; dy < copyH; dy++ {
			rowOff := (by+dy)*width + bx
			transition.BlendUniformBytes(
				dst[rowOff:rowOff+copyW],
				warpA[rowOff:rowOff+copyW],
				warpB[rowOff:rowOff+copyW],
				128,
			)
		}

	case 1:
		// Forward-occluded: use warpB only
		for dy := 0; dy < copyH; dy++ {
			rowOff := (by+dy)*width + bx
			copy(dst[rowOff:rowOff+copyW], warpB[rowOff:rowOff+copyW])
		}

	case 2:
		// Backward-occluded: use warpA only
		for dy := 0; dy < copyH; dy++ {
			rowOff := (by+dy)*width + bx
			copy(dst[rowOff:rowOff+copyW], warpA[rowOff:rowOff+copyW])
		}

	case 3:
		// Both occluded: use nearest source frame
		src := f0
		if alpha >= 0.5 {
			src = f1
		}
		for dy := 0; dy < copyH; dy++ {
			rowOff := (by+dy)*width + bx
			copy(dst[rowOff:rowOff+copyW], src[rowOff:rowOff+copyW])
		}
	}
}
