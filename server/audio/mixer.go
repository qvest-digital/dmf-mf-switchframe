package audio

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/metrics"
)

// crossfadeTimeout is the maximum time to wait for both sources to deliver
// frames during a crossfade. If the outgoing source disconnects, the crossfade
// completes with only the incoming source's audio after this deadline.
// crossfadeTimeout is reduced from 50ms to 25ms because PCM pre-buffering
// eliminates the need to wait for the outgoing source. Only the incoming
// source needs to deliver a frame, so one AAC frame (~21.3ms) is sufficient.
const crossfadeTimeout = 25 * time.Millisecond

// MixerConfig configures the AudioMixer.
type MixerConfig struct {
	SampleRate     int
	Channels       int
	Output         func(*media.AudioFrame)
	DecoderFactory DecoderFactory // nil = passthrough only (no mixing)
	EncoderFactory EncoderFactory // nil = passthrough only (no mixing)
}

// Channel tracks per-source audio state.
type Channel struct {
	sourceKey   string
	level       float64 // dB (-inf to +12), fader level
	trim        float64 // dB (-20 to +20), input gain/trim
	muted       bool
	afv         bool
	active      bool
	decoder     AudioDecoder // lazy init, nil in passthrough
	decoderOnce sync.Once    // ensures decoder factory is called at most once
	peakL       float64      // linear amplitude [0,1] — updated on every decoded frame
	peakR       float64      // linear amplitude [0,1]
	eq          *EQ          // 3-band parametric EQ (always initialized)
	compressor  *Compressor  // single-band compressor (always initialized)
}

// AudioMixer mixes audio from multiple sources.
type AudioMixer struct {
	mu          sync.RWMutex
	channels    map[string]*Channel
	masterLevel float64 // dB, default 0.0
	sampleRate  int
	numChannels int
	encoder     AudioEncoder
	output      func(*media.AudioFrame)
	passthrough bool
	config      MixerConfig

	// Mix accumulation state: tracks which active unmuted channels
	// have contributed to the current mix cycle.
	mixBuffer   map[string][]float32 // sourceKey → decoded PCM for current cycle
	mixPTS      int64                // PTS of the current mix cycle
	mixStarted  bool                 // true when at least one channel has contributed
	mixDeadline time.Time            // deadline for current mix cycle

	// Background ticker for deadline enforcement
	stopTicker chan struct{}
	tickerWg   sync.WaitGroup

	// Pre-buffered PCM: last decoded frame per source for instant crossfade.
	lastDecodedPCM map[string][]float32

	// Crossfade state: one AAC frame (~23ms) equal-power crossfade on cut.
	crossfadeFrom     string               // outgoing source key
	crossfadeTo       string               // incoming source key
	crossfadeActive   bool
	crossfadePCM      map[string][]float32 // "from" and "to" PCM buffers
	crossfadeDeadline time.Time            // timeout for crossfade completion

	// Transition crossfade state: multi-frame crossfade synced with video transition.
	transCrossfadeActive   bool
	transCrossfadeFrom     string                      // outgoing source key
	transCrossfadeTo       string                      // incoming source key
	transCrossfadePosition float64                     // 0.0 = fully old, 1.0 = fully new
	transCrossfadeMode     internal.AudioTransitionMode // gain curve selection
	transCrossfadePrevPos  float64                     // previous position for per-sample interpolation

	// Program mute: true while FTB is held (screen is black, audio is silent).
	programMuted bool

	// Program bus limiter (always active)
	limiter *Limiter

	// Metering state
	programPeakL float64 // linear amplitude [0,1]
	programPeakR float64 // linear amplitude [0,1]

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// Debug counters (atomic, no lock needed)
	framesPassthrough atomic.Int64
	framesMixed       atomic.Int64
	crossfadeCount    atomic.Int64
	crossfadeTimeouts atomic.Int64
	decodeErrors      atomic.Int64
	encodeErrors      atomic.Int64
}

// mixCycleDeadline is the maximum time to wait for all active channels to
// contribute a frame before producing output with whatever is available.
// Prevents deadlock when a source stops sending audio.
const mixCycleDeadline = 50 * time.Millisecond

// NewMixer creates an AudioMixer.
func NewMixer(config MixerConfig) *AudioMixer {
	m := &AudioMixer{
		channels:       make(map[string]*Channel),
		masterLevel:    0.0,
		sampleRate:     config.SampleRate,
		numChannels:    config.Channels,
		output:         config.Output,
		passthrough:    true,
		config:         config,
		stopTicker:     make(chan struct{}),
		limiter:        NewLimiter(config.SampleRate),
		lastDecodedPCM: make(map[string][]float32),
	}
	m.tickerWg.Add(1)
	go m.mixDeadlineTicker()
	return m
}

// SetMetrics attaches Prometheus metrics to the mixer.
func (m *AudioMixer) SetMetrics(pm *metrics.Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promMetrics = pm
}

// Close releases all codec resources and stops the background ticker.
func (m *AudioMixer) Close() error {
	close(m.stopTicker)
	m.tickerWg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.channels {
		if ch.decoder != nil {
			ch.decoder.Close()
		}
	}
	if m.encoder != nil {
		m.encoder.Close()
	}
	return nil
}

// initChannelDecoder ensures the channel's AAC decoder is initialized exactly
// once using sync.Once. If the factory returns an error, ch.decoder remains nil
// and callers must handle that (all call sites already check ch.decoder != nil).
// If ch.decoder was set externally (e.g., in tests), this is a no-op.
// Caller must hold m.mu (read or write).
func (m *AudioMixer) initChannelDecoder(ch *Channel) {
	if ch.decoder != nil || m.config.DecoderFactory == nil {
		return
	}
	ch.decoderOnce.Do(func() {
		dec, err := m.config.DecoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			slog.Warn("mixer: decoder factory error", "source", ch.sourceKey, "err", err)
			return
		}
		ch.decoder = dec
	})
}

// mixDeadlineTicker runs in the background and forces a mix cycle flush
// when the per-cycle deadline expires. This prevents deadlock when a source
// stops sending audio while the mixer waits for all channels to contribute.
func (m *AudioMixer) mixDeadlineTicker() {
	defer m.tickerWg.Done()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopTicker:
			return
		case <-ticker.C:
			m.mu.Lock()
			var outputFrame *media.AudioFrame
			if m.mixStarted && !m.mixDeadline.IsZero() && time.Now().After(m.mixDeadline) {
				outputFrame = m.collectMixCycleLocked()
			}
			m.mu.Unlock()
			if outputFrame != nil {
				m.output(outputFrame)
			}
		}
	}
}

// collectMixCycleLocked sums the accumulated mix buffers, applies master gain,
// program mute, metering, encodes to AAC, and returns the output frame.
// Returns nil if there is nothing to output (empty buffer, encoder error, etc.).
//
// Caller must hold m.mu write lock. The lock is held for the entire call.
// Callers are responsible for calling m.output() after releasing the lock.
func (m *AudioMixer) collectMixCycleLocked() *media.AudioFrame {
	if m.mixBuffer == nil || len(m.mixBuffer) == 0 {
		m.resetMixCycleLocked()
		return nil
	}

	// Sum all channel PCM buffers
	var mixLen int
	for _, buf := range m.mixBuffer {
		if len(buf) > mixLen {
			mixLen = len(buf)
		}
	}
	mixed := make([]float32, mixLen)
	for _, buf := range m.mixBuffer {
		for i := 0; i < len(buf) && i < mixLen; i++ {
			mixed[i] += buf[i]
		}
	}

	// Apply master gain
	masterGain := float32(DBToLinear(m.masterLevel))
	for i := range mixed {
		mixed[i] *= masterGain
	}

	// Apply brickwall limiter at -1 dBFS (always active)
	m.limiter.Process(mixed)

	// Apply program mute (FTB held): zero the buffer so output is silent
	if m.programMuted {
		for i := range mixed {
			mixed[i] = 0
		}
	}

	// Update program peak metering (after mute so meters show silence)
	peakL, peakR := PeakLevel(mixed, m.numChannels)
	m.programPeakL = peakL
	m.programPeakR = peakR

	// Lazy-init encoder
	if m.encoder == nil && m.config.EncoderFactory != nil {
		enc, err := m.config.EncoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.resetMixCycleLocked()
			return nil
		}
		m.encoder = enc
	}
	if m.encoder == nil {
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
		slog.Warn("mixer: encode error", "err", err)
		return nil
	}
	m.framesMixed.Add(1)
	if m.promMetrics != nil {
		m.promMetrics.FramesMixedTotal.Inc()
	}

	pts := m.mixPTS

	// Reset mix cycle for next round
	m.resetMixCycleLocked()

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
func (m *AudioMixer) resetMixCycleLocked() {
	m.mixBuffer = nil
	m.mixStarted = false
	m.mixDeadline = time.Time{}
}

// AddChannel registers a source with the mixer.
func (m *AudioMixer) AddChannel(sourceKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[sourceKey] = &Channel{
		sourceKey:  sourceKey,
		eq:         NewEQ(m.sampleRate),
		compressor: NewCompressor(m.sampleRate),
	}
	m.recalcPassthrough()
}

// RemoveChannel unregisters a source.
func (m *AudioMixer) RemoveChannel(sourceKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[sourceKey]; ok {
		if ch.decoder != nil {
			ch.decoder.Close()
		}
		delete(m.channels, sourceKey)
		delete(m.lastDecodedPCM, sourceKey)
	}
	m.recalcPassthrough()
}

// SetActive activates or deactivates a channel.
func (m *AudioMixer) SetActive(sourceKey string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[sourceKey]; ok {
		ch.active = active
		m.recalcPassthrough()
	}
}

// ErrInvalidTrim is returned when trim is outside the valid range.
var ErrInvalidTrim = errors.New("trim must be between -20 and +20 dB")

// SetTrim sets the input trim in dB for a channel (-20 to +20 dB).
// Trim is applied before the fader in the mix pipeline.
func (m *AudioMixer) SetTrim(sourceKey string, trimDB float64) error {
	if trimDB < -20 || trimDB > 20 {
		return ErrInvalidTrim
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	ch.trim = trimDB
	m.recalcPassthrough()
	return nil
}

// SetLevel sets the gain in dB for a channel.
func (m *AudioMixer) SetLevel(sourceKey string, levelDB float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	ch.level = levelDB
	m.recalcPassthrough()
	return nil
}

// SetMuted sets the mute state for a channel.
func (m *AudioMixer) SetMuted(sourceKey string, muted bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	ch.muted = muted
	m.recalcPassthrough()
	return nil
}

// SetMasterLevel sets the master output level in dB.
func (m *AudioMixer) SetMasterLevel(levelDB float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.masterLevel = levelDB
	m.recalcPassthrough()
}

// SetAFV enables or disables audio-follows-video for a channel.
// When AFV is enabled, the channel activates when its source goes to program
// and deactivates when it leaves program.
func (m *AudioMixer) SetAFV(sourceKey string, afv bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	ch.afv = afv
	return nil
}

// IsChannelActive returns whether a channel is currently active.
func (m *AudioMixer) IsChannelActive(sourceKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return false
	}
	return ch.active
}

// OnProgramChange updates AFV channel states based on the new program source.
// Channels with AFV enabled activate when they match the program source and
// deactivate when they don't. Non-AFV channels are unaffected.
func (m *AudioMixer) OnProgramChange(newProgramSource string) {
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

// OnCut initiates a one-frame equal-power crossfade between old and new source.
// Called by the switcher when a cut occurs. A timeout ensures the crossfade
// completes even if the outgoing source stops sending frames.
func (m *AudioMixer) OnCut(oldSource, newSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crossfadeFrom = oldSource
	m.crossfadeTo = newSource
	m.crossfadeActive = true
	m.crossfadePCM = make(map[string][]float32)
	// Pre-seed the old source's PCM from the buffer — no waiting needed.
	// Apply pipeline (Trim -> EQ -> Compressor -> Fader) to match
	// what ingestCrossfadeFrame does for frames arriving from the decode path.
	if lastPCM, ok := m.lastDecodedPCM[oldSource]; ok && len(lastPCM) > 0 {
		cp := make([]float32, len(lastPCM))
		if ch, chOk := m.channels[oldSource]; chOk {
			trimGain := float32(DBToLinear(ch.trim))
			for i, s := range lastPCM {
				cp[i] = s * trimGain
			}
			if !ch.eq.IsBypassed() {
				ch.eq.Process(cp)
			}
			if !ch.compressor.IsBypassed() {
				ch.compressor.Process(cp)
			}
			faderGain := float32(DBToLinear(ch.level))
			for i := range cp {
				cp[i] *= faderGain
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
//   - AudioCrossfade: equal-power A→B (mix/dissolve)
//   - AudioDipToSilence: A→silence→B (dip through black)
//   - AudioFadeOut: A→silence (fade to black)
//   - AudioFadeIn: silence→A (fade from black)
//
// The new source channel is activated so its audio frames are accepted.
func (m *AudioMixer) OnTransitionStart(oldSource, newSource string, mode internal.AudioTransitionMode, durationMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = mode
	m.transCrossfadePrevPos = 0.0

	// Ensure the incoming source's channel is active so frames are accepted
	if ch, ok := m.channels[newSource]; ok {
		ch.active = true
	}
	m.recalcPassthrough()
}

// OnTransitionPosition updates the crossfade position (0.0 = fully old, 1.0 = fully new).
// Called by the switcher as the video transition progresses. Tracks the previous position
// for per-sample gain interpolation within audio frames.
func (m *AudioMixer) OnTransitionPosition(position float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transCrossfadePrevPos = m.transCrossfadePosition
	m.transCrossfadePosition = position
}

// OnTransitionComplete clears the transition crossfade state.
// Called by the switcher when the video transition finishes.
func (m *AudioMixer) OnTransitionComplete() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transCrossfadeActive = false
	m.transCrossfadeFrom = ""
	m.transCrossfadeTo = ""
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = 0
	m.transCrossfadePrevPos = 0.0
	m.recalcPassthrough()
}

// SetProgramMute sets the program output mute state. When muted, the mixer
// produces silent output (FTB held). Metering reflects silence.
func (m *AudioMixer) SetProgramMute(muted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.programMuted = muted
	m.recalcPassthrough()
}

// IsProgramMuted returns whether program output is muted (FTB held).
func (m *AudioMixer) IsProgramMuted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.programMuted
}

// IsInTransitionCrossfade returns whether a multi-frame transition crossfade is active.
func (m *AudioMixer) IsInTransitionCrossfade() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transCrossfadeActive
}

// TransitionPosition returns the current transition crossfade position (0.0–1.0).
func (m *AudioMixer) TransitionPosition() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transCrossfadePosition
}

// TransitionGains returns the crossfade gains for the old and new sources based
// on the current transition position and mode. When no transition is active,
// returns (1.0, 0.0).
func (m *AudioMixer) TransitionGains() (oldGain, newGain float64) {
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
func transitionFromGain(mode internal.AudioTransitionMode, pos float64) float64 {
	switch mode {
	case internal.AudioCrossfade:
		return math.Cos(pos * math.Pi / 2)
	case internal.AudioDipToSilence:
		if pos < 0.5 {
			// Phase 1: fade out A (equal-power over the first half)
			return math.Cos(pos * 2 * math.Pi / 2)
		}
		return 0
	case internal.AudioFadeOut:
		return math.Cos(pos * math.Pi / 2)
	case internal.AudioFadeIn:
		// FTB reverse: fade the "from" source IN from silence
		return math.Sin(pos * math.Pi / 2)
	}
	return 1.0
}

// transitionToGain computes the gain for the incoming ("to") source at the
// given position and mode.
func transitionToGain(mode internal.AudioTransitionMode, pos float64) float64 {
	switch mode {
	case internal.AudioCrossfade:
		return math.Sin(pos * math.Pi / 2)
	case internal.AudioDipToSilence:
		if pos >= 0.5 {
			// Phase 2: fade in B (equal-power over the second half)
			return math.Sin((pos*2 - 1) * math.Pi / 2)
		}
		return 0
	case internal.AudioFadeOut, internal.AudioFadeIn:
		// FTB has no "to" source
		return 0
	}
	return 0
}

// IsPassthrough returns whether the mixer is in passthrough mode.
func (m *AudioMixer) IsPassthrough() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.passthrough
}

// IngestFrame processes an audio frame from a source.
func (m *AudioMixer) IngestFrame(sourceKey string, frame *media.AudioFrame) {
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
	if !ok || !ch.active {
		m.mu.RUnlock()
		return
	}

	if ch.muted {
		m.mu.RUnlock()
		return
	}

	if m.passthrough {
		m.mu.RUnlock()

		// Decode for peak metering even in passthrough (skip encode).
		m.mu.Lock()
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
				// Copy to avoid aliasing if decoder reuses its internal buffer.
				stored := make([]float32, len(pcm))
				copy(stored, pcm)
				m.lastDecodedPCM[sourceKey] = stored
			} else if err != nil {
				m.decodeErrors.Add(1)
				slog.Warn("mixer: decode error", "source", sourceKey, "err", err)
			}
		}
		m.mu.Unlock()

		m.output(frame)
		m.framesPassthrough.Add(1)
		if m.promMetrics != nil {
			m.promMetrics.PassthroughBypassTotal.Inc()
		}
		return
	}
	m.mu.RUnlock()

	// Multi-channel mixing: decode, gain, accumulate, sum, encode
	m.mu.Lock()

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
		slog.Warn("mixer: decode error", "source", sourceKey, "err", err)
		return
	}

	// Update per-channel peaks (pre-fader, pre-gain)
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Store a copy of last decoded PCM for instant crossfade on future cuts.
	// Copy to avoid aliasing if decoder reuses its internal buffer.
	storedPCM := make([]float32, len(pcm))
	copy(storedPCM, pcm)
	m.lastDecodedPCM[sourceKey] = storedPCM

	// Pipeline order: Trim -> EQ -> Compressor -> Fader -> Mix -> Master -> Limiter -> Encode

	// Apply trim (input gain)
	trimGain := float32(DBToLinear(ch.trim))
	trimmedPCM := make([]float32, len(pcm))
	for i, s := range pcm {
		trimmedPCM[i] = s * trimGain
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Apply fader level with per-sample transition interpolation
	faderGain := float32(DBToLinear(ch.level))
	gainedPCM := make([]float32, len(trimmedPCM))

	isTransParticipant := m.transCrossfadeActive &&
		(sourceKey == m.transCrossfadeFrom || sourceKey == m.transCrossfadeTo)

	if isTransParticipant {
		// Per-sample interpolation: ramp gain smoothly from prevPos to currentPos
		// across the frame to eliminate zipper noise.
		var gainStartFn, gainEndFn func(float64) float64
		if sourceKey == m.transCrossfadeFrom {
			gainStartFn = func(p float64) float64 { return transitionFromGain(m.transCrossfadeMode, p) }
			gainEndFn = gainStartFn
		} else {
			gainStartFn = func(p float64) float64 { return transitionToGain(m.transCrossfadeMode, p) }
			gainEndFn = gainStartFn
		}
		gStart := float32(gainStartFn(m.transCrossfadePrevPos))
		gEnd := float32(gainEndFn(m.transCrossfadePosition))
		n := float32(len(trimmedPCM))
		for i, s := range trimmedPCM {
			t := float32(i) / n
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

	// Initialize mix buffer if needed (new cycle)
	if m.mixBuffer == nil {
		m.mixBuffer = make(map[string][]float32)
	}

	// Mix on frame arrival: each source contributes its latest frame.
	m.mixBuffer[sourceKey] = gainedPCM
	m.mixPTS = frame.PTS

	// Start the per-cycle deadline on first contribution
	if !m.mixStarted {
		m.mixStarted = true
		m.mixDeadline = time.Now().Add(mixCycleDeadline)
	}

	// Flush when all active unmuted channels have contributed OR deadline exceeded
	var outputFrame *media.AudioFrame
	if len(m.mixBuffer) >= activeUnmuted {
		outputFrame = m.collectMixCycleLocked()
	}
	m.mu.Unlock()
	if outputFrame != nil {
		m.output(outputFrame)
	}
}

// ingestCrossfadeFrame handles frames during an active crossfade transition.
// It collects one frame from both old and new source, applies equal-power crossfade, and outputs.
func (m *AudioMixer) ingestCrossfadeFrame(sourceKey string, frame *media.AudioFrame) {
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
		slog.Warn("mixer: decode error", "source", sourceKey, "err", err)
		return
	}

	// Pipeline: Trim -> EQ -> Compressor -> Fader
	trimGain := float32(DBToLinear(ch.trim))
	trimmedPCM := make([]float32, len(pcm))
	for i, s := range pcm {
		trimmedPCM[i] = s * trimGain
	}
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM)
	}
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}
	faderGain := float32(DBToLinear(ch.level))
	gainedPCM := make([]float32, len(trimmedPCM))
	for i, s := range trimmedPCM {
		gainedPCM[i] = s * faderGain
	}

	m.crossfadePCM[sourceKey] = gainedPCM

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
		slog.Warn("mixer: crossfade timeout",
			"source", missingSrc,
			"deadline_ms", crossfadeTimeout.Milliseconds())
	}

	// Apply equal-power crossfade (or use single source if timed out)
	var mixed []float32
	if hasFrom && hasTo {
		mixed = EqualPowerCrossfade(m.crossfadePCM[m.crossfadeFrom], m.crossfadePCM[m.crossfadeTo])
	} else if hasTo {
		// Outgoing source timed out — use incoming source only
		mixed = m.crossfadePCM[m.crossfadeTo]
	} else {
		// Incoming source timed out — use outgoing source only (unusual)
		mixed = m.crossfadePCM[m.crossfadeFrom]
	}

	// Apply master gain
	masterGain := float32(DBToLinear(m.masterLevel))
	for i := range mixed {
		mixed[i] *= masterGain
	}

	// Lazy-init encoder
	if m.encoder == nil && m.config.EncoderFactory != nil {
		enc, err := m.config.EncoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.crossfadeActive = false
			m.mu.Unlock()
			return
		}
		m.encoder = enc
	}
	if m.encoder == nil {
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
		slog.Warn("mixer: encode error", "err", err)
		return
	}

	// Clear crossfade state
	m.crossfadeActive = false
	m.crossfadePCM = nil

	// Build output frame before releasing lock
	outputFrame := &media.AudioFrame{
		PTS:        frame.PTS,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	}
	m.mu.Unlock()

	// Output outside the lock to avoid blocking other goroutines
	m.output(outputFrame)
}

// recalcPassthrough updates the passthrough flag. Caller must hold m.mu write lock.
// Logs when the mode actually changes (rare — only on cuts, mute toggles, etc.).
func (m *AudioMixer) recalcPassthrough() {
	prev := m.passthrough

	// Program mute or active transition crossfade require the mixing path.
	if m.programMuted || m.transCrossfadeActive {
		m.passthrough = false
		if prev != m.passthrough {
			slog.Info("mixer: passthrough mode changed",
				slog.Bool("passthrough", false),
				slog.String("reason", "program muted or transition crossfade active"))
		}
		return
	}

	activeCount := 0
	var activeKey string
	for key, ch := range m.channels {
		if ch.active {
			activeCount++
			activeKey = key
		}
	}

	if activeCount == 1 && m.masterLevel == 0 {
		ch := m.channels[activeKey]
		m.passthrough = !ch.muted && ch.level == 0 && ch.trim == 0 &&
			ch.eq.IsBypassed() && ch.compressor.IsBypassed()
	} else {
		m.passthrough = false
	}

	if prev != m.passthrough {
		var reason string
		if m.passthrough {
			reason = "single active source at 0dB"
		} else if activeCount == 0 {
			reason = "no active sources"
		} else if activeCount == 1 {
			reason = "single active source with gain or mute"
		} else {
			reason = "multiple active sources or master gain"
		}
		slog.Info("mixer: passthrough mode changed",
			slog.Bool("passthrough", m.passthrough),
			slog.String("reason", reason))
	}
}

// ProgramPeak returns the current program output peak levels in dBFS.
// Returns [leftDBFS, rightDBFS]. Silence is -Inf.
func (m *AudioMixer) ProgramPeak() [2]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return [2]float64{LinearToDBFS(m.programPeakL), LinearToDBFS(m.programPeakR)}
}

// ChannelStates returns a snapshot of all channel states for state broadcast.
func (m *AudioMixer) ChannelStates() map[string]internal.AudioChannel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]internal.AudioChannel, len(m.channels))
	for key, ch := range m.channels {
		ac := internal.AudioChannel{
			Level: ch.level,
			Trim:  ch.trim,
			Muted: ch.muted,
			AFV:   ch.afv,
			PeakL: LinearToDBFS(ch.peakL),
			PeakR: LinearToDBFS(ch.peakR),
		}
		// Include EQ band settings
		if ch.eq != nil {
			bands := ch.eq.GetBands()
			for i := 0; i < 3; i++ {
				ac.EQ[i] = internal.EQBand{
					Frequency: bands[i].Frequency,
					Gain:      bands[i].Gain,
					Q:         bands[i].Q,
					Enabled:   bands[i].Enabled,
				}
			}
		}
		// Include compressor settings and gain reduction
		if ch.compressor != nil {
			threshold, ratio, attack, release, makeup := ch.compressor.GetParams()
			ac.Compressor = internal.CompressorSettings{
				Threshold:  threshold,
				Ratio:      ratio,
				Attack:     attack,
				Release:    release,
				MakeupGain: makeup,
			}
			ac.GainReduction = ch.compressor.GainReduction()
		}
		result[key] = ac
	}
	return result
}

// SetEQ sets a single EQ band on a channel.
func (m *AudioMixer) SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error {
	m.mu.Lock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	eq := ch.eq
	m.mu.Unlock()
	if err := eq.SetBand(band, frequency, gain, q, enabled); err != nil {
		return err
	}
	m.mu.Lock()
	m.recalcPassthrough()
	m.mu.Unlock()
	return nil
}

// GetEQ returns the current EQ settings for a channel.
func (m *AudioMixer) GetEQ(sourceKey string) ([3]EQBandSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return [3]EQBandSettings{}, fmt.Errorf("channel %q not found", sourceKey)
	}
	return ch.eq.GetBands(), nil
}

// SetCompressor sets all compressor parameters for a channel.
func (m *AudioMixer) SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error {
	m.mu.Lock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	comp := ch.compressor
	m.mu.Unlock()
	if err := comp.SetParams(threshold, ratio, attack, release, makeupGain); err != nil {
		return err
	}
	m.mu.Lock()
	m.recalcPassthrough()
	m.mu.Unlock()
	return nil
}

// GetCompressor returns the current compressor settings and gain reduction for a channel.
func (m *AudioMixer) GetCompressor(sourceKey string) (threshold, ratio, attack, release, makeupGain, gainReduction float64, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("channel %q not found", sourceKey)
	}
	threshold, ratio, attack, release, makeupGain = ch.compressor.GetParams()
	gainReduction = ch.compressor.GainReduction()
	return
}

// GainReduction returns the current limiter gain reduction in dB.
// 0 means no limiting; positive values indicate dB of reduction applied.
func (m *AudioMixer) GainReduction() float64 {
	return m.limiter.GainReduction()
}

// MasterLevel returns the current master level in dB.
func (m *AudioMixer) MasterLevel() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.masterLevel
}

// DebugSnapshot implements debug.SnapshotProvider.
func (m *AudioMixer) DebugSnapshot() map[string]any {
	m.mu.RLock()
	mode := "mixing"
	if m.passthrough {
		mode = "passthrough"
	}
	activeCount := 0
	mutedCount := 0
	for _, ch := range m.channels {
		if ch.active {
			activeCount++
		}
		if ch.muted {
			mutedCount++
		}
	}
	peak := [2]float64{LinearToDBFS(m.programPeakL), LinearToDBFS(m.programPeakR)}
	m.mu.RUnlock()

	return map[string]any{
		"mode":                mode,
		"program_peak_dbfs":  peak,
		"channels_active":    activeCount,
		"channels_muted":     mutedCount,
		"frames_passthrough": m.framesPassthrough.Load(),
		"frames_mixed":       m.framesMixed.Load(),
		"crossfade_count":    m.crossfadeCount.Load(),
		"crossfade_timeouts": m.crossfadeTimeouts.Load(),
		"decode_errors":      m.decodeErrors.Load(),
		"encode_errors":      m.encodeErrors.Load(),
	}
}

// DBToLinear converts decibels to a linear gain multiplier.
func DBToLinear(db float64) float64 {
	if math.IsInf(db, -1) {
		return 0
	}
	return math.Pow(10, db/20)
}
