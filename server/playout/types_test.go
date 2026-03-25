package playout

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChannelState_String(t *testing.T) {
	tests := []struct {
		state ChannelState
		want  string
	}{
		{StateStopped, "stopped"},
		{StateCued, "cued"},
		{StateOnAir, "on_air"},
		{StateCompleted, "completed"},
		{ChannelState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("ChannelState(%d).String() = %q, want %q", int(tt.state), got, tt.want)
		}
	}
}

func TestOnAirMode_String(t *testing.T) {
	tests := []struct {
		mode OnAirMode
		want string
	}{
		{ModeAutomated, "automated"},
		{ModeManual, "manual"},
		{ModeBreak, "break"},
		{OnAirMode(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("OnAirMode(%d).String() = %q, want %q", int(tt.mode), got, tt.want)
		}
	}
}

func TestItemType_Constants(t *testing.T) {
	if ItemClip != "clip" {
		t.Errorf("ItemClip = %q, want %q", ItemClip, "clip")
	}
	if ItemLive != "live" {
		t.Errorf("ItemLive = %q, want %q", ItemLive, "live")
	}
	if ItemSlate != "slate" {
		t.Errorf("ItemSlate = %q, want %q", ItemSlate, "slate")
	}
}

func TestTransitionConfig_ZeroValue(t *testing.T) {
	var tc TransitionConfig
	if tc.Type != "" {
		t.Errorf("zero TransitionConfig.Type = %q, want empty", tc.Type)
	}
	if tc.DurationMs != 0 {
		t.Errorf("zero TransitionConfig.DurationMs = %d, want 0", tc.DurationMs)
	}
}

func TestPlaylistItem_JSONRoundTrip(t *testing.T) {
	scheduled := time.Date(2026, 3, 24, 14, 0, 0, 0, time.UTC)
	item := PlaylistItem{
		ID:         "item-001",
		Type:       ItemClip,
		AssetURI:   "clip://abc123",
		Title:      "Opening Bumper",
		InPointMs:  0,
		OutPointMs: 15000,
		DurationMs: 15000,
		Hold:       false,
		Transition: &TransitionConfig{
			Type:       "dissolve",
			DurationMs: 500,
		},
		ScheduledTime: &scheduled,
		SecondaryEvents: []SecondaryEvent{
			{
				Type:     "graphic_on",
				Layer:    1,
				Template: "lower-third",
				OffsetMs: 2000,
				Anchor:   "start",
			},
		},
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got PlaylistItem
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != item.ID {
		t.Errorf("ID = %q, want %q", got.ID, item.ID)
	}
	if got.Type != item.Type {
		t.Errorf("Type = %q, want %q", got.Type, item.Type)
	}
	if got.AssetURI != item.AssetURI {
		t.Errorf("AssetURI = %q, want %q", got.AssetURI, item.AssetURI)
	}
	if got.Title != item.Title {
		t.Errorf("Title = %q, want %q", got.Title, item.Title)
	}
	if got.DurationMs != item.DurationMs {
		t.Errorf("DurationMs = %d, want %d", got.DurationMs, item.DurationMs)
	}
	if got.Transition == nil {
		t.Fatal("Transition is nil")
	}
	if got.Transition.Type != "dissolve" {
		t.Errorf("Transition.Type = %q, want %q", got.Transition.Type, "dissolve")
	}
	if got.Transition.DurationMs != 500 {
		t.Errorf("Transition.DurationMs = %d, want %d", got.Transition.DurationMs, 500)
	}
	if got.ScheduledTime == nil {
		t.Fatal("ScheduledTime is nil")
	}
	if !got.ScheduledTime.Equal(scheduled) {
		t.Errorf("ScheduledTime = %v, want %v", got.ScheduledTime, scheduled)
	}
	if len(got.SecondaryEvents) != 1 {
		t.Fatalf("SecondaryEvents len = %d, want 1", len(got.SecondaryEvents))
	}
	se := got.SecondaryEvents[0]
	if se.Type != "graphic_on" {
		t.Errorf("SecondaryEvent.Type = %q, want %q", se.Type, "graphic_on")
	}
	if se.Layer != 1 {
		t.Errorf("SecondaryEvent.Layer = %d, want %d", se.Layer, 1)
	}
	if se.OffsetMs != 2000 {
		t.Errorf("SecondaryEvent.OffsetMs = %d, want %d", se.OffsetMs, 2000)
	}
	if se.Anchor != "start" {
		t.Errorf("SecondaryEvent.Anchor = %q, want %q", se.Anchor, "start")
	}
}

func TestPlaylist_JSONRoundTrip(t *testing.T) {
	pl := Playlist{
		ID:      "pl-001",
		Name:    "Morning Show",
		Channel: "channel-1",
		Mode:    "automated",
		DefaultTransition: TransitionConfig{
			Type:       "cut",
			DurationMs: 0,
		},
		Items: []PlaylistItem{
			{
				ID:         "item-001",
				Type:       ItemClip,
				AssetURI:   "clip://abc",
				Title:      "Opener",
				DurationMs: 10000,
			},
			{
				ID:     "item-002",
				Type:   ItemLive,
				Source: "camera-1",
				Title:  "Host Camera",
				Hold:   true,
			},
		},
	}

	data, err := json.Marshal(pl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Playlist
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != pl.ID {
		t.Errorf("ID = %q, want %q", got.ID, pl.ID)
	}
	if got.Name != pl.Name {
		t.Errorf("Name = %q, want %q", got.Name, pl.Name)
	}
	if got.Channel != pl.Channel {
		t.Errorf("Channel = %q, want %q", got.Channel, pl.Channel)
	}
	if got.Mode != pl.Mode {
		t.Errorf("Mode = %q, want %q", got.Mode, pl.Mode)
	}
	if got.DefaultTransition.Type != "cut" {
		t.Errorf("DefaultTransition.Type = %q, want %q", got.DefaultTransition.Type, "cut")
	}
	if len(got.Items) != 2 {
		t.Fatalf("Items len = %d, want 2", len(got.Items))
	}
	if got.Items[0].Type != ItemClip {
		t.Errorf("Items[0].Type = %q, want %q", got.Items[0].Type, ItemClip)
	}
	if got.Items[1].Type != ItemLive {
		t.Errorf("Items[1].Type = %q, want %q", got.Items[1].Type, ItemLive)
	}
	if got.Items[1].Source != "camera-1" {
		t.Errorf("Items[1].Source = %q, want %q", got.Items[1].Source, "camera-1")
	}
	if !got.Items[1].Hold {
		t.Error("Items[1].Hold = false, want true")
	}
}

func TestPlaylistItem_JSONOmitsZeroValues(t *testing.T) {
	item := PlaylistItem{
		ID:    "item-minimal",
		Type:  ItemSlate,
		Title: "Slate",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	// These fields should be omitted due to omitempty.
	for _, key := range []string{"asset_uri", "source", "in_point_ms", "out_point_ms", "duration_ms", "hold", "transition", "scheduled_time", "secondary_events"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected key %q to be omitted, but it was present", key)
		}
	}

	// These fields should always be present.
	for _, key := range []string{"id", "type", "title"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected key %q to be present, but it was missing", key)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	errors := []error{
		ErrNoPlaylist,
		ErrAlreadyOnAir,
		ErrNotOnAir,
		ErrInvalidChannel,
		ErrInvalidItem,
		ErrEmptyPlaylist,
		ErrItemNotFound,
		ErrAlreadyCued,
		ErrInvalidState,
	}
	for _, err := range errors {
		if err == nil {
			t.Error("sentinel error is nil")
		}
		if err.Error() == "" {
			t.Error("sentinel error has empty message")
		}
	}
}

func TestMaxChannels(t *testing.T) {
	if MaxChannels != 2 {
		t.Errorf("MaxChannels = %d, want 2", MaxChannels)
	}
}

func TestChannelStatus_JSON(t *testing.T) {
	status := ChannelStatus{
		ID:            1,
		State:         StateStopped.String(),
		Mode:          ModeAutomated.String(),
		PlaylistName:  "Evening News",
		CurrentItemID: "item-001",
		CurrentTitle:  "Opening",
		NextItemID:    "item-002",
		NextTitle:     "Headlines",
		ItemIndex:     0,
		ItemCount:     10,
		ItemProgress:  0.45,
		ItemRemainMs:  5500,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ChannelStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != 1 {
		t.Errorf("ID = %d, want 1", got.ID)
	}
	if got.State != "stopped" {
		t.Errorf("State = %q, want %q", got.State, "stopped")
	}
	if got.Mode != "automated" {
		t.Errorf("Mode = %q, want %q", got.Mode, "automated")
	}
	if got.PlaylistName != "Evening News" {
		t.Errorf("PlaylistName = %q, want %q", got.PlaylistName, "Evening News")
	}
	if got.ItemProgress != 0.45 {
		t.Errorf("ItemProgress = %f, want %f", got.ItemProgress, 0.45)
	}
	if got.ItemRemainMs != 5500 {
		t.Errorf("ItemRemainMs = %d, want %d", got.ItemRemainMs, 5500)
	}
}

func TestAssetStateString(t *testing.T) {
	tests := []struct {
		state AssetState
		want  string
	}{
		{AssetPending, "pending"},
		{AssetFetching, "fetching"},
		{AssetTranscoding, "transcoding"},
		{AssetCached, "cached"},
		{AssetError, "error"},
		{AssetState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("AssetState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestAssetURIParse(t *testing.T) {
	tests := []struct {
		uri    string
		scheme string
		path   string
	}{
		{"local:///mnt/media/clip.ts", "local", "/mnt/media/clip.ts"},
		{"s3://bucket/key.ts", "s3", "bucket/key.ts"},
		{"http://cdn.example.com/clip.ts", "http", "cdn.example.com/clip.ts"},
		{"https://cdn.example.com/clip.ts", "https", "cdn.example.com/clip.ts"},
		{"/absolute/path.ts", "local", "/absolute/path.ts"},
		{"file:///path.ts", "local", "/path.ts"},

		// Edge cases.
		{"", "local", ""},                                                              // empty string
		{"clip.ts", "local", "clip.ts"},                                                // relative path (no scheme)
		{"file://path.ts", "file", "path.ts"},                                          // file:// with 2 slashes (not 3) — falls to generic ://
		{"http://cdn.example.com/clip.ts?token=abc", "http", "cdn.example.com/clip.ts?token=abc"}, // URI with query params
		{"local://path.ts", "local", "path.ts"},                                        // local:// with 2 slashes (not 3) — falls to generic ://
	}
	for _, tt := range tests {
		scheme, path := ParseAssetURI(tt.uri)
		if scheme != tt.scheme {
			t.Errorf("ParseAssetURI(%q) scheme = %q, want %q", tt.uri, scheme, tt.scheme)
		}
		if path != tt.path {
			t.Errorf("ParseAssetURI(%q) path = %q, want %q", tt.uri, path, tt.path)
		}
	}
}

func TestCacheStatusJSON(t *testing.T) {
	cs := CacheStatus{
		CacheDir:      "/data/cache",
		MaxBytes:      100 * 1024 * 1024 * 1024,
		UsedBytes:     50 * 1024 * 1024 * 1024,
		ItemCount:     42,
		FetchingCount: 2,
		ErrorCount:    1,
	}
	data, err := json.Marshal(cs)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CacheStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ItemCount != 42 {
		t.Errorf("ItemCount = %d, want 42", decoded.ItemCount)
	}
}

func TestCacheEntryJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	entry := CacheEntry{
		AssetURI:     "s3://bucket/clip.ts",
		LocalPath:    "/data/cache/clip.ts",
		State:        AssetCached,
		ByteSize:     1024 * 1024 * 50,
		Checksum:     "sha256:abc123",
		Transcoded:   true,
		LastAccessed: now,
		FetchedAt:    now,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var got CacheEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.AssetURI != entry.AssetURI {
		t.Errorf("AssetURI = %q, want %q", got.AssetURI, entry.AssetURI)
	}
	if got.State != AssetCached {
		t.Errorf("State = %d, want %d", got.State, AssetCached)
	}
	if got.ByteSize != entry.ByteSize {
		t.Errorf("ByteSize = %d, want %d", got.ByteSize, entry.ByteSize)
	}
	if !got.Transcoded {
		t.Error("Transcoded = false, want true")
	}
	if got.Checksum != "sha256:abc123" {
		t.Errorf("Checksum = %q, want %q", got.Checksum, "sha256:abc123")
	}
}

func TestCacheConfigDefaults(t *testing.T) {
	cfg := CacheConfig{
		Dir:              "/data/cache",
		MaxBytes:         100 * 1024 * 1024 * 1024,
		LookaheadMinutes: 30,
		PreDecodeItems:   3,
	}
	if cfg.Dir != "/data/cache" {
		t.Errorf("Dir = %q, want %q", cfg.Dir, "/data/cache")
	}
	if cfg.LookaheadMinutes != 30 {
		t.Errorf("LookaheadMinutes = %d, want 30", cfg.LookaheadMinutes)
	}
	if cfg.PreDecodeItems != 3 {
		t.Errorf("PreDecodeItems = %d, want 3", cfg.PreDecodeItems)
	}
}

func boolPtr(b bool) *bool         { return &b }
func float64Ptr(f float64) *float64 { return &f }

func TestSecondaryEvent_ExtendedJSONRoundTrip(t *testing.T) {
	events := []SecondaryEvent{
		{
			Type:     "graphics_on",
			Layer:    2,
			Template: "lower-third",
			OffsetMs: 1000,
			Anchor:   "start",
		},
		{
			Type:      "audio_level",
			SourceKey: "camera-1",
			LevelDB:   float64Ptr(-6.5),
			OffsetMs:  500,
			Anchor:    "start",
		},
		{
			Type:      "audio_mute",
			SourceKey: "mic-1",
			Muted:     boolPtr(true),
			OffsetMs:  0,
			Anchor:    "start",
		},
		{
			Type:      "audio_afv",
			SourceKey: "mic-2",
			AFV:       boolPtr(false),
			OffsetMs:  -1000,
			Anchor:    "end",
		},
		{
			Type:        "caption_mode",
			CaptionMode: "passthrough",
			OffsetMs:    0,
			Anchor:      "start",
		},
		{
			Type:        "caption_text",
			CaptionText: "Hello, world!",
			OffsetMs:    2000,
			Anchor:      "start",
		},
		{
			Type:     "scte35_cue",
			OffsetMs: -5000,
			Anchor:   "end",
			SCTE35: &SCTE35EventData{
				BreakDurationMs: 30000,
				AutoReturn:      true,
			},
		},
	}

	for i, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("event[%d] Marshal: %v", i, err)
		}

		var got SecondaryEvent
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("event[%d] Unmarshal: %v", i, err)
		}

		if got.Type != ev.Type {
			t.Errorf("event[%d] Type = %q, want %q", i, got.Type, ev.Type)
		}
		if got.Layer != ev.Layer {
			t.Errorf("event[%d] Layer = %d, want %d", i, got.Layer, ev.Layer)
		}
		if got.Template != ev.Template {
			t.Errorf("event[%d] Template = %q, want %q", i, got.Template, ev.Template)
		}
		if got.OffsetMs != ev.OffsetMs {
			t.Errorf("event[%d] OffsetMs = %d, want %d", i, got.OffsetMs, ev.OffsetMs)
		}
		if got.Anchor != ev.Anchor {
			t.Errorf("event[%d] Anchor = %q, want %q", i, got.Anchor, ev.Anchor)
		}
		if got.SourceKey != ev.SourceKey {
			t.Errorf("event[%d] SourceKey = %q, want %q", i, got.SourceKey, ev.SourceKey)
		}
		if (got.LevelDB == nil) != (ev.LevelDB == nil) {
			t.Errorf("event[%d] LevelDB nil mismatch", i)
		} else if got.LevelDB != nil && *got.LevelDB != *ev.LevelDB {
			t.Errorf("event[%d] LevelDB = %f, want %f", i, *got.LevelDB, *ev.LevelDB)
		}
		if got.CaptionMode != ev.CaptionMode {
			t.Errorf("event[%d] CaptionMode = %q, want %q", i, got.CaptionMode, ev.CaptionMode)
		}
		if got.CaptionText != ev.CaptionText {
			t.Errorf("event[%d] CaptionText = %q, want %q", i, got.CaptionText, ev.CaptionText)
		}

		// Check *bool pointer fields.
		if ev.Muted != nil {
			if got.Muted == nil {
				t.Errorf("event[%d] Muted = nil, want %v", i, *ev.Muted)
			} else if *got.Muted != *ev.Muted {
				t.Errorf("event[%d] Muted = %v, want %v", i, *got.Muted, *ev.Muted)
			}
		} else if got.Muted != nil {
			t.Errorf("event[%d] Muted = %v, want nil", i, *got.Muted)
		}

		if ev.AFV != nil {
			if got.AFV == nil {
				t.Errorf("event[%d] AFV = nil, want %v", i, *ev.AFV)
			} else if *got.AFV != *ev.AFV {
				t.Errorf("event[%d] AFV = %v, want %v", i, *got.AFV, *ev.AFV)
			}
		} else if got.AFV != nil {
			t.Errorf("event[%d] AFV = %v, want nil", i, *got.AFV)
		}

		// Check nested SCTE35 data.
		if ev.SCTE35 != nil {
			if got.SCTE35 == nil {
				t.Errorf("event[%d] SCTE35 = nil, want non-nil", i)
			} else {
				if got.SCTE35.BreakDurationMs != ev.SCTE35.BreakDurationMs {
					t.Errorf("event[%d] SCTE35.BreakDurationMs = %d, want %d", i, got.SCTE35.BreakDurationMs, ev.SCTE35.BreakDurationMs)
				}
				if got.SCTE35.AutoReturn != ev.SCTE35.AutoReturn {
					t.Errorf("event[%d] SCTE35.AutoReturn = %v, want %v", i, got.SCTE35.AutoReturn, ev.SCTE35.AutoReturn)
				}
			}
		} else if got.SCTE35 != nil {
			t.Errorf("event[%d] SCTE35 = non-nil, want nil", i)
		}
	}
}

func TestSecondaryEvent_OmitsEmptyFields(t *testing.T) {
	// A minimal graphics event should not include audio/caption/scte35 fields.
	ev := SecondaryEvent{
		Type:     "graphics_on",
		Layer:    1,
		Template: "lower-third",
		OffsetMs: 0,
		Anchor:   "start",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	for _, key := range []string{"source_key", "level_db", "muted", "afv", "caption_mode", "caption_text", "scte35"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected key %q to be omitted for graphics event, but it was present", key)
		}
	}
}

func TestAssetPipelineSentinelErrors(t *testing.T) {
	errs := []error{
		ErrAssetNotFound,
		ErrAssetNotReady,
		ErrCacheFull,
		ErrUnsupportedURI,
		ErrPathTraversal,
		ErrFetchFailed,
		ErrTranscodeFailed,
	}
	for _, err := range errs {
		if err == nil {
			t.Error("sentinel error is nil")
		}
		if err.Error() == "" {
			t.Error("sentinel error has empty message")
		}
	}
}
