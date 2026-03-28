package clock

import (
	"math"
	"time"
)

// StandardRate represents a standard broadcast frame rate.
type StandardRate struct {
	Num, Den int
	FPS      float64 // precomputed for comparison
}

// StandardRates lists all standard broadcast frame rates.
var StandardRates = []StandardRate{
	{24000, 1001, 23.976},
	{24, 1, 24.0},
	{25, 1, 25.0},
	{30000, 1001, 29.97},
	{30, 1, 30.0},
	{50, 1, 50.0},
	{60000, 1001, 59.94},
	{60, 1, 60.0},
}

const (
	windowSize     = 64
	minSamples     = 16
)

// ClockRecovery measures program source frame arrival rate and snaps to
// the nearest standard broadcast rate. Requires stability (N consecutive
// frames at new rate) before changing.
type ClockRecovery struct {
	window   [windowSize]time.Time
	writePos int
	count    int

	// Current recovered rate
	fpsNum, fpsDen int

	// Raw measured FPS (before snapping)
	measuredFPS float64

	// Stability tracking
	candidateNum, candidateDen int
	candidateFrames            int
	stableThreshold            int
}

// NewClockRecovery creates a ClockRecovery with the given initial rate and
// stability threshold. The stableThreshold is the number of consecutive
// measurements at a new rate before the recovered rate changes.
func NewClockRecovery(initialFPSNum, initialFPSDen, stableThreshold int) *ClockRecovery {
	return &ClockRecovery{
		fpsNum:          initialFPSNum,
		fpsDen:          initialFPSDen,
		stableThreshold: stableThreshold,
	}
}

// RecordArrival records a frame arrival. Returns true if rate changed.
func (cr *ClockRecovery) RecordArrival(now time.Time) bool {
	// Store in circular buffer
	cr.window[cr.writePos%windowSize] = now
	cr.writePos++
	if cr.count < windowSize {
		cr.count++
	}

	// Need minimum samples to measure
	if cr.count < minSamples {
		return false
	}

	// Compute average FPS over the window
	// Find oldest and newest entries
	n := cr.count
	newestIdx := (cr.writePos - 1) % windowSize
	oldestIdx := (cr.writePos - n) % windowSize
	if oldestIdx < 0 {
		oldestIdx += windowSize
	}

	newest := cr.window[newestIdx]
	oldest := cr.window[oldestIdx]

	span := newest.Sub(oldest)
	if span <= 0 {
		return false
	}

	cr.measuredFPS = float64(n-1) / span.Seconds()

	// Snap to nearest standard rate
	snapNum, snapDen := SnapToStandard(cr.measuredFPS)

	// Check if snapped rate differs from current
	if snapNum == cr.fpsNum && snapDen == cr.fpsDen {
		// Same as current — reset candidate
		cr.candidateNum = 0
		cr.candidateDen = 0
		cr.candidateFrames = 0
		return false
	}

	// Different from current — track stability
	if snapNum == cr.candidateNum && snapDen == cr.candidateDen {
		cr.candidateFrames++
	} else {
		// New candidate
		cr.candidateNum = snapNum
		cr.candidateDen = snapDen
		cr.candidateFrames = 1
	}

	// Check if stable enough to change
	if cr.candidateFrames >= cr.stableThreshold {
		cr.fpsNum = cr.candidateNum
		cr.fpsDen = cr.candidateDen
		cr.candidateNum = 0
		cr.candidateDen = 0
		cr.candidateFrames = 0
		return true
	}

	return false
}

// Rate returns the current recovered rate as num/den.
func (cr *ClockRecovery) Rate() (fpsNum, fpsDen int) {
	return cr.fpsNum, cr.fpsDen
}

// MeasuredFPS returns the raw measured FPS (before snapping). For diagnostics.
func (cr *ClockRecovery) MeasuredFPS() float64 {
	return cr.measuredFPS
}

// SnapToStandard finds the nearest standard broadcast rate to the measured FPS.
func SnapToStandard(measuredFPS float64) (fpsNum, fpsDen int) {
	bestDist := math.MaxFloat64
	bestNum, bestDen := 30, 1 // fallback

	for _, sr := range StandardRates {
		dist := math.Abs(measuredFPS - sr.FPS)
		if dist < bestDist {
			bestDist = dist
			bestNum = sr.Num
			bestDen = sr.Den
		}
	}

	return bestNum, bestDen
}
