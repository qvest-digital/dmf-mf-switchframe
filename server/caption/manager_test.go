package caption

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/zsiec/ccx"
)

func TestManagerNewDefault(t *testing.T) {
	m := NewManager()
	if m.Mode() != ModeOff {
		t.Errorf("default mode = %v, want ModeOff", m.Mode())
	}

	state := m.State()
	if state.Mode != "off" {
		t.Errorf("state.Mode = %q, want %q", state.Mode, "off")
	}
}

func TestManagerSetMode(t *testing.T) {
	m := NewManager()

	m.SetMode(ModeAuthor)
	if m.Mode() != ModeAuthor {
		t.Errorf("mode = %v, want ModeAuthor", m.Mode())
	}

	m.SetMode(ModePassThrough)
	if m.Mode() != ModePassThrough {
		t.Errorf("mode = %v, want ModePassThrough", m.Mode())
	}

	m.SetMode(ModeOff)
	if m.Mode() != ModeOff {
		t.Errorf("mode = %v, want ModeOff", m.Mode())
	}
}

func TestManagerConsumeOff_EmitsNullPair(t *testing.T) {
	// Even in ModeOff, ConsumeForFrame emits a null pair to maintain continuous
	// ATSC A/53 cc_data presence for downstream decoder acquisition.
	m := NewManager()
	pairs := m.ConsumeForFrame()
	if len(pairs) != 1 {
		t.Fatalf("off mode should return 1 null pair, got %d pairs", len(pairs))
	}
	if !pairs[0].IsNull() {
		t.Errorf("off mode pair should be null (0x80,0x80), got %v", pairs[0])
	}
}

func TestManagerAuthorText(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	m.IngestText("Hi")

	// Consume frames until we get null pairs (queue empty).
	var allPairs []CCPair
	for i := 0; i < 20; i++ { // max iterations to prevent infinite loop
		pairs := m.ConsumeForFrame()
		if pairs == nil {
			break
		}
		if len(pairs) == 1 && pairs[0].IsNull() {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	// Should have init (RU2, RU2, PAC) + character pair "Hi" = 4 pairs.
	if len(allPairs) < 4 {
		t.Fatalf("got %d pairs, want at least 4", len(allPairs))
	}

	// Verify the text pair is in there (with odd parity applied).
	found := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity('H') && p.Data[1] == oddParity('i') {
			found = true
			break
		}
	}
	if !found {
		t.Error("did not find 'Hi' pair with parity in output")
	}
}

func TestManagerAuthorNewline(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	m.IngestNewline()

	// Consume all pairs.
	var allPairs []CCPair
	for i := 0; i < 20; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil || (len(pairs) == 1 && pairs[0].IsNull()) {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	// Should contain CR commands with odd parity.
	foundCR := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity(cc608Ctrl) && p.Data[1] == oddParity(cc608CR) {
			foundCR = true
			break
		}
	}
	if !foundCR {
		t.Error("did not find CR command with parity in output")
	}
}

func TestManagerAuthorClear(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	m.Clear()

	var allPairs []CCPair
	for i := 0; i < 10; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil || (len(pairs) == 1 && pairs[0].IsNull()) {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	foundEDM := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity(cc608Ctrl) && p.Data[1] == oddParity(cc608EDM) {
			foundEDM = true
			break
		}
	}
	if !foundEDM {
		t.Error("did not find EDM command with parity in output")
	}
}

func TestManagerPassThrough(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("AB")

	// Drain all pairs — should include init (RU2x2, PAC) + "AB" char pair.
	var allPairs []CCPair
	for i := 0; i < 20; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	// Should have at least init pairs + text pair.
	if len(allPairs) < 4 {
		t.Fatalf("got %d pairs, want at least 4 (init+text)", len(allPairs))
	}

	// Verify the text pair is in there (with parity).
	found := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity('A') && p.Data[1] == oddParity('B') {
			found = true
			break
		}
	}
	if !found {
		t.Error("did not find 'AB' pair with parity in pass-through output")
	}

	// Same text should not re-queue — ConsumeForFrame returns null pair only.
	m.SetPassThroughText("AB")
	got := m.ConsumeForFrame()
	if len(got) != 1 || !got[0].IsNull() {
		t.Errorf("same text should produce only null pair, got %v", got)
	}
}

func TestManagerPassThroughIgnoredInOtherModes(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	// SetPassThroughText should be ignored in author mode.
	m.SetPassThroughText("Hello")

	// Drain any author init pairs.
	for i := 0; i < 10; i++ {
		pairs := m.ConsumeForFrame()
		if len(pairs) == 1 && pairs[0].IsNull() {
			break
		}
	}

	// Should still get null pairs (no pass-through text queued in author mode).
	pairs := m.ConsumeForFrame()
	if len(pairs) != 1 || !pairs[0].IsNull() {
		t.Errorf("expected null pair in author mode, got %v", pairs)
	}
}

func TestManagerPassThroughNewText(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	m.SetPassThroughText("AB")

	// Drain just 1 pair.
	m.ConsumeForFrame()

	// Change text — should reset and re-encode.
	m.SetPassThroughText("CD")

	var allPairs []CCPair
	for i := 0; i < 20; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	// Should find "CD" pair (new text, not stale "AB").
	found := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity('C') && p.Data[1] == oddParity('D') {
			found = true
			break
		}
	}
	if !found {
		t.Error("did not find 'CD' pair after text change")
	}
}

func TestManagerModeChangeResetsState(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("Hello")

	// Switch to passthrough — author state should be cleared.
	m.SetMode(ModePassThrough)

	state := m.State()
	if state.AuthorBuffer != "" {
		t.Errorf("author buffer should be empty after mode change, got %q", state.AuthorBuffer)
	}
}

func TestManagerIngestTextInWrongMode(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough)

	// IngestText should be ignored in passthrough mode.
	m.IngestText("Hello")

	state := m.State()
	if state.AuthorBuffer != "" {
		t.Errorf("author buffer should be empty in passthrough mode, got %q", state.AuthorBuffer)
	}
}

func TestManagerSourceCaptions(t *testing.T) {
	m := NewManager()

	m.NotifySourceCaptions("cam1", true)
	m.NotifySourceCaptions("cam2", false)
	m.NotifySourceCaptions("cam3", true)

	state := m.State()
	if !state.SourceCaptions["cam1"] {
		t.Error("cam1 should have captions")
	}
	if state.SourceCaptions["cam2"] {
		t.Error("cam2 should not have captions")
	}
	if !state.SourceCaptions["cam3"] {
		t.Error("cam3 should have captions")
	}
}

func TestManagerOnStateChange(t *testing.T) {
	m := NewManager()

	callCount := 0
	m.OnStateChange(func() {
		callCount++
	})

	m.SetMode(ModeAuthor)
	if callCount != 1 {
		t.Errorf("callback count = %d, want 1", callCount)
	}

	m.IngestText("A")
	if callCount != 2 {
		t.Errorf("callback count = %d, want 2", callCount)
	}

	// Same mode should not trigger.
	m.SetMode(ModeAuthor)
	if callCount != 2 {
		t.Errorf("callback count = %d, want 2 (no change)", callCount)
	}
}

func TestManagerVANCSink(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	var sinkPairs []CCPair
	m.SetVANCSink(func(pairs []CCPair) {
		sinkPairs = append(sinkPairs, pairs...)
	})

	m.IngestText("AB")

	// Consume all frames with VANC.
	for i := 0; i < 20; i++ {
		pairs := m.ConsumeForFrameWithVANC()
		if pairs == nil {
			break
		}
		// Stop after we've consumed the text pair.
		if len(pairs) == 1 && pairs[0].IsNull() {
			break
		}
	}

	if len(sinkPairs) == 0 {
		t.Error("VANC sink should have received pairs")
	}
}

func TestManagerAuthorBufferTruncation(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	// Ingest more than 200 characters.
	longText := ""
	for i := 0; i < 250; i++ {
		longText += "A"
	}
	m.IngestText(longText)

	state := m.State()
	if len(state.AuthorBuffer) > 200 {
		t.Errorf("author buffer length = %d, want <= 200", len(state.AuthorBuffer))
	}
}

func TestManagerConcurrency(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			m.IngestText("Hello")
		}()
		go func() {
			defer wg.Done()
			m.ConsumeForFrame()
		}()
		go func() {
			defer wg.Done()
			m.State()
		}()
	}
	wg.Wait()
}

func TestManagerBroadcastSink(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	var frames []*ccx.CaptionFrame
	m.SetBroadcastSink(func(f *ccx.CaptionFrame) {
		frames = append(frames, f)
	})

	m.IngestText("HELLO")
	if len(frames) != 1 {
		t.Fatalf("expected 1 broadcast frame after IngestText, got %d", len(frames))
	}
	if frames[0].Channel != 1 {
		t.Errorf("channel = %d, want 1", frames[0].Channel)
	}
	if frames[0].Text != "HELLO" {
		t.Errorf("text = %q, want %q", frames[0].Text, "HELLO")
	}
	if len(frames[0].Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(frames[0].Regions))
	}
	if len(frames[0].Regions[0].Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(frames[0].Regions[0].Rows))
	}
	if frames[0].Regions[0].Rows[0].Spans[0].Text != "HELLO" {
		t.Errorf("span text = %q, want %q", frames[0].Regions[0].Rows[0].Spans[0].Text, "HELLO")
	}

	// IngestNewline should also broadcast.
	m.IngestNewline()
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames after IngestNewline, got %d", len(frames))
	}

	// Clear should broadcast empty frame.
	m.Clear()
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames after Clear, got %d", len(frames))
	}
	if frames[2].Text != "" {
		t.Errorf("clear frame text = %q, want empty", frames[2].Text)
	}
}

func TestManagerBroadcastSinkIgnoredInWrongMode(t *testing.T) {
	m := NewManager()
	m.SetMode(ModePassThrough) // not author

	callCount := 0
	m.SetBroadcastSink(func(f *ccx.CaptionFrame) {
		callCount++
	})

	m.IngestText("HELLO")
	if callCount != 0 {
		t.Errorf("broadcast sink called %d times in passthrough mode, want 0", callCount)
	}
}

func TestManagerBroadcastSinkMultiline(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	var lastFrame *ccx.CaptionFrame
	m.SetBroadcastSink(func(f *ccx.CaptionFrame) {
		lastFrame = f
	})

	m.IngestText("LINE 1")
	m.IngestNewline()
	m.IngestText("LINE 2")

	if lastFrame == nil {
		t.Fatal("no broadcast frame received")
	}
	if len(lastFrame.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(lastFrame.Regions))
	}
	if len(lastFrame.Regions[0].Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(lastFrame.Regions[0].Rows))
	}
	// Rows should be at bottom of screen (CEA-608 rows 14-15).
	if lastFrame.Regions[0].Rows[0].Row != 14 {
		t.Errorf("row[0].Row = %d, want 14", lastFrame.Regions[0].Rows[0].Row)
	}
	if lastFrame.Regions[0].Rows[1].Row != 15 {
		t.Errorf("row[1].Row = %d, want 15", lastFrame.Regions[0].Rows[1].Row)
	}
}

func TestModeAuto_ParseAndString(t *testing.T) {
	// String() round-trip.
	if ModeAuto.String() != "auto" {
		t.Errorf("ModeAuto.String() = %q, want %q", ModeAuto.String(), "auto")
	}

	// ParseMode round-trip.
	mode, ok := ParseMode("auto")
	if !ok {
		t.Fatal("ParseMode(\"auto\") returned ok=false")
	}
	if mode != ModeAuto {
		t.Errorf("ParseMode(\"auto\") = %v, want ModeAuto", mode)
	}

	// Verify ModeAuto is distinct from ModeAuthor.
	if ModeAuto == ModeAuthor {
		t.Error("ModeAuto should be distinct from ModeAuthor")
	}
}

func TestModeAuto_IngestText(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuto)

	m.IngestText("Hi")

	// Consume frames until we get null pairs (queue empty).
	var allPairs []CCPair
	for i := 0; i < 20; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil {
			break
		}
		if len(pairs) == 1 && pairs[0].IsNull() {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	// Should have init (RU2, RU2, PAC) + character pair "Hi" = 4 pairs.
	if len(allPairs) < 4 {
		t.Fatalf("got %d pairs, want at least 4", len(allPairs))
	}

	// Verify the text pair is in there (with odd parity applied).
	found := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity('H') && p.Data[1] == oddParity('i') {
			found = true
			break
		}
	}
	if !found {
		t.Error("did not find 'Hi' pair with parity in auto mode output")
	}

	// Verify state includes author buffer.
	state := m.State()
	if state.Mode != "auto" {
		t.Errorf("state.Mode = %q, want %q", state.Mode, "auto")
	}
	if state.AuthorBuffer == "" {
		t.Error("state.AuthorBuffer should contain text in auto mode")
	}
}

func TestModeAuto_IngestNewline(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuto)

	m.IngestNewline()

	var allPairs []CCPair
	for i := 0; i < 20; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil || (len(pairs) == 1 && pairs[0].IsNull()) {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	foundCR := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity(cc608Ctrl) && p.Data[1] == oddParity(cc608CR) {
			foundCR = true
			break
		}
	}
	if !foundCR {
		t.Error("did not find CR command with parity in auto mode output")
	}
}

func TestModeAuto_Clear(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuto)

	m.Clear()

	var allPairs []CCPair
	for i := 0; i < 10; i++ {
		pairs := m.ConsumeForFrame()
		if pairs == nil || (len(pairs) == 1 && pairs[0].IsNull()) {
			break
		}
		allPairs = append(allPairs, pairs...)
	}

	foundEDM := false
	for _, p := range allPairs {
		if p.Data[0] == oddParity(cc608Ctrl) && p.Data[1] == oddParity(cc608EDM) {
			foundEDM = true
			break
		}
	}
	if !foundEDM {
		t.Error("did not find EDM command with parity in auto mode output")
	}
}

func TestModeAuto_NullPairWhenEmpty(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuto)

	// Drain any init pairs, then verify null pair emitted.
	for i := 0; i < 10; i++ {
		pairs := m.ConsumeForFrame()
		if len(pairs) == 1 && pairs[0].IsNull() {
			return // Pass: null pair emitted when queue is empty.
		}
	}

	pairs := m.ConsumeForFrame()
	if len(pairs) != 1 || !pairs[0].IsNull() {
		t.Errorf("expected null pair when auto queue empty, got %v", pairs)
	}
}

func TestModeAuto_BroadcastSink(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuto)

	var frames []*ccx.CaptionFrame
	m.SetBroadcastSink(func(f *ccx.CaptionFrame) {
		frames = append(frames, f)
	})

	m.IngestText("TEST")
	if len(frames) != 1 {
		t.Fatalf("expected 1 broadcast frame after IngestText in auto mode, got %d", len(frames))
	}
	if frames[0].Text != "TEST" {
		t.Errorf("text = %q, want %q", frames[0].Text, "TEST")
	}
}

func TestManagerAuthorNullPairWhenEmpty(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	// Drain any init pairs.
	for i := 0; i < 10; i++ {
		pairs := m.ConsumeForFrame()
		if len(pairs) == 1 && pairs[0].IsNull() {
			// Good — null pair emitted when queue is empty.
			return
		}
	}

	// After draining, should get null pairs.
	pairs := m.ConsumeForFrame()
	if len(pairs) != 1 || !pairs[0].IsNull() {
		t.Errorf("expected null pair when author queue empty, got %v", pairs)
	}
}

func TestManager_AuthorBufferRuneSafeTruncation(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	// 150 ASCII runes + 100 runes containing a 4-byte emoji at position 50.
	// Resulting input stream is 250 runes; buffer must trim to the last 200
	// runes and remain valid UTF-8 — regardless of where the emoji's 4
	// bytes fall relative to any byte-offset trim boundary.
	const prefixRunes = 150
	const suffixRunes = 100
	const emojiAt = 50
	const emoji = "🎬" // 4-byte UTF-8 (U+1F3AC)

	prefix := strings.Repeat("a", prefixRunes)
	suffix := strings.Repeat("b", emojiAt) + emoji + strings.Repeat("c", suffixRunes-emojiAt-1)

	m.IngestText(prefix)
	m.IngestText(suffix)

	buf := m.State().AuthorBuffer
	if !utf8.ValidString(buf) {
		t.Fatalf("authorBuffer not valid UTF-8: %q", buf)
	}
	if got := utf8.RuneCountInString(buf); got != 200 {
		t.Fatalf("authorBuffer rune count = %d, want 200", got)
	}
	if !strings.ContainsRune(buf, '🎬') {
		t.Fatalf("authorBuffer should still contain the emoji (was at rune position %d from end): %q",
			(prefixRunes+suffixRunes)-(prefixRunes+emojiAt), buf)
	}
}

func TestManager_AuthorBufferHandlesMultiByteStartOfChunk(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	// 199 ASCII bytes (= 199 runes) then a single 3-byte character. Under a
	// byte-wise trim to 200 bytes, bytes 199, 200, 201 would keep 201-byte
	// input at 200 bytes: the last 200 bytes start mid-codepoint (byte 2 of
	// the 3-byte char). Rune-aware trim keeps the buffer at exactly 200 runes
	// with valid UTF-8.
	prefix := strings.Repeat("a", 199)
	const threeByte = "€" // 3-byte UTF-8 (U+20AC)

	m.IngestText(prefix)
	m.IngestText(threeByte)

	buf := m.State().AuthorBuffer
	if !utf8.ValidString(buf) {
		t.Fatalf("authorBuffer not valid UTF-8: %q", buf)
	}
	if got := utf8.RuneCountInString(buf); got != 200 {
		t.Fatalf("authorBuffer rune count = %d, want 200 (buffer len = %d bytes)", got, len(buf))
	}
	if !strings.HasSuffix(buf, threeByte) {
		t.Fatalf("authorBuffer should end with euro sign: %q", buf)
	}
}
