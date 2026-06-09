package caption

import (
	"log/slog"
	"math/bits"
	"sync"
	"time"
)

const subWarnInterval = time.Minute

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
	mu            sync.Mutex
	queue         []CCPair
	rollUpRows    int
	inited        bool
	lastSubWarnAt time.Time
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

// DisplayWidth returns the number of CEA-608 display cells the given UTF-8 text
// occupies once encoded. This is NOT len(text): the encoder maps each rune to a
// fixed set of on-screen cells, and a multi-byte UTF-8 rune usually renders as a
// single cell. Use this (not len) for line-wrap/cursor math so non-English
// captions (café, señor, …) wrap at the correct column.
//
// Per-rune cell counts mirror lookupRune:
//   - basic / Spanish-basic / special (0x11) / unsupported ('?') -> 1 cell
//   - extended (0x12/0x13): the fallback ASCII char is auto-backspaced by the
//     decoder, so the rune occupies 1 cell
//   - ASCII-sequence runes expand to their literal width (… -> "..." = 3 cells,
//     em dash -> "--" = 2 cells)
//   - control/skip runes -> 0 cells
func DisplayWidth(text string) int {
	cells := 0
	for _, r := range text {
		m := lookupRune(r)
		switch m.kind {
		case runeKindAsciiSeq:
			cells += len(m.asciiSeq)
		case runeKindSkip:
			// no display cell
		default:
			// basic, special, extended, unsupported all render as one cell.
			cells++
		}
	}
	return cells
}

// IngestText queues CEA-608 byte pairs for the given UTF-8 text.
// Runes are decoded and mapped via lookupRune; unmapped non-Latin runes
// become '?' and a rate-limited warning is emitted.
func (e *Encoder) IngestText(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ensureInit()

	// Pair-aware packer. A 2-byte special/extended code (prefix 0x11/0x12/0x13
	// in byte[0], data in byte[1]) must occupy a single transmitted CC pair per
	// CEA-608-E §8.5.4–5.6. Conformant decoders identify these codes by
	// inspecting byte[0]; if the prefix lands in byte[1] the code is dropped or
	// rendered as garbage. So we buffer one pending basic byte and, when a
	// 2-byte code appears at an odd position, pad with 0x80 to realign.
	var pending byte
	hasPending := false
	emitBasic := func(b byte) {
		if hasPending {
			e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(pending), oddParity(b)}})
			hasPending = false
			return
		}
		pending = b
		hasPending = true
	}
	emitTwoByte := func(prefix, data byte) {
		if hasPending {
			e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(pending), 0x80}})
			hasPending = false
		}
		e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(prefix), oddParity(data)}})
	}

	var unsupported []rune
	for _, r := range text {
		m := lookupRune(r)
		switch m.kind {
		case runeKindBasic:
			emitBasic(m.basic)
		case runeKindAsciiSeq:
			for i := 0; i < len(m.asciiSeq); i++ {
				emitBasic(m.asciiSeq[i])
			}
		case runeKindSpecial:
			// Special characters (0x11 prefix) do NOT trigger a decoder
			// backspace — emit the 2-byte code directly.
			emitTwoByte(m.twoByte[0], m.twoByte[1])
		case runeKindExtended:
			// Extended characters (0x12/0x13 prefix) trigger an automatic
			// decoder backspace per CEA-608-E §8.4. Transmit the closest
			// standard ASCII char first so the backspace consumes it (and
			// non-extended decoders show a sensible glyph) instead of deleting
			// the previous legitimate on-screen character.
			emitBasic(m.fallback)
			emitTwoByte(m.twoByte[0], m.twoByte[1])
		case runeKindUnsupported:
			emitBasic('?')
			unsupported = append(unsupported, r)
		case runeKindSkip:
		}
	}
	if hasPending {
		e.queue = append(e.queue, CCPair{Data: [2]byte{oddParity(pending), 0x80}})
	}

	if len(unsupported) > 0 && time.Since(e.lastSubWarnAt) >= subWarnInterval {
		e.lastSubWarnAt = time.Now()
		slog.Warn("caption: characters substituted",
			"count", len(unsupported),
			"sample", string(unsupported[:min(len(unsupported), 8)]),
			"hint", "CEA-608 supports Latin scripts only; use CEA-708 native or EBU-TT-D for full Unicode")
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
