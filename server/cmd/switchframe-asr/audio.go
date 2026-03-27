package main

import (
	"math"

	"github.com/zsiec/switchframe/server/audio"
)

// audioProcessor handles stereo→mono downmix and 48kHz→16kHz resampling
// for ASR inference input preparation.
type audioProcessor struct {
	resampler *audio.Resampler
	channels  int
	monoBuf   []float32 // reusable buffer for mono downmix
}

// newAudioProcessor creates an audio processor that downmixes to mono
// and resamples from srcRate to 16kHz for ASR inference.
func newAudioProcessor(srcRate, channels int) *audioProcessor {
	return &audioProcessor{
		resampler: audio.NewResampler(srcRate, 16000, 1), // output is always mono
		channels:  channels,
	}
}

// downmix converts interleaved multi-channel audio to mono by averaging
// all channels. For mono input, returns the input unchanged (via buffer copy).
func (p *audioProcessor) downmix(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	ch := p.channels
	frames := len(samples) / ch

	if frames == 0 {
		return nil
	}

	// Grow reusable buffer if needed.
	if cap(p.monoBuf) < frames {
		p.monoBuf = make([]float32, frames)
	}
	p.monoBuf = p.monoBuf[:frames]

	if ch == 1 {
		// Mono passthrough — copy into buffer.
		copy(p.monoBuf, samples[:frames])
		return p.monoBuf
	}

	// Average all channels per frame.
	scale := 1.0 / float32(ch)
	for i := 0; i < frames; i++ {
		var sum float32
		base := i * ch
		for c := 0; c < ch; c++ {
			sum += samples[base+c]
		}
		p.monoBuf[i] = sum * scale
	}

	return p.monoBuf
}

// process takes interleaved float32 samples at the source rate and returns
// mono float32 samples at 16kHz suitable for ASR inference.
func (p *audioProcessor) process(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Step 1: downmix to mono.
	mono := p.downmix(samples)
	if len(mono) == 0 {
		return nil
	}

	// Step 2: resample to 16kHz.
	return p.resampler.Resample(mono)
}

// computeRMS returns the RMS energy of the mono signal.
func computeRMS(mono []float32) float64 {
	if len(mono) == 0 {
		return 0
	}
	var sumSq float64
	for _, s := range mono {
		sumSq += float64(s) * float64(s)
	}
	return math.Sqrt(sumSq / float64(len(mono)))
}
