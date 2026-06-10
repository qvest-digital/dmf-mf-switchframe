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

	// next is the deadline of the next output frame. time.Ticker's channel
	// has a capacity of 1, so when a tick overruns the interval (slow cgo
	// encode, lock convoy with ingest decodes) the missed ticks are silently
	// coalesced — without explicit deadline accounting each missed tick would
	// be ~21.3ms of program audio permanently dropped. Tracking the frame
	// deadline lets a late tick produce the frames it missed (the backlog is
	// sitting in the per-channel ring buffers).
	next := time.Now().Add(interval)
	for {
		select {
		case <-m.stopTicker:
			return
		case <-ticker.C:
			var frames, dropped int
			frames, dropped, next = tickerCatchUp(time.Now(), next, interval)
			if dropped > 0 {
				m.tickerFramesDropped.Add(int64(dropped))
			}
			for i := 0; i < frames; i++ {
				frame := m.tick()
				if frame != nil {
					m.recordAndOutput(frame)
				}
			}
		}
	}
}

// maxTickerCatchUpFrames bounds the catch-up burst after a long stall
// (~0.5s of output at 48kHz). The per-channel ring buffers hold ~10 frames
// of input, so far beyond that a burst would only emit silence and flood
// downstream consumers; instead the backlog is dropped and the deadline
// realigned to the present.
const maxTickerCatchUpFrames = 24

// tickerCatchUp returns how many output frames are due for a ticker fire at
// time now given the next frame deadline, how many overdue frames were
// dropped by the burst cap, plus the updated deadline. A fire at or past the
// deadline owes one frame per elapsed interval (minimum one); a buffered tick
// delivered before the deadline (possible right after a catch-up burst) owes
// none. Bursts are capped at maxTickerCatchUpFrames, after which the deadline
// resyncs to now (e.g. system suspend) and the excess is reported as dropped.
func tickerCatchUp(now, next time.Time, interval time.Duration) (frames, dropped int, newNext time.Time) {
	if now.Before(next) {
		return 0, 0, next
	}
	frames = 1 + int(now.Sub(next)/interval)
	if frames > maxTickerCatchUpFrames {
		return maxTickerCatchUpFrames, frames - maxTickerCatchUpFrames, now.Add(interval)
	}
	return frames, 0, next.Add(time.Duration(frames) * interval)
}

// tick produces one output frame by reading from all active channel ring buffers.
// Returns nil if no output should be produced (e.g., no encoder available).
// Caller must NOT hold m.mu.
func (m *Mixer) tick() *media.AudioFrame {
	m.mu.Lock()

	aacFrameSamples := 1024 * m.numChannels

	// Ensure encoder is available. If no encoder, still advance the cut
	// crossfade state machine so AFV deferred deactivation completes.
	if err := m.ensureEncoder(); err != nil || m.encoder == nil {
		m.advanceCutCrossfade()
		m.mu.Unlock()
		return nil
	}

	// Produce mixed PCM samples (gains, crossfade, stinger)
	mixed := m.produceSamplesLocked(aacFrameSamples)

	// Auto-advance cut crossfade position (driven by frame counter, not timer).
	// This runs after the mix so the current tick used the position set above.
	m.advanceCutCrossfade()

	// PTS
	pts := m.advanceOutputPTS(0)

	// Apply master chain (gain -> LUFS -> limiter -> mute -> metering)
	m.applyMasterChain(mixed, pts)

	// Encode
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.encodeErrors.Add(1)
		m.mu.Unlock()
		return nil
	}
	m.framesMixed.Add(1)

	m.mu.Unlock()

	return &media.AudioFrame{
		PTS:        pts,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	}
}

// produceSamplesLocked reads from all active channel ring buffers, applies
// per-channel gains, crossfade, and stinger audio overlay. Returns
// totalValues float32 samples of mixed PCM. Does NOT apply the master
// output chain or advance PTS — callers handle those steps because:
//   - tick() needs real PTS for the MXL sink in applyMasterChain
//   - ProduceSamples() applies the master chain with PTS=0 (caller manages PTS)
//
// This is the core mixing logic shared by both:
//   - tick() — the clock-driven output ticker (1024 samples per AAC frame)
//   - ProduceSamples() — on-demand production for the reference clock path
//
// totalValues is in interleaved sample values (samples * channels).
// Caller must hold m.mu.
func (m *Mixer) produceSamplesLocked(totalValues int) []float32 {
	// Snapshot transition position for this mix cycle
	if m.transCrossfadeActive {
		m.mixCycleTransPos = m.transCrossfadePosition
	}

	// Read from each active channel's ring buffer and apply gain
	m.mixAccum = growBuf(m.mixAccum, totalValues)
	for i := range m.mixAccum[:totalValues] {
		m.mixAccum[i] = 0
	}

	// Measure ring buffer depth of active channels before reading.
	// With drift compensation, depth stays near the 2-frame target.
	// This measurement is retained for diagnostics and debug snapshot.
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

		pcm := ch.ringBuf.PopDriftCompensated(totalValues, m.driftTarget(), m.numChannels)
		if pcm == nil {
			continue // never pushed — skip
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

// advanceCutCrossfade advances the cut crossfade state machine by one tick.
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
