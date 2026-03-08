package switcher

import (
	"sort"
	"time"

	"github.com/zsiec/switchframe/server/switcher/frcasm"
	"github.com/zsiec/switchframe/server/transition"
)

// FRCQuality controls the frame rate conversion interpolation method.
type FRCQuality int

const (
	// FRCNone uses frame duplication (current behavior).
	FRCNone FRCQuality = iota
	// FRCNearest selects the nearest source frame by PTS distance.
	FRCNearest
	// FRCBlend linearly blends between source frames (may ghost on motion).
	FRCBlend
	// FRCMCFI uses motion-compensated frame interpolation.
	FRCMCFI
)

// String returns the string representation of an FRCQuality value.
func (q FRCQuality) String() string {
	switch q {
	case FRCNone:
		return "none"
	case FRCNearest:
		return "nearest"
	case FRCBlend:
		return "blend"
	case FRCMCFI:
		return "mcfi"
	default:
		return "none"
	}
}

// ParseFRCQuality converts a string to FRCQuality. Returns FRCNone for unknown values.
func ParseFRCQuality(s string) FRCQuality {
	switch s {
	case "none":
		return FRCNone
	case "nearest":
		return FRCNearest
	case "blend":
		return FRCBlend
	case "mcfi":
		return FRCMCFI
	default:
		return FRCNone
	}
}

// frcMEBlockSize is the block size for motion estimation (matches frc_me.go).
const frcMEBlockSize = 16

// frcMESearchRange is the search range for diamond search motion estimation.
const frcMESearchRange = 32

// frcDegradeThresholdRatio is the fraction of avg source frame interval
// above which ME is considered too slow and quality degrades.
const frcDegradeThresholdRatio = 0.5

// frcDegradeRetryInterval is how often we re-attempt MCFI after degrading.
const frcDegradeRetryInterval = 5 * time.Second

// frcNearThresholdLow is the alpha below which we return prevFrame directly.
const frcNearThresholdLow = 0.05

// frcNearThresholdHigh is the alpha above which we return currFrame directly.
const frcNearThresholdHigh = 0.95

// frcSADHistorySize is the number of SAD values kept for adaptive threshold.
const frcSADHistorySize = 16

// frcMinSceneThreshold is the minimum SAD threshold for scene change detection.
const frcMinSceneThreshold uint64 = 20

// frcSource holds per-source FRC state for frame rate conversion.
// Accessed only under syncSource.mu — no separate locking needed.
type frcSource struct {
	requestedQuality FRCQuality
	effectiveQuality FRCQuality // may degrade under load

	// Frame buffer: previous and current source frames
	prevFrame *ProcessingFrame
	currFrame *ProcessingFrame
	prevPTS   int64 // original source PTS (90kHz)
	currPTS   int64
	hasTwo    bool // true when both prev and curr are populated

	// Cached motion vector field (computed once per source frame pair)
	mvField *motionVectorField
	mvValid bool

	// Scene change state
	sceneChange     bool
	sadHistory      [frcSADHistorySize]uint64
	sadHistoryIdx   int
	sadHistoryCount int

	// Adaptive quality: degrade if ME exceeds budget
	meLastNs      int64     // duration of last ME in nanoseconds
	degradedSince time.Time // when we last degraded

	// Tick tracking for emit alpha computation.
	// ticksSinceLastFresh counts ticks since the last fresh source frame was popped.
	// Used to compute interpolation position relative to source frame cadence.
	ticksSinceLastFresh int
	tickIntervalPTS     int64 // pipeline tick interval in 90kHz PTS units

	// Reusable buffers (zero-alloc steady state)
	warpA    []byte // forward-warped scratch
	warpB    []byte // backward-warped scratch
	blendOut []byte // final blended output

	// FPS tracking from PTS deltas
	avgIntervalTicks int64 // EMA of source PTS interval (90kHz units)
	intervalCount    int
}

// newFRCSource creates an frcSource with the given quality level.
func newFRCSource(quality FRCQuality, tickIntervalPTS int64) *frcSource {
	return &frcSource{
		requestedQuality: quality,
		effectiveQuality: quality,
		tickIntervalPTS:  tickIntervalPTS,
	}
}

// ingest adds a new decoded source frame. When 2+ frames are buffered and
// quality is FRCMCFI, runs motion estimation (cached until next frame pair).
// Detects scene changes via subsampled SAD.
func (fs *frcSource) ingest(pf *ProcessingFrame) {
	// Shift frames: prev = curr, curr = new
	fs.prevFrame = fs.currFrame
	fs.prevPTS = fs.currPTS
	fs.currFrame = pf
	fs.currPTS = pf.PTS

	if fs.prevFrame != nil {
		fs.hasTwo = true
	}

	if !fs.hasTwo {
		return
	}

	// Update FPS tracking: EMA of PTS intervals
	interval := fs.currPTS - fs.prevPTS
	if interval > 0 {
		if fs.intervalCount == 0 {
			fs.avgIntervalTicks = interval
		} else {
			// EMA with alpha=0.2
			fs.avgIntervalTicks = (fs.avgIntervalTicks*4 + interval) / 5
		}
		fs.intervalCount++
	}

	// Invalidate cached MV field
	fs.mvValid = false

	if fs.effectiveQuality < FRCMCFI {
		return
	}

	// Detect scene change via subsampled SAD
	fs.sceneChange = fs.detectSceneChange(fs.prevFrame, fs.currFrame)

	if fs.sceneChange {
		return
	}

	// Run motion estimation
	fs.ensureMVField(fs.prevFrame.Width, fs.prevFrame.Height)

	meStart := time.Now()
	estimateMotionField(fs.prevFrame, fs.currFrame, fs.mvField, frcMESearchRange)
	medianFilterMVField(fs.mvField)
	checkConsistency(fs.mvField, 4)
	meElapsed := time.Since(meStart)
	fs.meLastNs = meElapsed.Nanoseconds()
	fs.mvValid = true

	// Adaptive degradation: if ME takes > 50% of avg source frame interval, degrade
	if fs.avgIntervalTicks > 0 {
		// Convert avg interval from 90kHz ticks to nanoseconds
		avgIntervalNs := fs.avgIntervalTicks * int64(time.Second) / mpegtsClock
		if fs.meLastNs > int64(float64(avgIntervalNs)*frcDegradeThresholdRatio) {
			fs.effectiveQuality = FRCBlend
			fs.degradedSince = time.Now()
			fs.mvValid = false
		}
	}

	// Periodically re-attempt MCFI if degraded
	if fs.effectiveQuality < fs.requestedQuality &&
		!fs.degradedSince.IsZero() &&
		time.Since(fs.degradedSince) >= frcDegradeRetryInterval {
		fs.effectiveQuality = fs.requestedQuality
		fs.degradedSince = time.Time{}
	}
}

// canInterpolate returns true if the FRC has enough state to produce an
// interpolated frame (i.e., 2 frames are buffered).
func (fs *frcSource) canInterpolate() bool {
	return fs.hasTwo
}

// emit produces an output frame for the given tick PTS by interpolating
// between the two buffered source frames. The interpolation method depends
// on effectiveQuality.
func (fs *frcSource) emit(tickPTS int64) *ProcessingFrame {
	if !fs.hasTwo {
		return nil
	}

	// Compute alpha from PTS. The tickPTS must be on the source PTS timeline
	// (not the synthetic tick counter). The frame sync passes
	// lastReleasedPTS + tickIntervalPTS for interpolation ticks.
	ptsDelta := fs.currPTS - fs.prevPTS
	if ptsDelta <= 0 {
		return fs.currFrame
	}

	alpha := float64(tickPTS-fs.prevPTS) / float64(ptsDelta)

	// Clamp alpha to [0, 1]
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}

	// Near-threshold: return source frame directly (no blend)
	if alpha < frcNearThresholdLow {
		return fs.prevFrame
	}
	if alpha > frcNearThresholdHigh {
		return fs.currFrame
	}

	switch fs.effectiveQuality {
	case FRCNearest:
		return fs.emitNearest(alpha)
	case FRCBlend:
		return fs.emitBlend(alpha, tickPTS)
	case FRCMCFI:
		return fs.emitMCFI(alpha, tickPTS)
	default:
		// FRCNone: should not reach here (canInterpolate is only used
		// when FRC is enabled), but return nearest as fallback
		return fs.emitNearest(alpha)
	}
}

// emitNearest returns the nearest frame by alpha distance.
func (fs *frcSource) emitNearest(alpha float64) *ProcessingFrame {
	if alpha < 0.5 {
		return fs.prevFrame
	}
	return fs.currFrame
}

// emitBlend produces a linear blend of prevFrame and currFrame.
func (fs *frcSource) emitBlend(alpha float64, tickPTS int64) *ProcessingFrame {
	w := fs.prevFrame.Width
	h := fs.prevFrame.Height
	totalSize := w * h * 3 / 2

	fs.ensureBlendOut(totalSize)

	// Convert alpha to 0-256 range for BlendUniformBytes
	pos := int(alpha * 256)
	if pos < 0 {
		pos = 0
	}
	if pos > 256 {
		pos = 256
	}

	transition.BlendUniformBytes(fs.blendOut, fs.prevFrame.YUV[:totalSize], fs.currFrame.YUV[:totalSize], pos)

	return &ProcessingFrame{
		YUV:        fs.blendOut,
		Width:      w,
		Height:     h,
		PTS:        tickPTS,
		IsKeyframe: false,
		Codec:      fs.currFrame.Codec,
	}
}

// emitMCFI produces a motion-compensated interpolated frame.
// Falls back to nearest if MVs are invalid or scene change detected.
func (fs *frcSource) emitMCFI(alpha float64, tickPTS int64) *ProcessingFrame {
	if !fs.mvValid || fs.sceneChange {
		return fs.emitNearest(alpha)
	}

	w := fs.prevFrame.Width
	h := fs.prevFrame.Height
	totalSize := w * h * 3 / 2

	fs.ensureBlendOut(totalSize)
	fs.ensureWarpBuffers(totalSize)

	mcfiInterpolate(fs.blendOut, fs.warpA, fs.warpB, fs.prevFrame, fs.currFrame, fs.mvField, alpha)

	return &ProcessingFrame{
		YUV:        fs.blendOut,
		Width:      w,
		Height:     h,
		PTS:        tickPTS,
		IsKeyframe: false,
		Codec:      fs.currFrame.Codec,
	}
}

// reset clears all state (called when source is removed).
func (fs *frcSource) reset() {
	fs.prevFrame = nil
	fs.currFrame = nil
	fs.prevPTS = 0
	fs.currPTS = 0
	fs.hasTwo = false
	fs.mvField = nil
	fs.mvValid = false
	fs.sceneChange = false
	fs.sadHistoryIdx = 0
	fs.sadHistoryCount = 0
	fs.meLastNs = 0
	fs.degradedSince = time.Time{}
	fs.intervalCount = 0
	fs.avgIntervalTicks = 0
	// Keep warpA/warpB/blendOut buffers for reuse
}

// detectSceneChange computes a subsampled SAD between two frames and
// compares against an adaptive threshold derived from recent SAD history.
func (fs *frcSource) detectSceneChange(prev, curr *ProcessingFrame) bool {
	width := prev.Width
	height := prev.Height
	yA := prev.YUV[:width*height]
	yB := curr.YUV[:width*height]

	var totalSAD uint64
	rows := 0
	for row := 0; row < height; row += 4 {
		totalSAD += frcasm.SadRow(&yA[row*width], &yB[row*width], width)
		rows++
	}
	if rows == 0 {
		return false
	}
	avgSAD := totalSAD / uint64(rows*width)

	// Update history
	fs.sadHistory[fs.sadHistoryIdx] = avgSAD
	fs.sadHistoryIdx = (fs.sadHistoryIdx + 1) % frcSADHistorySize
	if fs.sadHistoryCount < frcSADHistorySize {
		fs.sadHistoryCount++
	}

	median := fs.medianSAD()
	threshold := median * 5
	if threshold < frcMinSceneThreshold {
		threshold = frcMinSceneThreshold
	}

	return avgSAD > threshold
}

// medianSAD computes the median of the SAD history buffer.
func (fs *frcSource) medianSAD() uint64 {
	if fs.sadHistoryCount == 0 {
		return 0
	}

	// Copy valid entries and sort
	vals := make([]uint64, fs.sadHistoryCount)
	if fs.sadHistoryCount == frcSADHistorySize {
		copy(vals, fs.sadHistory[:])
	} else {
		// History hasn't wrapped yet; entries are at indices 0..sadHistoryCount-1
		copy(vals, fs.sadHistory[:fs.sadHistoryCount])
	}

	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals[len(vals)/2]
}

// ensureMVField allocates or reuses the motion vector field for the given dimensions.
func (fs *frcSource) ensureMVField(width, height int) {
	if fs.mvField != nil &&
		fs.mvField.cols == width/frcMEBlockSize &&
		fs.mvField.rows == height/frcMEBlockSize {
		return
	}
	fs.mvField = newMotionVectorField(width, height, frcMEBlockSize)
}

// ensureBlendOut allocates or reuses the blend output buffer.
func (fs *frcSource) ensureBlendOut(size int) {
	if len(fs.blendOut) >= size {
		fs.blendOut = fs.blendOut[:size]
		return
	}
	fs.blendOut = make([]byte, size)
}

// ensureWarpBuffers allocates or reuses the warp scratch buffers.
func (fs *frcSource) ensureWarpBuffers(size int) {
	if len(fs.warpA) < size {
		fs.warpA = make([]byte, size)
	} else {
		fs.warpA = fs.warpA[:size]
	}
	if len(fs.warpB) < size {
		fs.warpB = make([]byte, size)
	} else {
		fs.warpB = fs.warpB[:size]
	}
}
