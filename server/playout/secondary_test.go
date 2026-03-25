package playout

import (
	"fmt"
	"testing"
)

// mockCall records a single method invocation on the mock handler.
type mockCall struct {
	Method string
	Args   string
}

// mockHandler records calls for verification.
type mockHandler struct {
	calls []mockCall
}

var _ SecondaryEventHandler = (*mockHandler)(nil)

func (m *mockHandler) OnGraphicsOn(layer int, template string) error {
	m.calls = append(m.calls, mockCall{"OnGraphicsOn", fmt.Sprintf("layer=%d template=%s", layer, template)})
	return nil
}

func (m *mockHandler) OnGraphicsOff(layer int) error {
	m.calls = append(m.calls, mockCall{"OnGraphicsOff", fmt.Sprintf("layer=%d", layer)})
	return nil
}

func (m *mockHandler) OnGraphicsUpdate(layer int, template string) error {
	m.calls = append(m.calls, mockCall{"OnGraphicsUpdate", fmt.Sprintf("layer=%d template=%s", layer, template)})
	return nil
}

func (m *mockHandler) OnAudioLevel(sourceKey string, levelDB float64) error {
	m.calls = append(m.calls, mockCall{"OnAudioLevel", fmt.Sprintf("source=%s level=%.1f", sourceKey, levelDB)})
	return nil
}

func (m *mockHandler) OnAudioMute(sourceKey string, muted bool) error {
	m.calls = append(m.calls, mockCall{"OnAudioMute", fmt.Sprintf("source=%s muted=%v", sourceKey, muted)})
	return nil
}

func (m *mockHandler) OnAudioAFV(sourceKey string, afv bool) error {
	m.calls = append(m.calls, mockCall{"OnAudioAFV", fmt.Sprintf("source=%s afv=%v", sourceKey, afv)})
	return nil
}

func (m *mockHandler) OnCaptionMode(mode string) error {
	m.calls = append(m.calls, mockCall{"OnCaptionMode", fmt.Sprintf("mode=%s", mode)})
	return nil
}

func (m *mockHandler) OnCaptionText(text string) error {
	m.calls = append(m.calls, mockCall{"OnCaptionText", fmt.Sprintf("text=%s", text)})
	return nil
}

func (m *mockHandler) OnSCTE35Cue(data *SCTE35EventData) error {
	if data != nil {
		m.calls = append(m.calls, mockCall{"OnSCTE35Cue", fmt.Sprintf("break=%dms auto_return=%v", data.BreakDurationMs, data.AutoReturn)})
	} else {
		m.calls = append(m.calls, mockCall{"OnSCTE35Cue", "nil"})
	}
	return nil
}

func TestMockHandler_SatisfiesInterface(t *testing.T) {
	// Compile-time check via the var _ above is sufficient,
	// but exercise the mock to be thorough.
	h := &mockHandler{}
	_ = h.OnGraphicsOn(1, "test")
	_ = h.OnGraphicsOff(1)
	if len(h.calls) != 2 {
		t.Errorf("expected 2 calls, got %d", len(h.calls))
	}
}

func TestScheduler_StartAnchoredEvents(t *testing.T) {
	h := &mockHandler{}
	s := NewSecondaryEventScheduler(h)

	events := []SecondaryEvent{
		{Type: "graphics_on", Layer: 1, Template: "lower-third", OffsetMs: 0, Anchor: "start"},
		{Type: "audio_level", SourceKey: "cam-1", LevelDB: float64Ptr(-3.0), OffsetMs: 500, Anchor: "start"},
		{Type: "graphics_off", Layer: 1, OffsetMs: 2000, Anchor: "start"},
	}

	s.BeginItem(events, 5000)

	// At t=0, only the first event should fire.
	s.Tick(0)
	if len(h.calls) != 1 {
		t.Fatalf("at t=0: expected 1 call, got %d", len(h.calls))
	}
	if h.calls[0].Method != "OnGraphicsOn" {
		t.Errorf("at t=0: expected OnGraphicsOn, got %s", h.calls[0].Method)
	}

	// At t=500, the audio level event fires.
	s.Tick(500)
	if len(h.calls) != 2 {
		t.Fatalf("at t=500: expected 2 calls, got %d", len(h.calls))
	}
	if h.calls[1].Method != "OnAudioLevel" {
		t.Errorf("at t=500: expected OnAudioLevel, got %s", h.calls[1].Method)
	}

	// At t=2000, the graphics off event fires.
	s.Tick(2000)
	if len(h.calls) != 3 {
		t.Fatalf("at t=2000: expected 3 calls, got %d", len(h.calls))
	}
	if h.calls[2].Method != "OnGraphicsOff" {
		t.Errorf("at t=2000: expected OnGraphicsOff, got %s", h.calls[2].Method)
	}
}

func TestScheduler_EndAnchoredEvents(t *testing.T) {
	h := &mockHandler{}
	s := NewSecondaryEventScheduler(h)

	events := []SecondaryEvent{
		{Type: "graphics_on", Layer: 2, Template: "bug", OffsetMs: -5000, Anchor: "end"},
		{Type: "graphics_off", Layer: 2, OffsetMs: -1000, Anchor: "end"},
	}

	// 10s item: end-anchored at -5000 fires at 5000, -1000 fires at 9000.
	s.BeginItem(events, 10000)

	s.Tick(4999)
	if len(h.calls) != 0 {
		t.Fatalf("at t=4999: expected 0 calls, got %d", len(h.calls))
	}

	s.Tick(5000)
	if len(h.calls) != 1 {
		t.Fatalf("at t=5000: expected 1 call, got %d", len(h.calls))
	}
	if h.calls[0].Method != "OnGraphicsOn" {
		t.Errorf("at t=5000: expected OnGraphicsOn, got %s", h.calls[0].Method)
	}

	s.Tick(9000)
	if len(h.calls) != 2 {
		t.Fatalf("at t=9000: expected 2 calls, got %d", len(h.calls))
	}
	if h.calls[1].Method != "OnGraphicsOff" {
		t.Errorf("at t=9000: expected OnGraphicsOff, got %s", h.calls[1].Method)
	}
}

func TestScheduler_EndItem_Resets(t *testing.T) {
	h := &mockHandler{}
	s := NewSecondaryEventScheduler(h)

	events := []SecondaryEvent{
		{Type: "graphics_on", Layer: 1, Template: "lower-third", OffsetMs: 0, Anchor: "start"},
	}

	s.BeginItem(events, 5000)
	s.Tick(0)
	if len(h.calls) != 1 {
		t.Fatalf("first item: expected 1 call, got %d", len(h.calls))
	}

	// End the item and begin a new one with no events.
	s.EndItem()
	s.BeginItem(nil, 3000)
	s.Tick(0)
	s.Tick(1000)
	s.Tick(3000)
	// No new calls should happen.
	if len(h.calls) != 1 {
		t.Fatalf("second item: expected 1 call total, got %d", len(h.calls))
	}
}

func TestScheduler_NoEventsIsNoOp(t *testing.T) {
	h := &mockHandler{}
	s := NewSecondaryEventScheduler(h)

	s.BeginItem(nil, 10000)
	s.Tick(0)
	s.Tick(5000)
	s.Tick(10000)

	if len(h.calls) != 0 {
		t.Errorf("expected 0 calls for empty events, got %d", len(h.calls))
	}
}

func TestScheduler_DuplicateTickDoesNotRefire(t *testing.T) {
	h := &mockHandler{}
	s := NewSecondaryEventScheduler(h)

	events := []SecondaryEvent{
		{Type: "graphics_on", Layer: 1, Template: "bug", OffsetMs: 0, Anchor: "start"},
	}

	s.BeginItem(events, 5000)

	// Tick at 0 three times — event should fire only once.
	s.Tick(0)
	s.Tick(0)
	s.Tick(0)

	if len(h.calls) != 1 {
		t.Errorf("expected 1 call, got %d (event refired)", len(h.calls))
	}
}

func TestScheduler_AllEventTypes(t *testing.T) {
	tests := []struct {
		name       string
		event      SecondaryEvent
		wantMethod string
	}{
		{"graphics_on", SecondaryEvent{Type: "graphics_on", Layer: 1, Template: "bug", OffsetMs: 0, Anchor: "start"}, "OnGraphicsOn"},
		{"graphics_off", SecondaryEvent{Type: "graphics_off", Layer: 1, OffsetMs: 0, Anchor: "start"}, "OnGraphicsOff"},
		{"graphics_update", SecondaryEvent{Type: "graphics_update", Layer: 2, Template: "new-bug", OffsetMs: 0, Anchor: "start"}, "OnGraphicsUpdate"},
		{"audio_level", SecondaryEvent{Type: "audio_level", SourceKey: "cam-1", LevelDB: float64Ptr(-12.0), OffsetMs: 0, Anchor: "start"}, "OnAudioLevel"},
		{"audio_mute", SecondaryEvent{Type: "audio_mute", SourceKey: "cam-1", Muted: boolPtr(true), OffsetMs: 0, Anchor: "start"}, "OnAudioMute"},
		{"audio_afv", SecondaryEvent{Type: "audio_afv", SourceKey: "cam-1", AFV: boolPtr(false), OffsetMs: 0, Anchor: "start"}, "OnAudioAFV"},
		{"caption_mode", SecondaryEvent{Type: "caption_mode", CaptionMode: "author", OffsetMs: 0, Anchor: "start"}, "OnCaptionMode"},
		{"caption_text", SecondaryEvent{Type: "caption_text", CaptionText: "Hello world", OffsetMs: 0, Anchor: "start"}, "OnCaptionText"},
		{"scte35_cue", SecondaryEvent{Type: "scte35_cue", SCTE35: &SCTE35EventData{BreakDurationMs: 30000, AutoReturn: true}, OffsetMs: 0, Anchor: "start"}, "OnSCTE35Cue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &mockHandler{}
			s := NewSecondaryEventScheduler(h)
			s.BeginItem([]SecondaryEvent{tt.event}, 10000)
			s.Tick(0)

			if len(h.calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(h.calls))
			}
			if h.calls[0].Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", h.calls[0].Method, tt.wantMethod)
			}
		})
	}
}

func TestScheduler_Reanchor(t *testing.T) {
	h := &mockHandler{}
	s := NewSecondaryEventScheduler(h)

	events := []SecondaryEvent{
		{Type: "graphics_on", Layer: 1, Template: "lower-third", OffsetMs: 500, Anchor: "start"},
		{Type: "graphics_off", Layer: 1, OffsetMs: -2000, Anchor: "end"},
	}

	// 10s item: start-anchored fires at 500ms, end-anchored at 10000-2000=8000ms.
	s.BeginItem(events, 10000)

	// Tick to 500 — start-anchored fires.
	s.Tick(500)
	if len(h.calls) != 1 {
		t.Fatalf("at t=500: expected 1 call, got %d", len(h.calls))
	}
	if h.calls[0].Method != "OnGraphicsOn" {
		t.Errorf("at t=500: expected OnGraphicsOn, got %s", h.calls[0].Method)
	}

	// Reanchor to 6000ms — end-anchored now fires at 6000-2000=4000ms.
	s.Reanchor(6000)

	// Tick to 3999 — end-anchored should not have fired yet.
	s.Tick(3999)
	if len(h.calls) != 1 {
		t.Fatalf("at t=3999 after reanchor: expected 1 call, got %d", len(h.calls))
	}

	// Tick to 4000 — end-anchored fires.
	s.Tick(4000)
	if len(h.calls) != 2 {
		t.Fatalf("at t=4000 after reanchor: expected 2 calls, got %d", len(h.calls))
	}
	if h.calls[1].Method != "OnGraphicsOff" {
		t.Errorf("at t=4000: expected OnGraphicsOff, got %s", h.calls[1].Method)
	}

	// Verify the start-anchored event was NOT re-fired by the reanchor.
	graphicsOnCount := 0
	for _, c := range h.calls {
		if c.Method == "OnGraphicsOn" {
			graphicsOnCount++
		}
	}
	if graphicsOnCount != 1 {
		t.Errorf("OnGraphicsOn fired %d times, want exactly 1", graphicsOnCount)
	}
}
