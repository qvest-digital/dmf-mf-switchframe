package audio

import (
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// IngestFrame processes an audio frame from a source.
func (m *Mixer) IngestFrame(sourceKey string, frame *media.AudioFrame) {
	// Apply per-source audio delay for lip-sync correction.
	// Done before all processing so PFL monitoring reflects corrected timing.
	m.mu.RLock()
	if ch, ok := m.channels[sourceKey]; ok && ch.audioDelay != nil && ch.audioDelay.DelayMs() > 0 {
		m.mu.RUnlock()
		frame = ch.audioDelay.Ingest(frame)
		if frame == nil {
			return // still filling delay buffer
		}
	} else {
		m.mu.RUnlock()
	}

	m.mu.RLock()
	crossfadeActive := m.crossfadeActive
	crossfadeFrom := m.crossfadeFrom
	crossfadeTo := m.crossfadeTo
	crossfadeDeadline := m.crossfadeDeadline
	m.mu.RUnlock()

	isParticipant := sourceKey == crossfadeFrom || sourceKey == crossfadeTo

	// Cancel expired crossfade if a non-participant source triggers it
	if crossfadeActive && !isParticipant && !crossfadeDeadline.IsZero() && time.Now().After(crossfadeDeadline) {
		m.mu.Lock()
		if m.crossfadeActive {
			m.crossfadeActive = false
			m.crossfadePCM = nil
		}
		m.mu.Unlock()
		crossfadeActive = false
	}

	// Handle crossfade mode (participants route here; timeout handled inside)
	if crossfadeActive && isParticipant {
		m.ingestCrossfadeFrame(sourceKey, frame)
		return
	}

	m.mu.RLock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.RUnlock()
		return
	}

	// For inactive/muted channels, decode for input metering only (no mixing).
	if !ch.active || ch.muted {
		m.mu.RUnlock()
		m.mu.Lock()
		m.initChannelDecoder(ch)
		if ch.decoder != nil {
			adtsFrame := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)
			if pcm, err := ch.decoder.Decode(adtsFrame); err == nil && len(pcm) > 0 {
				ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)
			}
		}
		m.mu.Unlock()
		return
	}

	if m.passthrough {
		m.mu.RUnlock()

		// Upgrade to write lock for metering. Re-check passthrough after
		// acquiring — between RUnlock and Lock another goroutine may have
		// changed it to false, which would cause both a passthrough frame
		// and a mixed frame for the same audio tick.
		m.mu.Lock()
		if !m.passthrough {
			// Passthrough was disabled while we waited for the write lock.
			// Fall through to the mixing path (we already hold m.mu.Lock).
			goto mixing
		}

		// Decode for peak metering even in passthrough (skip encode).
		m.initChannelDecoder(ch)
		if ch.decoder != nil {
			adtsFrame := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)
			if pcm, err := ch.decoder.Decode(adtsFrame); err == nil && len(pcm) > 0 {
				peakL, peakR := PeakLevel(pcm, m.numChannels)
				// In passthrough mode, channel peak and program peak are identical
				// (no fader/trim applied since passthrough requires 0dB on everything).
				m.programPeakL = peakL
				m.programPeakR = peakR
				ch.peakL, ch.peakR = peakL, peakR
				// Store a copy for crossfade pre-buffer even in passthrough.
				// Copy to avoid aliasing since decoder reuses its internal buffer.
				ch.storedBuf = growBuf(ch.storedBuf, len(pcm))
				copy(ch.storedBuf, pcm)
				m.lastDecodedPCM[sourceKey] = ch.storedBuf
			} else if err != nil {
				m.decodeErrors.Add(1)
				m.log.Warn("decode error", "source", sourceKey, "err", err)
			}
		}

		// Stamp output PTS from the monotonic counter — same clock as mixing mode.
		// Raw AAC bytes are still forwarded (zero CPU decode/encode), but the PTS
		// is continuous across passthrough↔mixing transitions.
		outFrame := &media.AudioFrame{
			PTS:        m.advanceOutputPTS(frame.PTS),
			Data:       frame.Data,
			SampleRate: frame.SampleRate,
			Channels:   frame.Channels,
		}
		m.mu.Unlock()

		m.recordAndOutput(outFrame)
		m.framesPassthrough.Add(1)
		if m.promMetrics != nil {
			m.promMetrics.PassthroughBypassTotal.Inc()
		}
		return
	}
	m.mu.RUnlock()

	// Multi-channel mixing: decode, gain, accumulate, sum, encode
	m.mu.Lock()
mixing:

	// Lazy-init decoder for this channel
	m.initChannelDecoder(ch)
	if ch.decoder == nil {
		m.mu.Unlock()
		return
	}

	// Ensure ADTS header is present — FDK decoder requires ADTS framing.
	adtsFrame := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	// Decode AAC → float32 PCM
	pcm, err := ch.decoder.Decode(adtsFrame)
	if err != nil {
		m.decodeErrors.Add(1)
		m.mu.Unlock()
		m.log.Warn("decode error", "source", sourceKey, "err", err)
		return
	}

	// Warn once per channel if the decoded frame's sample rate doesn't match
	// the mixer's configured rate. Wrong rates cause incorrect EQ, compressor,
	// and LUFS behavior because filter coefficients are sample-rate dependent.
	if frame.SampleRate > 0 && frame.SampleRate != m.sampleRate && !ch.sampleRateWarned {
		ch.sampleRateWarned = true
		m.log.Warn("audio: source sample rate mismatch",
			"source", sourceKey,
			"expected", m.sampleRate,
			"actual", frame.SampleRate)
	}

	// Update per-channel peaks (pre-fader, pre-gain)
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Store a copy of last decoded PCM for instant crossfade on future cuts.
	// Copy to avoid aliasing if decoder reuses its internal buffer.
	ch.storedBuf = growBuf(ch.storedBuf, len(pcm))
	copy(ch.storedBuf, pcm)
	m.lastDecodedPCM[sourceKey] = ch.storedBuf

	// Pipeline order: Trim -> EQ -> Compressor -> Fader -> Mix -> Master -> Limiter -> Encode

	// Apply trim (input gain)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Start the per-cycle deadline on first contribution.
	// Must happen before gain computation so mixCycleTransPos is snapshotted.
	if !m.mixStarted {
		m.mixStarted = true
		m.mixDeadline = time.Now().Add(mixCycleDeadline)
		// Snapshot transition position for this mix cycle so both participants
		// use the same target position and no video-driven updates cause jumps.
		if m.transCrossfadeActive {
			m.mixCycleTransPos = m.transCrossfadePosition
		}
	}

	// Apply fader level with per-sample transition interpolation
	faderGain := ch.levelLinear
	ch.gainBuf = growBuf(ch.gainBuf, len(trimmedPCM))
	gainedPCM := ch.gainBuf

	isTransParticipant := m.transCrossfadeActive &&
		(sourceKey == m.transCrossfadeFrom || sourceKey == m.transCrossfadeTo)

	if isTransParticipant {
		// Per-sample interpolation: ramp gain smoothly from the audio-tracked
		// position (end of previous mix cycle) to the snapshotted position for
		// this cycle. This eliminates gain discontinuities when multiple video
		// position updates happen between audio frames.
		var gainFn func(float64) float64
		if sourceKey == m.transCrossfadeFrom {
			gainFn = func(p float64) float64 { return transitionFromGain(m.transCrossfadeMode, p) }
		} else {
			gainFn = func(p float64) float64 { return transitionToGain(m.transCrossfadeMode, p) }
		}
		gStart := float32(gainFn(m.transCrossfadeAudioPos))
		gEnd := float32(gainFn(m.mixCycleTransPos))
		channels := m.numChannels
		pairCount := float32(len(trimmedPCM) / channels)
		for i, s := range trimmedPCM {
			t := float32(i/channels) / pairCount
			transGain := gStart + (gEnd-gStart)*t
			gainedPCM[i] = s * faderGain * transGain
		}
	} else {
		for i, s := range trimmedPCM {
			gainedPCM[i] = s * faderGain
		}
	}

	// Count active unmuted channels for this cycle
	activeUnmuted := 0
	for _, c := range m.channels {
		if c.active && !c.muted {
			activeUnmuted++
		}
	}

	// Mix on frame arrival: each source contributes its latest frame.
	m.mixBuffer[sourceKey] = gainedPCM
	// During transition crossfade, only the TO (incoming) source sets mixPTS.
	// The video transition engine outputs frames with the TO source's PTS,
	// so audio must match. Without this guard, the FROM source could overwrite
	// mixPTS if it ingests last, causing audio/video PTS mismatch.
	if !m.transCrossfadeActive || sourceKey == m.transCrossfadeTo {
		m.mixPTS = frame.PTS
	}

	// Flush when all active unmuted channels have contributed OR deadline exceeded
	var outputFrame *media.AudioFrame
	if len(m.mixBuffer) >= activeUnmuted {
		outputFrame = m.collectMixCycleLocked()
	}
	m.mu.Unlock()
	if outputFrame != nil {
		m.recordAndOutput(outputFrame)
	}
}

// IngestPCM processes raw interleaved float32 PCM from a source (e.g. MXL).
// Unlike IngestFrame, this skips ADTS parsing and AAC decoding — the PCM is
// already in float32 format. The processing pipeline is identical:
//
//	Peak metering → Store for crossfade → Trim → EQ → Compressor → Fader → Mix → collectMixCycle
//
// PCM input is interleaved float32 (e.g. 1024 samples * 2 channels = 2048 values for stereo).
// The pts parameter is the presentation timestamp in 90 kHz clock units.
// The channels parameter is the source's actual channel count (1=mono, 2=stereo).
// If channels < mixer's numChannels, mono samples are upmixed to stereo.
func (m *Mixer) IngestPCM(sourceKey string, pcm []float32, pts int64, channels int) {
	// Check for active crossfade before acquiring the write lock.
	m.mu.RLock()
	crossfadeActive := m.crossfadeActive
	crossfadeFrom := m.crossfadeFrom
	crossfadeTo := m.crossfadeTo
	crossfadeDeadline := m.crossfadeDeadline
	m.mu.RUnlock()

	isParticipant := sourceKey == crossfadeFrom || sourceKey == crossfadeTo

	// Cancel expired crossfade if a non-participant source triggers it
	if crossfadeActive && !isParticipant && !crossfadeDeadline.IsZero() && time.Now().After(crossfadeDeadline) {
		m.mu.Lock()
		if m.crossfadeActive {
			m.crossfadeActive = false
			m.crossfadePCM = nil
		}
		m.mu.Unlock()
		crossfadeActive = false
	}

	// Handle crossfade mode (participants route here; timeout handled inside)
	if crossfadeActive && isParticipant {
		m.ingestCrossfadePCM(sourceKey, pcm, pts, channels)
		return
	}

	m.mu.Lock()

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Mono→stereo upmix: if source delivers fewer channels than the mixer
	// expects, duplicate each sample to fill all channels.
	if channels > 0 && channels < m.numChannels {
		pcm = m.upmixMono(ch, pcm, channels)
	}

	// Always compute peak levels for input metering, even for inactive/muted channels.
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	if !ch.active || ch.muted {
		m.mu.Unlock()
		return
	}

	// Raw PCM cannot use passthrough (passthrough forwards raw AAC bytes).
	// Force mixing mode if currently in passthrough.
	if m.passthrough {
		m.passthrough = false
	}

	// Store a copy of PCM for instant crossfade on future cuts.
	ch.storedBuf = growBuf(ch.storedBuf, len(pcm))
	copy(ch.storedBuf, pcm)
	m.lastDecodedPCM[sourceKey] = ch.storedBuf

	// Pipeline order: Trim -> EQ -> Compressor -> Fader -> Mix -> Master -> Limiter -> Encode

	// Apply trim (input gain)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Start the per-cycle deadline on first contribution.
	if !m.mixStarted {
		m.mixStarted = true
		m.mixDeadline = time.Now().Add(mixCycleDeadline)
		if m.transCrossfadeActive {
			m.mixCycleTransPos = m.transCrossfadePosition
		}
	}

	// Apply fader level with per-sample transition interpolation
	faderGain := ch.levelLinear
	ch.gainBuf = growBuf(ch.gainBuf, len(trimmedPCM))
	gainedPCM := ch.gainBuf

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
		channels := m.numChannels
		pairCount := float32(len(trimmedPCM) / channels)
		for i, s := range trimmedPCM {
			t := float32(i/channels) / pairCount
			transGain := gStart + (gEnd-gStart)*t
			gainedPCM[i] = s * faderGain * transGain
		}
	} else {
		for i, s := range trimmedPCM {
			gainedPCM[i] = s * faderGain
		}
	}

	// Count active unmuted channels for this cycle
	activeUnmuted := 0
	for _, c := range m.channels {
		if c.active && !c.muted {
			activeUnmuted++
		}
	}

	// Mix on frame arrival: each source contributes its latest frame.
	m.mixBuffer[sourceKey] = gainedPCM
	// During transition crossfade, only the TO (incoming) source sets mixPTS.
	if !m.transCrossfadeActive || sourceKey == m.transCrossfadeTo {
		m.mixPTS = pts
	}

	// Flush when all active unmuted channels have contributed OR deadline exceeded
	var outputFrame *media.AudioFrame
	if len(m.mixBuffer) >= activeUnmuted {
		outputFrame = m.collectMixCycleLocked()
	}
	m.mu.Unlock()
	if outputFrame != nil {
		m.recordAndOutput(outputFrame)
	}
}
