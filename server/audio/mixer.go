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
	DecoderFactory DecoderFactory // nil = no AAC decoding (PCM-only sources)
	EncoderFactory EncoderFactory // nil = no AAC encoding (tick returns nil)
	ClockDriven    bool           // when true, skip internal ticker (external clock drives output via ProduceSamples)
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

// GateState holds the current state of a channel's noise gate.
type GateState struct {
	Threshold     float64
	Range         float64
	Attack        float64
	Release       float64
	Hold          float64
	Enabled       bool
	GainReduction float64
}

// Channel tracks per-source audio state.
type Channel struct {
	sourceKey            string
	level                float64 // dB (-inf to +12), fader level
	levelLinear          float32 // cached linear gain from level (avoids per-frame math.Pow)
	trim                 float64 // dB (-20 to +20), input gain/trim
	trimLinear           float32 // cached linear gain from trim (avoids per-frame math.Pow)
	hpf                  *HPF    // high-pass filter (always initialized)
	gate                 *Gate   // noise gate (always initialized)
	phaseInvert          bool
	muted                bool
	afv                  bool
	active               bool
	decoder              Decoder        // lazy init on first IngestFrame
	decoderOnce          sync.Once      // ensures decoder factory is called at most once
	primingSkipCount     int            // number of empty (priming) frames the decoder has returned before first valid PCM
	primingOffsetApplied bool           // true once per-source AAC priming silence has been pushed to ringBuf
	peakL                float64        // linear amplitude [0,1] — updated on every decoded frame
	peakR                float64        // linear amplitude [0,1]
	eq                   *EQ            // 3-band parametric EQ (always initialized)
	compressor           *Compressor    // single-band compressor (always initialized)
	balance              float64        // -1.0 (hard left) to +1.0 (hard right), 0.0 = center
	balL                 float32        // cached left gain from BalanceGains()
	balR                 float32        // cached right gain from BalanceGains()
	audioDelay           *DelayBuffer   // per-source audio delay for lip-sync correction
	isPCM                bool           // true if this channel receives raw PCM (not AAC)
	sampleRateWarned     bool           // true after first sample rate mismatch log (once per channel)
	resampler            *Resampler     // nil when source rate matches mixer rate; lazy-init on mismatch
	resamplerSrcRate     int            // source rate the resampler was created for (detect rate changes)
	ringBuf              *PCMRingBuffer // processed PCM ring buffer for clock-driven output

	// Reusable work buffers (hot-path allocation elimination)
	trimBuf    []float32
	gainBuf    []float32
	upmixBuf   []float32 // reused by upmixMono to avoid per-call allocation
	downmixBuf []float32 // reused by downmixChannels to avoid per-call allocation

	// surroundWarned is set the first time a source delivers more channels
	// than the mixer supports. Reset on source reconnect via RemoveChannel.
	surroundWarned bool
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
	config       MixerConfig

	// Audio forwarding: when set, audio ingested for srcKey is also
	// ingested for dstKey. Used by playout to route live source audio
	// through the playout mixer channel.
	audioForwards map[string]string // srcKey → dstKey

	// Reusable accumulator for mix output (used by tick())
	mixAccum []float32

	// Background ticker for clock-driven output
	stopTicker chan struct{}
	tickerWg   sync.WaitGroup
	closeOnce  sync.Once

	// closed is set true under m.mu before Close() frees the codecs. A
	// straggler tick()/ingest that wins the lock after a shutdown-wait timeout
	// checks this flag before (re)creating or using ch.decoder/m.encoder, so it
	// cannot resurrect or touch codecs Close() is about to release.
	closed atomic.Bool

	// Transition crossfade state: multi-frame crossfade synced with video transition.
	// Also used for cut crossfade (2-tick ramp driven by cutFramesRemaining).
	transCrossfadeActive   bool
	transCrossfadeFrom     string         // outgoing source key
	transCrossfadeTo       string         // incoming source key
	transCrossfadePosition float64        // 0.0 = fully old, 1.0 = fully new
	transCrossfadeMode     TransitionMode // gain curve selection
	transCrossfadeAudioPos float64        // position at end of last audio output (for smooth interpolation)
	mixCycleTransPos       float64        // snapshotted transition position for current mix cycle

	// transAbortActivated is the source key of the channel that
	// OnTransitionStart activated (it was inactive before the transition
	// began). OnTransitionAbort rolls that activation back so the preview
	// source's audio does not keep mixing into program at full fader gain
	// after an aborted transition (e.g. T-bar pulled back to 0).
	// OnTransitionComplete simply clears it — the channel legitimately
	// stays active because its source is now on program.
	transAbortActivated string

	// Cut crossfade state: driven by tick() frame counter.
	// When cutFramesRemaining > 0, tick() auto-advances transCrossfadePosition.
	cutFramesRemaining int // >0 means cut crossfade in progress
	cutTotalFrames     int // total frames in cut crossfade (usually 2)

	// programAFVSource is the source key of the AFV channel currently on
	// program. Tracked explicitly so OnProgramChange picks the outgoing source
	// deterministically instead of via random map iteration (which would pick an
	// arbitrary one when several AFV channels are momentarily active and hard-cut
	// the rest, causing a click and a nondeterministic mix).
	programAFVSource string

	// AFV deferred deactivation: when OnProgramChange sets up a crossfade, the
	// outgoing AFV channel(s) stay active during the ramp and are deactivated
	// when the cut crossfade completes. A set (not a single key) so that if
	// several AFV channels are active at once — e.g. OnProgramChange is
	// re-invoked mid-crossfade — every one of them fades out with the transition
	// rather than only the first being ramped and the others hard-cut.
	afvDeactivateOnComplete map[string]struct{}

	// Stinger audio overlay (optional, active during stinger transitions)
	stingerAudio    []float32 // interleaved PCM from stinger clip
	stingerOffset   int       // current read position in stingerAudio
	stingerChannels int       // channel count of stinger audio

	// Program mute: true while FTB is held (screen is black, audio is silent).
	programMuted        bool
	unmuteFadeRemaining int // samples remaining in unmute fade-in ramp (0 = inactive)

	// Wall-clock-based output PTS: seeded from video pipeline's first frame
	// PTS + wall time (SeedPTSFromVideo), then tracks elapsed wall clock.
	// Both video and audio use wall clock, so they stay aligned during gaps.
	outputPTS         int64
	outputPTSInited   bool
	outputPTSStart    int64     // source PTS at epoch
	outputPTSEpoch    time.Time // wall-clock time at epoch
	videoSeedExpected bool      // true when wall-clock mode expects SeedPTSFromVideo

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
	mxlSinkBuf []float32 // reused by MXL raw audio sink copy

	// Raw audio output tap for MXL (atomic, lock-free read)
	rawAudioSink atomic.Pointer[RawAudioSink]

	// Direct output callback — bypasses the relay for zero-latency
	// delivery to the MPEG-TS muxer. Called from the output ticker goroutine.
	outputAudioCallback atomic.Pointer[func(*media.AudioFrame)]

	// Debug counters (atomic, no lock needed)
	framesMixed  atomic.Int64
	decodeErrors atomic.Int64
	encodeErrors atomic.Int64

	// Ring buffer latency: depth of the program source's ring buffer in
	// samples at the moment each tick reads from it. Converted to 90kHz
	// ticks for lip-sync offset. Updated every tick.
	ringBufLatency90k atomic.Int64

	// Audio timing diagnostics (atomic, lock-free)
	outputFrameCount  atomic.Int64 // total frames output
	lastOutputNano    atomic.Int64 // UnixNano of last output frame
	maxInterFrameNano atomic.Int64 // max gap between consecutive output frames (ns)
	transCrossfades   atomic.Int64 // transition crossfade start count

	// Mix cycle timing (atomic, lock-free)
	lastMixCycleNs atomic.Int64
}

// NewMixer creates a Mixer.
func NewMixer(config MixerConfig) *Mixer {
	// Default to a no-op output when the caller leaves it unset. recordAndOutput
	// calls output unconditionally once the encoder factory produces a frame;
	// an unguarded nil callback would panic.
	output := config.Output
	if output == nil {
		output = func(*media.AudioFrame) {}
	}
	m := &Mixer{
		log:          slog.With("component", "audio"),
		channels:     make(map[string]*Channel),
		masterLevel:  0.0,
		masterLinear: 1.0, // 0 dB = unity
		sampleRate:   config.SampleRate,
		numChannels:  config.Channels,
		output:       output,
		config:       config,
		limiter:      NewLimiter(config.SampleRate, config.Channels),
		loudness:     NewLoudnessMeter(config.SampleRate, config.Channels),
	}
	if !config.ClockDriven {
		m.stopTicker = make(chan struct{})
		m.tickerWg.Add(1)
		go m.outputTicker()
	}
	return m
}

// SetOutputAudioCallback registers a direct audio output callback that
// bypasses the relay for zero-latency delivery to the MPEG-TS muxer.
// Called synchronously from the output ticker goroutine. Pass nil to clear.
func (m *Mixer) SetOutputAudioCallback(fn func(*media.AudioFrame)) {
	if fn != nil {
		m.outputAudioCallback.Store(&fn)
	} else {
		m.outputAudioCallback.Store(nil)
	}
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

// AddAudioForward sets up forwarding: audio ingested for srcKey will also be
// ingested for dstKey. Used by playout to route live source audio through
// the playout mixer channel during live playlist items.
func (m *Mixer) AddAudioForward(srcKey, dstKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.audioForwards == nil {
		m.audioForwards = make(map[string]string)
	}
	m.audioForwards[srcKey] = dstKey
}

// RemoveAudioForward removes a previously set audio forward.
func (m *Mixer) RemoveAudioForward(srcKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.audioForwards, srcKey)
}

// ProduceSamples produces exactly sampleCount mixed PCM samples on demand.
// This is the reference-clock-driven path — the caller (synchronous processing
// loop) controls timing, not an internal ticker.
// sampleCount is in SAMPLES (not samples*channels — the method handles channels).
func (m *Mixer) ProduceSamples(sampleCount int) []float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	totalValues := sampleCount * m.numChannels
	mixed := m.produceSamplesLocked(totalValues)
	// Apply master chain with PTS=0; the reference clock caller manages PTS externally.
	m.applyMasterChain(mixed, 0)
	return mixed
}

// AdvanceCrossfade advances the cut crossfade state machine by one tick.
// Called by the synchronous processing loop after ProduceSamples.
func (m *Mixer) AdvanceCrossfade() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.advanceCutCrossfade()
}

// Close releases all codec resources and stops the background ticker.
// It is safe to call multiple times.
//
// Returns a non-nil error if the background ticker goroutine did not exit
// within the shutdown timeout. In that pathological case Close still proceeds
// to free the codecs (leaking them forever would be worse), but it first sets
// m.closed so a still-running tick()/ingest cannot recreate or reuse the
// codecs it is about to release — see ensureEncoder/initChannelDecoder.
func (m *Mixer) Close() error {
	var closeErr error
	m.closeOnce.Do(func() {
		if m.stopTicker != nil {
			close(m.stopTicker)
			if !internal.WaitWithTimeout(&m.tickerWg, 5*time.Second) {
				closeErr = errors.New("audio: mixer ticker shutdown timed out after 5s")
				m.log.Error("mixer ticker shutdown timed out after 5s; freeing codecs anyway")
			}
		}

		// Collect codecs under lock, then close outside lock to avoid
		// holding mu during cgo calls (which may block). Set m.closed first
		// (under the same lock) so any straggler tick()/ingest that acquires
		// the lock after this point sees the mixer as closed and refuses to
		// recreate or use the codecs we are about to free.
		m.mu.Lock()
		m.closed.Store(true)
		var decoders []Decoder
		for _, ch := range m.channels {
			if ch.decoder != nil {
				decoders = append(decoders, ch.decoder)
				ch.decoder = nil
			}
		}
		enc := m.encoder
		m.encoder = nil
		m.mu.Unlock()

		for _, dec := range decoders {
			_ = dec.Close()
		}
		if enc != nil {
			_ = enc.Close()
		}
	})
	return closeErr
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
	// Don't resurrect a decoder after Close() has begun freeing codecs.
	if m.closed.Load() {
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
	// Don't resurrect the encoder after Close() has begun freeing codecs. A
	// straggler tick() that wins the lock post-timeout lands here; returning an
	// error makes it produce no output instead of recreating a live encoder.
	if m.closed.Load() {
		return errors.New("audio: mixer is closed")
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

// frameDuration90k returns the duration of one AAC frame (1024 samples) in 90 kHz PTS ticks.
func (m *Mixer) frameDuration90k() int64 {
	return int64(1024) * 90000 / int64(m.sampleRate)
}

// ExpectVideoSeed tells the mixer to wait for SeedPTSFromVideo before
// self-seeding from audio input. Without this, the mixer seeds from the
// first IngestPCM call which may use a different PTS timeline.
// No-op when ClockDriven is true (PTS comes from the external clock).
func (m *Mixer) ExpectVideoSeed() {
	if m.config.ClockDriven {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoSeedExpected = true
}

// SeedPTSFromVideo sets the audio PTS epoch from the video pipeline's first
// frame. Called by the switcher when the first program video frame is
// processed, ensuring audio and video PTS share the same starting point.
// Thread-safe — can be called from any goroutine.
// No-op when ClockDriven is true (PTS comes from the external clock).
func (m *Mixer) SeedPTSFromVideo(videoPTS int64) {
	if m.config.ClockDriven {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Always override — audio may have already seeded via advanceOutputPTS
	// (audio frames arrive before video during FFmpeg probe). The video seed
	// is authoritative because it aligns with the frame sync's video PTS.
	m.outputPTSStart = videoPTS
	m.outputPTSEpoch = time.Now()
	m.outputPTS = videoPTS
	m.outputPTSInited = true
}

// advanceOutputPTS returns a hybrid PTS: frame-counter for regular spacing
// (browser-friendly) with periodic wall-clock resync to prevent drift during
// gaps. Seeded from the video pipeline's first frame (SeedPTSFromVideo).
// Caller must hold m.mu.
func (m *Mixer) advanceOutputPTS(inputPTS int64) int64 {
	now := time.Now()
	if !m.outputPTSInited {
		if m.videoSeedExpected {
			// Wall-clock mode: wait for SeedPTSFromVideo to align epoch.
			// Use temp counter until seed arrives (overridden by seed).
			m.outputPTS += m.frameDuration90k()
			m.outputPTS &= 0x1FFFFFFFF
			return m.outputPTS
		}
		if inputPTS > 0 {
			// Legacy mode (no video seed): self-seed from first audio PTS.
			m.outputPTSStart = inputPTS
			m.outputPTSEpoch = now
			m.outputPTS = inputPTS
			m.outputPTSInited = true
		} else {
			m.outputPTS += m.frameDuration90k()
			m.outputPTS &= 0x1FFFFFFFF
			return m.outputPTS
		}
	} else {
		// Frame counter: regular 1920-tick spacing (no inputPtsJumps).
		m.outputPTS += m.frameDuration90k()

		// Wall-clock resync: nudge toward wall clock to prevent drift
		// during no-audio gaps. Gradual correction avoids PTS jumps.
		wallPTS := m.outputPTSStart + int64(now.Sub(m.outputPTSEpoch).Seconds()*90000)
		drift := wallPTS - m.outputPTS
		if drift > m.frameDuration90k() {
			// Behind wall clock — add half a frame to catch up gradually.
			m.outputPTS += m.frameDuration90k() / 2
		} else if drift < -m.frameDuration90k() {
			// Ahead of wall clock — subtract half a frame to slow down.
			m.outputPTS -= m.frameDuration90k() / 2
		}
	}
	// No 33-bit mask — the muxer handles PTS rebasing and wrapping.
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
		hpf:         NewHPF(m.sampleRate),
		gate:        NewGate(m.sampleRate, m.numChannels),
		eq:          NewEQ(m.sampleRate, m.numChannels),
		compressor:  NewCompressor(m.sampleRate, m.numChannels),
		balance:     0,
		balL:        1.0,
		balR:        1.0,
		audioDelay:  NewDelayBuffer(0),
		ringBuf:     m.newChannelRingBuf(),
	}
}

// newChannelRingBuf creates a PCM ring buffer for a mixer channel.
func (m *Mixer) newChannelRingBuf() *PCMRingBuffer {
	return NewPCMRingBufferWithChannels(10*1024*m.numChannels, m.numChannels)
}

// driftTarget returns the target ring buffer depth in total sample values
// (samples × channels) for drift compensation. 2 AAC frames provides
// enough jitter buffer for bursty SRT delivery while staying within
// EBU R37 ±40ms imperceptible range.
func (m *Mixer) driftTarget() int {
	return 2 * 1024 * m.numChannels
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
	}
}

// SetActive activates or deactivates a channel.
func (m *Mixer) SetActive(sourceKey string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[sourceKey]; ok {
		ch.active = active
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
	return nil
}

// SetMasterLevel sets the master output level in dB.
// Returns an error if the level is NaN, Inf, or outside [-100, 20].
func (m *Mixer) SetMasterLevel(levelDB float64) error {
	if math.IsNaN(levelDB) || math.IsInf(levelDB, 0) {
		return errors.New("level must be a finite number")
	}
	if levelDB < -100 || levelDB > 20 {
		return fmt.Errorf("level %f out of range [-100, 20]", levelDB)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.masterLevel = levelDB
	m.masterLinear = float32(DBToLinear(levelDB))
	return nil
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
			ch.gate.Reset()
		}
		m.unmuteFadeRemaining = 0
	} else if wasMuted {
		// Schedule a 5ms fade-in ramp to prevent uncompressed burst
		// after compressor/limiter envelopes were reset to zero.
		m.unmuteFadeRemaining = m.sampleRate * m.numChannels * 5 / 1000
	}
}

// SetStingerAudio provides the stinger clip's audio PCM for additive overlay
// during a stinger transition. The audio is consumed sample-by-sample during
// mix cycles until exhausted or cleared by OnTransitionComplete.
// Sample-rate mismatch is resampled; channel-count mismatch is converted to
// m.numChannels (BS.775-3 Lo/Ro downmix for surround sources, AES3 §6.3
// duplicate for mono upmix). Channels outside [1, 6] are rejected — the
// historical log-and-drop path now applies only to that invalid range and
// to the unsupported stereo→surround upmix case (deferred: layout-aware matrix).
func (m *Mixer) SetStingerAudio(audio []float32, sampleRate, channels int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if channels <= 0 || channels > 6 {
		m.log.Warn("stinger audio channel count out of range, skipping audio",
			"stinger_channels", channels, "mixer_channels", m.numChannels)
		return
	}
	if channels != m.numChannels {
		switch {
		case channels > m.numChannels:
			srcChannels := channels
			var buf []float32
			var result downmixResult
			audio, result = m.downmixChannels(&buf, audio, srcChannels)
			channels = m.numChannels
			model := "truncation"
			if result == downmixLoRo {
				model = "BS.775-3 Lo/Ro"
			}
			m.log.Info("stinger audio downmixed",
				"src_channels", srcChannels,
				"to_channels", m.numChannels, "model", model)
		case channels == 1:
			var buf []float32
			audio = m.upmixMono(&buf, audio, channels)
			channels = m.numChannels
			m.log.Info("stinger audio mono upmixed",
				"to_channels", m.numChannels)
		default:
			m.log.Warn("stinger audio channel layout unsupported, skipping audio",
				"stinger_channels", channels, "mixer_channels", m.numChannels)
			return
		}
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

	// Fade envelope to prevent clicks.
	// Use sample-frames (time steps) so L and R channels at the same
	// time instant get identical gain.
	fadeInFrames := m.sampleRate * 10 / 1000  // 10ms in sample-frames
	fadeOutFrames := m.sampleRate * 50 / 1000 // 50ms in sample-frames
	totalFrames := len(m.stingerAudio) / m.stingerChannels

	for i := 0; i < n; i++ {
		pos := m.stingerOffset + i
		framePos := pos / m.stingerChannels
		gain := float32(1.0)

		// Fade in
		if framePos < fadeInFrames {
			gain = float32(framePos) / float32(fadeInFrames)
		}
		// Fade out
		distFromEnd := totalFrames - framePos
		if distFromEnd < fadeOutFrames {
			fadeGain := float32(distFromEnd) / float32(fadeOutFrames)
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
			PhaseInvert:  ch.phaseInvert,
			Balance:      ch.balance,
			HPFFrequency: ch.hpf.Frequency(),
			HPFEnabled:   ch.hpf.Enabled(),
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
		// Include gate settings and gain reduction
		if ch.gate != nil {
			gThreshold, gRange, gAttack, gRelease, gHold, gEnabled := ch.gate.Settings()
			ac.Gate = internal.GateSettings{
				Threshold: gThreshold,
				Range:     gRange,
				Attack:    gAttack,
				Release:   gRelease,
				Hold:      gHold,
			}
			ac.GateEnabled = gEnabled
			ac.GateReduction = ch.gate.GainReduction()
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

// SetLimiterThreshold changes the master limiter threshold.
// thresholdDB: -12 to 0 dBTP (true-peak).
func (m *Mixer) SetLimiterThreshold(thresholdDB float64) error {
	if thresholdDB < -12 || thresholdDB > 0 {
		return fmt.Errorf("audio: limiter threshold must be between -12 and 0 dBTP")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limiter.SetThreshold(thresholdDB)
	return nil
}

// SetLimiterCeiling changes the master limiter output ceiling.
// ceilingDB: -3 to 0 dBTP (true-peak).  -1 dBTP matches EBU R128 s1 for
// streaming; -2 dBTP matches ATSC A/85 §6.5 for broadcast delivery.
func (m *Mixer) SetLimiterCeiling(ceilingDB float64) error {
	if ceilingDB < -3 || ceilingDB > 0 {
		return fmt.Errorf("audio: limiter ceiling must be between -3 and 0 dBTP")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limiter.SetCeiling(ceilingDB)
	return nil
}

// LimiterSettings returns the current limiter threshold, ceiling (in dBTP,
// true-peak), gain reduction (in dB, positive = reducing), and the measured
// true-peak from the last block (in dBTP).  The TruePeak return value is
// JSON-safe: math.Inf(-1) and math.NaN — possible from the raw detector on
// cold start or after Reset — are coerced to -144 dBTP (far below any audible
// signal) so callers can flow the value through encoding/json without
// encode errors.
func (m *Mixer) LimiterSettings() (thresholdDB, ceilingDB, gainReduction, truePeakDBTP float64) {
	// RLock synchronizes the threshold/ceiling reads with SetLimiterThreshold/
	// SetLimiterCeiling (write under m.mu) and Limiter.Process (reads under m.mu),
	// and guarantees a consistent (threshold, ceiling) pair. GainReduction/TruePeak
	// are atomic but read here under the lock for a coherent snapshot.
	m.mu.RLock()
	defer m.mu.RUnlock()
	tp := m.limiter.TruePeakDBTP()
	if math.IsInf(tp, -1) || math.IsNaN(tp) {
		tp = -144.0
	}
	return m.limiter.ThresholdDB(), m.limiter.CeilingDB(), m.limiter.GainReduction(), tp
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
	// Direct output path: zero-latency delivery to MPEG-TS muxer.
	if cb := m.outputAudioCallback.Load(); cb != nil {
		(*cb)(frame)
	}
}

// RingBufferLatency90k returns the program source's ring buffer depth in
// 90kHz ticks. Used by the muxer as a dynamic lip-sync offset — the ring
// buffer adds FIFO latency to audio that the video path (newest-wins frame
// sync) doesn't have.
func (m *Mixer) RingBufferLatency90k() int64 {
	return m.ringBufLatency90k.Load()
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
	}
	return ch.resampler.Resample(pcm)
}

// logDownmixWarn emits the one-time channel-mismatch warning with a message
// that matches what downmixChannels actually did. Fallback-path warnings must
// not claim a Lo/Ro downmix was performed.
func (m *Mixer) logDownmixWarn(sourceKey, path string, srcChannels int, result downmixResult) {
	if result == downmixLoRo {
		m.log.Warn("audio: source channel count exceeds mixer — applying BS.775-3 Lo/Ro downmix",
			"source", sourceKey,
			"path", path,
			"src_channels", srcChannels,
			"mixer_channels", m.numChannels)
		return
	}
	m.log.Warn("audio: source channel count exceeds mixer — no layout-specific matrix, keeping first N channels (surround content discarded)",
		"source", sourceKey,
		"path", path,
		"src_channels", srcChannels,
		"mixer_channels", m.numChannels)
}

// downmixResult reports which coefficient model downmixChannels applied so
// callers can log accurately. Lo/Ro downmixes audibly attenuate surround
// content; the truncation fallback silently drops it and deserves a
// different warning string.
type downmixResult int

const (
	downmixLoRo      downmixResult = iota // ITU-R BS.775-3 5.1 → stereo
	downmixTruncated                      // kept first m.numChannels source channels
)

// downmixChannels folds interleaved multi-channel PCM down to m.numChannels
// so the rest of the mixer hot path sees its configured stride. For the
// 5.1 → stereo case (srcChannels == 6, m.numChannels == 2) the coefficients
// are ITU-R BS.775-3 Lo/Ro — L' = L + 0.707·C + 0.707·Ls, R' = R + 0.707·C +
// 0.707·Rs, LFE dropped — matching the consumer-delivery reference. For any
// other srcChannels > m.numChannels combination the fallback keeps only the
// first m.numChannels source channels; this is non-corrupting but lossy (a
// real layout-aware mix matrix is a future item). The returned
// downmixResult lets callers log which model ran. buf is a reusable output
// slice owned by the caller; it is regrown as needed and the returned slice
// aliases *buf. Caller must hold m.mu (write lock): *buf is mutated.
func (m *Mixer) downmixChannels(buf *[]float32, pcm []float32, srcChannels int) ([]float32, downmixResult) {
	if srcChannels <= 0 || srcChannels <= m.numChannels || len(pcm) == 0 {
		return pcm, downmixLoRo
	}
	framesPerSrc := len(pcm) / srcChannels
	if framesPerSrc == 0 {
		return pcm, downmixLoRo
	}
	needed := framesPerSrc * m.numChannels
	*buf = growBuf(*buf, needed)
	out := *buf

	if srcChannels == 6 && m.numChannels == 2 {
		const k = float32(0.7071067811865475) // 1/sqrt(2) = -3 dB
		for i := 0; i < framesPerSrc; i++ {
			src := pcm[i*6:]
			l := src[0]
			r := src[1]
			c := src[2]
			// src[3] is LFE — dropped per BS.775-3 Lo/Ro.
			ls := src[4]
			rs := src[5]
			out[i*2+0] = l + k*c + k*ls
			out[i*2+1] = r + k*c + k*rs
		}
		return out, downmixLoRo
	}

	// Fallback for layouts outside 5.1→stereo: keep the first m.numChannels
	// source channels. Avoids the stride-stripe corruption the pre-fix code
	// produced, but makes no claim to preserve surround content.
	for i := 0; i < framesPerSrc; i++ {
		base := i * srcChannels
		outBase := i * m.numChannels
		for oc := 0; oc < m.numChannels; oc++ {
			out[outBase+oc] = pcm[base+oc]
		}
	}
	return out, downmixTruncated
}

// upmixMono duplicates each mono sample to all mixer channels.
// srcChannels is the source's actual channel count (must be < m.numChannels).
// buf is a reusable output slice owned by the caller; it is regrown as needed
// and the returned slice aliases *buf. Caller must hold m.mu.
func (m *Mixer) upmixMono(buf *[]float32, pcm []float32, srcChannels int) []float32 {
	if srcChannels <= 0 || srcChannels >= m.numChannels || len(pcm) == 0 {
		return pcm
	}
	// For mono→stereo: each sample is duplicated to numChannels positions.
	// For N→M where N<M: interleave source channels into M output channels,
	// duplicating the last source channel to fill remaining output channels.
	samplesPerSrcFrame := len(pcm) / srcChannels
	needed := samplesPerSrcFrame * m.numChannels
	*buf = growBuf(*buf, needed)
	upmixed := *buf
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
