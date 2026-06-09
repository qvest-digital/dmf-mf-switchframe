package audio

import (
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// aacPrimingSamples is the number of PCM samples FDK-AAC-LC silently discards
// before emitting valid output (one full MDCT frame).
const aacPrimingSamples = 1024

// IngestFrame processes an AAC audio frame from a source. Decodes to PCM,
// applies the per-channel processing chain (trim -> EQ -> compressor), and
// pushes the processed result into the channel's ring buffer for clock-driven
// output. No immediate output is produced -- the outputTicker reads from ring
// buffers at a fixed cadence.
func (m *Mixer) IngestFrame(sourceKey string, frame *media.AudioFrame) {
	// Audio forwarding: if this source is being forwarded to another channel
	// (e.g., live source → playout channel), ingest for the destination too.
	// Use ingestFrameInner to avoid infinite recursion (forwards don't chain).
	m.mu.RLock()
	fwdDst := m.audioForwards[sourceKey]
	m.mu.RUnlock()
	if fwdDst != "" {
		fwdFrame := *frame
		// Deep-copy Data to prevent aliasing — both paths decode/modify the slice.
		fwdFrame.Data = append([]byte(nil), frame.Data...)
		m.ingestFrameInner(fwdDst, &fwdFrame)
	}

	m.ingestFrameInner(sourceKey, frame)
}

func (m *Mixer) ingestFrameInner(sourceKey string, frame *media.AudioFrame) {
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

	m.mu.Lock()

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Always decode AAC -> PCM (no passthrough in clock-driven mixer)
	m.initChannelDecoder(ch)
	if ch.decoder == nil {
		m.mu.Unlock()
		return
	}

	// Ensure ADTS header is present -- FDK decoder requires ADTS framing.
	adtsFrame := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	// Decode AAC -> float32 PCM
	pcm, err := ch.decoder.Decode(adtsFrame)
	if err != nil {
		m.decodeErrors.Add(1)
		m.mu.Unlock()
		m.log.Warn("decode error", "source", sourceKey, "err", err)
		return
	}

	// FDK-AAC-LC silently discards 1–2 priming frames while the MDCT overlap
	// buffer fills. Without compensation, valid PCM lands in the ring buffer
	// earlier than the source timeline expects — audio plays ahead of video.
	// On the first valid emission, prepend one silence frame per observed
	// priming skip, scaled to mixer channels and (when the source rate differs
	// from the mixer rate) the mixer's sample rate. Silence pushed before the
	// real PCM, so the ring-buffer timeline matches the source timeline.
	if len(pcm) == 0 {
		if !ch.primingOffsetApplied {
			ch.primingSkipCount++
		}
		m.mu.Unlock()
		return
	}
	if ch.primingSkipCount > 0 && !ch.primingOffsetApplied {
		ch.primingOffsetApplied = true
		silencePerFrame := aacPrimingSamples
		if frame.SampleRate != 0 && frame.SampleRate != m.sampleRate {
			silencePerFrame = aacPrimingSamples * m.sampleRate / frame.SampleRate
		}
		ch.ringBuf.Push(make([]float32, silencePerFrame*m.numChannels*ch.primingSkipCount))
	}

	// Surround → mixer-channels downmix: if the bitstream decoded to more
	// channels than the mixer supports, fold before the resampler and ring
	// buffer, which are both sized for m.numChannels.
	srcChannels := ch.decoder.Channels()
	if srcChannels > m.numChannels {
		var result downmixResult
		pcm, result = m.downmixChannels(&ch.downmixBuf, pcm, srcChannels)
		if !ch.surroundWarned {
			ch.surroundWarned = true
			m.logDownmixWarn(sourceKey, "AAC", srcChannels, result)
		}
	}

	// Resample if source rate doesn't match mixer rate.
	pcm = m.resampleIfNeeded(ch, pcm, frame.SampleRate)

	// Muted channels skip ring buffer entirely.
	if ch.muted {
		ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)
		m.mu.Unlock()
		return
	}

	// Update per-channel peaks (pre-fader, pre-gain)
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Pipeline order: Trim -> Phase -> HPF -> Gate -> EQ -> Compressor -> Balance -> [push to ring buffer]
	// Fader gain and transition gain are applied by the ticker at read time.

	// Apply trim (input gain)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}

	// Apply phase invert
	if ch.phaseInvert {
		for i := range trimmedPCM {
			trimmedPCM[i] = -trimmedPCM[i]
		}
	}

	// Apply high-pass filter
	if !ch.hpf.IsBypassed() {
		ch.hpf.Process(trimmedPCM, m.numChannels)
	}

	// Apply noise gate
	if !ch.gate.IsBypassed() {
		ch.gate.Process(trimmedPCM)
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Apply stereo balance (after compressor, before ring buffer push).
	// At center (0.0) both gains are 1.0, so this is a no-op.
	// Only apply when numChannels >= 2 — balance is a stereo concept.
	if m.numChannels >= 2 && (ch.balL != 1.0 || ch.balR != 1.0) {
		stride := m.numChannels
		for i := 0; i+1 < len(trimmedPCM); i += stride {
			trimmedPCM[i] *= ch.balL
			trimmedPCM[i+1] *= ch.balR
		}
	}

	// Push processed PCM to ring buffer for clock-driven output.
	ch.ringBuf.Push(trimmedPCM)
	m.mu.Unlock()
}

// IngestPCM processes raw interleaved float32 PCM from a source (e.g. MXL).
// Unlike IngestFrame, this skips ADTS parsing and AAC decoding -- the PCM is
// already in float32 format. Applies the per-channel processing chain
// (trim -> EQ -> compressor) and pushes into the channel's ring buffer.
//
// PCM input is interleaved float32 (e.g. 1024 samples * 2 channels = 2048 values for stereo).
// The pts parameter is the presentation timestamp in 90 kHz clock units.
// The channels parameter is the source's actual channel count (1=mono, 2=stereo).
// If channels < mixer's numChannels, mono samples are upmixed to stereo.
func (m *Mixer) IngestPCM(sourceKey string, pcm []float32, pts int64, channels int) {
	// Audio forwarding (same pattern as IngestFrame).
	m.mu.RLock()
	fwdDst := m.audioForwards[sourceKey]
	m.mu.RUnlock()
	if fwdDst != "" {
		// Deep-copy PCM to prevent aliasing.
		fwdPCM := make([]float32, len(pcm))
		copy(fwdPCM, pcm)
		m.ingestPCMInner(fwdDst, fwdPCM, pts, channels)
	}
	m.ingestPCMInner(sourceKey, pcm, pts, channels)
}

func (m *Mixer) ingestPCMInner(sourceKey string, pcm []float32, pts int64, channels int) {
	m.mu.Lock()

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Mono->stereo upmix: if source delivers fewer channels than the mixer
	// expects, duplicate each sample to fill all channels.
	if channels > 0 && channels < m.numChannels {
		pcm = m.upmixMono(&ch.upmixBuf, pcm, channels)
	}

	// Surround → mixer-channels downmix: symmetric to upmix above. A 5.1 PCM
	// source pushed into a stereo mixer would otherwise be read with the
	// wrong stride by the per-channel DSP chain and the ring buffer.
	if channels > m.numChannels {
		var result downmixResult
		pcm, result = m.downmixChannels(&ch.downmixBuf, pcm, channels)
		if !ch.surroundWarned {
			ch.surroundWarned = true
			m.logDownmixWarn(sourceKey, "PCM", channels, result)
		}
	}

	// Always compute peak levels for input metering, even for inactive/muted channels.
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Muted channels skip ring buffer entirely — there's no unmute crossfade.
	if ch.muted {
		m.mu.Unlock()
		return
	}

	// Mark channel as PCM source (no passthrough recovery needed in clock-driven mixer).
	if !ch.isPCM {
		ch.isPCM = true
	}

	// Resample if source rate doesn't match mixer rate.
	pcm = m.resampleIfNeeded(ch, pcm, 0) // PCM sources don't carry sample rate in-band

	// Pipeline order: Trim -> Phase -> HPF -> Gate -> EQ -> Compressor -> Balance -> [push to ring buffer]
	// Fader gain and transition gain are applied by the ticker at read time.

	// Apply trim (input gain)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}

	// Apply phase invert
	if ch.phaseInvert {
		for i := range trimmedPCM {
			trimmedPCM[i] = -trimmedPCM[i]
		}
	}

	// Apply high-pass filter
	if !ch.hpf.IsBypassed() {
		ch.hpf.Process(trimmedPCM, m.numChannels)
	}

	// Apply noise gate
	if !ch.gate.IsBypassed() {
		ch.gate.Process(trimmedPCM)
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Apply stereo balance (after compressor, before ring buffer push).
	// At center (0.0) both gains are 1.0, so this is a no-op.
	// Only apply when numChannels >= 2 — balance is a stereo concept.
	if m.numChannels >= 2 && (ch.balL != 1.0 || ch.balR != 1.0) {
		stride := m.numChannels
		for i := 0; i+1 < len(trimmedPCM); i += stride {
			trimmedPCM[i] *= ch.balL
			trimmedPCM[i+1] *= ch.balR
		}
	}

	// Always push to ring buffer, even for inactive channels. This keeps the
	// buffer primed so that when a channel activates via AFV, there is no
	// silence gap from an empty ring buffer. The tick() loop only reads from
	// active channels, so inactive data won't be mixed into output.
	ch.ringBuf.Push(trimmedPCM)
	m.mu.Unlock()
}
