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
	warpA       []byte
	warpB       []byte
	blendOut    []byte
	fallbackBuf []byte
}

// NewMCFIState creates a new MCFI interpolation state.
func NewMCFIState() *MCFIState {
	return &MCFIState{}
}

// Interpolate produces a motion-compensated interpolated frame between frameA
// and frameB at position alpha (0.0=frameA, 1.0=frameB). Both frames must be
// YUV420 with dimensions width × height.
//
// Motion estimation runs once per unique frame pair (~5-15ms for 1080p) and
// is cached. Subsequent calls with different alpha values only perform the
// fast warp+blend step (~2-3ms). Falls back to linear blend on scene change.
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

		// Ensure buffers are allocated
		s.ensureBuffers(frameSize)

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

			// Run full ME pipeline: diamond search → median filter → consistency check
			estimateMotionField(pfA, pfB, s.mvf, frcMESearchRange)
			medianFilterMVField(s.mvf)
			checkConsistency(s.mvf, 4)
			s.mvValid = true
		}
	}

	// Motion-compensated interpolation (cached MVs + fast warp)
	if s.mvValid && !s.sceneChange {
		pfA := &ProcessingFrame{YUV: frameA, Width: width, Height: height}
		pfB := &ProcessingFrame{YUV: frameB, Width: width, Height: height}
		mcfiInterpolate(s.blendOut, s.warpA, s.warpB, pfA, pfB, s.mvf, alpha)
		return s.blendOut[:frameSize]
	}

	// Fallback: linear blend (scene change or invalid MVs)
	s.ensureBuffers(frameSize)
	invAlpha := 1.0 - alpha
	for i := 0; i < frameSize && i < len(frameA) && i < len(frameB); i++ {
		s.fallbackBuf[i] = byte(float64(frameA[i])*invAlpha + float64(frameB[i])*alpha + 0.5)
	}
	return s.fallbackBuf[:frameSize]
}

// ensureBuffers allocates scratch buffers if needed.
func (s *MCFIState) ensureBuffers(frameSize int) {
	if len(s.warpA) < frameSize {
		s.warpA = make([]byte, frameSize)
	}
	if len(s.warpB) < frameSize {
		s.warpB = make([]byte, frameSize)
	}
	if len(s.blendOut) < frameSize {
		s.blendOut = make([]byte, frameSize)
	}
	if len(s.fallbackBuf) < frameSize {
		s.fallbackBuf = make([]byte, frameSize)
	}
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
