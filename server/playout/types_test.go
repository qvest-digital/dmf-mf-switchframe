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
