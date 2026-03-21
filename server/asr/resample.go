package asr

// Resampler converts multi-channel audio at one sample rate to mono audio
// at a target sample rate using linear interpolation.
type Resampler struct {
	srcRate    int
	srcCh      int
	dstRate    int
	ratio      float64 // srcRate / dstRate
	phase      float64 // fractional sample position in source
	lastSample float32 // last mono sample for interpolation
	hasLast    bool
}

// NewResampler creates a resampler that converts interleaved multi-channel PCM
// at srcRate to mono PCM at dstRate. Linear interpolation is used, which is
// sufficient for ASR (speech recognition) input where high-fidelity reconstruction
// is not required.
func NewResampler(srcRate, srcChannels, dstRate int) *Resampler {
	return &Resampler{
		srcRate: srcRate,
		srcCh:   srcChannels,
		dstRate: dstRate,
		ratio:   float64(srcRate) / float64(dstRate),
	}
}

// Process resamples interleaved multi-channel PCM to mono at the target rate.
// It maintains state across calls for phase continuity. The input slice contains
// interleaved samples (e.g., [L0, R0, L1, R1, ...] for stereo).
func (r *Resampler) Process(interleaved []float32) []float32 {
	if len(interleaved) == 0 {
		return nil
	}

	// Step 1: Mix down to mono by averaging channels.
	nFrames := len(interleaved) / r.srcCh
	mono := make([]float32, nFrames)
	invCh := 1.0 / float32(r.srcCh)
	for i := 0; i < nFrames; i++ {
		var sum float32
		for ch := 0; ch < r.srcCh; ch++ {
			sum += interleaved[i*r.srcCh+ch]
		}
		mono[i] = sum * invCh
	}

	// Step 2: Resample mono signal using linear interpolation.
	maxOut := int(float64(nFrames)/r.ratio) + 2
	out := make([]float32, 0, maxOut)

	for r.phase < float64(nFrames) {
		idx := int(r.phase)
		frac := float32(r.phase - float64(idx))

		var s0, s1 float32
		if idx == 0 && r.hasLast {
			s0 = r.lastSample
		} else if idx > 0 {
			s0 = mono[idx-1]
		}
		if idx < nFrames {
			s1 = mono[idx]
		} else {
			s1 = s0
		}

		out = append(out, s0+(s1-s0)*frac)
		r.phase += r.ratio
	}

	// Carry phase into next call.
	r.phase -= float64(nFrames)
	if nFrames > 0 {
		r.lastSample = mono[nFrames-1]
		r.hasLast = true
	}
	return out
}

// Reset clears the resampler's internal state, allowing it to be reused
// for a new audio stream without residual phase or sample history.
func (r *Resampler) Reset() {
	r.phase = 0
	r.lastSample = 0
	r.hasLast = false
}
