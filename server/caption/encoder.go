package caption

import (
	"math/bits"
	"sync"
)

// CEA-608 control code constants for CC1 (field 1, channel 1).
const (
	// Roll-up mode commands (sent twice per spec).
	cc608RU2 byte = 0x25 // Roll-Up 2 rows
	cc608RU3 byte = 0x26 // Roll-Up 3 rows
	cc608RU4 byte = 0x27 // Roll-Up 4 rows

	// Miscellaneous control codes (channel 1).
	cc608CR  byte = 0x2D // Carriage Return (scroll up + new line)
	cc608EDM byte = 0x2C // Erase Displayed Memory (clear screen)
	cc608ENM byte = 0x2E // Erase Non-Displayed Memory
	cc608EOC byte = 0x2F // End of Caption (flip memories)
	cc608BS  byte = 0x21 // Backspace

	// Control byte prefix for CC1 (channel 1, field 1).
	cc608Ctrl byte = 0x14
)

// oddParity sets bit 7 to produce odd parity for CEA-608 data bytes.
// CEA-608 requires that each byte has an odd number of 1-bits.
func oddParity(b byte) byte {
	// Clear bit 7, count 1-bits in lower 7 bits.
	low7 := b & 0x7F
	if bits.OnesCount8(low7)%2 == 0 {
		// Even number of 1s — set bit 7 to make it odd.
		return low7 | 0x80
	}
	// Already odd — clear bit 7.
	return low7
}

// Encoder converts text input to CEA-608 CC1 byte pairs using roll-up mode.
// This is the standard mode for real-time captioning where text scrolls
// upward line by line. Rate is limited to 2 bytes per frame at 29.97fps
// (~60 displayable characters per second).
type Encoder struct {
	mu         sync.Mutex
	queue      []CCPair
	rollUpRows int
	inited     bool
}

// NewEncoder creates a CEA-608 encoder in roll-up mode.
// rollUpRows must be 2, 3, or 4 (defaults to 2 if invalid).
func NewEncoder(rollUpRows int) *Encoder {
	if rollUpRows < 2 || rollUpRows > 4 {
		rollUpRows = 2
	}
	return &Encoder{
		rollUpRows: rollUpRows,
	}
}

// rollUpCmd returns the roll-up command byte for the configured row count.
func (e *Encoder) rollUpCmd() byte {
	switch e.rollUpRows {
	case 3:
		return cc608RU3
	case 4:
		return cc608RU4
	default:
		return cc608RU2
	}
}

// ensureInit queues the initialization sequence if not yet sent:
// 1. Roll-Up command (sent twice per CEA-608 spec)
// 2. PAC (Preamble Address Code) to position cursor at row 14 (bottom safe area)
func (e *Encoder) ensureInit() {
	if e.inited {
		return
	}
	e.inited = true

	ruCmd := e.rollUpCmd()

	// Roll-up mode command — must be sent twice per CEA-608 spec.
	// Apply odd parity to both bytes.
	e.queue = append(e.queue,
		CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(ruCmd)}},
		CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(ruCmd)}},
	)

	// PAC: Row 14 (bottom of safe area), column 0, no attributes.
	// Row 14 PAC for CC1: 0x14, 0x60 (row 14, indent 0).
	e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(0x60)}})
}

// IngestText queues character pairs for the given text.
// ASCII printable characters (0x20-0x7F) map directly to CEA-608.
// Characters are queued as pairs — the encoder emits one pair per frame.
func (e *Encoder) IngestText(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ensureInit()

	// Build character byte slice from printable ASCII (0x20-0x7E).
	// CEA-608 character set excludes 0x7F (DEL).
	var chars []byte
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch >= 0x20 && ch <= 0x7E {
			chars = append(chars, ch)
		}
	}

	// Pack characters into pairs with odd parity.
	// CEA-608 allows two characters per pair.
	for i := 0; i < len(chars); i += 2 {
		if i+1 < len(chars) {
			e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(chars[i]), oddParity(chars[i+1])}})
		} else {
			// Odd character — pad second byte with null.
			e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(chars[i]), 0x80}})
		}
	}
}

// IngestNewline queues a Carriage Return command which scrolls existing
// text up and positions the cursor at the beginning of a new bottom line.
func (e *Encoder) IngestNewline() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ensureInit()

	// CR must be sent twice per spec for reliability.
	e.queue = append(e.queue,
		CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(cc608CR)}},
		CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(cc608CR)}},
	)
}

// Clear queues an Erase Displayed Memory command to clear the screen.
func (e *Encoder) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// EDM sent twice per spec.
	e.queue = append(e.queue,
		CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(cc608EDM)}},
		CCPair{Data: [2]byte{oddParity(cc608Ctrl), oddParity(cc608EDM)}},
	)
}

// NextPair returns the next caption pair for the current frame.
// Returns nil when the queue is empty (caller should emit a null pair 0x80,0x80).
func (e *Encoder) NextPair() *CCPair {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.queue) == 0 {
		return nil
	}

	pair := e.queue[0]
	e.queue = e.queue[1:]

	// Reclaim backing array memory when queue drains. The queue[1:] reslicing
	// keeps the entire backing array alive even after full drain, so nil it out
	// to allow GC to collect it. Reallocation on next IngestText is cheap.
	if len(e.queue) == 0 {
		e.queue = nil
	}

	return &pair
}

// QueueLen returns the number of pairs waiting to be emitted.
func (e *Encoder) QueueLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queue)
}

// Reset clears the encoder state, forcing re-initialization on next use.
func (e *Encoder) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queue = nil
	e.inited = false
}
