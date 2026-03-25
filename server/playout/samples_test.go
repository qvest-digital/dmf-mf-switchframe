package playout

import (
	"testing"
	"time"
)

func TestAvailableTemplates(t *testing.T) {
	names := AvailableTemplates()
	if len(names) != 3 {
		t.Fatalf("expected 3 templates, got %d: %v", len(names), names)
	}

	expected := map[string]bool{
		"Live Event with Breaks": true,
		"VOD Playlist":           true,
		"Scheduled Broadcast":    true,
	}
	for _, name := range names {
		if !expected[name] {
			t.Errorf("unexpected template name: %q", name)
		}
	}
}

// mockSamples returns a set of sample clips with fake paths for testing.
func mockSamples() []SampleClip {
	return []SampleClip{
		{Name: "holding-slate", Title: "Holding Slate", Path: "/tmp/samples/holding-slate.ts", Duration: 10},
		{Name: "bumper-in", Title: "Bumper In", Path: "/tmp/samples/bumper-in.ts", Duration: 3},
		{Name: "bumper-out", Title: "Bumper Out", Path: "/tmp/samples/bumper-out.ts", Duration: 3},
		{Name: "sample-ad-a", Title: "Sample Ad A", Path: "/tmp/samples/sample-ad-a.ts", Duration: 15},
		{Name: "sample-ad-b", Title: "Sample Ad B", Path: "/tmp/samples/sample-ad-b.ts", Duration: 30},
		{Name: "countdown", Title: "Countdown", Path: "/tmp/samples/countdown.ts", Duration: 10},
	}
}

func TestTemplatePlaylist_LiveEvent(t *testing.T) {
	samples := mockSamples()
	pl := TemplatePlaylist("Live Event with Breaks", samples)
	if pl == nil {
		t.Fatal("expected non-nil playlist for 'Live Event with Breaks'")
	}

	if pl.Name != "Live Event with Breaks" {
		t.Errorf("expected name 'Live Event with Breaks', got %q", pl.Name)
	}

	// Expect 10 items: slate, bumper-in, live, bumper-out, ad-a, ad-b, bumper-in, live, bumper-out, countdown
	if len(pl.Items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(pl.Items))
	}

	// First item should be holding slate with loop.
	if pl.Items[0].Type != ItemClip {
		t.Errorf("item[0] type = %q, want clip", pl.Items[0].Type)
	}
	if !pl.Items[0].Loop {
		t.Error("item[0] should be looped")
	}

	// Item 2 (index 2) should be live.
	if pl.Items[2].Type != ItemLive {
		t.Errorf("item[2] type = %q, want live", pl.Items[2].Type)
	}
	if pl.Items[2].Source == "" {
		t.Error("item[2] live source should not be empty")
	}

	// Should have idle asset set.
	if pl.IdleAssetURI == "" {
		t.Error("expected non-empty idle asset URI")
	}

	// Should have pod templates.
	if len(pl.PodTemplates) != 2 {
		t.Errorf("expected 2 pod templates, got %d", len(pl.PodTemplates))
	}

	// Bumper-out items should have SCTE-35 secondary events.
	var scte35Count int
	for _, item := range pl.Items {
		for _, ev := range item.SecondaryEvents {
			if ev.Type == "scte35_cue" {
				scte35Count++
			}
		}
	}
	if scte35Count < 2 {
		t.Errorf("expected at least 2 SCTE-35 events, got %d", scte35Count)
	}
}

func TestTemplatePlaylist_VOD(t *testing.T) {
	samples := mockSamples()
	pl := TemplatePlaylist("VOD Playlist", samples)
	if pl == nil {
		t.Fatal("expected non-nil playlist for 'VOD Playlist'")
	}

	if pl.Name != "VOD Playlist" {
		t.Errorf("expected name 'VOD Playlist', got %q", pl.Name)
	}

	// 4 items: ad-a, ad-b, countdown, ad-a (loop)
	if len(pl.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(pl.Items))
	}

	// Default transition should be dissolve 500ms.
	if pl.DefaultTransition.Type != "dissolve" {
		t.Errorf("default transition type = %q, want dissolve", pl.DefaultTransition.Type)
	}
	if pl.DefaultTransition.DurationMs != 500 {
		t.Errorf("default transition duration = %d, want 500", pl.DefaultTransition.DurationMs)
	}

	// Last item should loop.
	last := pl.Items[len(pl.Items)-1]
	if !last.Loop {
		t.Error("last VOD item should be looped")
	}

	// Items should have dissolve transitions.
	for i, item := range pl.Items {
		if item.Transition == nil {
			t.Errorf("item[%d] missing transition", i)
		} else if item.Transition.Type != "dissolve" {
			t.Errorf("item[%d] transition type = %q, want dissolve", i, item.Transition.Type)
		}
	}
}

func TestTemplatePlaylist_Scheduled(t *testing.T) {
	samples := mockSamples()
	pl := TemplatePlaylist("Scheduled Broadcast", samples)
	if pl == nil {
		t.Fatal("expected non-nil playlist for 'Scheduled Broadcast'")
	}

	if pl.Name != "Scheduled Broadcast" {
		t.Errorf("expected name 'Scheduled Broadcast', got %q", pl.Name)
	}

	// 4 items: countdown, ad-a, ad-b, bumper-out
	if len(pl.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(pl.Items))
	}

	// All items should have scheduled times.
	now := time.Now()
	for i, item := range pl.Items {
		if item.ScheduledTime == nil {
			t.Errorf("item[%d] missing scheduled_time", i)
		} else {
			// Should be in the future (or very near now).
			delta := item.ScheduledTime.Sub(now)
			if delta < -5*time.Second {
				t.Errorf("item[%d] scheduled_time is too far in the past: %v", i, delta)
			}
		}
	}

	// Should have idle asset.
	if pl.IdleAssetURI == "" {
		t.Error("expected non-empty idle asset URI")
	}
}

func TestTemplatePlaylist_Unknown(t *testing.T) {
	samples := mockSamples()
	pl := TemplatePlaylist("Nonexistent Template", samples)
	if pl != nil {
		t.Errorf("expected nil for unknown template, got %+v", pl)
	}
}

func TestTemplatePlaylist_ValidatesOK(t *testing.T) {
	samples := mockSamples()
	for _, name := range AvailableTemplates() {
		t.Run(name, func(t *testing.T) {
			pl := TemplatePlaylist(name, samples)
			if pl == nil {
				t.Fatal("TemplatePlaylist returned nil")
			}
			if err := ValidatePlaylist(pl); err != nil {
				t.Errorf("ValidatePlaylist failed: %v", err)
			}
		})
	}
}
