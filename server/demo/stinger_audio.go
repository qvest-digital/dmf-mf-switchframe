package demo

import (
	"encoding/binary"
	"math"
)

// GenerateWAV creates a valid WAV file from interleaved float32 PCM.
// Output format: PCM int16 (audioFormat=1, bitsPerSample=16).
// Float32 values are clamped to [-1.0, 1.0] before conversion.
func GenerateWAV(pcm []float32, sampleRate, channels int) []byte {
	numSamples := len(pcm)
	dataSize := numSamples * 2 // 2 bytes per int16 sample
	fileSize := 44 + dataSize  // 44-byte header + data

	buf := make([]byte, fileSize)

	// RIFF header
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(fileSize-8))
	copy(buf[8:12], "WAVE")

	// fmt chunk
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)               // chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)                // audioFormat = PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels)) // numChannels
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	blockAlign := channels * 2
	byteRate := sampleRate * blockAlign
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))   // byteRate
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign)) // blockAlign
	binary.LittleEndian.PutUint16(buf[34:36], 16)                 // bitsPerSample

	// data chunk
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	// Convert float32 to int16
	for i, s := range pcm {
		// Clamp to [-1.0, 1.0]
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		sample := int16(s * 32767)
		binary.LittleEndian.PutUint16(buf[44+i*2:44+i*2+2], uint16(sample))
	}

	return buf
}

// SynthesizeWhoosh generates a frequency sweep sound.
// Returns interleaved float32 PCM with values in [-1.0, 1.0].
func SynthesizeWhoosh(sampleRate, channels int, durationSec float64) []float32 {
	totalSamples := int(float64(sampleRate) * durationSec)
	pcm := make([]float32, totalSamples*channels)

	const (
		startFreq = 200.0
		endFreq   = 4000.0
		level     = 0.7
	)

	var phase float64
	for i := 0; i < totalSamples; i++ {
		t := float64(i) / float64(totalSamples) // normalized 0..1

		// Exponential frequency sweep
		freq := startFreq * math.Pow(endFreq/startFreq, t)

		// Phase accumulation
		phase += 2.0 * math.Pi * freq / float64(sampleRate)

		// Sine with inharmonic overtones (2.3x and 4.7x phase produce spectral
		// complexity that shifts with the sweep, creating a richer whoosh effect)
		sample := 0.4*math.Sin(phase) + 0.2*math.Sin(2.3*phase) + 0.1*math.Sin(4.7*phase)

		// Amplitude envelope: fast attack (first 5%), sustain, fade out (last 15%)
		var envelope float64
		if t < 0.05 {
			// Fast attack
			envelope = t / 0.05
		} else if t > 0.85 {
			// Fade out (last 15%)
			envelope = (1.0 - t) / 0.15
		} else {
			envelope = 1.0
		}

		val := float32(sample * envelope * level)

		// Write to all channels
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}

	return pcm
}

// SynthesizeSlam generates a percussive impact sound.
// Returns interleaved float32 PCM with values in [-1.0, 1.0].
func SynthesizeSlam(sampleRate, channels int, durationSec float64) []float32 {
	totalSamples := int(float64(sampleRate) * durationSec)
	pcm := make([]float32, totalSamples*channels)

	// Deterministic LCG noise
	seed := uint32(12345)

	for i := 0; i < totalSamples; i++ {
		t := float64(i) / float64(sampleRate) // time in seconds
		tNorm := float64(i) / float64(totalSamples)

		// LCG: seed = seed*1664525 + 1013904223
		seed = seed*1664525 + 1013904223
		// Convert to [-1, 1]
		noise := float64(int32(seed)) / float64(math.MaxInt32)

		// Fast exponential decay on noise
		noiseComponent := noise * math.Exp(-t*30) * 0.6

		// Low frequency thud
		thudComponent := math.Sin(2*math.Pi*60*t) * math.Exp(-t*8) * 0.5

		sample := noiseComponent + thudComponent

		// Fade out last 10%
		if tNorm > 0.90 {
			fade := (1.0 - tNorm) / 0.10
			sample *= fade
		}

		val := float32(sample)

		// Write to all channels
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}

	return pcm
}

// SynthesizeMusical generates a major chord sting (C major).
// Returns interleaved float32 PCM with values in [-1.0, 1.0].
func SynthesizeMusical(sampleRate, channels int, durationSec float64) []float32 {
	totalSamples := int(float64(sampleRate) * durationSec)
	pcm := make([]float32, totalSamples*channels)

	const (
		c4    = 261.63
		e4    = 329.63
		g4    = 392.00
		level = 0.6
	)

	attackSamples := int(0.020 * float64(sampleRate)) // 20ms attack
	sustainEnd := int(0.40 * float64(totalSamples))   // sustain to 40%

	for i := 0; i < totalSamples; i++ {
		t := float64(i) / float64(sampleRate) // time in seconds

		// C major chord: each note is a sine wave, averaged
		chord := (math.Sin(2*math.Pi*c4*t) +
			math.Sin(2*math.Pi*e4*t) +
			math.Sin(2*math.Pi*g4*t)) / 3.0

		// ADSR envelope
		var envelope float64
		if i < attackSamples {
			// Attack: 20ms linear ramp
			envelope = float64(i) / float64(attackSamples)
		} else if i < sustainEnd {
			// Sustain at full level
			envelope = 1.0
		} else {
			// Release: slow linear fade
			envelope = 1.0 - float64(i-sustainEnd)/float64(totalSamples-sustainEnd)
			if envelope < 0 {
				envelope = 0
			}
		}

		val := float32(chord * envelope * level)

		// Write to all channels
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}

	return pcm
}
