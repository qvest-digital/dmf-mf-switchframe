package audio

import (
	"math"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio/vec"
	"github.com/zsiec/switchframe/server/codec"
)

// OnProgramChange updates AFV channel states based on the new program source.
// Channels with AFV enabled activate when they match the program source and
// deactivate when they don't. Non-AFV channels are unaffected.
func (m *Mixer) OnProgramChange(newProgramSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, ch := range m.channels {
		if !ch.afv {
			continue
		}
		ch.active = (key == newProgramSource)
	}
	m.recalcPassthrough()
}

// OnCut initiates a 2-frame (~42ms) equal-power crossfade between old and new
// source. Called by the switcher when a cut occurs. Two frames provide enough
// time to mask AAC codec warmup artifacts at the passthrough→mixing boundary.
// A timeout ensures the crossfade completes even if a source stops sending.
func (m *Mixer) OnCut(oldSource, newSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crossfadeFrom = oldSource
	m.crossfadeTo = newSource
	m.crossfadeActive = true
	m.crossfadeFramesRemaining = 2
	m.crossfadeTotalFrames = 2
	m.crossfadePCM = make(map[string][]float32)
	// Pre-seed the old source's PCM from the buffer — no waiting needed.
	// Apply only stateless gain (Trim * Fader) to avoid advancing EQ/compressor
	// internal state with potentially stale audio data. The crossfade is short
	// enough that the slight EQ/compressor difference is inaudible.
	if lastPCM, ok := m.lastDecodedPCM[oldSource]; ok && len(lastPCM) > 0 {
		cp := make([]float32, len(lastPCM))
		if ch, chOk := m.channels[oldSource]; chOk {
			for i, s := range lastPCM {
				cp[i] = s * ch.trimLinear * ch.levelLinear
			}
		} else {
			copy(cp, lastPCM)
		}
		m.crossfadePCM[oldSource] = cp
	}
	m.crossfadeDeadline = time.Now().Add(crossfadeTimeout)
	m.crossfadeCount.Add(1)
}

// OnTransitionStart begins a multi-frame crossfade between old and new source,
// synchronized with a video transition. The mode selects the gain curve:
//   - Crossfade: equal-power A→B (mix/dissolve)
//   - DipToSilence: A→silence→B (dip through black)
//   - FadeOut: A→silence (fade to black)
//   - FadeIn: silence→A (fade from black)
//
// The new source channel is activated so its audio frames are accepted.
func (m *Mixer) OnTransitionStart(oldSource, newSource string, mode TransitionMode, durationMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If currently in passthrough mode, flush the pending cycle so the
	// last passthrough frame is output before switching to mixing mode.
	// This prevents a gap at the passthrough→mixing boundary.
	if m.passthrough && m.mixStarted {
		if out := m.collectMixCycleLocked(); out != nil {
			// Can't call recordAndOutput under lock — defer it.
			defer m.recordAndOutput(out)
		}
	}

	m.transCrossfadeActive = true
	m.transCrossfades.Add(1)
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = mode
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0

	// Ensure the incoming source's channel is active so frames are accepted.
	if ch, ok := m.channels[newSource]; ok {
		ch.active = true
		// Pre-warm the decoder so the first frame from this source
		// doesn't produce warmup transients in the mix output.
		m.initChannelDecoder(ch)
	}

	// Pre-warm the encoder so the first real output frame doesn't have
	// MDCT warmup artifacts (audible pop at passthrough→mixing boundary).
	_ = m.ensureEncoder()

	m.recalcPassthrough()
}

// OnTransitionPosition updates the crossfade position (0.0 = fully old, 1.0 = fully new).
// Called by the switcher as the video transition progresses. Tracks the previous position
// for per-sample gain interpolation within audio frames.
func (m *Mixer) OnTransitionPosition(position float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transCrossfadePosition = position
}

// OnTransitionComplete clears the transition crossfade state.
// Called by the switcher when the video transition finishes.
func (m *Mixer) OnTransitionComplete() {
	m.mu.Lock()
	// Flush any pending mix cycle before switching modes.
	// Without this, partially-accumulated frames are abandoned when
	// passthrough re-enables, causing an audible gap at the boundary.
	var outputFrame *media.AudioFrame
	if m.mixStarted {
		outputFrame = m.collectMixCycleLocked()
	}
	m.transCrossfadeActive = false
	m.transCrossfadeFrom = ""
	m.transCrossfadeTo = ""
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = 0
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0
	m.stingerAudio = nil
	m.stingerOffset = 0
	m.stingerChannels = 0
	m.recalcPassthrough()
	m.mu.Unlock()
	if outputFrame != nil {
		m.recordAndOutput(outputFrame)
	}
}

// OnTransitionAbort handles a cancelled transition (e.g. T-bar pulled back
// to 0). Unlike OnTransitionComplete, it snaps the crossfade position to 0.0
// (fully original source) and flushes a final mix cycle at that position
// before clearing state. This prevents an audio discontinuity when the
// crossfade was at an intermediate position.
func (m *Mixer) OnTransitionAbort() {
	m.mu.Lock()
	var outputFrame *media.AudioFrame
	if m.mixStarted {
		// Snap position to 0 (full original source) for the final mix cycle.
		m.transCrossfadePosition = 0.0
		m.mixCycleTransPos = 0.0
		outputFrame = m.collectMixCycleLocked()
	}
	m.transCrossfadeActive = false
	m.transCrossfadeFrom = ""
	m.transCrossfadeTo = ""
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = 0
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0
	m.stingerAudio = nil
	m.stingerOffset = 0
	m.stingerChannels = 0
	m.recalcPassthrough()
	m.mu.Unlock()
	if outputFrame != nil {
		m.recordAndOutput(outputFrame)
	}
}

// IsInTransitionCrossfade returns whether a multi-frame transition crossfade is active.
func (m *Mixer) IsInTransitionCrossfade() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transCrossfadeActive
}

// TransitionPosition returns the current transition crossfade position (0.0–1.0).
func (m *Mixer) TransitionPosition() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transCrossfadePosition
}

// TransitionGains returns the crossfade gains for the old and new sources based
// on the current transition position and mode. When no transition is active,
// returns (1.0, 0.0).
func (m *Mixer) TransitionGains() (oldGain, newGain float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.transCrossfadeActive {
		return 1.0, 0.0
	}
	return transitionFromGain(m.transCrossfadeMode, m.transCrossfadePosition),
		transitionToGain(m.transCrossfadeMode, m.transCrossfadePosition)
}

// transitionFromGain computes the gain for the outgoing ("from") source at the
// given position and mode.
func transitionFromGain(mode TransitionMode, pos float64) float64 {
	switch mode {
	case Crossfade:
		return math.Cos(pos * math.Pi / 2)
	case DipToSilence:
		if pos < 0.5 {
			// Phase 1: fade out A (equal-power over the first half)
			return math.Cos(pos * 2 * math.Pi / 2)
		}
		return 0
	case FadeOut:
		return math.Cos(pos * math.Pi / 2)
	case FadeIn:
		// FTB reverse: fade the "from" source IN from silence
		return math.Sin(pos * math.Pi / 2)
	}
	return 1.0
}

// transitionToGain computes the gain for the incoming ("to") source at the
// given position and mode.
func transitionToGain(mode TransitionMode, pos float64) float64 {
	switch mode {
	case Crossfade:
		return math.Sin(pos * math.Pi / 2)
	case DipToSilence:
		if pos >= 0.5 {
			// Phase 2: fade in B (equal-power over the second half)
			return math.Sin((pos*2 - 1) * math.Pi / 2)
		}
		return 0
	case FadeOut, FadeIn:
		// FTB has no "to" source
		return 0
	}
	return 0
}

// ingestCrossfadeFrame handles frames during an active crossfade transition.
// It collects one frame from both old and new source, applies equal-power crossfade, and outputs.
func (m *Mixer) ingestCrossfadeFrame(sourceKey string, frame *media.AudioFrame) {
	m.mu.Lock()

	if !m.crossfadeActive {
		m.mu.Unlock()
		return
	}

	// Ensure decoder exists for this channel
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}
	m.initChannelDecoder(ch)
	if ch.decoder == nil {
		m.mu.Unlock()
		return
	}

	// Ensure ADTS header is present — FDK decoder requires ADTS framing.
	adtsFrame := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	// Decode
	pcm, err := ch.decoder.Decode(adtsFrame)
	if err != nil {
		m.decodeErrors.Add(1)
		m.mu.Unlock()
		m.log.Warn("decode error", "source", sourceKey, "err", err)
		return
	}

	// Pipeline: Trim -> EQ -> Compressor -> Fader (reuse channel work buffers)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}
	ch.gainBuf = growBuf(ch.gainBuf, len(trimmedPCM))
	gainedPCM := ch.gainBuf
	for i, s := range trimmedPCM {
		gainedPCM[i] = s * ch.levelLinear
	}

	m.crossfadePCM[sourceKey] = gainedPCM

	// Run the shared crossfade pipeline (wait for sources, blend, master,
	// encode, output). This method handles unlocking m.mu.
	m.processCrossfadePipeline(frame.PTS)
}

// ingestCrossfadePCM handles raw PCM frames during an active crossfade transition.
// This is the PCM equivalent of ingestCrossfadeFrame — it collects PCM from both
// old and new source, applies equal-power crossfade, and outputs. Unlike
// ingestCrossfadeFrame, no AAC decode step is needed since the PCM is already
// in float32 format.
func (m *Mixer) ingestCrossfadePCM(sourceKey string, pcm []float32, pts int64, channels int) {
	m.mu.Lock()

	if !m.crossfadeActive {
		m.mu.Unlock()
		return
	}

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Mono→stereo upmix (same as normal IngestPCM path).
	if channels > 0 && channels < m.numChannels {
		pcm = m.upmixMono(ch, pcm, channels)
	}

	// Pipeline: Trim -> EQ -> Compressor -> Fader (reuse channel work buffers)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}
	ch.gainBuf = growBuf(ch.gainBuf, len(trimmedPCM))
	gainedPCM := ch.gainBuf
	for i, s := range trimmedPCM {
		gainedPCM[i] = s * ch.levelLinear
	}

	m.crossfadePCM[sourceKey] = gainedPCM

	// Run the shared crossfade pipeline (wait for sources, blend, master,
	// encode, output). This method handles unlocking m.mu.
	m.processCrossfadePipeline(pts)
}

// processCrossfadePipeline runs the crossfade pipeline after both (or timed-out)
// sources have stored their processed PCM in m.crossfadePCM. Handles: waiting
// for both sources, crossfade blending, stinger overlay, master gain, LUFS,
// limiter, MXL tap, mute, unmute ramp, peak metering, encode, and output.
// Caller must hold m.mu. This method always unlocks m.mu before returning.
func (m *Mixer) processCrossfadePipeline(inputPTS int64) {
	// Wait for both sources (with timeout)
	_, hasFrom := m.crossfadePCM[m.crossfadeFrom]
	_, hasTo := m.crossfadePCM[m.crossfadeTo]
	timedOut := !m.crossfadeDeadline.IsZero() && time.Now().After(m.crossfadeDeadline)
	if !hasFrom && !hasTo {
		m.mu.Unlock()
		return
	}
	if (!hasFrom || !hasTo) && !timedOut {
		m.mu.Unlock()
		return
	}

	// Track crossfade timeouts (timed out with only one source)
	if timedOut && (!hasFrom || !hasTo) {
		m.crossfadeTimeouts.Add(1)
		missingSrc := m.crossfadeFrom
		if hasFrom {
			missingSrc = m.crossfadeTo
		}
		m.log.Warn("crossfade timeout",
			"source", missingSrc,
			"deadline_ms", crossfadeTimeout.Milliseconds())
	}

	// Compute crossfade position range for this frame within the multi-frame ramp.
	frameIdx := m.crossfadeTotalFrames - m.crossfadeFramesRemaining // 0-based
	posStart := float64(frameIdx) / float64(m.crossfadeTotalFrames)
	posEnd := float64(frameIdx+1) / float64(m.crossfadeTotalFrames)

	// Apply equal-power crossfade (or use single source if timed out)
	var mixed []float32
	if hasFrom && hasTo {
		m.crossfadeBuf = EqualPowerCrossfadeRanged(m.crossfadeBuf, m.crossfadePCM[m.crossfadeFrom], m.crossfadePCM[m.crossfadeTo], m.numChannels, posStart, posEnd)
		mixed = m.crossfadeBuf
	} else if hasTo {
		// Outgoing source timed out — use incoming source only
		mixed = m.crossfadePCM[m.crossfadeTo]
	} else {
		// Incoming source timed out — use outgoing source only (unusual)
		mixed = m.crossfadePCM[m.crossfadeFrom]
	}

	// Stinger audio overlay: add stinger PCM on top of source crossfade.
	// This path runs for the crossfade (transCrossfadeActive=true).
	// collectMixCycleLocked has its own injection point for the multi-source path.
	// Mutual exclusion: crossfade participants are routed here, not to collectMixCycleLocked.
	if m.stingerAudio != nil {
		m.addStingerAudio(mixed)
	}

	// Apply master gain (skip if unity — preserves passthrough optimization)
	if m.masterLinear != 1.0 && len(mixed) > 0 {
		vec.ScaleFloat32(&mixed[0], m.masterLinear, len(mixed))
	}

	// Feed LUFS meter (after master fader, before limiter — measures perceived loudness)
	m.loudness.Process(mixed)

	// Apply brickwall limiter at -1 dBFS (always active)
	m.limiter.Process(mixed)

	// Monotonic PTS: computed before MXL tap and encode so both receive the correct PTS.
	pts := m.advanceOutputPTS(inputPTS)

	// MXL output tap — copy mixed PCM after master processing (fader + limiter)
	if sinkPtr := m.rawAudioSink.Load(); sinkPtr != nil {
		m.mxlSinkBuf = growBuf(m.mxlSinkBuf, len(mixed))
		copy(m.mxlSinkBuf, mixed)
		(*sinkPtr)(m.mxlSinkBuf, pts, m.sampleRate, m.numChannels)
	}

	// Apply program mute (FTB held): zero the buffer so output is silent
	if m.programMuted {
		for i := range mixed {
			mixed[i] = 0
		}
	}

	// Apply unmute fade-in ramp: prevents uncompressed burst after
	// compressor/limiter envelopes were reset to zero during mute.
	if m.unmuteFadeRemaining > 0 {
		fadeSamples := m.unmuteFadeRemaining
		for i := range mixed {
			if fadeSamples <= 0 {
				break
			}
			// Linear ramp from 0 to 1 over the remaining fade samples
			rampTotal := m.sampleRate * m.numChannels * 5 / 1000
			progress := float32(rampTotal-fadeSamples) / float32(rampTotal)
			mixed[i] *= progress
			fadeSamples--
		}
		m.unmuteFadeRemaining = fadeSamples
	}

	// Update program peak metering (after mute so meters show silence during FTB)
	peakL, peakR := PeakLevel(mixed, m.numChannels)
	m.programPeakL = peakL
	m.programPeakR = peakR

	// Lazy-init encoder with priming (prevents MDCT warmup artifacts).
	if err := m.ensureEncoder(); err != nil || m.encoder == nil {
		m.crossfadeActive = false
		m.mu.Unlock()
		return
	}

	// Encode
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.encodeErrors.Add(1)
		if m.promMetrics != nil {
			m.promMetrics.EncodeErrorsTotal.Inc()
		}
		m.crossfadeActive = false
		m.mu.Unlock()
		m.log.Warn("encode error", "err", err)
		return
	}

	// Decrement multi-frame crossfade counter. If more frames remain,
	// reset the PCM map and deadline so we collect another frame pair.
	m.crossfadeFramesRemaining--
	if m.crossfadeFramesRemaining <= 0 {
		m.crossfadeActive = false
		m.crossfadePCM = nil
	} else {
		m.crossfadePCM = make(map[string][]float32)
		m.crossfadeDeadline = time.Now().Add(crossfadeTimeout)
	}

	// Build output frame before releasing lock
	outputFrame := &media.AudioFrame{
		PTS:        pts,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	}
	m.mu.Unlock()

	// Output outside the lock to avoid blocking other goroutines
	m.recordAndOutput(outputFrame)
}
