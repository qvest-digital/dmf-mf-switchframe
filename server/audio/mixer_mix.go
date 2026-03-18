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
// When resampling produces non-1024-sample PCM, the raw mix is accumulated in
// m.encodeBuf and drained in exact 1024-sample frames. Each chunk is independently
// processed through the full master chain (gain → LUFS → limiter → encode) to
// avoid double-processing artifacts.
//
// Caller must hold m.mu write lock. In the multi-frame resampling path, the
// lock is temporarily dropped to call m.recordAndOutput() for intermediate
// frames, then reacquired. The lock is always held on return.
// Callers are responsible for calling m.output() after releasing the lock.
func (m *Mixer) collectMixCycleLocked() *media.AudioFrame {
	if len(m.mixBuffer) == 0 {
		m.resetMixCycleLocked()
		return nil
	}

	// Diagnostic logging during transition crossfade
	if m.transCrossfadeActive {
		sources := make([]string, 0, len(m.mixBuffer))
		lengths := make([]int, 0, len(m.mixBuffer))
		for k, v := range m.mixBuffer {
			sources = append(sources, k)
			lengths = append(lengths, len(v))
		}
		m.log.Info("mix-cycle-diag",
			"sources", sources,
			"pcm_lengths", lengths,
			"encodeBuf_len", len(m.encodeBuf),
			"trans_pos", m.mixCycleTransPos,
			"audio_pos", m.transCrossfadeAudioPos,
			"from", m.transCrossfadeFrom,
			"to", m.transCrossfadeTo,
		)
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

	// Stinger audio overlay
	if m.stingerAudio != nil {
		m.addStingerAudio(mixed)
	}

	// Lazy-init encoder with priming (prevents MDCT warmup artifacts).
	if err := m.ensureEncoder(); err != nil || m.encoder == nil {
		m.resetMixCycleLocked()
		return nil
	}

	aacFrameSamples := 1024 * m.numChannels
	pts := m.advanceOutputPTS(m.mixPTS)

	if len(m.encodeBuf) > 0 || len(mixed) > aacFrameSamples {
		// Buffered path: resampling produced non-standard frame size.
		// Accumulate raw mixed PCM (pre-master) and drain in 1024-sample chunks.
		// Each chunk goes through the full master chain independently.
		m.encodeBuf = append(m.encodeBuf, mixed...)

		var frames []*media.AudioFrame

		for len(m.encodeBuf) >= aacFrameSamples {
			chunk := m.encodeBuf[:aacFrameSamples]

			// Apply full master chain to this chunk
			m.applyMasterChain(chunk, pts)

			aacData, err := m.encoder.Encode(chunk)
			if err != nil {
				m.encodeErrors.Add(1)
				if m.promMetrics != nil {
					m.promMetrics.EncodeErrorsTotal.Inc()
				}
				m.log.Warn("encode error", "err", err)
				m.encodeBuf = m.encodeBuf[aacFrameSamples:]
				continue
			}
			m.framesMixed.Add(1)
			if m.promMetrics != nil {
				m.promMetrics.FramesMixedTotal.Inc()
			}

			framePTS := pts
			if len(frames) > 0 {
				framePTS = m.advanceOutputPTS(pts)
			}
			frames = append(frames, &media.AudioFrame{
				PTS:        framePTS,
				Data:       aacData,
				SampleRate: m.sampleRate,
				Channels:   m.numChannels,
			})
			m.encodeBuf = m.encodeBuf[aacFrameSamples:]
		}

		// Advance audio position tracking
		if m.transCrossfadeActive {
			m.transCrossfadeAudioPos = m.mixCycleTransPos
		}
		m.resetMixCycleLocked()
		mixDur := time.Now().UnixNano() - mixStart
		m.lastMixCycleNs.Store(mixDur)
		atomicutil.UpdateMax(&m.maxMixCycleNs, mixDur)

		if len(frames) == 0 {
			return nil
		}

		// Output all but the last frame directly. The lock is dropped
		// during each output call to avoid holding it across I/O, then
		// reacquired before proceeding.
		for _, f := range frames[:len(frames)-1] {
			m.mu.Unlock()
			m.recordAndOutput(f)
			m.mu.Lock()
		}
		// Return the last frame for the caller to output after releasing lock
		return frames[len(frames)-1]
	}

	// Direct path: mixed ≤ 1024 samples (normal case, no resampling active)
	m.applyMasterChain(mixed, pts)

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

	if m.transCrossfadeActive {
		m.transCrossfadeAudioPos = m.mixCycleTransPos
	}
	m.resetMixCycleLocked()
	mixDur := time.Now().UnixNano() - mixStart
	m.lastMixCycleNs.Store(mixDur)
	atomicutil.UpdateMax(&m.maxMixCycleNs, mixDur)

	return &media.AudioFrame{
		PTS:        pts,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	}
}

// applyMasterChain runs the master output processing on a PCM buffer:
// master gain → LUFS metering → limiter → MXL tap → program mute → unmute ramp → peak metering.
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

// resetMixCycleLocked clears the mix accumulation state for the next cycle.
// Caller must hold m.mu write lock.
func (m *Mixer) resetMixCycleLocked() {
	clear(m.mixBuffer)
	m.mixStarted = false
	m.mixDeadline = time.Time{}
}
