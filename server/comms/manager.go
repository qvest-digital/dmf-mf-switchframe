package comms

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// Sentinel errors for the comms manager.
var (
	ErrCommsFull       = errors.New("comms session full")
	ErrOpusUnavailable = errors.New("opus codec not available")
	ErrNotInComms      = errors.New("operator not in comms")
)

// Manager orchestrates the voice comms channel, managing participant
// lifecycle, audio mixing, and encoded output distribution.
type Manager struct {
	log *slog.Logger

	mu           sync.Mutex
	participants map[string]*participant
	mixer        *mixer
	onBroadcast  func()
	// encFactory / decFactory construct the Opus codec pair for a new
	// participant. Defaults to the real libopus factories; tests swap in
	// tracking mocks via newManagerWithFactories.
	encFactory encoderFactory
	decFactory decoderFactory

	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager creates a Manager and starts the mix loop goroutine.
func NewManager(onBroadcast func()) *Manager {
	return newManagerWithFactories(onBroadcast, defaultEncoderFactory, defaultDecoderFactory)
}

// newManagerWithEncoderFactory is the test-facing constructor for injecting
// a custom encoder factory while keeping the default decoder.
func newManagerWithEncoderFactory(onBroadcast func(), factory encoderFactory) *Manager {
	return newManagerWithFactories(onBroadcast, factory, defaultDecoderFactory)
}

// newManagerWithFactories is the test-facing constructor for injecting both
// codec factories (tracking mocks that observe post-close call patterns).
func newManagerWithFactories(onBroadcast func(), enc encoderFactory, dec decoderFactory) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		log:          slog.Default().With("component", "comms"),
		participants: make(map[string]*participant),
		mixer:        newMixer(),
		onBroadcast:  onBroadcast,
		encFactory:   enc,
		decFactory:   dec,
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

	// Idempotent re-join.
	if existing, ok := m.participants[operatorID]; ok {
		existing.name = name // Update name on re-join
		m.mu.Unlock()
		return nil
	}

	if len(m.participants) >= MaxParticipants {
		m.mu.Unlock()
		return ErrCommsFull
	}

	p, err := newParticipantWithFactory(operatorID, name, m.encFactory, m.decFactory)
	if err != nil {
		m.mu.Unlock()
		return ErrOpusUnavailable
	}

	m.participants[operatorID] = p
	m.mu.Unlock()

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

	encodeBuf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mixTick(encodeBuf)
		}
	}
}

// mixTick performs one mix cycle: collect PCM, mix N-1, Opus encode, distribute.
func (m *Manager) mixTick(encodeBuf []byte) {
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

	// For each participant, produce their N-1 mix, then delegate the encode
	// + non-blocking send to participant.encodeAndSend so the encoder call is
	// gated by the participant's p.mu / closed lifecycle. This closes the
	// TOCTOU window where a concurrent Leave's close() would otherwise let
	// mixTick Encode on a just-closed cgo handle.
	for id, p := range participants {
		mix := m.mixer.mixFor(id, inputs)
		if err := p.encodeAndSend(mix, encodeBuf); err != nil {
			m.log.Warn("failed to encode mix", "operator", id, "err", err)
		}
	}
	// Speaking state is updated in IngestAudio — no need to recompute here.
}

// Close shuts down the manager, stopping the mix loop and closing all participants.
func (m *Manager) Close() {
	m.cancel()
	if !internal.WaitChanTimeout(m.done, 5*time.Second) {
		m.log.Warn("comms mix loop shutdown timed out after 5s")
	}

	m.mu.Lock()
	for id, p := range m.participants {
		p.close()
		delete(m.participants, id)
	}
	m.mu.Unlock()
}
