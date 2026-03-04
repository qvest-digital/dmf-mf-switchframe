package audio

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
)

// crossfadeTimeout is the maximum time to wait for both sources to deliver
// frames during a crossfade. If the outgoing source disconnects, the crossfade
// completes with only the incoming source's audio after this deadline.
const crossfadeTimeout = 50 * time.Millisecond

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
	sourceKey string
	level     float64 // dB (-inf to +12)
	muted     bool
	afv       bool
	active    bool
	decoder   AudioDecoder // lazy init, nil in passthrough
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
	mixBuffer    map[string][]float32 // sourceKey → decoded PCM for current cycle
	mixPTS       int64                // PTS of the current mix cycle
	mixCycleSize int                  // how many active unmuted channels expected

	// Crossfade state: one AAC frame (~23ms) equal-power crossfade on cut.
	crossfadeFrom     string               // outgoing source key
	crossfadeTo       string               // incoming source key
	crossfadeActive   bool
	crossfadePCM      map[string][]float32 // "from" and "to" PCM buffers
	crossfadeDeadline time.Time            // timeout for crossfade completion

	// Transition crossfade state: multi-frame crossfade synced with video transition.
	transCrossfadeActive   bool
	transCrossfadeFrom     string  // outgoing source key
	transCrossfadeTo       string  // incoming source key
	transCrossfadePosition float64 // 0.0 = fully old, 1.0 = fully new

	// Metering state
	programPeakL float64 // linear amplitude [0,1]
	programPeakR float64 // linear amplitude [0,1]

	// Debug counters (atomic, no lock needed)
	framesPassthrough atomic.Int64
	framesMixed       atomic.Int64
	crossfadeCount    atomic.Int64
	crossfadeTimeouts atomic.Int64
	decodeErrors      atomic.Int64
	encodeErrors      atomic.Int64
}

// NewMixer creates an AudioMixer.
func NewMixer(config MixerConfig) *AudioMixer {
	return &AudioMixer{
		channels:    make(map[string]*Channel),
		masterLevel: 0.0,
		sampleRate:  config.SampleRate,
		numChannels: config.Channels,
		output:      config.Output,
		passthrough: true,
		config:      config,
	}
}

// Close releases all codec resources.
func (m *AudioMixer) Close() error {
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

// AddChannel registers a source with the mixer.
func (m *AudioMixer) AddChannel(sourceKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[sourceKey] = &Channel{sourceKey: sourceKey}
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
	m.crossfadeDeadline = time.Now().Add(crossfadeTimeout)
	m.crossfadeCount.Add(1)
}

// OnTransitionStart begins a multi-frame crossfade between old and new source,
// synchronized with a video transition. The new source channel is activated so
// its audio frames are accepted during the transition.
func (m *AudioMixer) OnTransitionStart(oldSource, newSource string, durationMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadePosition = 0.0

	// Ensure the incoming source's channel is active so frames are accepted
	if ch, ok := m.channels[newSource]; ok {
		ch.active = true
	}
	m.recalcPassthrough()
}

// OnTransitionPosition updates the crossfade position (0.0 = fully old, 1.0 = fully new).
// Called by the switcher as the video transition progresses.
func (m *AudioMixer) OnTransitionPosition(position float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.recalcPassthrough()
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

// TransitionGains returns the equal-power crossfade gains for the old and new sources
// based on the current transition position:
//
//	oldGain = cos(position × π/2)
//	newGain = sin(position × π/2)
//
// When no transition is active, returns (1.0, 0.0).
func (m *AudioMixer) TransitionGains() (oldGain, newGain float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.transCrossfadeActive {
		return 1.0, 0.0
	}
	oldGain = math.Cos(m.transCrossfadePosition * math.Pi / 2)
	newGain = math.Sin(m.transCrossfadePosition * math.Pi / 2)
	return oldGain, newGain
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
		m.output(frame)
		m.framesPassthrough.Add(1)
		return
	}
	m.mu.RUnlock()

	// Multi-channel mixing: decode, gain, accumulate, sum, encode
	m.mu.Lock()

	// Lazy-init decoder for this channel
	if ch.decoder == nil && m.config.DecoderFactory != nil {
		dec, err := m.config.DecoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.mu.Unlock()
			return
		}
		ch.decoder = dec
	}
	if ch.decoder == nil {
		m.mu.Unlock()
		return
	}

	// Decode AAC → float32 PCM
	pcm, err := ch.decoder.Decode(frame.Data)
	if err != nil {
		m.decodeErrors.Add(1)
		m.mu.Unlock()
		return
	}

	// Apply per-channel gain
	gain := float32(DBToLinear(ch.level))

	// Apply transition crossfade gain if active
	if m.transCrossfadeActive {
		if sourceKey == m.transCrossfadeFrom {
			gain *= float32(math.Cos(m.transCrossfadePosition * math.Pi / 2))
		} else if sourceKey == m.transCrossfadeTo {
			gain *= float32(math.Sin(m.transCrossfadePosition * math.Pi / 2))
		}
	}

	gainedPCM := make([]float32, len(pcm))
	for i, s := range pcm {
		gainedPCM[i] = s * gain
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
	// When all active unmuted channels have contributed, produce output.
	m.mixBuffer[sourceKey] = gainedPCM
	m.mixPTS = frame.PTS
	m.mixCycleSize = activeUnmuted

	// Check if all active unmuted channels have contributed
	if len(m.mixBuffer) < activeUnmuted {
		m.mu.Unlock()
		return // wait for more channels
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

	// Update program peak metering
	peakL, peakR := PeakLevel(mixed, m.numChannels)
	m.programPeakL = peakL
	m.programPeakR = peakR

	// Lazy-init encoder
	if m.encoder == nil && m.config.EncoderFactory != nil {
		enc, err := m.config.EncoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.mixBuffer = make(map[string][]float32)
			m.mu.Unlock()
			return
		}
		m.encoder = enc
	}
	if m.encoder == nil {
		m.mixBuffer = make(map[string][]float32)
		m.mu.Unlock()
		return
	}

	// Encode mixed PCM → AAC
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.encodeErrors.Add(1)
		m.mixBuffer = make(map[string][]float32)
		m.mu.Unlock()
		return
	}
	m.framesMixed.Add(1)

	// Reset mix buffer for next cycle
	m.mixBuffer = make(map[string][]float32)

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
	if ch.decoder == nil && m.config.DecoderFactory != nil {
		dec, err := m.config.DecoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.mu.Unlock()
			return
		}
		ch.decoder = dec
	}
	if ch.decoder == nil {
		m.mu.Unlock()
		return
	}

	// Decode
	pcm, err := ch.decoder.Decode(frame.Data)
	if err != nil {
		m.decodeErrors.Add(1)
		m.mu.Unlock()
		return
	}

	// Apply per-channel gain
	gain := float32(DBToLinear(ch.level))
	gainedPCM := make([]float32, len(pcm))
	for i, s := range pcm {
		gainedPCM[i] = s * gain
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
		m.crossfadeActive = false
		m.mu.Unlock()
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
func (m *AudioMixer) recalcPassthrough() {
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
		m.passthrough = !ch.muted && ch.level == 0
	} else {
		m.passthrough = false
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
		result[key] = internal.AudioChannel{Level: ch.level, Muted: ch.muted, AFV: ch.afv}
	}
	return result
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
