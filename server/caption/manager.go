package caption

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zsiec/ccx"
)

// authorBufferMaxRunes bounds the UI preview buffer. Counted in runes, not
// bytes, so the UI never sees a torn multi-byte UTF-8 sequence.
const authorBufferMaxRunes = 200

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

	// Broadcast sink for MoQ caption track (authored captions).
	broadcastSink func(*ccx.CaptionFrame)
}

// NewManager creates a caption manager in ModeOff.
func NewManager() *Manager {
	return &Manager{
		encoder:        NewEncoder(3),
		ptEncoder:      NewEncoder(3),
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

	if m.mode != ModeAuthor && m.mode != ModeAuto {
		m.mu.Unlock()
		return
	}

	m.encoder.IngestText(text)
	m.authorBuffer += text
	m.trimAuthorBuffer()

	cb := m.onStateChange
	bc := m.broadcastSink
	var frame *ccx.CaptionFrame
	if bc != nil {
		frame = m.buildAuthorCaptionFrame()
	}
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
	if bc != nil && frame != nil {
		bc(frame)
	}
}

// IngestNewline queues a carriage return for CEA-608 encoding (author mode).
func (m *Manager) IngestNewline() {
	m.mu.Lock()

	if m.mode != ModeAuthor && m.mode != ModeAuto {
		m.mu.Unlock()
		return
	}

	m.encoder.IngestNewline()
	m.authorBuffer += "\n"
	m.trimAuthorBuffer()

	cb := m.onStateChange
	bc := m.broadcastSink
	var frame *ccx.CaptionFrame
	if bc != nil {
		frame = m.buildAuthorCaptionFrame()
	}
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
	if bc != nil && frame != nil {
		bc(frame)
	}
}

// trimAuthorBuffer bounds m.authorBuffer to authorBufferMaxRunes runes while
// preserving codepoint boundaries. A byte-offset slice would tear multi-byte
// UTF-8 sequences (emoji, CJK, curly quotes) and surface an invalid-UTF-8
// string to the UI state broadcast. Caller must hold m.mu.
func (m *Manager) trimAuthorBuffer() {
	if utf8.RuneCountInString(m.authorBuffer) <= authorBufferMaxRunes {
		return
	}
	runes := []rune(m.authorBuffer)
	m.authorBuffer = string(runes[len(runes)-authorBufferMaxRunes:])
}

// Clear queues an erase-display command (author mode).
func (m *Manager) Clear() {
	m.mu.Lock()

	if m.mode != ModeAuthor && m.mode != ModeAuto {
		m.mu.Unlock()
		return
	}

	m.encoder.Clear()
	m.authorBuffer = ""

	cb := m.onStateChange
	bc := m.broadcastSink
	var frame *ccx.CaptionFrame
	if bc != nil {
		frame = m.buildAuthorCaptionFrame()
	}
	m.mu.Unlock()

	if cb != nil {
		cb()
	}
	if bc != nil && frame != nil {
		bc(frame)
	}
}

// SetPassThroughText re-encodes decoded caption text for pass-through mode.
// Called from the switcher's handleCaptionFrame path with the FULL current
// on-screen text on every incoming caption frame. The text is run through
// a CEA-608 encoder to produce properly formatted cc_data pairs with parity,
// control codes, and rate limiting (1 pair per frame via ConsumeForFrame).
//
// Because the drain rate is limited to one CC pair per video frame, the
// whole screen must NOT be re-transmitted on every update: live roll-up
// sources change the screen text several times per second, far faster than
// a full screen can be re-sent, and re-ingesting already-displayed text in
// roll-up (append) mode duplicates it on the downstream display. Instead,
// only the appended delta is transmitted when the new text continues the
// previous text (including the case where leading rows scrolled off the
// top); a wholesale change erases the downstream display and re-transmits.
func (m *Manager) SetPassThroughText(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.mode != ModePassThrough {
		return
	}

	if text == m.passThroughText {
		return
	}

	prev := m.passThroughText
	m.passThroughText = text

	if text == "" {
		// Upstream cleared the screen: drop any pending output and erase
		// the downstream display rather than leaving stale rows visible.
		m.ptEncoder.Reset()
		m.ptEncoder.Clear()
		return
	}

	if delta, ok := passThroughDelta(prev, text); ok {
		// Roll-up continuation: transmit only the newly appended text.
		// A leading newline in the delta becomes a CR, which makes the
		// downstream decoder scroll exactly like the upstream display did.
		m.ingestPassThroughLocked(delta)
		return
	}

	// Unrelated text (pop-on flip, correction, source change): drop the
	// pending queue, erase the downstream display so previously shown rows
	// are not duplicated, and re-transmit from scratch.
	m.ptEncoder.Reset()
	m.ptEncoder.Clear()
	m.ingestPassThroughLocked(text)
}

// ingestPassThroughLocked feeds text (possibly containing newlines) to the
// pass-through encoder. Caller must hold m.mu.
func (m *Manager) ingestPassThroughLocked(text string) {
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

// passThroughDelta returns the suffix of next that was appended to prev,
// accounting for roll-up scrolling where complete leading lines of prev fall
// off the top of the screen. ok is false when next does not continue prev
// (the text changed in some other way and must be re-transmitted).
func passThroughDelta(prev, next string) (string, bool) {
	if prev == "" {
		return next, true
	}
	rest := prev
	for {
		if strings.HasPrefix(next, rest) {
			return next[len(rest):], true
		}
		// Drop the topmost remaining line of prev (it scrolled off
		// upstream) and try again.
		idx := strings.IndexByte(rest, '\n')
		if idx < 0 {
			return "", false
		}
		rest = rest[idx+1:]
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
//
// Every mode emits at least a null pair (0x80, 0x80) per frame. This maintains
// continuous ATSC A/53 cc_data presence in the stream, signaling to downstream
// decoders (Evertz, Harmonic, etc.) that the stream carries CEA-608/708 captions
// from the very first frame. Without this, decoders would not detect caption
// carriage until the first actual text arrives, causing acquisition delay.
func (m *Manager) consumeForFrameLocked() []CCPair {
	switch m.mode {
	case ModeOff:
		// Null pair keeps the cc_data channel open so downstream decoders
		// know captions are available when the mode changes.
		return []CCPair{NullPair()}

	case ModePassThrough:
		pair := m.ptEncoder.NextPair()
		if pair == nil {
			return []CCPair{NullPair()}
		}
		return []CCPair{*pair}

	case ModeAuthor, ModeAuto:
		pair := m.encoder.NextPair()
		if pair == nil {
			return []CCPair{NullPair()}
		}
		return []CCPair{*pair}

	default:
		return []CCPair{NullPair()}
	}
}

// State returns the current caption state for ControlRoomState broadcast.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := State{
		Mode: m.mode.String(),
	}

	if m.mode == ModeAuthor || m.mode == ModeAuto {
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

// SetBroadcastSink registers a callback for broadcasting authored captions
// via the MoQ captions track. Called with a CaptionFrame whenever author
// text changes (IngestText, IngestNewline, Clear).
func (m *Manager) SetBroadcastSink(fn func(*ccx.CaptionFrame)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastSink = fn
}

// buildAuthorCaptionFrame constructs a CaptionFrame from the current authorBuffer.
// Uses CEA-608 CC1 channel with standard bottom-of-screen roll-up positioning.
// Must be called with m.mu held. Returns nil if buffer is empty and not clearing.
func (m *Manager) buildAuthorCaptionFrame() *ccx.CaptionFrame {
	text := m.authorBuffer

	frame := &ccx.CaptionFrame{
		Channel: 1, // CC1
		Text:    text,
	}

	if text == "" {
		// Empty frame signals clear.
		return frame
	}

	// Build structured region for proper CEA-608 rendering.
	lines := strings.Split(text, "\n")
	// Keep last 4 lines (roll-up style).
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}

	var rows []ccx.CaptionRow
	for i, line := range lines {
		if line == "" {
			continue
		}
		// CEA-608 rows 12-15 (bottom of screen, 0-indexed).
		rowIdx := 15 - (len(lines) - 1 - i)
		rows = append(rows, ccx.CaptionRow{
			Row: rowIdx,
			Spans: []ccx.CaptionSpan{{
				Text:      line,
				FgColor:   "ffffff",
				BgColor:   "000000",
				FgOpacity: 1, // solid
				BgOpacity: 2, // semi-transparent
			}},
		})
	}

	if len(rows) > 0 {
		frame.Regions = []ccx.CaptionRegion{{
			ID:      0,
			Justify: 2, // center
			Rows:    rows,
		}}
	}

	return frame
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

// PendingPair returns the next caption pair to embed in the current video frame.
// All modes (including ModeOff) emit a continuous null pair so downstream
// CEA-608/708 decoders detect the channel from the very first frame.
func (m *Manager) PendingPair() (CCPair, bool) {
	m.mu.Lock()
	pairs := m.consumeForFrameLocked()
	m.mu.Unlock()
	if len(pairs) == 0 {
		return CCPair{}, false
	}
	return pairs[0], true
}

// PendingCaptionSEI returns an Annex B SEI NALU carrying the next caption pair,
// framed for the given codec ("h264" or "h265"). Returns nil only when the
// manager itself is nil — null pairs are still wrapped in a SEI to maintain
// continuous ATSC A/53 cc_data presence in the stream.
func (m *Manager) PendingCaptionSEI(codec string) []byte {
	if m == nil {
		return nil
	}
	pair, ok := m.PendingPair()
	if !ok {
		return nil
	}
	return BuildSEINALU([]CCPair{pair}, codec)
}
