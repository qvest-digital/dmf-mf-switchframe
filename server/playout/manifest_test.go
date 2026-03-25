package playout

import (
	"os"
	"path/filepath"
	"testing"
)

func validPlaylist() *Playlist {
	return &Playlist{
		ID:   "pl-1",
		Name: "Test Show",
		Items: []PlaylistItem{
			{
				ID:       "item-1",
				Type:     ItemClip,
				AssetURI: "file:///clips/intro.ts",
				Title:    "Intro",
			},
			{
				ID:     "item-2",
				Type:   ItemLive,
				Source: "camera-1",
				Title:  "Live Camera",
			},
			{
				ID:       "item-3",
				Type:     ItemSlate,
				AssetURI: "file:///slates/brb.ts",
				Title:    "BRB Slate",
			},
		},
	}
}

func TestValidatePlaylist_Valid(t *testing.T) {
	pl := validPlaylist()
	if err := ValidatePlaylist(pl); err != nil {
		t.Fatalf("expected valid playlist, got error: %v", err)
	}
}

func TestValidatePlaylist_Empty(t *testing.T) {
	pl := &Playlist{ID: "pl-1", Name: "Empty"}
	err := ValidatePlaylist(pl)
	if err != ErrEmptyPlaylist {
		t.Fatalf("expected ErrEmptyPlaylist, got: %v", err)
	}
}

func TestValidatePlaylist_DuplicateIDs(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Dupes",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemClip, AssetURI: "file:///a.ts"},
			{ID: "item-1", Type: ItemClip, AssetURI: "file:///b.ts"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
}

func TestValidatePlaylist_ClipWithoutURI(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "No URI",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemClip, Title: "Missing URI"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for clip without asset_uri")
	}
}

func TestValidatePlaylist_LiveWithoutSource(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "No Source",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemLive, Title: "Missing Source"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for live without source")
	}
}

func TestValidatePlaylist_SlateWithoutAssetURI(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Slate No URI",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemSlate, Title: "Black Slate", DurationMs: 3000},
		},
	}
	err := ValidatePlaylist(pl)
	if err != nil {
		t.Fatalf("slate without asset_uri should be valid, got: %v", err)
	}
}

func TestValidatePlaylist_MissingID(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "No Item ID",
		Items: []PlaylistItem{
			{Type: ItemClip, AssetURI: "file:///a.ts"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for item without ID")
	}
}

func TestValidatePlaylist_UnknownType(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Bad Type",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemType("unknown"), AssetURI: "file:///a.ts"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for unknown item type")
	}
}

func TestLoadPlaylist_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playlist.json")

	original := validPlaylist()
	if err := SavePlaylist(path, original); err != nil {
		t.Fatalf("SavePlaylist failed: %v", err)
	}

	loaded, err := LoadPlaylist(path)
	if err != nil {
		t.Fatalf("LoadPlaylist failed: %v", err)
	}

	if loaded.ID != original.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, original.ID)
	}
	if loaded.Name != original.Name {
		t.Errorf("Name mismatch: got %q, want %q", loaded.Name, original.Name)
	}
	if len(loaded.Items) != len(original.Items) {
		t.Fatalf("Items count mismatch: got %d, want %d", len(loaded.Items), len(original.Items))
	}
	for i, item := range loaded.Items {
		orig := original.Items[i]
		if item.ID != orig.ID {
			t.Errorf("item[%d] ID mismatch: got %q, want %q", i, item.ID, orig.ID)
		}
		if item.Type != orig.Type {
			t.Errorf("item[%d] Type mismatch: got %q, want %q", i, item.Type, orig.Type)
		}
		if item.AssetURI != orig.AssetURI {
			t.Errorf("item[%d] AssetURI mismatch: got %q, want %q", i, item.AssetURI, orig.AssetURI)
		}
		if item.Source != orig.Source {
			t.Errorf("item[%d] Source mismatch: got %q, want %q", i, item.Source, orig.Source)
		}
		if item.Title != orig.Title {
			t.Errorf("item[%d] Title mismatch: got %q, want %q", i, item.Title, orig.Title)
		}
	}
}

func TestLoadPlaylist_FileNotFound(t *testing.T) {
	_, err := LoadPlaylist("/nonexistent/path/playlist.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadPlaylist_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json}"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadPlaylist(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidateSecondaryEvent_ValidTypes(t *testing.T) {
	validTypes := []struct {
		name  string
		event SecondaryEvent
	}{
		{
			name: "graphics_on",
			event: SecondaryEvent{
				Type: "graphics_on", Layer: 1, Template: "lower-third", Anchor: "start",
			},
		},
		{
			name: "graphics_off",
			event: SecondaryEvent{
				Type: "graphics_off", Layer: 1, Anchor: "start",
			},
		},
		{
			name: "graphics_update",
			event: SecondaryEvent{
				Type: "graphics_update", Layer: 1, Template: "updated", Anchor: "end",
			},
		},
		{
			name: "audio_level",
			event: SecondaryEvent{
				Type: "audio_level", SourceKey: "camera-1", LevelDB: float64Ptr(-6.0), Anchor: "start",
			},
		},
		{
			name: "audio_mute",
			event: SecondaryEvent{
				Type: "audio_mute", SourceKey: "mic-1", Muted: boolPtr(true), Anchor: "start",
			},
		},
		{
			name: "audio_afv",
			event: SecondaryEvent{
				Type: "audio_afv", SourceKey: "mic-2", AFV: boolPtr(false), Anchor: "end",
			},
		},
		{
			name: "caption_mode",
			event: SecondaryEvent{
				Type: "caption_mode", CaptionMode: "passthrough", Anchor: "start",
			},
		},
		{
			name: "caption_text",
			event: SecondaryEvent{
				Type: "caption_text", CaptionText: "Hello", Anchor: "start",
			},
		},
		{
			name: "scte35_cue",
			event: SecondaryEvent{
				Type: "scte35_cue", Anchor: "end", SCTE35: &SCTE35EventData{BreakDurationMs: 30000},
			},
		},
	}

	for _, tt := range validTypes {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateSecondaryEvent(&tt.event); err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
		})
	}
}

func TestValidateSecondaryEvent_InvalidType(t *testing.T) {
	ev := SecondaryEvent{Type: "bogus", Anchor: "start"}
	if err := ValidateSecondaryEvent(&ev); err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestValidateSecondaryEvent_MissingAnchor(t *testing.T) {
	ev := SecondaryEvent{Type: "graphics_on", Layer: 1}
	if err := ValidateSecondaryEvent(&ev); err == nil {
		t.Error("expected error for missing anchor")
	}
}

func TestValidateSecondaryEvent_BadAnchor(t *testing.T) {
	ev := SecondaryEvent{Type: "graphics_on", Layer: 1, Anchor: "middle"}
	if err := ValidateSecondaryEvent(&ev); err == nil {
		t.Error("expected error for bad anchor")
	}
}

func TestValidateSecondaryEvent_AudioMissingSourceKey(t *testing.T) {
	audioTypes := []string{"audio_level", "audio_mute", "audio_afv"}
	for _, typ := range audioTypes {
		t.Run(typ, func(t *testing.T) {
			ev := SecondaryEvent{Type: typ, Anchor: "start"}
			if err := ValidateSecondaryEvent(&ev); err == nil {
				t.Errorf("expected error for %s without source_key", typ)
			}
		})
	}
}

func TestValidateSecondaryEvent_CaptionModeMissingField(t *testing.T) {
	ev := SecondaryEvent{Type: "caption_mode", Anchor: "start"}
	if err := ValidateSecondaryEvent(&ev); err == nil {
		t.Error("expected error for caption_mode without caption_mode field")
	}
}

func TestValidateSecondaryEvent_CaptionTextMissingField(t *testing.T) {
	ev := SecondaryEvent{Type: "caption_text", Anchor: "start"}
	if err := ValidateSecondaryEvent(&ev); err == nil {
		t.Error("expected error for caption_text without caption_text field")
	}
}

func TestValidateSecondaryEvent_SCTE35MissingData(t *testing.T) {
	ev := SecondaryEvent{Type: "scte35_cue", Anchor: "end"}
	if err := ValidateSecondaryEvent(&ev); err == nil {
		t.Error("expected error for scte35_cue without scte35 data")
	}
}

func TestValidatePlaylist_InvalidSecondaryEvent(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Bad Secondary",
		Items: []PlaylistItem{
			{
				ID:       "item-1",
				Type:     ItemClip,
				AssetURI: "file:///clips/intro.ts",
				Title:    "Intro",
				SecondaryEvents: []SecondaryEvent{
					{Type: "bogus_type", Anchor: "start"},
				},
			},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for invalid secondary event in playlist")
	}
}

func TestValidatePlaylist_ValidSecondaryEvents(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Good Secondary",
		Items: []PlaylistItem{
			{
				ID:       "item-1",
				Type:     ItemClip,
				AssetURI: "file:///clips/intro.ts",
				Title:    "Intro",
				SecondaryEvents: []SecondaryEvent{
					{Type: "graphics_on", Layer: 1, Template: "lt", Anchor: "start", OffsetMs: 1000},
					{Type: "graphics_off", Layer: 1, Anchor: "end", OffsetMs: -500},
				},
			},
		},
	}
	if err := ValidatePlaylist(pl); err != nil {
		t.Fatalf("expected valid playlist, got: %v", err)
	}
}

func TestValidatePlaylist_IdleAsset_Valid(t *testing.T) {
	pl := validPlaylist()
	pl.IdleAssetURI = "file:///slates/idle.ts"
	pl.IdleLoop = true
	if err := ValidatePlaylist(pl); err != nil {
		t.Fatalf("expected valid playlist with idle_asset, got: %v", err)
	}
}

func TestValidatePlaylist_PodTemplates_Valid(t *testing.T) {
	pl := validPlaylist()
	pl.PodTemplates = []PodTemplateDef{
		{Label: "60s Local", DurationMs: 60000, AutoReturn: true, Priority: PriorityNormal},
		{Label: "90s National", DurationMs: 90000, AutoReturn: false, Priority: PriorityHigh},
	}
	if err := ValidatePlaylist(pl); err != nil {
		t.Fatalf("expected valid playlist with pod templates, got: %v", err)
	}
}

func TestValidatePlaylist_PodTemplates_DuplicateLabel(t *testing.T) {
	pl := validPlaylist()
	pl.PodTemplates = []PodTemplateDef{
		{Label: "60s Local", DurationMs: 60000, AutoReturn: true},
		{Label: "60s Local", DurationMs: 30000, AutoReturn: false},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for duplicate pod template label")
	}
}

func TestValidatePlaylist_PodTemplates_EmptyLabel(t *testing.T) {
	pl := validPlaylist()
	pl.PodTemplates = []PodTemplateDef{
		{Label: "", DurationMs: 60000, AutoReturn: true},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for empty pod template label")
	}
}

func TestValidatePlaylist_PodTemplates_NegativeDuration(t *testing.T) {
	pl := validPlaylist()
	pl.PodTemplates = []PodTemplateDef{
		{Label: "Bad Duration", DurationMs: -1000},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for negative pod template duration")
	}
}

func TestValidatePlaylist_LoopOnLive_Error(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Loop Live",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemLive, Source: "camera-1", Title: "Live", Loop: true},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for loop on live item")
	}
}

func TestValidatePlaylist_LoopOnSlate_Error(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Loop Slate",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemSlate, Title: "Slate", Loop: true},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for loop on slate item")
	}
}

func TestValidatePlaylist_LoopOnClip_Valid(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Loop Clip",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemClip, AssetURI: "file:///clips/loop.ts", Title: "Loop Clip", Loop: true},
		},
	}
	if err := ValidatePlaylist(pl); err != nil {
		t.Fatalf("expected valid playlist with loop on clip, got: %v", err)
	}
}

func TestValidatePodTemplates_Empty(t *testing.T) {
	// Empty slice is valid (no templates defined).
	if err := ValidatePodTemplates(nil); err != nil {
		t.Fatalf("expected nil templates to be valid, got: %v", err)
	}
}

func TestValidatePodTemplates_ZeroDuration(t *testing.T) {
	// Zero duration means "manual return only".
	defs := []PodTemplateDef{
		{Label: "Manual", DurationMs: 0, AutoReturn: false},
	}
	if err := ValidatePodTemplates(defs); err != nil {
		t.Fatalf("expected zero duration to be valid, got: %v", err)
	}
}
