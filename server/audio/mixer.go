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
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/metrics"
)

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
	sourceKey        string
	level            float64 // dB (-inf to +12), fader level
	levelLinear      float32 // cached linear gain from level (avoids per-frame math.Pow)
	trim             float64 // dB (-20 to +20), input gain/trim
	trimLinear       float32 // cached linear gain from trim (avoids per-frame math.Pow)
	muted            bool
	afv              bool
	active           bool
	decoder          Decoder      // lazy init, nil in passthrough
	decoderOnce      sync.Once    // ensures decoder factory is called at most once
	peakL            float64      // linear amplitude [0,1] — updated on every decoded frame
	peakR            float64      // linear amplitude [0,1]
	eq               *EQ          // 3-band parametric EQ (always initialized)
	compressor       *Compressor  // single-band compressor (always initialized)
	audioDelay       *DelayBuffer // per-source audio delay for lip-sync correction
	sampleRateWarned bool         // true after first sample rate mismatch log (once per channel)
	resampler        *Resampler   // nil when source rate matches mixer rate; lazy-init on mismatch
	resamplerSrcRate int          // source rate the resampler was created for (detect rate changes)

	// Reusable work buffers (hot-path allocation elimination)
	trimBuf   []float32
	gainBuf   []float32
	storedBuf []float32
	upmixBuf  []float32 // reused by upmixMono to avoid per-call allocation
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
	transCrossfadeFrom     string         // outgoing source key
	transCrossfadeTo       string         // incoming source key
	transCrossfadePosition float64        // 0.0 = fully old, 1.0 = fully new
	transCrossfadeMode     TransitionMode // gain curve selection
	transCrossfadeAudioPos float64        // position at end of last audio output (for smooth interpolation)
	mixCycleTransPos       float64        // snapshotted transition position for current mix cycle

	// Stinger audio overlay (optional, active during stinger transitions)
	stingerAudio    []float32 // interleaved PCM from stinger clip
	stingerOffset   int       // current read position in stingerAudio
	stingerChannels int       // channel count of stinger audio

	// Program mute: true while FTB is held (screen is black, audio is silent).
	programMuted        bool
	unmuteFadeRemaining int // samples remaining in unmute fade-in ramp (0 = inactive)

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

	// Encode buffer: accumulates mixed PCM across cycles when resampling
	// produces non-1024-sample chunks. Drained in 1024-sample frames.
	encodeBuf []float32

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
		return errors.New("no encoder factory")
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
// If the stinger audio has a different sample rate, it is resampled to the
// mixer's rate. Channel count mismatches are still rejected (no upmix logic).
func (m *Mixer) SetStingerAudio(audio []float32, sampleRate, channels int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if channels != m.numChannels {
		m.log.Warn("stinger audio channel count mismatch, skipping audio",
			"stinger_channels", channels, "mixer_channels", m.numChannels)
		return
	}
	// Resample stinger audio to mixer rate if needed (one-time conversion).
	if sampleRate != 0 && sampleRate != m.sampleRate {
		r := NewResampler(sampleRate, m.sampleRate, channels)
		audio = r.Resample(audio)
		m.log.Info("stinger audio resampled",
			"from", sampleRate, "to", m.sampleRate)
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

// IsPassthrough returns whether the mixer is in passthrough mode.
func (m *Mixer) IsPassthrough() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.passthrough
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
		atomicutil.UpdateMax(&m.maxInterFrameNano, gap)
	}
	m.output(frame)
}

// ResetMaxInterFrameGap resets the max inter-frame gap counter, allowing
// fresh measurement after a transition or other event.
func (m *Mixer) ResetMaxInterFrameGap() {
	m.maxInterFrameNano.Store(0)
}

// resampleIfNeeded checks if decoded PCM needs sample rate conversion and
// applies the per-channel polyphase FIR resampler if so. Returns the
// (possibly resampled) PCM. SampleRate==0 means unknown — no resampling.
// Caller must hold m.mu (write lock).
func (m *Mixer) resampleIfNeeded(ch *Channel, pcm []float32, srcRate int) []float32 {
	if srcRate == 0 || srcRate == m.sampleRate {
		return pcm
	}
	// Lazy-init or replace resampler if source rate changed
	if ch.resampler == nil || ch.resamplerSrcRate != srcRate {
		ch.resampler = NewResampler(srcRate, m.sampleRate, m.numChannels)
		ch.resamplerSrcRate = srcRate
		if !ch.sampleRateWarned {
			ch.sampleRateWarned = true
			m.log.Info("audio: resampling source",
				"source", ch.sourceKey,
				"from", srcRate,
				"to", m.sampleRate,
				"taps_per_phase", ch.resampler.TapsPerPhase(),
				"L", ch.resampler.UpFactor(),
				"M", ch.resampler.DownFactor())
		}
		m.recalcPassthrough()
	}
	return ch.resampler.Resample(pcm)
}

// upmixMono duplicates each mono sample to all mixer channels.
// srcChannels is the source's actual channel count (must be < m.numChannels).
// ch is used to cache the upmix buffer across calls for the same channel.
// Caller must hold m.mu.
func (m *Mixer) upmixMono(ch *Channel, pcm []float32, srcChannels int) []float32 {
	if srcChannels <= 0 || srcChannels >= m.numChannels || len(pcm) == 0 {
		return pcm
	}
	// For mono→stereo: each sample is duplicated to numChannels positions.
	// For N→M where N<M: interleave source channels into M output channels,
	// duplicating the last source channel to fill remaining output channels.
	samplesPerSrcFrame := len(pcm) / srcChannels
	needed := samplesPerSrcFrame * m.numChannels
	ch.upmixBuf = growBuf(ch.upmixBuf, needed)
	upmixed := ch.upmixBuf
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

// DBToLinear converts decibels to a linear gain multiplier.
func DBToLinear(db float64) float64 {
	if math.IsInf(db, -1) {
		return 0
	}
	return math.Pow(10, db/20)
}
