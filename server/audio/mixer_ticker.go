package audio

import (
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio/vec"
)

// outputTicker runs the clock-driven audio output loop.
// Produces one AAC frame per tick at sampleRate/1024 Hz (~21.3ms at 48kHz).
// This goroutine is the sole producer of mixed audio output in the new
// clock-driven architecture. It reads from per-channel ring buffers
// (populated by the ingest path) and produces output at a fixed cadence
// regardless of when source frames arrive.
func (m *Mixer) outputTicker() {
	defer m.tickerWg.Done()

	interval := time.Duration(float64(time.Second) * 1024.0 / float64(m.sampleRate))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopTicker:
			return
		case <-ticker.C:
			frame := m.tick()
			if frame != nil {
				m.recordAndOutput(frame)
			}
		}
	}
}

// tick produces one output frame by reading from all active channel ring buffers.
// Returns nil if no output should be produced (e.g., no encoder available).
// Caller must NOT hold m.mu.
func (m *Mixer) tick() *media.AudioFrame {
	m.mu.Lock()

	aacFrameSamples := 1024 * m.numChannels

	// Ensure encoder is available
	if err := m.ensureEncoder(); err != nil || m.encoder == nil {
		m.mu.Unlock()
		return nil
	}

	// Snapshot transition position for this tick
	if m.transCrossfadeActive {
		m.mixCycleTransPos = m.transCrossfadePosition
	}

	// Read from each active channel's ring buffer and apply gain
	m.mixAccum = growBuf(m.mixAccum, aacFrameSamples)
	for i := range m.mixAccum[:aacFrameSamples] {
		m.mixAccum[i] = 0
	}

	hasOutput := false
	for sourceKey, ch := range m.channels {
		if !ch.active || ch.muted || ch.ringBuf == nil {
			continue
		}

		pcm := ch.ringBuf.Pop(aacFrameSamples)
		if pcm == nil {
			continue // never pushed — skip
		}
		hasOutput = true

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
		if n > aacFrameSamples {
			n = aacFrameSamples
		}
		if n > 0 {
			vec.AddFloat32(&m.mixAccum[0], &gained[0], n)
		}
	}

	// Stinger audio overlay
	if m.stingerAudio != nil {
		m.addStingerAudio(m.mixAccum[:aacFrameSamples])
	}

	// Advance transition audio position
	if m.transCrossfadeActive {
		m.transCrossfadeAudioPos = m.mixCycleTransPos
	}

	// Auto-advance cut crossfade position (driven by frame counter, not timer).
	// This runs after the mix so the current tick used the position set above.
	if m.cutFramesRemaining > 0 {
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
		}
	}

	// PTS
	pts := m.advanceOutputPTS(0)

	// Apply master chain (gain -> LUFS -> limiter -> mute -> metering)
	mixed := m.mixAccum[:aacFrameSamples]
	m.applyMasterChain(mixed, pts)

	// Encode
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.encodeErrors.Add(1)
		m.mu.Unlock()
		return nil
	}
	m.framesMixed.Add(1)

	// Track whether we had real audio data for diagnostics
	_ = hasOutput

	m.mu.Unlock()

	return &media.AudioFrame{
		PTS:        pts,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	}
}
