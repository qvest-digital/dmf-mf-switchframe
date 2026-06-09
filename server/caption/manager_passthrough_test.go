package caption

import (
	"strings"
	"testing"
)

// drainPassThrough consumes caption pairs until the manager runs dry
// (returns a null pair), collecting the non-null pairs.
func drainPassThrough(t *testing.T, m *Manager) []CCPair {
	t.Helper()
	var out []CCPair
	for i := 0; i < 2000; i++ {
		pairs := m.ConsumeForFrame()
		if len(pairs) == 1 && pairs[0].IsNull() {
			return out
		}
		out = append(out, pairs...)
	}
	t.Fatal("encoder did not drain within 2000 frames")
	return nil
}

// decode608Text extracts printable basic characters from CC pairs (parity
// stripped), ignoring control codes and padding.
func decode608Text(pairs []CCPair) string {
	var b strings.Builder
	for _, p := range pairs {
		b0 := p.Data[0] & 0x7F
		b1 := p.Data[1] & 0x7F
		if b0 >= 0x20 {
			b.WriteByte(b0)
			if b1 >= 0x20 {
				b.WriteByte(b1)
			}
		}
	}
	return b.String()
}

// countCtrl counts pairs matching the CC1 control code (0x14, code), with parity.
func countCtrl(pairs []CCPair, code byte) int {
	n := 0
	for _, p := range pairs {
		if p.Data[0]&0x7F == cc608Ctrl && p.Data[1]&0x7F == code {
			n++
		}
	}
	return n
}

// countRollUpInit counts roll-up mode commands (RU2/RU3/RU4).
func countRollUpInit(pairs []CCPair) int {
	return countCtrl(pairs, cc608RU2) + countCtrl(pairs, cc608RU3) + countCtrl(pairs, cc608RU4)
}

// TestPassThrough_RollUpAppend_OnlyDeltaTransmitted reproduces finding #85:
// roll-up sources re-deliver the FULL screen text on every caption frame.
// The manager must transmit only the appended delta — resetting the encoder
// and re-transmitting the whole screen garbles and duplicates the output
// (the drain rate is just 1 pair per video frame).
func TestPassThrough_RollUpAppend_OnlyDeltaTransmitted(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("HELLO")
	first := drainPassThrough(t, m)
	if got := decode608Text(first); got != "HELLO" {
		t.Fatalf("initial text = %q, want %q", got, "HELLO")
	}

	// Upstream roll-up appends to the same line.
	m.SetPassThroughText("HELLO WORLD")
	delta := drainPassThrough(t, m)

	if got := decode608Text(delta); got != " WORLD" {
		t.Errorf("delta text = %q, want %q (already-displayed text must not be re-transmitted)",
			got, " WORLD")
	}
	if n := countRollUpInit(delta); n != 0 {
		t.Errorf("append delta re-initialized roll-up mode %d times, want 0", n)
	}
	if n := countCtrl(delta, cc608EDM); n != 0 {
		t.Errorf("append delta erased the display %d times, want 0", n)
	}
}

// TestPassThrough_RollUpScroll_EmitsCRAndNewLineOnly: when the top row
// scrolls off upstream, only a carriage return plus the new bottom row must
// be transmitted — the CEA-608 decoder scrolls its own display on CR.
func TestPassThrough_RollUpScroll_EmitsCRAndNewLineOnly(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("ROW ONE\nROW TWO")
	drainPassThrough(t, m)

	// ROW ONE scrolled off; ROW THREE appended.
	m.SetPassThroughText("ROW TWO\nROW THREE")
	delta := drainPassThrough(t, m)

	if got := decode608Text(delta); got != "ROW THREE" {
		t.Errorf("scroll delta text = %q, want %q", got, "ROW THREE")
	}
	if n := countCtrl(delta, cc608CR); n != 2 { // CR is sent twice per spec
		t.Errorf("scroll delta carriage returns = %d, want 2", n)
	}
	if n := countRollUpInit(delta); n != 0 {
		t.Errorf("scroll delta re-initialized roll-up mode %d times, want 0", n)
	}
}

// TestPassThrough_UnrelatedText_ClearsDownstreamDisplay: a wholesale text
// change (pop-on flip, source change) must erase the downstream display
// before re-transmitting, otherwise previously displayed rows duplicate.
func TestPassThrough_UnrelatedText_ClearsDownstreamDisplay(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("FIRST CAPTION")
	drainPassThrough(t, m)

	m.SetPassThroughText("SOMETHING ELSE")
	pairs := drainPassThrough(t, m)

	if got := decode608Text(pairs); got != "SOMETHING ELSE" {
		t.Errorf("text = %q, want %q", got, "SOMETHING ELSE")
	}
	if n := countCtrl(pairs, cc608EDM); n == 0 {
		t.Error("unrelated text change did not erase the downstream display (EDM missing); old rows would duplicate on screen")
	}
}

// TestPassThrough_EmptyText_ClearsDisplay: upstream clearing the screen must
// clear the downstream display too.
func TestPassThrough_EmptyText_ClearsDisplay(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("HI")
	drainPassThrough(t, m)

	m.SetPassThroughText("")
	pairs := drainPassThrough(t, m)

	if got := decode608Text(pairs); got != "" {
		t.Errorf("text after clear = %q, want empty", got)
	}
	if n := countCtrl(pairs, cc608EDM); n == 0 {
		t.Error("empty pass-through text did not erase the downstream display")
	}
}

// TestPassThrough_MidTransmissionAppend_NoDuplication: an update arriving
// while the previous text is still queued must not duplicate or drop content.
func TestPassThrough_MidTransmissionAppend_NoDuplication(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("AB")
	// Consume only one pair (the first RU init command) — the rest is queued.
	got := m.ConsumeForFrame()
	if len(got) != 1 || got[0].IsNull() {
		t.Fatalf("expected one queued pair, got %v", got)
	}

	// Upstream appends while transmission is in flight.
	m.SetPassThroughText("AB CD")
	rest := drainPassThrough(t, m)

	if text := decode608Text(rest); text != "AB CD" {
		t.Errorf("delivered text = %q, want %q (no duplication, no loss)", text, "AB CD")
	}
}
