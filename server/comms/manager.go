package comms

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Sentinel errors for the comms manager.
var (
	ErrCommsFull      = errors.New("comms session full")
	ErrOpusUnavailable = errors.New("opus codec not available")
	ErrNotInComms     = errors.New("operator not in comms")
)

// Manager orchestrates the voice comms channel, managing participant
// lifecycle, audio mixing, and encoded output distribution.
type Manager struct {
	log *slog.Logger

	mu           sync.Mutex
	participants map[string]*participant
	mixer        *mixer
	onBroadcast  func()

	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager creates a Manager and starts the mix loop goroutine.
func NewManager(onBroadcast func()) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		log:          slog.Default().With("component", "comms"),
		participants: make(map[string]*participant),
		mixer:        newMixer(),
		onBroadcast:  onBroadcast,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	go m.mixLoop(ctx)
	return m
}

// Join adds an operator to the comms channel. If the operator is already
// present, the call is idempotent and returns nil. Returns ErrCommsFull
// if the channel is at capacity.
func (m *Manager) Join(operatorID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Idempotent re-join.
	if _, ok := m.participants[operatorID]; ok {
		return nil
	}

	if len(m.participants) >= MaxParticipants {
		return ErrCommsFull
	}

	p, err := newParticipant(operatorID, name)
	if err != nil {
		return ErrOpusUnavailable
	}

	m.participants[operatorID] = p
	m.log.Info("operator joined comms", "operator", operatorID, "name", name)

	if m.onBroadcast != nil {
		m.onBroadcast()
	}

	return nil
}

// Leave removes an operator from the comms channel.
func (m *Manager) Leave(operatorID string) {
	m.mu.Lock()
	p, ok := m.participants[operatorID]
	if ok {
		delete(m.participants, operatorID)
	}
	m.mu.Unlock()

	if ok {
		p.close()
		m.log.Info("operator left comms", "operator", operatorID)
		if m.onBroadcast != nil {
			m.onBroadcast()
		}
	}
}

// SetMuted sets the mute state for a participant. Returns ErrNotInComms
// if the operator is not in the channel.
func (m *Manager) SetMuted(operatorID string, muted bool) error {
	m.mu.Lock()
	p, ok := m.participants[operatorID]
	m.mu.Unlock()

	if !ok {
		return ErrNotInComms
	}

	p.setMuted(muted)

	if m.onBroadcast != nil {
		m.onBroadcast()
	}

	return nil
}

// State returns the current comms state for broadcast. Returns nil if
// there are no participants (omitted from ControlRoomState).
func (m *Manager) State() *State {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.participants) == 0 {
		return nil
	}

	infos := make([]ParticipantInfo, 0, len(m.participants))
	for _, p := range m.participants {
		infos = append(infos, p.info())
	}

	return &State{
		Active:       true,
		Participants: infos,
	}
}

// IngestAudio decodes incoming Opus audio from a participant, updates
// their speaking state, and stores the PCM for the next mix cycle.
func (m *Manager) IngestAudio(operatorID string, opusData []byte) error {
	m.mu.Lock()
	p, ok := m.participants[operatorID]
	m.mu.Unlock()

	if !ok {
		return ErrNotInComms
	}

	pcm, err := p.decodeAudio(opusData)
	if err != nil {
		return err
	}

	p.updateSpeaking(pcm)
	return nil
}

// IngestRawPCM accepts raw int16 PCM samples from a participant (bypassing
// Opus decode). Used when the browser sends unencoded audio. The data byte
// slice is interpreted as little-endian int16 samples.
func (m *Manager) IngestRawPCM(operatorID string, data []byte) error {
	m.mu.Lock()
	p, ok := m.participants[operatorID]
	m.mu.Unlock()

	if !ok {
		return ErrNotInComms
	}

	// Convert bytes to int16 (little-endian, matching browser's Int16Array)
	sampleCount := len(data) / 2
	if sampleCount == 0 {
		return nil
	}
	if sampleCount > FrameSize {
		sampleCount = FrameSize
	}

	pcm := make([]int16, sampleCount)
	for i := 0; i < sampleCount; i++ {
		pcm[i] = int16(data[2*i]) | int16(data[2*i+1])<<8
	}

	p.ingestRawPCM(pcm)
	p.updateSpeaking(pcm)
	return nil
}

// GetParticipant returns the participant for the given operator ID.
func (m *Manager) GetParticipant(operatorID string) (*participant, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.participants[operatorID]
	return p, ok
}

// SendTestPacket enqueues an encoded audio packet directly to a participant's
// send channel for testing the write path. Returns false if the participant is
// not found or the channel is full.
func (m *Manager) SendTestPacket(operatorID string, data []byte) bool {
	m.mu.Lock()
	p, ok := m.participants[operatorID]
	m.mu.Unlock()

	if !ok {
		return false
	}

	return p.trySend(data)
}

// mixLoop runs at 20ms intervals, mixing audio for all participants.
func (m *Manager) mixLoop(ctx context.Context) {
	defer close(m.done)

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mixTick()
		}
	}
}

// mixTick performs one mix cycle: collect PCM, mix N-1, distribute.
func (m *Manager) mixTick() {
	m.mu.Lock()

	// Skip if fewer than 2 participants.
	if len(m.participants) < 2 {
		m.mu.Unlock()
		return
	}

	// Collect PCM from all participants.
	inputs := make(map[string][]int16, len(m.participants))
	participants := make(map[string]*participant, len(m.participants))
	for id, p := range m.participants {
		participants[id] = p
		if pcm := p.consumePCM(); pcm != nil {
			inputs[id] = pcm
		}
	}
	m.mu.Unlock()

	// If no audio data was contributed, skip mixing.
	if len(inputs) == 0 {
		return
	}

	speakingChanged := false

	// For each participant, produce their N-1 mix and send as raw PCM.
	for id, p := range participants {
		mix := m.mixer.mixFor(id, inputs)

		// Convert int16 PCM to little-endian bytes (matching browser Int16Array).
		packet := make([]byte, len(mix)*2)
		for i, s := range mix {
			packet[2*i] = byte(s)
			packet[2*i+1] = byte(s >> 8)
		}

		// Non-blocking send — drop if channel full or participant closed.
		p.trySend(packet)

		// Track speaking state changes.
		p.mu.Lock()
		wasSpeaking := p.speaking
		p.mu.Unlock()

		// Check if this participant contributed audio and recompute speaking.
		if pcm, ok := inputs[id]; ok {
			p.updateSpeaking(pcm)
			p.mu.Lock()
			nowSpeaking := p.speaking
			p.mu.Unlock()
			if wasSpeaking != nowSpeaking {
				speakingChanged = true
			}
		}
	}

	if speakingChanged && m.onBroadcast != nil {
		m.onBroadcast()
	}
}

// Close shuts down the manager, stopping the mix loop and closing all participants.
func (m *Manager) Close() {
	m.cancel()
	<-m.done

	m.mu.Lock()
	for id, p := range m.participants {
		p.close()
		delete(m.participants, id)
	}
	m.mu.Unlock()
}
