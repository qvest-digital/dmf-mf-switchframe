package audio

import (
	"math"

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
		var gStart, gEnd float64
		if ch.active {
			gStart = m.transGainLocked(sourceKey, ch, m.transCrossfadeAudioPos)
			gEnd = m.transGainLocked(sourceKey, ch, m.mixCycleTransPos)
		}
		ch.xfGain = gEnd
		if !ch.active || ch.muted || ch.ringBuf == nil {
			continue
		}

		pcm := m.popAt(ch, start, totalValues/m.numChannels)
		if pcm == nil {
			continue // never pushed -- skip
		}

		// Apply fader gain and the transition gain, interpolated linearly
		// across the period.
		ch.gainBuf = growBuf(ch.gainBuf, len(pcm))
		gained := ch.gainBuf[:len(pcm)]
		faderGain := ch.levelLinear
		if gStart == 1 && gEnd == 1 {
			for i, s := range pcm {
				gained[i] = s * faderGain
			}
		} else {
			g0, g1 := float32(gStart), float32(gEnd)
			channels := m.numChannels
			pairCount := len(pcm) / channels
			divisor := float32(pairCount)
			if pairCount > 1 {
				divisor = float32(pairCount - 1)
			}
			for i, s := range pcm {
				t := float32(i/channels) / divisor
				gained[i] = s * faderGain * (g0 + (g1-g0)*t)
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

// cutRampPeriods is the length of the crossfade a cut starts, in periods.
const cutRampPeriods = 4

// cutSpan is the gain a cut ramp moves one channel between, from the
// gain it aired when the ramp started to the gain it airs once the ramp
// ends.
type cutSpan struct{ from, to float64 }

// at returns the span's gain at ramp position p, 0 to 1. A rise follows
// the sine and a fall the cosine, so a ramp from one source at full gain
// to another from silence is the equal-power crossfade, and a span from
// any other gain eases along the same curve.
func (s cutSpan) at(p float64) float64 {
	if s.to >= s.from {
		return s.from + (s.to-s.from)*math.Sin(p*math.Pi/2)
	}
	return s.to + (s.from-s.to)*math.Cos(p*math.Pi/2)
}

// transGainLocked returns the transition gain of an active channel at
// position p: its cut span during a cut ramp, the transition's curve for
// the two sources of a transition, and full gain otherwise. A mix or dip
// does not fade out a source that does not follow video: it stays on
// after the transition, as after a cut. Caller must hold m.mu.
func (m *Mixer) transGainLocked(sourceKey string, ch *Channel, p float64) float64 {
	if !m.transCrossfadeActive {
		return 1
	}
	if s, ok := m.cutSpans[sourceKey]; ok {
		return s.at(p)
	}
	switch sourceKey {
	case m.transCrossfadeFrom:
		if !ch.afv && m.transCrossfadeTo != "" {
			return 1
		}
		return transitionFromGain(m.transCrossfadeMode, p)
	case m.transCrossfadeTo:
		return transitionToGain(m.transCrossfadeMode, p)
	}
	return 1
}

// cutTargetLocked returns the gain a channel airs once a cut ramp ends:
// silence when it goes off with the ramp, full gain when it stays on.
// Caller must hold m.mu.
func (m *Mixer) cutTargetLocked(sourceKey string) float64 {
	if _, off := m.afvDeactivateOnComplete[sourceKey]; off {
		return 0
	}
	return 1
}

// cutTargetsMatchLocked reports whether the cut ramp in progress already
// takes every active channel where it ends up. Caller must hold m.mu.
func (m *Mixer) cutTargetsMatchLocked() bool {
	for key, ch := range m.channels {
		if !ch.active {
			continue
		}
		if s, ok := m.cutSpans[key]; !ok || s.to != m.cutTargetLocked(key) {
			return false
		}
	}
	return true
}

// startCutRampLocked starts the cut crossfade. The period a cut lands on
// already ramps the incoming source from silence to the first step, so
// its first sample airs on the cut tick, and the ramp reaches full gain
// at the end of its last period. Every active channel ramps from the gain
// it aired at the end of the last period, so a ramp started during another
// one, or during a transition, carries on from where that one left the
// channels instead of stepping. Must be called with m.mu held, after the
// channels the ramp puts on are active and the ones it takes off queued.
func (m *Mixer) startCutRampLocked() {
	m.cutSpans = make(map[string]cutSpan, len(m.channels))
	for key, ch := range m.channels {
		if ch.active {
			m.cutSpans[key] = cutSpan{from: ch.xfGain, to: m.cutTargetLocked(key)}
		}
	}
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0
	m.transCrossfadePosition = 1.0 / cutRampPeriods
	m.cutFramesRemaining = cutRampPeriods
	m.cutTotalFrames = cutRampPeriods
}

// advanceCutCrossfade advances the cut crossfade state machine by one period:
// it sets the position the next period ramps to. Must be called with m.mu
// held.
func (m *Mixer) advanceCutCrossfade() {
	if m.cutFramesRemaining <= 0 {
		return
	}
	m.cutFramesRemaining--
	frameIdx := m.cutTotalFrames - m.cutFramesRemaining
	// The position runs past 1 only on the last advance, which clears it.
	m.transCrossfadePosition = float64(frameIdx+1) / float64(m.cutTotalFrames)
	if m.cutFramesRemaining <= 0 {
		// Cut crossfade complete: clear all transition state.
		m.transCrossfadeActive = false
		m.cutSpans = nil
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
