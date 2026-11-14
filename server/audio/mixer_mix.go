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
		ch.aired = ch.active
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
// position p: its cut span during a cut ramp, its gain over the
// transition during a mix, dip or fade (mixGainLocked), the transition's
// curve for the two sources otherwise, and full gain outside a
// transition. Caller must hold m.mu.
func (m *Mixer) transGainLocked(sourceKey string, ch *Channel, p float64) float64 {
	if !m.transCrossfadeActive {
		return 1
	}
	if s, ok := m.cutSpans[sourceKey]; ok {
		return s.at(p)
	}
	if start, ok := m.mixStarts[sourceKey]; ok {
		return m.mixGainLocked(sourceKey, ch, start, p)
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

// mixGainLocked returns the gain of an active channel at position p of a
// transition that started with the channel at gain start. A fade to black
// takes every channel down from there to silence, so the program is
// silent when it is muted at the end; a fade from black, which starts from
// a muted program, brings every channel up from silence but the ones
// queued to go off. In a mix or dip the incoming source rises from its
// start to full gain on the transition's curve, the outgoing source, when
// it follows video, and the channels queued to go off fall from theirs to
// silence on it, and every other channel, a source on before the mix that
// does not follow video among them, eases to full gain, staying there
// when it starts there. With the outgoing source at full gain and the
// incoming one silent, these are the transitions' own curves. Caller must
// hold m.mu.
func (m *Mixer) mixGainLocked(sourceKey string, ch *Channel, start, p float64) float64 {
	mode := m.transCrossfadeMode
	_, queued := m.afvDeactivateOnComplete[sourceKey]
	switch {
	case mode == FadeOut:
		return start * transitionFromGain(mode, p)
	case mode == FadeIn && queued:
		return 0
	case mode == FadeIn:
		return transitionFromGain(mode, p)
	case sourceKey == m.transCrossfadeTo && (ch.afv || m.transAbortActivated == sourceKey):
		if mode == DipToSilence {
			return start*transitionFromGain(mode, p) + transitionToGain(mode, p)
		}
		return cutSpan{start, 1}.at(p)
	case sourceKey == m.transCrossfadeFrom && ch.afv, queued:
		return start * transitionFromGain(mode, p)
	}
	return cutSpan{start, 1}.at(p)
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
	m.mixStarts = nil
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

		// Deferred deactivation: the channels queued to go off were kept
		// active during the ramp so they could fade out. Never the channel
		// on program: a queued channel may have been put back on program
		// (e.g. re-activated by OnCut) after it was queued.
		m.deactivateQueuedLocked()
	}
}
