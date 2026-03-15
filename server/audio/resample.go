package audio

import "math"

// resamplerDefaultBeta is the Kaiser window shape parameter.
// β=10 yields ~100 dB stopband attenuation — aliasing artifacts are
// 100 dB below signal level, well beyond audible.
const resamplerDefaultBeta = 10.0

// resamplerMinTapsPerPhase is the lower bound on filter taps per phase.
const resamplerMinTapsPerPhase = 16

// resamplerStopbandDB is the target stopband attenuation in dB.
const resamplerStopbandDB = 100.0

// Resampler performs sample rate conversion using a polyphase FIR filter
// with Kaiser-windowed sinc kernel. It supports arbitrary rational rate
// pairs and maintains state across calls for click-free block processing.
type Resampler struct {
	srcRate  int
	dstRate  int
	channels int
	identity bool // true when srcRate == dstRate

	// Polyphase filter
	upFactor     int       // L (interpolation factor = dstRate / gcd)
	downFactor   int       // M (decimation factor = srcRate / gcd)
	tapsPerPhase int       // filter taps per polyphase branch
	coeffs       []float32 // [upFactor * tapsPerPhase], polyphase-ordered

	// Streaming state
	history []float32 // ring buffer of recent input samples [tapsPerPhase * channels]
	histPos int       // write position in history (in frames, not samples)
	phase   int       // phase accumulator (0..upFactor-1)

	// Reusable output buffer
	outBuf []float32

	// Frame-aligned output FIFO: accumulates resampled samples and emits
	// exactly frameSize*channels samples per ResampleFrameAligned call.
	fifo    []float32
	fifoBuf []float32 // reusable output for ResampleFrameAligned
}

// NewResampler creates a resampler that converts audio from srcRate to dstRate.
// The channels parameter specifies the number of interleaved audio channels.
func NewResampler(srcRate, dstRate, channels int) *Resampler {
	r := &Resampler{
		srcRate:  srcRate,
		dstRate:  dstRate,
		channels: channels,
	}

	if srcRate == dstRate {
		r.identity = true
		return r
	}

	g := gcd(srcRate, dstRate)
	r.upFactor = dstRate / g   // L
	r.downFactor = srcRate / g // M

	// Compute taps per phase from the actual transition bandwidth.
	// The narrowest transition band for tones near Nyquist is the gap
	// between the two Nyquist frequencies: |dstRate - srcRate| / 2.
	// For 44100→48000: (48000-44100)/2 = 1950 Hz transition band.
	transitionHz := math.Abs(float64(dstRate)-float64(srcRate)) / 2.0
	if transitionHz < 100 {
		transitionHz = 100
	}
	upsampledRate := float64(srcRate) * float64(r.upFactor)
	deltaOmega := 2.0 * math.Pi * transitionHz / upsampledRate

	// Kaiser formula: N ≈ (A - 7.95) / (2.285 × Δω)
	totalTapsNeeded := int(math.Ceil((resamplerStopbandDB - 7.95) / (2.285 * deltaOmega)))
	r.tapsPerPhase = (totalTapsNeeded + r.upFactor - 1) / r.upFactor // ceil division
	if r.tapsPerPhase < resamplerMinTapsPerPhase {
		r.tapsPerPhase = resamplerMinTapsPerPhase
	}

	r.coeffs = computePolyphaseCoeffs(r.upFactor, r.downFactor, r.tapsPerPhase, resamplerDefaultBeta)
	r.history = make([]float32, r.tapsPerPhase*channels)
	return r
}

// Resample converts interleaved float32 PCM from srcRate to dstRate.
// State is maintained across calls so consecutive blocks produce a
// continuous output stream with no discontinuities.
func (r *Resampler) Resample(in []float32) []float32 {
	if r.identity {
		return in
	}

	channels := r.channels
	inFrames := len(in) / channels
	if inFrames == 0 {
		return in
	}

	// Estimate output size: inFrames * L/M + safety margin
	maxOut := (inFrames*r.upFactor)/r.downFactor + r.tapsPerPhase
	if cap(r.outBuf) < maxOut*channels {
		r.outBuf = make([]float32, maxOut*channels)
	}
	out := r.outBuf[:0]

	taps := r.tapsPerPhase
	hist := r.history

	for i := 0; i < inFrames; i++ {
		// Push input frame into history ring buffer
		base := r.histPos * channels
		for ch := 0; ch < channels; ch++ {
			hist[base+ch] = in[i*channels+ch]
		}
		r.histPos = (r.histPos + 1) % taps

		// Produce output samples while phase < upFactor.
		// History is zero-initialized, representing signal starting from silence.
		// The first few output samples convolve with some zeros — this is the
		// correct transient response (~0.3ms at 48kHz, completely inaudible).
		for r.phase < r.upFactor {
			coeffBase := r.phase * taps
			for ch := 0; ch < channels; ch++ {
				var sum float32
				for t := 0; t < taps; t++ {
					// coeffs[0] multiplies newest sample, coeffs[taps-1] multiplies oldest.
					// histPos-1 is newest, histPos-taps is oldest.
					histIdx := (r.histPos - 1 - t + taps) % taps
					sum += r.coeffs[coeffBase+t] * hist[histIdx*channels+ch]
				}
				out = append(out, sum)
			}
			r.phase += r.downFactor
		}
		r.phase -= r.upFactor
	}

	return out
}

// ResampleFrameAligned resamples the input and returns exactly
// frameSize*channels output samples. Internally it accumulates resampled
// samples in a FIFO and drains exactly one frame per call. This ensures
// AAC encoders (which require exactly 1024 samples per frame) always
// receive the right amount.
//
// If not enough resampled data is available yet (startup transient),
// the output is zero-padded. Over time the FIFO converges and the
// padding disappears.
func (r *Resampler) ResampleFrameAligned(in []float32, frameSize int) []float32 {
	need := frameSize * r.channels

	if r.identity {
		// Identity: input should already be frameSize*channels. Return as-is.
		if len(in) >= need {
			return in[:need]
		}
		// Pad if somehow short
		r.fifoBuf = growBuf(r.fifoBuf, need)
		copy(r.fifoBuf, in)
		for i := len(in); i < need; i++ {
			r.fifoBuf[i] = 0
		}
		return r.fifoBuf[:need]
	}

	// Resample and append to FIFO
	resampled := r.Resample(in)
	r.fifo = append(r.fifo, resampled...)

	// Drain exactly one frame from FIFO
	r.fifoBuf = growBuf(r.fifoBuf, need)
	if len(r.fifo) >= need {
		copy(r.fifoBuf, r.fifo[:need])
		// Shift remaining FIFO data (avoid growing unbounded)
		remaining := len(r.fifo) - need
		copy(r.fifo, r.fifo[need:])
		r.fifo = r.fifo[:remaining]
	} else {
		// Not enough data yet — copy what we have, zero-pad the rest
		copy(r.fifoBuf, r.fifo)
		for i := len(r.fifo); i < need; i++ {
			r.fifoBuf[i] = 0
		}
		r.fifo = r.fifo[:0]
	}

	return r.fifoBuf[:need]
}

// TapsPerPhase returns the number of filter taps per polyphase branch.
func (r *Resampler) TapsPerPhase() int { return r.tapsPerPhase }

// UpFactor returns L (interpolation factor).
func (r *Resampler) UpFactor() int { return r.upFactor }

// DownFactor returns M (decimation factor).
func (r *Resampler) DownFactor() int { return r.downFactor }

// Reset clears the resampler's internal state, allowing it to be reused
// for a new audio stream without creating a new instance.
func (r *Resampler) Reset() {
	for i := range r.history {
		r.history[i] = 0
	}
	r.histPos = 0
	r.phase = 0
	r.fifo = r.fifo[:0]
}

// computePolyphaseCoeffs generates the polyphase FIR filter coefficient table.
// The prototype filter is a Kaiser-windowed sinc lowpass with cutoff at
// min(π/L, π/M). Coefficients are rearranged into polyphase order.
func computePolyphaseCoeffs(upFactor, downFactor, tapsPerPhase int, beta float64) []float32 {
	totalTaps := upFactor * tapsPerPhase
	center := float64(totalTaps-1) / 2.0

	// Cutoff = min(1/L, 1/M) prevents both imaging and aliasing.
	maxFactor := upFactor
	if downFactor > maxFactor {
		maxFactor = downFactor
	}
	cutoff := 1.0 / float64(maxFactor)

	// Compute prototype filter coefficients (windowed sinc).
	// sinc(x * cutoff) has DC gain = 1/cutoff = maxFactor.
	// After polyphase decomposition into L sub-filters, each sub-filter
	// has DC gain ≈ maxFactor/L. We need DC gain = 1 per sub-filter,
	// so scale by L/maxFactor.
	gainScale := float64(upFactor) / float64(maxFactor)

	proto := make([]float64, totalTaps)
	for i := 0; i < totalTaps; i++ {
		x := float64(i) - center

		// Sinc component
		sinc := 1.0
		if x != 0 {
			arg := math.Pi * x * cutoff
			sinc = math.Sin(arg) / arg
		}

		// Kaiser window
		w := kaiserWindow(float64(i), float64(totalTaps-1), beta)

		proto[i] = sinc * w * gainScale
	}

	// Rearrange into polyphase order:
	// For phase p and tap t: polyphase[p*tapsPerPhase + t] = proto[t*upFactor + p]
	coeffs := make([]float32, totalTaps)
	for p := 0; p < upFactor; p++ {
		for t := 0; t < tapsPerPhase; t++ {
			protoIdx := t*upFactor + p
			if protoIdx < totalTaps {
				coeffs[p*tapsPerPhase+t] = float32(proto[protoIdx])
			}
		}
	}

	return coeffs
}

// kaiserWindow computes the Kaiser window value at position n for a window
// of length N+1 with shape parameter beta.
// w(n) = I₀(β√(1-(2n/N-1)²)) / I₀(β)
func kaiserWindow(n, N, beta float64) float64 {
	t := 2*n/N - 1
	arg := 1 - t*t
	if arg < 0 {
		arg = 0
	}
	return besselI0(beta*math.Sqrt(arg)) / besselI0(beta)
}

// besselI0 computes the zeroth-order modified Bessel function of the first kind.
// Uses the standard power series expansion: I₀(x) = Σ ((x/2)^k / k!)²
func besselI0(x float64) float64 {
	// Series converges quickly for all x we encounter (β ≤ 14 or so)
	sum := 1.0
	term := 1.0
	halfX := x / 2.0
	for k := 1; k <= 25; k++ {
		term *= halfX / float64(k)
		sum += term * term
	}
	return sum
}

// gcd computes the greatest common divisor using Euclid's algorithm.
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
