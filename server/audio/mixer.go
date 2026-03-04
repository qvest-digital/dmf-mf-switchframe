package audio

import (
	"fmt"
	"math"
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
)

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
	crossfadeFrom   string // outgoing source key
	crossfadeTo     string // incoming source key
	crossfadeActive bool
	crossfadePCM    map[string][]float32 // "from" and "to" PCM buffers

	// Metering state
	programPeakL float64 // linear amplitude [0,1]
	programPeakR float64 // linear amplitude [0,1]
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
// Called by the switcher when a cut occurs.
func (m *AudioMixer) OnCut(oldSource, newSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crossfadeFrom = oldSource
	m.crossfadeTo = newSource
	m.crossfadeActive = true
	m.crossfadePCM = make(map[string][]float32)
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
	m.mu.RUnlock()

	// Handle crossfade mode
	if crossfadeActive && (sourceKey == crossfadeFrom || sourceKey == crossfadeTo) {
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
		return
	}
	m.mu.RUnlock()

	// Multi-channel mixing: decode, gain, accumulate, sum, encode
	m.mu.Lock()
	defer m.mu.Unlock()

	// Lazy-init decoder for this channel
	if ch.decoder == nil && m.config.DecoderFactory != nil {
		dec, err := m.config.DecoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			return
		}
		ch.decoder = dec
	}
	if ch.decoder == nil {
		return
	}

	// Decode AAC → float32 PCM
	pcm, err := ch.decoder.Decode(frame.Data)
	if err != nil {
		return
	}

	// Apply per-channel gain
	gain := float32(DBToLinear(ch.level))
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

	// If PTS changed or buffer is empty, start new cycle
	if len(m.mixBuffer) > 0 && m.mixPTS != frame.PTS {
		// PTS mismatch — flush old cycle and start fresh
		m.mixBuffer = make(map[string][]float32)
	}

	m.mixPTS = frame.PTS
	m.mixBuffer[sourceKey] = gainedPCM
	m.mixCycleSize = activeUnmuted

	// Check if all active unmuted channels have contributed
	if len(m.mixBuffer) < activeUnmuted {
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

	// Lazy-init encoder
	if m.encoder == nil && m.config.EncoderFactory != nil {
		enc, err := m.config.EncoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			m.mixBuffer = make(map[string][]float32)
			return
		}
		m.encoder = enc
	}
	if m.encoder == nil {
		m.mixBuffer = make(map[string][]float32)
		return
	}

	// Encode mixed PCM → AAC
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.mixBuffer = make(map[string][]float32)
		return
	}

	// Reset mix buffer for next cycle
	m.mixBuffer = make(map[string][]float32)

	// Output the mixed frame
	m.output(&media.AudioFrame{
		PTS:        frame.PTS,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	})
}

// ingestCrossfadeFrame handles frames during an active crossfade transition.
// It collects one frame from both old and new source, applies equal-power crossfade, and outputs.
func (m *AudioMixer) ingestCrossfadeFrame(sourceKey string, frame *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.crossfadeActive {
		return
	}

	// Ensure decoder exists for this channel
	ch, ok := m.channels[sourceKey]
	if !ok {
		return
	}
	if ch.decoder == nil && m.config.DecoderFactory != nil {
		dec, err := m.config.DecoderFactory(m.sampleRate, m.numChannels)
		if err != nil {
			return
		}
		ch.decoder = dec
	}
	if ch.decoder == nil {
		return
	}

	// Decode
	pcm, err := ch.decoder.Decode(frame.Data)
	if err != nil {
		return
	}

	// Apply per-channel gain
	gain := float32(DBToLinear(ch.level))
	gainedPCM := make([]float32, len(pcm))
	for i, s := range pcm {
		gainedPCM[i] = s * gain
	}

	m.crossfadePCM[sourceKey] = gainedPCM

	// Wait for both sources
	_, hasFrom := m.crossfadePCM[m.crossfadeFrom]
	_, hasTo := m.crossfadePCM[m.crossfadeTo]
	if !hasFrom || !hasTo {
		return
	}

	// Apply equal-power crossfade
	mixed := EqualPowerCrossfade(m.crossfadePCM[m.crossfadeFrom], m.crossfadePCM[m.crossfadeTo])

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
			return
		}
		m.encoder = enc
	}
	if m.encoder == nil {
		m.crossfadeActive = false
		return
	}

	// Encode
	aacData, err := m.encoder.Encode(mixed)
	if err != nil {
		m.crossfadeActive = false
		return
	}

	// Clear crossfade state
	m.crossfadeActive = false
	m.crossfadePCM = nil

	// Output the crossfaded frame
	m.output(&media.AudioFrame{
		PTS:        frame.PTS,
		Data:       aacData,
		SampleRate: m.sampleRate,
		Channels:   m.numChannels,
	})
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

// SetProgramPeak updates the stored program peak levels (linear amplitude).
// Called after metering mixed PCM output.
func (m *AudioMixer) SetProgramPeak(peakL, peakR float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.programPeakL = peakL
	m.programPeakR = peakR
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

// DBToLinear converts decibels to a linear gain multiplier.
func DBToLinear(db float64) float64 {
	if math.IsInf(db, -1) {
		return 0
	}
	return math.Pow(10, db/20)
}
