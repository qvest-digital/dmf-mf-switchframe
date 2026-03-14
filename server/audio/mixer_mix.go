package audio

import (
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio/vec"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
)

// collectMixCycleLocked sums the accumulated mix buffers, applies master gain,
// program mute, metering, encodes to AAC, and returns the output frame.
// Returns nil if there is nothing to output (empty buffer, encoder error, etc.).
//
// Caller must hold m.mu write lock. The lock is held for the entire call.
// Callers are responsible for calling m.output() after releasing the lock.
func (m *Mixer) collectMixCycleLocked() *media.AudioFrame {
	if len(m.mixBuffer) == 0 {
		m.resetMixCycleLocked()
		return nil
	}

	mixStart := time.Now().UnixNano()

	// Sum all channel PCM buffers using reusable accumulator
	var mixLen int
	for _, buf := range m.mixBuffer {
		if len(buf) > mixLen {
			mixLen = len(buf)
		}
	}
	m.mixAccum = growBuf(m.mixAccum, mixLen)
	for i := range m.mixAccum {
		m.mixAccum[i] = 0
	}
	for _, buf := range m.mixBuffer {
		n := len(buf)
		if n > mixLen {
			n = mixLen
		}
		if n > 0 {
			vec.AddFloat32(&m.mixAccum[0], &buf[0], n)
		}
	}
	mixed := m.mixAccum

	// Stinger audio overlay: add stinger PCM on top of source crossfade.
	// This path runs for multi-source mixing (passthrough disabled, no crossfade).
	// The crossfade path in ingestCrossfadeFrame has its own injection point.
	// Mutual exclusion: when transCrossfadeActive is true, sources participating
	// in the crossfade go through ingestCrossfadeFrame instead of collectMixCycleLocked.
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
	pts := m.advanceOutputPTS(m.mixPTS)

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
		rampTotal := m.sampleRate * m.numChannels * 5 / 1000
		ch := m.numChannels
		for i := 0; i < len(mixed); i += ch {
			if fadeSamples <= 0 {
				break
			}
			// Linear ramp from 0 to 1 over the remaining fade samples
			progress := float32(rampTotal-fadeSamples) / float32(rampTotal)
			for j := 0; j < ch && i+j < len(mixed); j++ {
				mixed[i+j] *= progress
			}
			fadeSamples -= ch
		}
		if fadeSamples < 0 {
			fadeSamples = 0
		}
		m.unmuteFadeRemaining = fadeSamples
	}

	// Update program peak metering (after mute so meters show silence)
	peakL, peakR := PeakLevel(mixed, m.numChannels)
	m.programPeakL = peakL
	m.programPeakR = peakR

	// Lazy-init encoder with priming (prevents MDCT warmup artifacts).
	if err := m.ensureEncoder(); err != nil || m.encoder == nil {
		m.resetMixCycleLocked()
		return nil
	}

	// Encode mixed PCM -> AAC
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.encodeErrors.Add(1)
		if m.promMetrics != nil {
			m.promMetrics.EncodeErrorsTotal.Inc()
		}
		m.resetMixCycleLocked()
		m.log.Warn("encode error", "err", err)
		return nil
	}
	m.framesMixed.Add(1)
	if m.promMetrics != nil {
		m.promMetrics.FramesMixedTotal.Inc()
	}

	// Advance audio position tracking so the next cycle's start gain
	// matches this cycle's end gain (continuous gain envelope).
	if m.transCrossfadeActive {
		m.transCrossfadeAudioPos = m.mixCycleTransPos
	}

	// Reset mix cycle for next round
	m.resetMixCycleLocked()

	// Record mix cycle timing
	mixDur := time.Now().UnixNano() - mixStart
	m.lastMixCycleNs.Store(mixDur)
	atomicutil.UpdateMax(&m.maxMixCycleNs, mixDur)

	// Build output frame — caller will output after releasing the lock
	return &media.AudioFrame{
		PTS:        pts,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	}
}

// resetMixCycleLocked clears the mix accumulation state for the next cycle.
// Caller must hold m.mu write lock.
func (m *Mixer) resetMixCycleLocked() {
	clear(m.mixBuffer)
	m.mixStarted = false
	m.mixDeadline = time.Time{}
}
