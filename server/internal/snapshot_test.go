package internal

import (
	"encoding/json"
	"testing"
)

func TestPlayoutChannelSnapshot_JSONRoundTrip(t *testing.T) {
	original := PlayoutChannelSnapshot{
		ChannelIndex:       1,
		ChannelState:       "on_air",
		CurrentItemIndex:   3,
		CurrentItemID:      "item-abc-123",
		ElapsedMs:          45000,
		SchedulerState:     "playing",
		SchedulerMode:      "scheduled",
		TransitionProgress: 0.75,
		PlaylistHash:       "sha256:deadbeef",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded PlayoutChannelSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != original {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", decoded, original)
	}
}

func TestPlayoutChannelSnapshot_OmitEmptyFields(t *testing.T) {
	sparse := PlayoutChannelSnapshot{
		ChannelIndex:     0,
		CurrentItemIndex: 2,
		ElapsedMs:        1000,
		SchedulerState:   "idle",
	}

	data, err := json.Marshal(sparse)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	for _, field := range []string{"channelState", "currentItemID", "schedulerMode", "transitionProgress", "playlistHash"} {
		if _, ok := m[field]; ok {
			t.Errorf("expected omitempty field %q to be absent from JSON, got: %s", field, string(data))
		}
	}
}

// populatedSnapshot returns a fully-populated FullStateSnapshot for use in
// apply tests. Every subsystem has representative data so ordering and
// best-effort tests can verify all code paths.
func populatedSnapshot() FullStateSnapshot {
	return FullStateSnapshot{
		Version:              "1",
		CapturedAt:           1234567890,
		ProgramSource:        "cam1",
		PreviewSource:        "cam2",
		TransitionType:       "mix",
		TransitionDurationMs: 1000,
		TransitionEasing:     "smoothstep",
		FTBActive:            true,
		Encoder:              "libx264",
		AudioChannels: map[string]AudioChannelSnapshot{
			"cam1": {
				Level:        -6.0,
				Trim:         0.0,
				Muted:        false,
				AFV:          true,
				PhaseInvert:  false,
				Balance:      0.0,
				HPFFrequency: 80.0,
				HPFEnabled:   true,
				EQ: [3]EQBandSnapshot{
					{Frequency: 100, Gain: -2.0, Q: 1.0, Enabled: true},
					{Frequency: 1000, Gain: 1.0, Q: 0.7, Enabled: true},
					{Frequency: 10000, Gain: -1.0, Q: 1.4, Enabled: false},
				},
				Compressor: CompressorSnapshot{
					Threshold: -18.0, Ratio: 4.0, Attack: 10.0, Release: 100.0, MakeupGain: 3.0,
				},
				Gate: GateSnapshot{
					Threshold: -40.0, Range: -60.0, Attack: 0.5, Release: 50.0, Hold: 20.0, Enabled: true,
				},
				AudioDelayMs: 40,
			},
			"cam2": {
				Level:       -12.0,
				Trim:        2.0,
				Muted:       true,
				AFV:         false,
				PhaseInvert: true,
				Balance:     -0.5,
				EQ: [3]EQBandSnapshot{
					{Frequency: 200, Gain: 0, Q: 1.0, Enabled: false},
					{Frequency: 2000, Gain: 0, Q: 1.0, Enabled: false},
					{Frequency: 8000, Gain: 0, Q: 1.0, Enabled: false},
				},
				Compressor:   CompressorSnapshot{Threshold: -24.0, Ratio: 2.0, Attack: 5.0, Release: 80.0},
				Gate:         GateSnapshot{Threshold: -50.0, Range: -80.0, Attack: 1.0, Release: 100.0, Hold: 50.0},
				AudioDelayMs: 20,
			},
		},
		AudioMaster: AudioMasterSnapshot{
			MasterLevel:      -8.0,
			LimiterThreshold: -1.0,
			LimiterCeiling:   -0.3,
		},
		Graphics: []GraphicsLayerSnapshot{
			{ID: 0, Active: true, ZOrder: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Template: "lower-third"},
			{ID: 1, Active: false, ZOrder: 2, X: 100, Y: 100, Width: 400, Height: 300},
		},
		Layout: LayoutSnapshot{
			ActivePreset: "side-by-side",
			Slots: []DVESlotSnapshot{
				{SourceKey: "cam1", Enabled: true, ZOrder: 0, PositionX: 0, PositionY: 0, Width: 0.5, Height: 1.0},
				{SourceKey: "cam2", Enabled: true, ZOrder: 1, PositionX: 0.5, PositionY: 0, Width: 0.5, Height: 1.0},
			},
		},
		CaptionMode: "passthrough",
		Sources: map[string]SourceConfigSnapshot{
			"cam1": {Label: "Camera 1", Position: 1, DelayMs: 100},
			"cam2": {Label: "Camera 2", Position: 2},
			"cam3": {Label: "Camera 3", Position: 3, DelayMs: 250},
		},
		UpstreamKeys: map[string]UpstreamKeySnapshot{
			"cam1": {Type: "chroma", Enabled: true, Similarity: 0.4, Smoothness: 0.08},
		},
		ColorGrade: &ColorGradeSnapshot{
			Enabled:   true,
			LookName:  "broadcast-warm",
			LookGroup: "broadcast",
			Intensity: 0.75,
		},
		STMaps: &STMapSnapshot{
			Sources: map[string]string{"cam1": "barrel_correction"},
			Program: "identity",
		},
	}
}

func TestPlayoutChannelSnapshot_DeserializeFromJSON(t *testing.T) {
	input := `{
		"channelIndex": 0,
		"channelState": "on_air",
		"currentItemIndex": 5,
		"currentItemID": "item-xyz-789",
		"elapsedMs": 12345,
		"schedulerState": "transitioning",
		"schedulerMode": "manual",
		"transitionProgress": 0.5,
		"playlistHash": "sha256:cafebabe"
	}`

	var snap PlayoutChannelSnapshot
	if err := json.Unmarshal([]byte(input), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if snap.ChannelState != "on_air" {
		t.Errorf("ChannelState = %q, want %q", snap.ChannelState, "on_air")
	}
	if snap.CurrentItemID != "item-xyz-789" {
		t.Errorf("CurrentItemID = %q, want %q", snap.CurrentItemID, "item-xyz-789")
	}
	if snap.SchedulerMode != "manual" {
		t.Errorf("SchedulerMode = %q, want %q", snap.SchedulerMode, "manual")
	}
	if snap.PlaylistHash != "sha256:cafebabe" {
		t.Errorf("PlaylistHash = %q, want %q", snap.PlaylistHash, "sha256:cafebabe")
	}
}
