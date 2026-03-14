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
	"github.com/zsiec/switchframe/server/audio/vec"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/metrics"
)

// updateAtomicMaxAudio atomically updates field to val if val > current.
func updateAtomicMaxAudio(field *atomic.Int64, val int64) {
	for {
		cur := field.Load()
		if val <= cur {
			return
		}
		if field.CompareAndSwap(cur, val) {
			return
		}
	}
}

// growBuf returns buf[:n] if cap(buf) >= n, otherwise allocates a new slice.
// Used to eliminate per-frame allocations on the mixing hot path.
func growBuf(buf []float32, n int) []float32 {
	if cap(buf) >= n {
		return buf[:n]
	}
	return make([]float32, n)
}

// crossfadeTimeout is the maximum time to wait for both sources to deliver
// frames during a crossfade. If the outgoing source disconnects, the crossfade
// completes with only the incoming source's audio after this deadline.
// crossfadeTimeout is reduced from 50ms to 25ms because PCM pre-buffering
// eliminates the need to wait for the outgoing source. Only the incoming
// source needs to deliver a frame, so one AAC frame (~21.3ms) is sufficient.
const crossfadeTimeout = 25 * time.Millisecond

// Sentinel errors for the audio mixer.
var (
	ErrChannelNotFound = errors.New("audio: channel not found")
	ErrInvalidTrim     = errors.New("audio: trim must be between -20 and +20 dB")
)

// RawAudioSink receives a copy of the mixed PCM after master processing
// (fader + limiter) but before AAC encode. Used by MXL output to write
// raw audio to shared memory.
type RawAudioSink func(pcm []float32, pts int64, sampleRate, channels int)

// MixerConfig configures the Mixer.
type MixerConfig struct {
	SampleRate     int
	Channels       int
	Output         func(*media.AudioFrame)
	DecoderFactory DecoderFactory // nil = passthrough only (no mixing)
	EncoderFactory EncoderFactory // nil = passthrough only (no mixing)
}

// CompressorState holds the current state of a channel's compressor.
type CompressorState struct {
	Threshold     float64
	Ratio         float64
	Attack        float64
	Release       float64
	MakeupGain    float64
	GainReduction float64
}

// Channel tracks per-source audio state.
type Channel struct {
	sourceKey   string
	level       float64 // dB (-inf to +12), fader level
	levelLinear float32 // cached linear gain from level (avoids per-frame math.Pow)
	trim        float64 // dB (-20 to +20), input gain/trim
	trimLinear  float32 // cached linear gain from trim (avoids per-frame math.Pow)
	muted       bool
	afv         bool
	active      bool
	decoder     Decoder      // lazy init, nil in passthrough
	decoderOnce sync.Once         // ensures decoder factory is called at most once
	peakL       float64           // linear amplitude [0,1] — updated on every decoded frame
	peakR       float64           // linear amplitude [0,1]
	eq          *EQ               // 3-band parametric EQ (always initialized)
	compressor  *Compressor       // single-band compressor (always initialized)
	audioDelay        *DelayBuffer // per-source audio delay for lip-sync correction
	sampleRateWarned  bool              // true after first sample rate mismatch warning (log once)

	// Reusable work buffers (hot-path allocation elimination)
	trimBuf   []float32
	gainBuf   []float32
	storedBuf []float32
}

// Mixer mixes audio from multiple sources.
type Mixer struct {
	log          *slog.Logger
	mu           sync.RWMutex
	channels     map[string]*Channel
	masterLevel  float64 // dB, default 0.0
	masterLinear float32 // cached linear gain from masterLevel
	sampleRate   int
	numChannels  int
	encoder      Encoder
	output       func(*media.AudioFrame)
	passthrough  bool
	config       MixerConfig

	// Mix accumulation state: tracks which active unmuted channels
	// have contributed to the current mix cycle.
	mixBuffer   map[string][]float32 // sourceKey → decoded PCM for current cycle
	mixAccum    []float32            // reusable accumulator for mix output
	mixPTS      int64                // PTS of the current mix cycle
	mixStarted  bool                 // true when at least one channel has contributed
	mixDeadline time.Time            // deadline for current mix cycle

	// Background ticker for deadline enforcement
	stopTicker chan struct{}
	tickerWg   sync.WaitGroup
	closeOnce  sync.Once

	// Pre-buffered PCM: last decoded frame per source for instant crossfade.
	lastDecodedPCM map[string][]float32

	// Crossfade state: 2-frame (~42ms) equal-power crossfade on cut.
	crossfadeFrom            string // outgoing source key
	crossfadeTo              string // incoming source key
	crossfadeActive          bool
	crossfadePCM             map[string][]float32 // "from" and "to" PCM buffers
	crossfadeDeadline        time.Time            // timeout for crossfade completion
	crossfadeFramesRemaining int                  // frames left in multi-frame crossfade
	crossfadeTotalFrames     int                  // total frames in crossfade (for position calc)

	// Transition crossfade state: multi-frame crossfade synced with video transition.
	transCrossfadeActive   bool
	transCrossfadeFrom     string              // outgoing source key
	transCrossfadeTo       string              // incoming source key
	transCrossfadePosition float64             // 0.0 = fully old, 1.0 = fully new
	transCrossfadeMode     TransitionMode // gain curve selection
	transCrossfadeAudioPos float64             // position at end of last audio output (for smooth interpolation)
	mixCycleTransPos       float64             // snapshotted transition position for current mix cycle

	// Stinger audio overlay (optional, active during stinger transitions)
	stingerAudio    []float32 // interleaved PCM from stinger clip
	stingerOffset   int       // current read position in stingerAudio
	stingerChannels int       // channel count of stinger audio

	// Program mute: true while FTB is held (screen is black, audio is silent).
	programMuted          bool
	unmuteFadeRemaining   int // samples remaining in unmute fade-in ramp (0 = inactive)

	// Monotonic output PTS counter
	outputPTS       int64
	outputPTSInited bool

	// Program bus limiter (always active)
	limiter *Limiter

	// BS.1770-4 LUFS loudness meter (program bus, after master fader)
	loudness *LoudnessMeter

	// Metering state
	programPeakL float64 // linear amplitude [0,1]
	programPeakR float64 // linear amplitude [0,1]

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// Reusable buffers for hot-path allocation elimination
	mxlSinkBuf   []float32 // reused by MXL raw audio sink copy
	crossfadeBuf []float32 // reused by ingestCrossfadeFrame crossfade output

	// Raw audio output tap for MXL (atomic, lock-free read)
	rawAudioSink atomic.Pointer[RawAudioSink]

	// Debug counters (atomic, no lock needed)
	framesPassthrough atomic.Int64
	framesMixed       atomic.Int64
	crossfadeCount    atomic.Int64
	crossfadeTimeouts atomic.Int64
	decodeErrors      atomic.Int64
	encodeErrors      atomic.Int64

	// Audio timing diagnostics (atomic, lock-free)
	outputFrameCount  atomic.Int64 // total frames output (passthrough + mixed)
	deadlineFlushes   atomic.Int64 // mix cycles flushed by deadline timeout
	lastOutputNano    atomic.Int64 // UnixNano of last output frame
	maxInterFrameNano atomic.Int64 // max gap between consecutive output frames (ns)
	modeTransitions   atomic.Int64 // number of passthrough↔mixing mode changes
	transCrossfades   atomic.Int64 // transition crossfade start count

	// Mix cycle timing (atomic, lock-free)
	lastMixCycleNs atomic.Int64
	maxMixCycleNs  atomic.Int64
}

// mixCycleDeadline is the maximum time to wait for all active channels to
// contribute a frame before producing output with whatever is available.
// Prevents deadlock when a source stops sending audio.
const mixCycleDeadline = 50 * time.Millisecond

// NewMixer creates a Mixer.
func NewMixer(config MixerConfig) *Mixer {
	m := &Mixer{
		log:            slog.With("component", "audio"),
		channels:       make(map[string]*Channel),
		masterLevel:    0.0,
		masterLinear:   1.0, // 0 dB = unity
		sampleRate:     config.SampleRate,
		numChannels:    config.Channels,
		output:         config.Output,
		passthrough:    true,
		config:         config,
		stopTicker:     make(chan struct{}),
		limiter:        NewLimiter(config.SampleRate, config.Channels),
		loudness:       NewLoudnessMeter(config.SampleRate, config.Channels),
		lastDecodedPCM: make(map[string][]float32),
		mixBuffer:      make(map[string][]float32),
	}
	m.tickerWg.Add(1)
	go m.mixDeadlineTicker()
	return m
}

// SetMetrics attaches Prometheus metrics to the mixer.
func (m *Mixer) SetMetrics(pm *metrics.Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promMetrics = pm
}

// SetRawAudioSink sets or clears the raw audio output tap.
// The sink receives a copy of the mixed PCM after master processing
// (fader + limiter) but before AAC encode. This is used by MXL output
// to write raw audio to shared memory. Pass nil to disable.
func (m *Mixer) SetRawAudioSink(sink RawAudioSink) {
	if sink != nil {
		m.rawAudioSink.Store(&sink)
	} else {
		m.rawAudioSink.Store(nil)
	}
}

// Close releases all codec resources and stops the background ticker.
// It is safe to call multiple times.
func (m *Mixer) Close() error {
	m.closeOnce.Do(func() {
		close(m.stopTicker)
		m.tickerWg.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, ch := range m.channels {
			if ch.decoder != nil {
				_ = ch.decoder.Close()
			}
		}
		if m.encoder != nil {
			_ = m.encoder.Close()
		}
	})
	return nil
}

// initChannelDecoder ensures the channel's AAC decoder is initialized exactly
// once using sync.Once. If the factory returns an error, ch.decoder remains nil
// and callers must handle that (all call sites already check ch.decoder != nil).
// If ch.decoder was set externally (e.g., in tests), this is a no-op.
// Caller must hold m.mu (read or write).
func (m *Mixer) initChannelDecoder(ch *Channel) {
	if ch.decoder != nil || m.config.DecoderFactory == nil {
		return
	}
	ch.decoderOnce.Do(func() {
		dec, err := m.config.DecoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.log.Warn("decoder factory error", "source", ch.sourceKey, "err", err)
			return
		}
		ch.decoder = dec
	})
}

// ensureEncoder lazy-initializes the AAC encoder if needed and primes it
// with a silent frame to avoid MDCT warmup artifacts. A cold encoder's first
// output frame has different spectral characteristics than a primed one,
// causing an audible pop at the passthrough→mixing boundary.
// Caller must hold m.mu.
func (m *Mixer) ensureEncoder() error {
	if m.encoder != nil {
		return nil
	}
	if m.config.EncoderFactory == nil {
		return fmt.Errorf("no encoder factory")
	}
	enc, err := m.config.EncoderFactory(m.sampleRate, m.numChannels)
	if err != nil {
		return err
	}
	// Encode a silent frame to prime the encoder's internal MDCT buffers.
	// The output is discarded — we just need the encoder state initialized.
	silence := make([]float32, 1024*m.numChannels)
	_, _ = enc.Encode(silence)
	m.encoder = enc
	return nil
}

// mixDeadlineTicker runs in the background and forces a mix cycle flush
// when the per-cycle deadline expires. This prevents deadlock when a source
// stops sending audio while the mixer waits for all channels to contribute.
func (m *Mixer) mixDeadlineTicker() {
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
				m.deadlineFlushes.Add(1)
			}
			m.mu.Unlock()
			if outputFrame != nil {
				m.recordAndOutput(outputFrame)
			}
		}
	}
}

// frameDuration90k returns the duration of one AAC frame (1024 samples) in 90 kHz PTS ticks.
func (m *Mixer) frameDuration90k() int64 {
	return int64(1024) * 90000 / int64(m.sampleRate)
}

// advanceOutputPTS returns a monotonically non-decreasing output PTS derived
// from the input PTS. This keeps audio on the same PTS timeline as the source
// (and therefore the video pipeline), maintaining A/V sync. The only adjustment
// is a monotonic guard: if the input PTS goes backward (e.g., source switch),
// the counter advances by one frame duration instead.
// Caller must hold m.mu.
func (m *Mixer) advanceOutputPTS(inputPTS int64) int64 {
	if !m.outputPTSInited {
		m.outputPTS = inputPTS
		m.outputPTSInited = true
	} else if inputPTS > m.outputPTS {
		// Normal forward progression (including large jumps): follow source PTS
		m.outputPTS = inputPTS
	} else {
		// Backward or duplicate — advance by one frame to stay monotonic
		m.outputPTS += m.frameDuration90k()
	}
	return m.outputPTS
}

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
	updateAtomicMaxAudio(&m.maxMixCycleNs, mixDur)

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

// AddChannel registers a source with the mixer.
func (m *Mixer) AddChannel(sourceKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[sourceKey] = &Channel{
		sourceKey:   sourceKey,
		levelLinear: 1.0, // 0 dB = unity
		trimLinear:  1.0, // 0 dB = unity
		eq:          NewEQ(m.sampleRate, m.numChannels),
		compressor:  NewCompressor(m.sampleRate, m.numChannels),
		audioDelay:  NewDelayBuffer(0),
	}
	m.recalcPassthrough()
}

// RemoveChannel unregisters a source.
func (m *Mixer) RemoveChannel(sourceKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[sourceKey]; ok {
		if ch.decoder != nil {
			_ = ch.decoder.Close()
		}
		delete(m.channels, sourceKey)
		delete(m.lastDecodedPCM, sourceKey)
	}
	m.recalcPassthrough()
}

// SetActive activates or deactivates a channel.
func (m *Mixer) SetActive(sourceKey string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[sourceKey]; ok {
		ch.active = active
		m.recalcPassthrough()
	}
}

// SetTrim sets the input trim in dB for a channel (-20 to +20 dB).
// Trim is applied before the fader in the mix pipeline.
func (m *Mixer) SetTrim(sourceKey string, trimDB float64) error {
	if trimDB < -20 || trimDB > 20 {
		return ErrInvalidTrim
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	ch.trim = trimDB
	ch.trimLinear = float32(DBToLinear(trimDB))
	m.recalcPassthrough()
	return nil
}

// SetLevel sets the gain in dB for a channel.
func (m *Mixer) SetLevel(sourceKey string, levelDB float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	ch.level = levelDB
	ch.levelLinear = float32(DBToLinear(levelDB))
	m.recalcPassthrough()
	return nil
}

// SetMuted sets the mute state for a channel.
func (m *Mixer) SetMuted(sourceKey string, muted bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	ch.muted = muted
	m.recalcPassthrough()
	return nil
}

// SetMasterLevel sets the master output level in dB.
func (m *Mixer) SetMasterLevel(levelDB float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.masterLevel = levelDB
	m.masterLinear = float32(DBToLinear(levelDB))
	m.recalcPassthrough()
}

// SetAFV enables or disables audio-follows-video for a channel.
// When AFV is enabled, the channel activates when its source goes to program
// and deactivates when it leaves program.
func (m *Mixer) SetAFV(sourceKey string, afv bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	ch.afv = afv
	return nil
}

// IsChannelActive returns whether a channel is currently active.
func (m *Mixer) IsChannelActive(sourceKey string) bool {
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

// SetProgramMute sets the program output mute state. When muted, the mixer
// produces silent output (FTB held). Metering reflects silence.
// On unmute, a 5ms fade-in ramp prevents an uncompressed burst caused by
// the compressor/limiter envelopes starting from zero.
func (m *Mixer) SetProgramMute(muted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wasMuted := m.programMuted
	m.programMuted = muted
	if muted {
		m.limiter.Reset()
		for _, ch := range m.channels {
			ch.compressor.Reset()
		}
		m.unmuteFadeRemaining = 0
	} else if wasMuted {
		// Schedule a 5ms fade-in ramp to prevent uncompressed burst
		// after compressor/limiter envelopes were reset to zero.
		m.unmuteFadeRemaining = m.sampleRate * m.numChannels * 5 / 1000
	}
	m.recalcPassthrough()
}

// SetStingerAudio provides the stinger clip's audio PCM for additive overlay
// during a stinger transition. The audio is consumed sample-by-sample during
// mix cycles until exhausted or cleared by OnTransitionComplete.
// If the stinger audio has a different sample rate or channel count than the
// mixer, a warning is logged and the audio is skipped to prevent artifacts.
func (m *Mixer) SetStingerAudio(audio []float32, sampleRate, channels int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sampleRate != m.sampleRate {
		m.log.Warn("stinger audio sample rate mismatch, skipping audio",
			"stinger_rate", sampleRate, "mixer_rate", m.sampleRate)
		return
	}
	if channels != m.numChannels {
		m.log.Warn("stinger audio channel count mismatch, skipping audio",
			"stinger_channels", channels, "mixer_channels", m.numChannels)
		return
	}
	m.stingerAudio = audio
	m.stingerOffset = 0
	m.stingerChannels = channels
}

// addStingerAudio adds stinger PCM to the mixed output buffer. It applies a
// fade envelope (10ms fade-in at start, 50ms fade-out at end) to avoid clicks.
// Caller must hold m.mu.
func (m *Mixer) addStingerAudio(mixed []float32) {
	remaining := len(m.stingerAudio) - m.stingerOffset
	if remaining <= 0 {
		m.stingerAudio = nil
		return
	}

	n := len(mixed)
	if n > remaining {
		n = remaining
	}

	// Fade envelope to prevent clicks
	fadeInSamples := m.sampleRate * m.stingerChannels * 10 / 1000  // 10ms
	fadeOutSamples := m.sampleRate * m.stingerChannels * 50 / 1000 // 50ms
	totalSamples := len(m.stingerAudio)

	for i := 0; i < n; i++ {
		pos := m.stingerOffset + i
		gain := float32(1.0)

		// Fade in
		if pos < fadeInSamples {
			gain = float32(pos) / float32(fadeInSamples)
		}
		// Fade out
		distFromEnd := totalSamples - pos
		if distFromEnd < fadeOutSamples {
			fadeGain := float32(distFromEnd) / float32(fadeOutSamples)
			if fadeGain < gain {
				gain = fadeGain
			}
		}

		mixed[i] += m.stingerAudio[pos] * gain
	}
	m.stingerOffset += n
}

// IsProgramMuted returns whether program output is muted (FTB held).
func (m *Mixer) IsProgramMuted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.programMuted
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

// IsPassthrough returns whether the mixer is in passthrough mode.
func (m *Mixer) IsPassthrough() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.passthrough
}

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
		pcm = m.upmixMono(pcm, channels)
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

// recalcPassthrough updates the passthrough flag. Caller must hold m.mu write lock.
// Logs when the mode actually changes (rare — only on cuts, mute toggles, etc.).
func (m *Mixer) recalcPassthrough() {
	prev := m.passthrough

	// Program mute or active transition crossfade require the mixing path.
	if m.programMuted || m.transCrossfadeActive {
		m.passthrough = false
		if prev != m.passthrough {
			m.modeTransitions.Add(1)
			m.log.Info("passthrough mode changed",
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
		m.modeTransitions.Add(1)
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
		m.log.Info("passthrough mode changed",
			slog.Bool("passthrough", m.passthrough),
			slog.String("reason", reason),
			slog.Int("active_count", activeCount))
	}
}

// ProgramPeak returns the current program output peak levels in dBFS.
// Returns [leftDBFS, rightDBFS]. Silence is -Inf.
func (m *Mixer) ProgramPeak() [2]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return [2]float64{LinearToDBFS(m.programPeakL), LinearToDBFS(m.programPeakR)}
}

// ChannelStates returns a snapshot of all channel states for state broadcast.
func (m *Mixer) ChannelStates() map[string]internal.AudioChannel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]internal.AudioChannel, len(m.channels))
	for key, ch := range m.channels {
		ac := internal.AudioChannel{
			Level:        ch.level,
			Trim:         ch.trim,
			Muted:        ch.muted,
			AFV:          ch.afv,
			PeakL:        LinearToDBFS(ch.peakL),
			PeakR:        LinearToDBFS(ch.peakR),
			AudioDelayMs: ch.audioDelay.DelayMs(),
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
func (m *Mixer) SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error {
	m.mu.Lock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
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
func (m *Mixer) GetEQ(sourceKey string) ([3]EQBandSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return [3]EQBandSettings{}, fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	return ch.eq.GetBands(), nil
}

// SetCompressor sets all compressor parameters for a channel.
func (m *Mixer) SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error {
	m.mu.Lock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
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
func (m *Mixer) GetCompressor(sourceKey string) (CompressorState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return CompressorState{}, fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	threshold, ratio, attack, release, makeupGain := ch.compressor.GetParams()
	return CompressorState{
		Threshold:     threshold,
		Ratio:         ratio,
		Attack:        attack,
		Release:       release,
		MakeupGain:    makeupGain,
		GainReduction: ch.compressor.GainReduction(),
	}, nil
}

// SetAudioDelay sets the audio delay in milliseconds for a source channel.
// Used for lip-sync correction in multi-camera setups.
func (m *Mixer) SetAudioDelay(sourceKey string, delayMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("channel %q: %w", sourceKey, ErrChannelNotFound)
	}
	ch.audioDelay.SetDelayMs(delayMs)
	return nil
}

// AudioDelayMs returns the current audio delay in milliseconds for a source channel.
func (m *Mixer) AudioDelayMs(sourceKey string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return 0
	}
	return ch.audioDelay.DelayMs()
}

// GainReduction returns the current limiter gain reduction in dB.
// 0 means no limiting; positive values indicate dB of reduction applied.
func (m *Mixer) GainReduction() float64 {
	return m.limiter.GainReduction()
}

// MasterLevel returns the current master level in dB.
func (m *Mixer) MasterLevel() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.masterLevel
}

// MomentaryLUFS returns the BS.1770-4 momentary loudness (400ms window).
func (m *Mixer) MomentaryLUFS() float64 {
	return m.loudness.MomentaryLUFS()
}

// ShortTermLUFS returns the BS.1770-4 short-term loudness (3s window).
func (m *Mixer) ShortTermLUFS() float64 {
	return m.loudness.ShortTermLUFS()
}

// IntegratedLUFS returns the BS.1770-4 integrated loudness (gated, since last reset).
func (m *Mixer) IntegratedLUFS() float64 {
	return m.loudness.IntegratedLUFS()
}

// ResetLoudness clears the integrated loudness measurement.
func (m *Mixer) ResetLoudness() {
	m.loudness.Reset()
}

// recordAndOutput tracks output timing diagnostics and delivers the frame.
func (m *Mixer) recordAndOutput(frame *media.AudioFrame) {
	now := time.Now().UnixNano()
	prev := m.lastOutputNano.Swap(now)
	m.outputFrameCount.Add(1)
	if prev > 0 {
		gap := now - prev
		// Update max inter-frame gap (atomic CAS loop)
		for {
			cur := m.maxInterFrameNano.Load()
			if gap <= cur {
				break
			}
			if m.maxInterFrameNano.CompareAndSwap(cur, gap) {
				break
			}
		}
	}
	m.output(frame)
}

// ResetMaxInterFrameGap resets the max inter-frame gap counter, allowing
// fresh measurement after a transition or other event.
func (m *Mixer) ResetMaxInterFrameGap() {
	m.maxInterFrameNano.Store(0)
}

// DebugSnapshot implements debug.SnapshotProvider.
func (m *Mixer) DebugSnapshot() map[string]any {
	m.mu.RLock()
	mode := "mixing"
	if m.passthrough {
		mode = "passthrough"
	}
	activeCount := 0
	mutedCount := 0
	channelDetails := make(map[string]any, len(m.channels))
	for key, ch := range m.channels {
		if ch.active {
			activeCount++
		}
		if ch.muted {
			mutedCount++
		}
		detail := map[string]any{
			"active":               ch.active,
			"muted":                ch.muted,
			"afv":                  ch.afv,
			"level":                ch.level,
			"trim":                 ch.trim,
			"eq_bypassed":          ch.eq.IsBypassed(),
			"compressor_bypassed":  ch.compressor.IsBypassed(),
			"delay_ms":             ch.audioDelay.DelayMs(),
			"peak_l_dbfs":          LinearToDBFS(ch.peakL),
			"peak_r_dbfs":          LinearToDBFS(ch.peakR),
		}
		channelDetails[key] = detail
	}
	transCrossfadeActive := m.transCrossfadeActive
	transCrossfadePos := m.transCrossfadePosition
	transCrossfadeFrom := m.transCrossfadeFrom
	transCrossfadeTo := m.transCrossfadeTo
	peak := [2]float64{LinearToDBFS(m.programPeakL), LinearToDBFS(m.programPeakR)}
	m.mu.RUnlock()

	maxGapMs := m.maxInterFrameNano.Load() / 1e6

	result := map[string]any{
		"mode":                   mode,
		"program_peak_dbfs":      peak,
		"channels_active":        activeCount,
		"channels_muted":         mutedCount,
		"channels":               channelDetails,
		"frames_passthrough":     m.framesPassthrough.Load(),
		"frames_mixed":           m.framesMixed.Load(),
		"frames_output_total":    m.outputFrameCount.Load(),
		"crossfade_count":        m.crossfadeCount.Load(),
		"crossfade_timeouts":     m.crossfadeTimeouts.Load(),
		"trans_crossfade_active": transCrossfadeActive,
		"trans_crossfade_pos":    transCrossfadePos,
		"trans_crossfade_from":   transCrossfadeFrom,
		"trans_crossfade_to":     transCrossfadeTo,
		"trans_crossfade_count":  m.transCrossfades.Load(),
		"decode_errors":          m.decodeErrors.Load(),
		"encode_errors":          m.encodeErrors.Load(),
		"deadline_flushes":       m.deadlineFlushes.Load(),
		"max_inter_frame_gap_ms": maxGapMs,
		"mode_transitions":       m.modeTransitions.Load(),
	}

	if m.loudness != nil {
		result["loudness"] = map[string]any{
			"momentary_lufs":  m.loudness.MomentaryLUFS(),
			"short_term_lufs": m.loudness.ShortTermLUFS(),
			"integrated_lufs": m.loudness.IntegratedLUFS(),
		}
	}

	return result
}

// upmixMono duplicates each mono sample to all mixer channels.
// srcChannels is the source's actual channel count (must be < m.numChannels).
// Caller must hold m.mu.
func (m *Mixer) upmixMono(pcm []float32, srcChannels int) []float32 {
	if srcChannels <= 0 || srcChannels >= m.numChannels || len(pcm) == 0 {
		return pcm
	}
	// For mono→stereo: each sample is duplicated to numChannels positions.
	// For N→M where N<M: interleave source channels into M output channels,
	// duplicating the last source channel to fill remaining output channels.
	samplesPerSrcFrame := len(pcm) / srcChannels
	upmixed := make([]float32, samplesPerSrcFrame*m.numChannels)
	for i := 0; i < samplesPerSrcFrame; i++ {
		for outCh := 0; outCh < m.numChannels; outCh++ {
			srcCh := outCh
			if srcCh >= srcChannels {
				srcCh = srcChannels - 1
			}
			upmixed[i*m.numChannels+outCh] = pcm[i*srcChannels+srcCh]
		}
	}
	return upmixed
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
		pcm = m.upmixMono(pcm, channels)
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

// DBToLinear converts decibels to a linear gain multiplier.
func DBToLinear(db float64) float64 {
	if math.IsInf(db, -1) {
		return 0
	}
	return math.Pow(10, db/20)
}
