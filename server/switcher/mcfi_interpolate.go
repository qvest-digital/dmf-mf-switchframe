package switcher

import (
	"unsafe"

	"github.com/zsiec/switchframe/server/switcher/frcasm"
)

// MCFIState holds reusable state for standalone motion-compensated frame
// interpolation. It satisfies the replay.FrameInterpolator interface via
// Go's structural typing (same Interpolate method signature).
//
// Motion vectors are computed once per unique frame pair and cached for
// subsequent calls with different alpha values. This is critical for
// slow-motion replay where dupCount calls share the same source frame pair
// but need different interpolation positions.
type MCFIState struct {
	// Motion vector field (reused across calls)
	mvf     *motionVectorField
	mvValid bool

	// Frame pair identity for MV caching (pointer comparison)
	lastPtrA unsafe.Pointer
	lastPtrB unsafe.Pointer

	// Scene change flag for current frame pair
	sceneChange bool

	// Reusable scratch buffers
	blendOut    []byte
	fallbackBuf []byte

	// Hierarchical ME state (reused across frame pairs)
	hme *hierarchicalME
}

// NewMCFIState creates a new MCFI interpolation state.
func NewMCFIState() *MCFIState {
	return &MCFIState{}
}

// Interpolate produces a motion-compensated interpolated frame between frameA
// and frameB at position alpha (0.0=frameA, 1.0=frameB). Both frames must be
// YUV420 with dimensions width × height.
//
// Uses per-pixel bilinear MV interpolation to eliminate block boundary
// artifacts. Motion vectors are still estimated at 16×16 block granularity
// (using SIMD-accelerated diamond search), but the warp smoothly interpolates
// MVs across block boundaries and samples source pixels with bilinear
// interpolation for sub-pixel accuracy.
//
// Motion estimation runs once per unique frame pair (~5-15ms for 1080p) and
// is cached. Subsequent calls with different alpha values only perform the
// smooth warp (~8-18ms). Falls back to linear blend on scene change.
//
// The returned slice references internal state and is valid until the next
// call to Interpolate.
func (s *MCFIState) Interpolate(frameA, frameB []byte, width, height int, alpha float64) []byte {
	frameSize := width * height * 3 / 2

	// Near-threshold: skip ME/warp for extreme alpha values
	if alpha < frcNearThresholdLow {
		return frameA
	}
	if alpha > frcNearThresholdHigh {
		return frameB
	}

	// Check if this is the same frame pair (pointer identity)
	ptrA := unsafe.Pointer(&frameA[0])
	ptrB := unsafe.Pointer(&frameB[0])
	newPair := ptrA != s.lastPtrA || ptrB != s.lastPtrB

	if newPair {
		s.lastPtrA = ptrA
		s.lastPtrB = ptrB
		s.mvValid = false

		// Ensure output buffers are allocated
		if len(s.blendOut) < frameSize {
			s.blendOut = make([]byte, frameSize)
		}
		if len(s.fallbackBuf) < frameSize {
			s.fallbackBuf = make([]byte, frameSize)
		}

		// Scene change detection via subsampled SAD
		s.sceneChange = detectSceneChangeSAD(frameA, frameB, width, height)

		if !s.sceneChange {
			// Wrap raw YUV in ProcessingFrame for the ME pipeline
			pfA := &ProcessingFrame{YUV: frameA, Width: width, Height: height}
			pfB := &ProcessingFrame{YUV: frameB, Width: width, Height: height}

			// Allocate/resize motion vector field
			cols := width / frcMEBlockSize
			rows := height / frcMEBlockSize
			if s.mvf == nil || s.mvf.cols != cols || s.mvf.rows != rows {
				s.mvf = newMotionVectorField(width, height, frcMEBlockSize)
			}

			// Run full ME pipeline: hierarchical diamond search → median filter → consistency check
			if s.hme == nil {
				s.hme = newHierarchicalME()
			}
			s.hme.estimate(pfA, pfB, s.mvf, frcMESearchRange)
			medianFilterMVField(s.mvf)
			checkConsistency(s.mvf, 4)
			s.mvValid = true
		}
	}

	// Motion-compensated interpolation with per-pixel warping (fixed-point, row-parallel)
	if s.mvValid && !s.sceneChange {
		mcfiInterpolateFast(s.blendOut, frameA, frameB, width, height, s.mvf, alpha)
		return s.blendOut[:frameSize]
	}

	// Fallback: linear blend (scene change or invalid MVs)
	if len(s.blendOut) < frameSize {
		s.blendOut = make([]byte, frameSize)
	}
	if len(s.fallbackBuf) < frameSize {
		s.fallbackBuf = make([]byte, frameSize)
	}
	invAlpha := 1.0 - alpha
	for i := 0; i < frameSize && i < len(frameA) && i < len(frameB); i++ {
		s.fallbackBuf[i] = byte(float64(frameA[i])*invAlpha + float64(frameB[i])*alpha + 0.5)
	}
	return s.fallbackBuf[:frameSize]
}

// mcfiInterpolateSmooth produces an interpolated frame using per-pixel
// bilinear MV interpolation. For each output pixel, the motion vector is
// smoothly interpolated from the 4 nearest block centers, then both source
// frames are sampled at the displaced position with bilinear pixel
// interpolation. The two warped samples are blended using per-block
// occlusion flags.
//
// This eliminates the visible 16×16 block boundary artifacts of the
// standard block-copy warp while using the same block-based ME results.
func mcfiInterpolateSmooth(dst, srcA, srcB []byte, width, height int, mvf *motionVectorField, alpha float64) {
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crOff := ySize + cbSize

	// Y plane
	smoothWarpBlendPlane(
		dst[:ySize], srcA[:ySize], srcB[:ySize],
		width, height, mvf, alpha, 1,
	)

	// Cb plane
	smoothWarpBlendPlane(
		dst[ySize:ySize+cbSize],
		srcA[ySize:ySize+cbSize],
		srcB[ySize:ySize+cbSize],
		width/2, height/2, mvf, alpha, 2,
	)

	// Cr plane
	smoothWarpBlendPlane(
		dst[crOff:crOff+cbSize],
		srcA[crOff:crOff+cbSize],
		srcB[crOff:crOff+cbSize],
		width/2, height/2, mvf, alpha, 2,
	)
}

// smoothWarpBlendPlane warps and blends a single YUV plane using per-pixel
// bilinear MV interpolation with occlusion-aware blending.
//
// chromaScale is 1 for luma (pixel coords = luma coords) or 2 for 4:2:0
// chroma (pixel coords map to 2x luma coords, MV displacement halved).
func smoothWarpBlendPlane(
	dst, srcA, srcB []byte,
	planeW, planeH int,
	mvf *motionVectorField,
	alpha float64,
	chromaScale int,
) {
	bs := mvf.blockSize
	invAlpha := 1.0 - alpha
	subPel := mvf.subPel
	if subPel < 1 {
		subPel = 1
	}
	mvScale := 1.0 / float64(chromaScale*subPel)
	halfBS := float64(bs) * 0.5

	for py := 0; py < planeH; py++ {
		// Map to luma coordinates for MV lookup
		lumaY := float64(py * chromaScale)

		// Pre-compute vertical block interpolation factors
		by := (lumaY - halfBS) / float64(bs)
		by0 := int(by)
		if by0 < 0 {
			by0 = 0
		}
		by1 := by0 + 1
		if by1 >= mvf.rows {
			by1 = mvf.rows - 1
		}
		fy := by - float64(by0)
		if fy < 0 {
			fy = 0
		}
		if fy > 1 {
			fy = 1
		}
		invFy := 1.0 - fy

		rowOff := py * planeW

		for px := 0; px < planeW; px++ {
			lumaX := float64(px * chromaScale)

			// Horizontal block interpolation factors
			bx := (lumaX - halfBS) / float64(bs)
			bx0 := int(bx)
			if bx0 < 0 {
				bx0 = 0
			}
			bx1 := bx0 + 1
			if bx1 >= mvf.cols {
				bx1 = mvf.cols - 1
			}
			fx := bx - float64(bx0)
			if fx < 0 {
				fx = 0
			}
			if fx > 1 {
				fx = 1
			}
			invFx := 1.0 - fx

			// Bilinear MV interpolation from 4 nearest block centers
			i00 := by0*mvf.cols + bx0
			i10 := by0*mvf.cols + bx1
			i01 := by1*mvf.cols + bx0
			i11 := by1*mvf.cols + bx1

			// Forward MV (A → B direction)
			fmvx := (float64(mvf.fwdX[i00])*invFx+float64(mvf.fwdX[i10])*fx)*invFy +
				(float64(mvf.fwdX[i01])*invFx+float64(mvf.fwdX[i11])*fx)*fy
			fmvy := (float64(mvf.fwdY[i00])*invFx+float64(mvf.fwdY[i10])*fx)*invFy +
				(float64(mvf.fwdY[i01])*invFx+float64(mvf.fwdY[i11])*fx)*fy

			// Backward MV (B → A direction)
			bmvx := (float64(mvf.bwdX[i00])*invFx+float64(mvf.bwdX[i10])*fx)*invFy +
				(float64(mvf.bwdX[i01])*invFx+float64(mvf.bwdX[i11])*fx)*fy
			bmvy := (float64(mvf.bwdY[i00])*invFx+float64(mvf.bwdY[i10])*fx)*invFy +
				(float64(mvf.bwdY[i01])*invFx+float64(mvf.bwdY[i11])*fx)*fy

			// Sample from each source at displaced position
			sA := bilinearSample(srcA, planeW, planeH,
				float64(px)+fmvx*alpha*mvScale,
				float64(py)+fmvy*alpha*mvScale)
			sB := bilinearSample(srcB, planeW, planeH,
				float64(px)+bmvx*invAlpha*mvScale,
				float64(py)+bmvy*invAlpha*mvScale)

			// Smooth alpha-weighted blend of both warps.
			// Avoids per-block occlusion switching which causes hard
			// boundaries when adjacent blocks have different flags.
			idx := rowOff + px
			dst[idx] = byte(float64(sA)*invAlpha + float64(sB)*alpha + 0.5)
		}
	}
}

// bilinearSample samples a plane at sub-pixel position (x, y) using
// bilinear interpolation. Coordinates are clamped to plane boundaries.
func bilinearSample(src []byte, width, height int, x, y float64) byte {
	// Clamp to valid range
	if x < 0 {
		x = 0
	}
	maxX := float64(width - 1)
	if x > maxX {
		x = maxX
	}
	if y < 0 {
		y = 0
	}
	maxY := float64(height - 1)
	if y > maxY {
		y = maxY
	}

	x0 := int(x)
	y0 := int(y)
	x1 := x0 + 1
	y1 := y0 + 1
	if x1 >= width {
		x1 = width - 1
	}
	if y1 >= height {
		y1 = height - 1
	}

	fx := x - float64(x0)
	fy := y - float64(y0)

	// Four corner samples
	off00 := y0*width + x0
	off10 := y0*width + x1
	off01 := y1*width + x0
	off11 := y1*width + x1

	v := float64(src[off00])*(1-fx)*(1-fy) +
		float64(src[off10])*fx*(1-fy) +
		float64(src[off01])*(1-fx)*fy +
		float64(src[off11])*fx*fy

	return byte(v + 0.5)
}

// detectSceneChangeSAD computes subsampled SAD between Y planes and compares
// against a fixed threshold. Returns true if frames differ significantly
// (e.g., at a cut boundary within the replay clip).
func detectSceneChangeSAD(frameA, frameB []byte, width, height int) bool {
	ySize := width * height
	if len(frameA) < ySize || len(frameB) < ySize {
		return true
	}

	var totalSAD uint64
	var rowCount int
	for row := 0; row < height; row += 4 {
		offset := row * width
		totalSAD += frcasm.SadRow(&frameA[offset], &frameB[offset], width)
		rowCount++
	}
	if rowCount == 0 {
		return true
	}

	avgSAD := totalSAD / uint64(rowCount*width)
	// Fixed threshold: per-pixel SAD > 25 indicates scene change.
	return avgSAD > 25
}
