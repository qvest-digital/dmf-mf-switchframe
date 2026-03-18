package audio

import (
	"github.com/zsiec/switchframe/server/audio/vec"
)

// applyMasterChain runs the master output processing on a PCM buffer:
// master gain -> LUFS metering -> limiter -> MXL tap -> program mute -> unmute ramp -> peak metering.
// Caller must hold m.mu.
func (m *Mixer) applyMasterChain(pcm []float32, pts int64) {
	// Master gain
	if m.masterLinear != 1.0 && len(pcm) > 0 {
		vec.ScaleFloat32(&pcm[0], m.masterLinear, len(pcm))
	}

	// LUFS metering (after master fader, before limiter)
	m.loudness.Process(pcm)

	// Brickwall limiter
	m.limiter.Process(pcm)

	// MXL output tap
	if sinkPtr := m.rawAudioSink.Load(); sinkPtr != nil {
		m.mxlSinkBuf = growBuf(m.mxlSinkBuf, len(pcm))
		copy(m.mxlSinkBuf, pcm)
		(*sinkPtr)(m.mxlSinkBuf, pts, m.sampleRate, m.numChannels)
	}

	// Program mute (FTB)
	if m.programMuted {
		for i := range pcm {
			pcm[i] = 0
		}
	}

	// Unmute fade-in ramp
	if m.unmuteFadeRemaining > 0 {
		fadeSamples := m.unmuteFadeRemaining
		rampTotal := m.sampleRate * m.numChannels * 5 / 1000
		ch := m.numChannels
		for i := 0; i < len(pcm); i += ch {
			if fadeSamples <= 0 {
				break
			}
			progress := float32(rampTotal-fadeSamples) / float32(rampTotal)
			for j := 0; j < ch && i+j < len(pcm); j++ {
				pcm[i+j] *= progress
			}
			fadeSamples -= ch
		}
		if fadeSamples < 0 {
			fadeSamples = 0
		}
		m.unmuteFadeRemaining = fadeSamples
	}

	// Peak metering
	peakL, peakR := PeakLevel(pcm, m.numChannels)
	m.programPeakL = peakL
	m.programPeakR = peakR
}
