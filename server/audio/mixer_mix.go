package audio

import (
	"github.com/zsiec/switchframe/server/audio/vec"
)

// applyMasterChain runs the master output processing on a PCM buffer:
// master gain -> program mute -> LUFS metering -> limiter -> MXL tap -> unmute ramp -> peak metering.
// Caller must hold m.mu.
func (m *Mixer) applyMasterChain(pcm []float32, pts int64) {
	// Master gain
	if m.masterLinear != 1.0 && len(pcm) > 0 {
		vec.ScaleFloat32(&pcm[0], m.masterLinear, len(pcm))
	}

	// Program mute (FTB). Zero BEFORE metering and the output taps so the
	// LUFS meter, limiter and MXL tap all see the silence that is actually
	// broadcast — SetProgramMute's contract is "Metering reflects silence".
	// Metering the live pre-mute mix would skew the gated integrated LUFS
	// (EBU R128 compliance) with audio that never went to air.
	if m.programMuted {
		for i := range pcm {
			pcm[i] = 0
		}
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

// produceSamplesLocked reads every active channel's ring at sample index
// start, applies per-channel gains, crossfade, and stinger audio overlay.
// Returns totalValues float32 samples of mixed PCM. Does NOT apply the
// master output chain; ProduceSamples does.
//
// totalValues is in interleaved sample values (samples * channels).
// Caller must hold m.mu.
func (m *Mixer) produceSamplesLocked(start uint64, totalValues int) []float32 {
	// Snapshot transition position for this mix cycle
	if m.transCrossfadeActive {
		m.mixCycleTransPos = m.transCrossfadePosition
	}

	// Read from each active channel's ring buffer and apply gain
	m.mixAccum = growBuf(m.mixAccum, totalValues)
	for i := range m.mixAccum[:totalValues] {
		m.mixAccum[i] = 0
	}

	// Measure ring buffer depth of active channels before reading, for
	// diagnostics and the debug snapshot.
	var maxFrameDepth int
	for _, ch := range m.channels {
		if ch.active && !ch.muted && ch.ringBuf != nil {
			depth := ch.ringBuf.FrameCount()
			if depth > maxFrameDepth {
				maxFrameDepth = depth
			}
		}
	}
	if maxFrameDepth > 0 {
		latency90k := int64(maxFrameDepth) * 90000 / int64(m.sampleRate)
		m.ringBufLatency90k.Store(latency90k)
	}

	for sourceKey, ch := range m.channels {
		if !ch.active || ch.muted || ch.ringBuf == nil {
			continue
		}

		pcm := m.popAt(ch, start, totalValues/m.numChannels)
		if pcm == nil {
			continue // never pushed -- skip
		}

		// Apply fader gain (and transition gain if active)
		ch.gainBuf = growBuf(ch.gainBuf, len(pcm))
		gained := ch.gainBuf[:len(pcm)]

		isTransParticipant := m.transCrossfadeActive &&
			(sourceKey == m.transCrossfadeFrom || sourceKey == m.transCrossfadeTo)

		if isTransParticipant {
			var gainFn func(float64) float64
			if sourceKey == m.transCrossfadeFrom {
				gainFn = func(p float64) float64 { return transitionFromGain(m.transCrossfadeMode, p) }
			} else {
				gainFn = func(p float64) float64 { return transitionToGain(m.transCrossfadeMode, p) }
			}
			gStart := float32(gainFn(m.transCrossfadeAudioPos))
			gEnd := float32(gainFn(m.mixCycleTransPos))
			faderGain := ch.levelLinear
			channels := m.numChannels
			pairCount := len(pcm) / channels
			divisor := float32(pairCount)
			if pairCount > 1 {
				divisor = float32(pairCount - 1)
			}
			for i, s := range pcm {
				t := float32(i/channels) / divisor
				transGain := gStart + (gEnd-gStart)*t
				gained[i] = s * faderGain * transGain
			}
		} else {
			faderGain := ch.levelLinear
			for i, s := range pcm {
				gained[i] = s * faderGain
			}
		}

		// Sum into accumulator
		n := len(gained)
		if n > totalValues {
			n = totalValues
		}
		if n > 0 {
			vec.AddFloat32(&m.mixAccum[0], &gained[0], n)
		}
	}

	// Stinger audio overlay
	if m.stingerAudio != nil {
		m.addStingerAudio(m.mixAccum[:totalValues])
	}

	// Advance transition audio position
	if m.transCrossfadeActive {
		m.transCrossfadeAudioPos = m.mixCycleTransPos
	}

	return m.mixAccum[:totalValues]
}

// advanceCutCrossfade advances the cut crossfade state machine by one period.
// Must be called with m.mu held.
func (m *Mixer) advanceCutCrossfade() {
	if m.cutFramesRemaining <= 0 {
		return
	}
	m.cutFramesRemaining--
	frameIdx := m.cutTotalFrames - m.cutFramesRemaining
	m.transCrossfadePosition = float64(frameIdx) / float64(m.cutTotalFrames)
	if m.cutFramesRemaining <= 0 {
		// Cut crossfade complete: clear all transition state.
		m.transCrossfadeActive = false
		m.transCrossfadeFrom = ""
		m.transCrossfadeTo = ""
		m.transCrossfadePosition = 0.0
		m.transCrossfadeAudioPos = 0.0
		m.mixCycleTransPos = 0.0
		m.cutFramesRemaining = 0
		m.cutTotalFrames = 0

		// Deferred AFV deactivation: the outgoing AFV channel(s) were
		// kept active during the crossfade ramp so their audio could
		// fade out smoothly. Now that the ramp is complete, deactivate them.
		if len(m.afvDeactivateOnComplete) > 0 {
			for key := range m.afvDeactivateOnComplete {
				// Never deactivate the channel currently on program: a
				// queued channel may have been put back on program (e.g.
				// re-activated by OnCut) after it was deferred here.
				if key == m.programAFVSource {
					continue
				}
				if ch, ok := m.channels[key]; ok {
					ch.active = false
				}
			}
			m.afvDeactivateOnComplete = nil
		}
	}
}
