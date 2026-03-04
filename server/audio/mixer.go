package audio

import (
	"fmt"
	"math"
	"sync"

	"github.com/zsiec/prism/media"
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

// IsPassthrough returns whether the mixer is in passthrough mode.
func (m *AudioMixer) IsPassthrough() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.passthrough
}

// IngestFrame processes an audio frame from a source.
func (m *AudioMixer) IngestFrame(sourceKey string, frame *media.AudioFrame) {
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

	// Multi-channel mixing handled in Task 7
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

// DBToLinear converts decibels to a linear gain multiplier.
func DBToLinear(db float64) float64 {
	if math.IsInf(db, -1) {
		return 0
	}
	return math.Pow(10, db/20)
}
