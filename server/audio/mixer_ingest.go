package audio

import (
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// IngestFrame processes an AAC audio frame from a source. Decodes to PCM,
// applies the per-channel processing chain (trim -> EQ -> compressor), and
// pushes the processed result into the channel's ring buffer for clock-driven
// output. No immediate output is produced -- the outputTicker reads from ring
// buffers at a fixed cadence.
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

	// Resample if source rate doesn't match mixer rate.
	pcm = m.resampleIfNeeded(ch, pcm, frame.SampleRate)

	// For inactive/muted channels, do metering only -- don't push to ring buffer.
	if !ch.active || ch.muted {
		ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)
		m.mu.Unlock()
		return
	}

	// Update per-channel peaks (pre-fader, pre-gain)
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Store a copy of last decoded PCM for instant crossfade on future cuts.
	// Copy to avoid aliasing if decoder reuses its internal buffer.
	ch.storedBuf = growBuf(ch.storedBuf, len(pcm))
	copy(ch.storedBuf, pcm)
	m.lastDecodedPCM[sourceKey] = ch.storedBuf

	// Pipeline order: Trim -> EQ -> Compressor -> [push to ring buffer]
	// Fader gain and transition gain are applied by the ticker at read time.

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
	m.mu.Lock()

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Mono->stereo upmix: if source delivers fewer channels than the mixer
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

	// Mark channel as PCM source (no passthrough recovery needed in clock-driven mixer).
	if !ch.isPCM {
		ch.isPCM = true
	}

	// Resample if source rate doesn't match mixer rate.
	pcm = m.resampleIfNeeded(ch, pcm, 0) // PCM sources don't carry sample rate in-band

	// Store a copy of PCM for instant crossfade on future cuts.
	ch.storedBuf = growBuf(ch.storedBuf, len(pcm))
	copy(ch.storedBuf, pcm)
	m.lastDecodedPCM[sourceKey] = ch.storedBuf

	// Pipeline order: Trim -> EQ -> Compressor -> [push to ring buffer]
	// Fader gain and transition gain are applied by the ticker at read time.

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

	// Push processed PCM to ring buffer for clock-driven output.
	ch.ringBuf.Push(trimmedPCM)
	m.mu.Unlock()
}
