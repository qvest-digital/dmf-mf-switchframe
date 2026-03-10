package caption

import (
	"strings"
	"sync"
)

// Manager is the central caption state machine. It manages the caption mode
// (off/passthrough/author), buffers authored text via a CEA-608 encoder,
// stores pass-through caption data from the program source, and provides
// per-frame caption pairs to the output path.
type Manager struct {
	mu sync.Mutex

	mode    Mode
	encoder *Encoder

	// Pass-through: encoder for re-encoding decoded caption text.
	ptEncoder       *Encoder
	passThroughText string

	// Per-source caption detection.
	sourceCaptions map[string]bool

	// Author input buffer for UI display.
	authorBuffer string

	// State change callback.
	onStateChange func()

	// VANC sink for MXL caption output.
	vancSink func([]CCPair)
}

// NewManager creates a caption manager in ModeOff.
func NewManager() *Manager {
	return &Manager{
		encoder:        NewEncoder(2),
		ptEncoder:      NewEncoder(2),
		sourceCaptions: make(map[string]bool),
	}
}

// SetMode sets the caption operating mode.
func (m *Manager) SetMode(mode Mode) {
	m.mu.Lock()

	if m.mode == mode {
		m.mu.Unlock()
		return
	}

	m.mode = mode

	// Reset state on mode change.
	m.ptEncoder.Reset()
	m.passThroughText = ""
	m.encoder.Reset()
	m.authorBuffer = ""

	cb := m.onStateChange
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// Mode returns the current caption mode.
func (m *Manager) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// IngestText queues text for CEA-608 encoding (author mode).
func (m *Manager) IngestText(text string) {
	m.mu.Lock()

	if m.mode != ModeAuthor {
		m.mu.Unlock()
		return
	}

	m.encoder.IngestText(text)
	m.authorBuffer += text
	// Keep buffer to a reasonable display length.
	if len(m.authorBuffer) > 200 {
		m.authorBuffer = m.authorBuffer[len(m.authorBuffer)-200:]
	}

	cb := m.onStateChange
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// IngestNewline queues a carriage return for CEA-608 encoding (author mode).
func (m *Manager) IngestNewline() {
	m.mu.Lock()

	if m.mode != ModeAuthor {
		m.mu.Unlock()
		return
	}

	m.encoder.IngestNewline()
	m.authorBuffer += "\n"
	if len(m.authorBuffer) > 200 {
		m.authorBuffer = m.authorBuffer[len(m.authorBuffer)-200:]
	}

	cb := m.onStateChange
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// Clear queues an erase-display command (author mode).
func (m *Manager) Clear() {
	m.mu.Lock()

	if m.mode != ModeAuthor {
		m.mu.Unlock()
		return
	}

	m.encoder.Clear()
	m.authorBuffer = ""

	cb := m.onStateChange
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// SetPassThroughText re-encodes decoded caption text for pass-through mode.
// Called from the switcher's handleCaptionFrame path. The text is run through
// a CEA-608 encoder to produce properly formatted cc_data pairs with parity,
// control codes, and rate limiting (1 pair per frame via ConsumeForFrame).
func (m *Manager) SetPassThroughText(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.mode != ModePassThrough {
		return
	}

	if text == m.passThroughText {
		return
	}

	m.passThroughText = text
	// Clear and re-encode the new text through the CEA-608 encoder.
	m.ptEncoder.Reset()
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			m.ptEncoder.IngestText(line)
		}
		if i < len(lines)-1 {
			m.ptEncoder.IngestNewline()
		}
	}
}

// NotifySourceCaptions tracks whether a source has embedded captions.
func (m *Manager) NotifySourceCaptions(sourceKey string, has bool) {
	m.mu.Lock()

	if has == m.sourceCaptions[sourceKey] {
		m.mu.Unlock()
		return
	}

	m.sourceCaptions[sourceKey] = has

	cb := m.onStateChange
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// ConsumeForFrame returns the caption pairs to embed in the current video frame.
// In author mode, returns the next pair from the encoder queue (or a null pair).
// In passthrough mode, returns the next re-encoded pair (rate-limited to 1/frame).
// In off mode, returns nil.
func (m *Manager) ConsumeForFrame() []CCPair {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.consumeForFrameLocked()
}

// consumeForFrameLocked implements ConsumeForFrame. Must be called with m.mu held.
func (m *Manager) consumeForFrameLocked() []CCPair {
	switch m.mode {
	case ModeOff:
		return nil

	case ModePassThrough:
		pair := m.ptEncoder.NextPair()
		if pair == nil {
			return nil
		}
		return []CCPair{*pair}

	case ModeAuthor:
		pair := m.encoder.NextPair()
		if pair == nil {
			// Emit null pair to maintain cc_data presence in stream.
			null := NullPair()
			return []CCPair{null}
		}
		return []CCPair{*pair}

	default:
		return nil
	}
}

// State returns the current caption state for ControlRoomState broadcast.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := State{
		Mode: m.mode.String(),
	}

	if m.mode == ModeAuthor {
		s.AuthorBuffer = m.authorBuffer
	}

	if len(m.sourceCaptions) > 0 {
		s.SourceCaptions = make(map[string]bool, len(m.sourceCaptions))
		for k, v := range m.sourceCaptions {
			s.SourceCaptions[k] = v
		}
	}

	return s
}

// OnStateChange registers a callback invoked when caption state changes.
// The callback is called WITHOUT holding the manager lock to prevent deadlocks
// (the callback typically chains to State() which acquires the lock).
func (m *Manager) OnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// SetVANCSink registers a callback for VANC caption output (MXL SDI).
// Called with pairs each time ConsumeForFrame returns non-nil data.
func (m *Manager) SetVANCSink(fn func([]CCPair)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vancSink = fn
}

// ConsumeForFrameWithVANC returns caption pairs and also dispatches to the VANC sink.
// Captures sink and pairs under lock, then calls sink after releasing lock to avoid
// blocking other Manager operations during potentially slow VANC writes.
func (m *Manager) ConsumeForFrameWithVANC() []CCPair {
	m.mu.Lock()
	pairs := m.consumeForFrameLocked()
	sink := m.vancSink
	m.mu.Unlock()

	if sink != nil && len(pairs) > 0 {
		sink(pairs)
	}

	return pairs
}
