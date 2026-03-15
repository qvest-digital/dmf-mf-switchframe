package switcher

import (
	"slices"
	"time"

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

// ptsDelta computes the forward distance from older to newer in 33-bit PTS
// space, handling the wraparound at 2^33 (~26.5 hours at 90 kHz).
// The result is always non-negative (unsigned modular distance).
func ptsDelta(newer, older int64) int64 {
	return (newer - older) & ptsMask33
}

// ptsSignedDelta computes the signed distance from ref to pts in 33-bit PTS
// space. Returns a negative value when pts is "before" ref (within half the
// 33-bit range), positive when "after". Used for alpha computation where
// the tick may be slightly before or after the source frame window.
func ptsSignedDelta(pts, ref int64) int64 {
	d := (pts - ref) & ptsMask33
	if d >= (1 << 32) {
		// Upper half of 33-bit range: interpret as negative
		return d - (int64(1) << 33)
	}
	return d
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

	// Pool reference for emitted ProcessingFrame structs, so DeepCopy
	// can allocate from the pool instead of falling back to heap.
	pool *FramePool

	// Reusable buffers (zero-alloc steady state)
	blendOut   []byte          // final blended output
	nearestOut []byte          // reusable buffer for emitNearest (avoids aliasing live frames)
	sortBuf    []uint64        // reusable buffer for medianSAD sort
	hme        *hierarchicalME // pyramid ME state (reused across frames)

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
	// Release FRC's reference to the old prevFrame.
	if fs.prevFrame != nil {
		fs.prevFrame.ReleaseYUV()
	}

	// Shift frames: prev = curr, curr = new
	fs.prevFrame = fs.currFrame
	fs.prevPTS = fs.currPTS
	fs.currFrame = pf
	fs.currPTS = pf.PTS

	// Take an additional reference for FRC ownership. The frame sync's
	// ring buffer and lastRawVideo also hold references to this frame.
	// Both owners release independently via refcount.
	pf.Ref()

	if fs.prevFrame != nil {
		fs.hasTwo = true
	}

	if !fs.hasTwo {
		return
	}

	// Update FPS tracking: EMA of PTS intervals (wrap-aware)
	interval := ptsDelta(fs.currPTS, fs.prevPTS)
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

	// Detect scene change via subsampled SAD. This is cheap (one SAD pass)
	// and must run at all quality levels — the emit path checks sceneChange
	// to decide fallback behavior even when ME is degraded.
	fs.sceneChange = fs.detectSceneChange(fs.prevFrame, fs.currFrame)

	if fs.effectiveQuality < FRCMCFI || fs.sceneChange {
		return
	}

	// Run motion estimation
	fs.ensureMVField(fs.prevFrame.Width, fs.prevFrame.Height)

	meStart := time.Now()
	if fs.hme == nil {
		fs.hme = newHierarchicalME()
	}
	fs.hme.estimate(fs.prevFrame, fs.currFrame, fs.mvField, frcMESearchRange)
	medianFilterMVField(fs.mvField)
	checkConsistency(fs.mvField, 4)
	computeReliability(fs.mvField)
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
	// Both deltas are wrap-aware for the 33-bit PTS boundary.
	frameDelta := ptsDelta(fs.currPTS, fs.prevPTS)
	if frameDelta <= 0 {
		return fs.emitNearest(1.0) // select currFrame via safe copy
	}

	alpha := float64(ptsSignedDelta(tickPTS, fs.prevPTS)) / float64(frameDelta)

	// Clamp alpha to [0, 1]
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}

	// Near-threshold: use emitNearest to get a safe copy
	if alpha < frcNearThresholdLow {
		return fs.emitNearest(0.0) // select prevFrame
	}
	if alpha > frcNearThresholdHigh {
		return fs.emitNearest(1.0) // select currFrame
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

// emitNearest returns a deep copy of the nearest frame by alpha distance.
// Copies into a reusable buffer to avoid aliasing live frames that ingest()
// may release on the next call.
func (fs *frcSource) emitNearest(alpha float64) *ProcessingFrame {
	var src *ProcessingFrame
	if alpha < 0.5 {
		src = fs.prevFrame
	} else {
		src = fs.currFrame
	}

	w, h := src.Width, src.Height
	totalSize := w * h * 3 / 2

	// Guard: the frame sync's releaseTick may have released this frame's
	// pool buffer (via lastRawVideo.ReleaseYUV) while the FRC still holds
	// a pointer to it. If YUV was released, we cannot emit.
	if len(src.YUV) < totalSize {
		return nil
	}

	fs.ensureNearestOut(totalSize)
	copy(fs.nearestOut, src.YUV[:totalSize])

	return &ProcessingFrame{
		YUV:        fs.nearestOut,
		Width:      w,
		Height:     h,
		PTS:        src.PTS,
		IsKeyframe: false,
		Codec:      src.Codec,
	}
}

// emitBlend produces a linear blend of prevFrame and currFrame.
func (fs *frcSource) emitBlend(alpha float64, tickPTS int64) *ProcessingFrame {
	w := fs.prevFrame.Width
	h := fs.prevFrame.Height
	totalSize := w * h * 3 / 2

	// Guard against released YUV buffers (see emitNearest comment).
	if len(fs.prevFrame.YUV) < totalSize || len(fs.currFrame.YUV) < totalSize {
		return nil
	}

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

	// Guard against released YUV buffers (see emitNearest comment).
	if len(fs.prevFrame.YUV) < totalSize || len(fs.currFrame.YUV) < totalSize {
		return nil
	}

	fs.ensureBlendOut(totalSize)

	mcfiInterpolateFast(fs.blendOut, fs.prevFrame.YUV, fs.currFrame.YUV, w, h, fs.mvField, alpha)

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
// Releases any held pool buffers before clearing references.
func (fs *frcSource) reset() {
	if fs.prevFrame != nil {
		fs.prevFrame.ReleaseYUV()
	}
	if fs.currFrame != nil {
		fs.currFrame.ReleaseYUV()
	}
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
	// Keep blendOut buffer for reuse
}

// detectSceneChange computes a subsampled SAD between two frames and
// compares against an adaptive threshold derived from recent SAD history.
// Both dimensions are subsampled by 4x to reduce compute.
func (fs *frcSource) detectSceneChange(prev, curr *ProcessingFrame) bool {
	width := prev.Width
	height := prev.Height
	yA := prev.YUV[:width*height]
	yB := curr.YUV[:width*height]

	var totalSAD uint64
	rows := 0
	pixels := 0
	for row := 0; row < height; row += 4 {
		off := row * width
		for col := 0; col < width; col += 4 {
			d := int(yA[off+col]) - int(yB[off+col])
			if d < 0 {
				d = -d
			}
			totalSAD += uint64(d)
			pixels++
		}
		rows++
	}
	if pixels == 0 {
		return false
	}
	avgSAD := totalSAD / uint64(pixels)

	median := fs.medianSAD()
	threshold := median * 5
	if threshold < frcMinSceneThreshold {
		threshold = frcMinSceneThreshold
	}

	isSceneChange := avgSAD > threshold

	// Only update history for non-scene-change frames to avoid
	// polluting the adaptive threshold with outlier SAD values.
	if !isSceneChange {
		fs.sadHistory[fs.sadHistoryIdx] = avgSAD
		fs.sadHistoryIdx = (fs.sadHistoryIdx + 1) % frcSADHistorySize
		if fs.sadHistoryCount < frcSADHistorySize {
			fs.sadHistoryCount++
		}
	}

	return isSceneChange
}

// medianSAD computes the median of the SAD history buffer.
// Uses a reusable sort buffer to avoid per-call allocation.
func (fs *frcSource) medianSAD() uint64 {
	if fs.sadHistoryCount == 0 {
		return 0
	}

	n := fs.sadHistoryCount
	if cap(fs.sortBuf) < n {
		fs.sortBuf = make([]uint64, n)
	}
	vals := fs.sortBuf[:n]
	copy(vals, fs.sadHistory[:n])

	slices.Sort(vals)
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

// ensureNearestOut allocates or reuses the nearest output buffer.
func (fs *frcSource) ensureNearestOut(size int) {
	if len(fs.nearestOut) >= size {
		fs.nearestOut = fs.nearestOut[:size]
		return
	}
	fs.nearestOut = make([]byte, size)
}
