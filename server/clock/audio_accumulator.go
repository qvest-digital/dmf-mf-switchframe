package clock

// AudioAccumulator computes exact audio sample counts per video frame using
// Bresenham-style integer accumulation. Zero cumulative error over any timespan.
//
// For non-integer ratios like 48000Hz at 29.97fps (30000/1001), each frame
// needs exactly 48000*1001/30000 = 1601.6 samples. The accumulator distributes
// this by alternating between 1601 and 1602 samples per frame, maintaining
// an integer remainder that guarantees zero drift over any number of frames.
type AudioAccumulator struct {
	sampleRate   int
	fpsNum       int   // frame rate numerator (e.g., 30000 for 29.97fps)
	fpsDen       int   // frame rate denominator (e.g., 1001)
	remainder    int   // Bresenham accumulator
	totalSamples int64 // cumulative samples produced
}

// NewAudioAccumulator creates an accumulator for the given sample rate and
// frame rate. The frame rate is expressed as fpsNum/fpsDen (e.g., 30000/1001
// for 29.97fps, 25/1 for 25fps).
func NewAudioAccumulator(sampleRate, fpsNum, fpsDen int) *AudioAccumulator {
	return &AudioAccumulator{
		sampleRate: sampleRate,
		fpsNum:     fpsNum,
		fpsDen:     fpsDen,
	}
}

// SamplesForTick returns the exact sample count for the next video frame.
// At 48000Hz / 29.97fps: alternates 1601/1602 to maintain exact ratio.
// Pure integer math -- no floating point.
func (a *AudioAccumulator) SamplesForTick() int {
	a.remainder += a.sampleRate * a.fpsDen
	samples := a.remainder / a.fpsNum
	a.remainder %= a.fpsNum
	a.totalSamples += int64(samples)
	return samples
}

// Reset reconfigures for a new rate (e.g., after source clock recovery
// detects a rate change). Clears the remainder and total sample count.
func (a *AudioAccumulator) Reset(sampleRate, fpsNum, fpsDen int) {
	a.sampleRate = sampleRate
	a.fpsNum = fpsNum
	a.fpsDen = fpsDen
	a.remainder = 0
	a.totalSamples = 0
}

// TotalSamples returns cumulative samples produced since creation or last Reset.
func (a *AudioAccumulator) TotalSamples() int64 {
	return a.totalSamples
}
