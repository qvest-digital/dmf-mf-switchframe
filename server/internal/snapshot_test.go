package internal

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// populatedSnapshot returns a fully populated FullStateSnapshot with realistic values
// for round-trip testing.
func populatedSnapshot() FullStateSnapshot {
	return FullStateSnapshot{
		Version:    "1",
		CapturedAt: time.Now().UnixMicro(),

		// Switching
		ProgramSource: "cam1",
		PreviewSource: "cam2",

		// Transition
		TransitionType:       "mix",
		TransitionDurationMs: 1000,
		TransitionEasing:     "smoothstep",
		FTBActive:            false,

		// Audio channels
		AudioChannels: map[string]AudioChannelSnapshot{
			"cam1": {
				Level:        -3.0,
				Trim:         2.5,
				Muted:        false,
				AFV:          true,
				PhaseInvert:  false,
				Balance:      0.0,
				HPFFrequency: 80.0,
				HPFEnabled:   true,
				EQ: [3]EQBandSnapshot{
					{Frequency: 100, Gain: -2.0, Q: 1.4, Enabled: true},
					{Frequency: 1000, Gain: 0.0, Q: 1.0, Enabled: false},
					{Frequency: 8000, Gain: 3.5, Q: 0.7, Enabled: true},
				},
				Compressor: CompressorSnapshot{
					Threshold:  -18.0,
					Ratio:      4.0,
					Attack:     10.0,
					Release:    100.0,
					MakeupGain: 3.0,
				},
				Gate: GateSnapshot{
					Threshold: -40.0,
					Range:     -80.0,
					Attack:    0.5,
					Release:   50.0,
					Hold:      25.0,
					Enabled:   true,
				},
				AudioDelayMs: 40,
			},
			"cam2": {
				Level:        -6.0,
				Trim:         0.0,
				Muted:        true,
				AFV:          false,
				PhaseInvert:  true,
				Balance:      -0.5,
				HPFFrequency: 0,
				HPFEnabled:   false,
				EQ: [3]EQBandSnapshot{
					{Frequency: 200, Gain: 1.0, Q: 1.0, Enabled: true},
					{Frequency: 2000, Gain: -1.5, Q: 2.0, Enabled: true},
					{Frequency: 10000, Gain: 0.0, Q: 1.0, Enabled: false},
				},
				Compressor: CompressorSnapshot{
					Threshold:  -24.0,
					Ratio:      2.0,
					Attack:     20.0,
					Release:    200.0,
					MakeupGain: 0.0,
				},
				Gate: GateSnapshot{
					Threshold: -50.0,
					Range:     -60.0,
					Attack:    1.0,
					Release:   100.0,
					Hold:      50.0,
					Enabled:   false,
				},
				AudioDelayMs: 0,
			},
		},

		// Audio master
		AudioMaster: AudioMasterSnapshot{
			MasterLevel:      -8.0,
			LimiterThreshold: -1.0,
			LimiterCeiling:   -0.5,
		},

		// Graphics
		Graphics: []GraphicsLayerSnapshot{
			{
				ID:        1,
				Active:    true,
				ZOrder:    0,
				X:         0,
				Y:         800,
				Width:     1920,
				Height:    280,
				Template:  "lower-third",
				ImageName: "guest_name.png",
				HTML5URL:  "http://example.com/lower-third.html",
			},
			{
				ID:        2,
				Active:    false,
				ZOrder:    1,
				X:         100,
				Y:         100,
				Width:     300,
				Height:    200,
				Template:  "score-bug",
				ImageName: "",
			},
		},

		// DVE/Layout
		Layout: LayoutSnapshot{
			ActivePreset: "side-by-side",
			Slots: []DVESlotSnapshot{
				{
					SourceKey:    "cam1",
					Enabled:      true,
					ZOrder:       0,
					PositionX:    0,
					PositionY:    0,
					Width:        960,
					Height:       540,
					RotationDeg:  0,
					AnchorX:      0.5,
					AnchorY:      0.5,
					PerspectiveX: 0,
					PerspectiveY: 0,
					CropTop:      0,
					CropBottom:   0,
					CropLeft:     0,
					CropRight:    0,
					CropSoftness: 0,
					Opacity:      1.0,
					ScaleMode:    "fit",
					CropAnchor:   [2]float64{0.5, 0.5},
					Border: &DVEBorderSnapshot{
						Width:        2.0,
						ColorY:       235,
						ColorCb:      128,
						ColorCr:      128,
						CornerRadius: 4.0,
						Softness:     0,
						InnerWidth:   0,
					},
					Shadow: &DVEShadowSnapshot{
						Enabled: true,
						OffsetX: 4,
						OffsetY: 4,
						Blur:    8,
						Opacity: 0.5,
						ColorY:  16,
					},
				},
				{
					SourceKey: "cam2",
					Enabled:   true,
					ZOrder:    1,
					PositionX: 960,
					PositionY: 0,
					Width:     960,
					Height:    540,
					Opacity:   1.0,
					ScaleMode: "fill",
				},
			},
		},

		// Captions
		CaptionMode: "passthrough",

		// Clip players
		ClipPlayers: []ClipPlayerSnapshot{
			{ID: 0, ClipID: "abc123", State: "playing", Speed: 1.0, Loop: false},
			{ID: 1, ClipID: "", State: "empty", Speed: 1.0, Loop: false},
			{ID: 2, ClipID: "def456", State: "paused", Speed: 0.5, Loop: true},
			{ID: 3, ClipID: "", State: "empty", Speed: 1.0, Loop: false},
		},

		// Source config
		Sources: map[string]SourceConfigSnapshot{
			"cam1": {Label: "Camera 1", Position: 1, DelayMs: 100},
			"cam2": {Label: "Camera 2", Position: 2},
			"cam3": {Label: "Replay", Position: 3, DelayMs: 250},
		},

		// Upstream keys
		UpstreamKeys: map[string]UpstreamKeySnapshot{
			"cam1": {
				Type:           "chroma",
				Enabled:        true,
				KeyColorY:      82,
				KeyColorCb:     90,
				KeyColorCr:     240,
				Similarity:     0.35,
				Smoothness:     0.15,
				SpillSuppress:  0.5,
				SpillReplaceCb: 128,
				SpillReplaceCr: 128,
				FillSource:     "cam3",
			},
		},

		// Color grading
		ColorGrade: &ColorGradeSnapshot{
			Enabled:   true,
			LookName:  "broadcast-warm",
			LookGroup: "broadcast",
			Intensity: 0.75,
		},

		// ST map assignments
		STMaps: &STMapSnapshot{
			Sources: map[string]string{"cam1": "barrel_correction"},
			Program: "identity",
		},
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	original := populatedSnapshot()

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var decoded FullStateSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Compare
	if !reflect.DeepEqual(original, decoded) {
		t.Errorf("round-trip mismatch")
		// Find which field differs for diagnostic clarity
		origJSON, _ := json.MarshalIndent(original, "", "  ")
		decodedJSON, _ := json.MarshalIndent(decoded, "", "  ")
		if string(origJSON) != string(decodedJSON) {
			t.Errorf("original JSON:\n%s", origJSON)
			t.Errorf("decoded JSON:\n%s", decodedJSON)
		}
	}
}

func TestSnapshotJSONTopLevelKeys(t *testing.T) {
	snap := populatedSnapshot()

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	expectedKeys := []string{
		"version",
		"capturedAt",
		"programSource",
		"previewSource",
		"transitionType",
		"transitionDurationMs",
		"transitionEasing",
		"audioChannels",
		"audioMaster",
		"graphics",
		"layout",
		"captionMode",
		"clipPlayers",
		"sources",
		"upstreamKeys",
		"colorGrade",
		"stmaps",
	}

	for _, key := range expectedKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected top-level key %q not found in JSON", key)
		}
	}
}

func TestSnapshotVersionField(t *testing.T) {
	snap := FullStateSnapshot{
		Version: "1",
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	var version string
	if err := json.Unmarshal(raw["version"], &version); err != nil {
		t.Fatalf("unmarshal version: %v", err)
	}
	if version != "1" {
		t.Errorf("expected version %q, got %q", "1", version)
	}
}

func TestSnapshotCapturedAtField(t *testing.T) {
	now := time.Now().UnixMicro()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: now,
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded FullStateSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.CapturedAt != now {
		t.Errorf("CapturedAt: expected %d, got %d", now, decoded.CapturedAt)
	}
}

func TestSnapshotEmptyState(t *testing.T) {
	// A minimal/empty snapshot should still round-trip cleanly.
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: time.Now().UnixMicro(),
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded FullStateSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if !reflect.DeepEqual(snap, decoded) {
		t.Error("empty snapshot round-trip mismatch")
	}
}

func TestSnapshotOmitsTransientData(t *testing.T) {
	// Verify the snapshot types don't have fields that would contain
	// transient metering data (peakL, peakR, gainReduction, etc.)
	snap := populatedSnapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	raw := string(data)

	transientKeys := []string{
		`"peakL"`,
		`"peakR"`,
		`"gainReduction"`,
		`"gateReduction"`,
		`"momentaryLufs"`,
		`"shortTermLufs"`,
		`"integratedLufs"`,
		`"bytesWritten"`,
		`"droppedPackets"`,
	}

	for _, key := range transientKeys {
		if contains(raw, key) {
			t.Errorf("snapshot JSON should not contain transient key %s", key)
		}
	}
}

// contains checks if s contains substr. Using a helper to avoid importing strings.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSnapshotAudioChannelFields(t *testing.T) {
	snap := populatedSnapshot()

	ch, ok := snap.AudioChannels["cam1"]
	if !ok {
		t.Fatal("expected cam1 audio channel")
	}

	// Verify restorable fields are captured
	if ch.Level != -3.0 {
		t.Errorf("Level: got %v, want -3.0", ch.Level)
	}
	if ch.Trim != 2.5 {
		t.Errorf("Trim: got %v, want 2.5", ch.Trim)
	}
	if ch.AFV != true {
		t.Error("AFV: expected true")
	}
	if ch.HPFEnabled != true {
		t.Error("HPFEnabled: expected true")
	}
	if ch.HPFFrequency != 80.0 {
		t.Errorf("HPFFrequency: got %v, want 80.0", ch.HPFFrequency)
	}
	if ch.EQ[0].Frequency != 100 {
		t.Errorf("EQ[0].Frequency: got %v, want 100", ch.EQ[0].Frequency)
	}
	if ch.Compressor.Threshold != -18.0 {
		t.Errorf("Compressor.Threshold: got %v, want -18.0", ch.Compressor.Threshold)
	}
	if ch.Gate.Threshold != -40.0 {
		t.Errorf("Gate.Threshold: got %v, want -40.0", ch.Gate.Threshold)
	}
	if ch.Gate.Enabled != true {
		t.Error("Gate.Enabled: expected true")
	}
	if ch.AudioDelayMs != 40 {
		t.Errorf("AudioDelayMs: got %v, want 40", ch.AudioDelayMs)
	}
}

func TestSnapshotDVESlotFields(t *testing.T) {
	snap := populatedSnapshot()

	if len(snap.Layout.Slots) != 2 {
		t.Fatalf("expected 2 DVE slots, got %d", len(snap.Layout.Slots))
	}

	slot := snap.Layout.Slots[0]
	if slot.SourceKey != "cam1" {
		t.Errorf("SourceKey: got %q, want %q", slot.SourceKey, "cam1")
	}
	if slot.Border == nil {
		t.Fatal("expected Border to be non-nil")
	}
	if slot.Border.Width != 2.0 {
		t.Errorf("Border.Width: got %v, want 2.0", slot.Border.Width)
	}
	if slot.Shadow == nil {
		t.Fatal("expected Shadow to be non-nil")
	}
	if slot.Shadow.Enabled != true {
		t.Error("Shadow.Enabled: expected true")
	}
}

func TestSnapshotUpstreamKeys(t *testing.T) {
	snap := populatedSnapshot()

	key, ok := snap.UpstreamKeys["cam1"]
	if !ok {
		t.Fatal("expected cam1 upstream key")
	}

	if key.Type != "chroma" {
		t.Errorf("Type: got %q, want %q", key.Type, "chroma")
	}
	if key.Enabled != true {
		t.Error("Enabled: expected true")
	}
	if key.Similarity != 0.35 {
		t.Errorf("Similarity: got %v, want 0.35", key.Similarity)
	}
	if key.FillSource != "cam3" {
		t.Errorf("FillSource: got %q, want %q", key.FillSource, "cam3")
	}
}

// --- New operational state type JSON tests ---

func TestRecordingSnapshot_JSON(t *testing.T) {
	snap := RecordingSnapshot{Active: true, Engine: "engine-a"}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RecordingSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestDestinationSnapshot_JSON(t *testing.T) {
	snap := DestinationSnapshot{
		ID: "dest-1",
		Config: DestinationSnapshotConfig{
			Type:     "srt_caller",
			Address:  "10.0.0.1",
			Port:     9000,
			Latency:  200,
			StreamID: "test",
			Name:     "Main Out",
			Engine:   "engine-a",
		},
		Active: true,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DestinationSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != snap.ID || got.Active != snap.Active || got.Config.Type != snap.Config.Type {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
	if got.Config.Port != 9000 || got.Config.Latency != 200 {
		t.Errorf("config detail mismatch: got %+v", got.Config)
	}
}

func TestReplayMarksSnapshot_JSON(t *testing.T) {
	snap := ReplayMarksSnapshot{Source: "cam1", MarkIn: 1000, MarkOut: 5000}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ReplayMarksSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestReplayPlayerSnapshot_JSON(t *testing.T) {
	snap := ReplayPlayerSnapshot{Source: "cam1", State: "playing", Speed: 0.5}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ReplayPlayerSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestMacroExecutionSnapshot_JSON(t *testing.T) {
	snap := MacroExecutionSnapshot{MacroID: "m1", StepIndex: 3, State: "running"}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got MacroExecutionSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestOperatorSessionSnapshot_JSON(t *testing.T) {
	snap := OperatorSessionSnapshot{
		OperatorID:       "op-1",
		Name:             "Alice",
		Role:             "director",
		LockedSubsystems: []string{"audio", "graphics"},
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OperatorSessionSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OperatorID != snap.OperatorID || got.Name != snap.Name || got.Role != snap.Role {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
	if len(got.LockedSubsystems) != 2 || got.LockedSubsystems[0] != "audio" {
		t.Errorf("locked subsystems mismatch: got %v", got.LockedSubsystems)
	}
}

func TestPlayoutChannelSnapshot_JSON(t *testing.T) {
	snap := PlayoutChannelSnapshot{
		ChannelIndex:       1,
		CurrentItemIndex:   5,
		ElapsedMs:          12345,
		SchedulerState:     "playing",
		TransitionProgress: 0.75,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got PlayoutChannelSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestSCTE35ActiveEventSnapshot_JSON(t *testing.T) {
	snap := SCTE35ActiveEventSnapshot{
		EventID:      42,
		Type:         "splice_insert",
		RemainingMs:  30000,
		Hold:         true,
		AutoReturnMs: 60000,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got SCTE35ActiveEventSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestClipPositionSnapshot_JSON(t *testing.T) {
	snap := ClipPositionSnapshot{
		ClipName:    "intro.mp4",
		PlayerIndex: 2,
		PositionMs:  5000,
		State:       "playing",
		Speed:       1.0,
		Loop:        true,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ClipPositionSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != snap {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, snap)
	}
}

func TestFullStateSnapshot_NewFields_JSON(t *testing.T) {
	snap := FullStateSnapshot{
		Version:       "1",
		CapturedAt:    1234567890,
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		Recording:     &RecordingSnapshot{Active: true, Engine: "engine-a"},
		OutputDestinations: []DestinationSnapshot{
			{ID: "d1", Config: DestinationSnapshotConfig{Type: "srt_caller", Address: "10.0.0.1", Port: 9000}, Active: true},
		},
		ReplayMarks:    &ReplayMarksSnapshot{Source: "cam1", MarkIn: 1000, MarkOut: 5000},
		ReplayPlayer:   &ReplayPlayerSnapshot{Source: "cam1", State: "playing", Speed: 0.5},
		MacroExecution: &MacroExecutionSnapshot{MacroID: "m1", StepIndex: 2, State: "running"},
		OperatorSessions: []OperatorSessionSnapshot{
			{OperatorID: "op-1", Name: "Alice", Role: "director", LockedSubsystems: []string{"audio"}},
		},
		CommsParticipants: []string{"op-1", "op-2"},
		PlayoutChannels: []PlayoutChannelSnapshot{
			{ChannelIndex: 0, CurrentItemIndex: 3, ElapsedMs: 5000, SchedulerState: "playing"},
		},
		SCTE35Active: []SCTE35ActiveEventSnapshot{
			{EventID: 42, Type: "splice_insert", RemainingMs: 30000, Hold: false},
		},
		StingerLoaded: []string{"whoosh", "slam"},
		ClipPlayerPositions: []ClipPositionSnapshot{
			{ClipName: "intro.mp4", PlayerIndex: 0, PositionMs: 1500, State: "playing", Speed: 1.0, Loop: false},
		},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got FullStateSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Recording == nil || !got.Recording.Active || got.Recording.Engine != "engine-a" {
		t.Error("recording mismatch")
	}
	if len(got.OutputDestinations) != 1 || got.OutputDestinations[0].ID != "d1" {
		t.Error("output destinations mismatch")
	}
	if got.ReplayMarks == nil || got.ReplayMarks.Source != "cam1" {
		t.Error("replay marks mismatch")
	}
	if got.ReplayPlayer == nil || got.ReplayPlayer.Speed != 0.5 {
		t.Error("replay player mismatch")
	}
	if got.MacroExecution == nil || got.MacroExecution.MacroID != "m1" {
		t.Error("macro execution mismatch")
	}
	if len(got.OperatorSessions) != 1 || got.OperatorSessions[0].Name != "Alice" {
		t.Error("operator sessions mismatch")
	}
	if len(got.CommsParticipants) != 2 {
		t.Error("comms participants mismatch")
	}
	if len(got.PlayoutChannels) != 1 || got.PlayoutChannels[0].SchedulerState != "playing" {
		t.Error("playout channels mismatch")
	}
	if len(got.SCTE35Active) != 1 || got.SCTE35Active[0].EventID != 42 {
		t.Error("scte-35 active mismatch")
	}
	if len(got.StingerLoaded) != 2 || got.StingerLoaded[0] != "whoosh" {
		t.Error("stinger loaded mismatch")
	}
	if len(got.ClipPlayerPositions) != 1 || got.ClipPlayerPositions[0].ClipName != "intro.mp4" {
		t.Error("clip player positions mismatch")
	}
}

func TestFullStateSnapshot_NewFields_OmitEmpty(t *testing.T) {
	snap := FullStateSnapshot{
		Version:       "1",
		CapturedAt:    1234567890,
		ProgramSource: "cam1",
		PreviewSource: "cam2",
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	for _, key := range []string{
		"recording", "outputDestinations", "replayMarks", "replayPlayer",
		"macroExecution", "operatorSessions", "commsParticipants",
		"playoutChannels", "scte35Active", "stingerLoaded", "clipPlayerPositions",
	} {
		if _, ok := raw[key]; ok {
			t.Errorf("field %q should be omitted when empty/nil, but was present", key)
		}
	}
}

// --- Apply tests for optional applier interfaces ---

// mockBaseTarget satisfies the base SnapshotApplyTarget with no-ops.
type mockBaseTarget struct{}

func (m *mockBaseTarget) Cut(ctx context.Context, source string) error             { return nil }
func (m *mockBaseTarget) SetPreview(ctx context.Context, source string) error      { return nil }
func (m *mockBaseTarget) SetTransitionConfig(string, int, string) error            { return nil }
func (m *mockBaseTarget) SetFTB(bool) error                                        { return nil }
func (m *mockBaseTarget) SetLevel(string, float64) error                           { return nil }
func (m *mockBaseTarget) SetTrim(string, float64) error                            { return nil }
func (m *mockBaseTarget) SetMuted(string, bool) error                              { return nil }
func (m *mockBaseTarget) SetAFV(string, bool) error                                { return nil }
func (m *mockBaseTarget) SetPhaseInvert(string, bool) error                        { return nil }
func (m *mockBaseTarget) SetBalance(string, float64) error                         { return nil }
func (m *mockBaseTarget) SetHPF(string, float64, bool) error                       { return nil }
func (m *mockBaseTarget) SetEQ(string, int, float64, float64, float64, bool) error { return nil }
func (m *mockBaseTarget) SetCompressor(string, float64, float64, float64, float64, float64) error {
	return nil
}
func (m *mockBaseTarget) SetGate(string, float64, float64, float64, float64, float64, bool) error {
	return nil
}
func (m *mockBaseTarget) SetAudioDelay(string, int) error                      { return nil }
func (m *mockBaseTarget) SetMasterLevel(float64) error                         { return nil }
func (m *mockBaseTarget) SetLimiterThreshold(float64) error                    { return nil }
func (m *mockBaseTarget) SetLimiterCeiling(float64) error                      { return nil }
func (m *mockBaseTarget) SetGraphicsLayerActive(int, bool) error               { return nil }
func (m *mockBaseTarget) SetGraphicsLayerZOrder(int, int) error                { return nil }
func (m *mockBaseTarget) SetGraphicsLayerRect(int, int, int, int, int) error   { return nil }
func (m *mockBaseTarget) SetDVELayout(LayoutSnapshot) error                    { return nil }
func (m *mockBaseTarget) SetSourceLabel(context.Context, string, string) error { return nil }
func (m *mockBaseTarget) SetSourcePosition(string, int) error                  { return nil }
func (m *mockBaseTarget) SetSourceDelay(string, int) error                     { return nil }
func (m *mockBaseTarget) SetUpstreamKey(string, UpstreamKeySnapshot) error     { return nil }
func (m *mockBaseTarget) SetCaptionMode(string) error                          { return nil }
func (m *mockBaseTarget) SetColorGrade(string, float32) error                  { return nil }
func (m *mockBaseTarget) DisableColorGrade() error                             { return nil }
func (m *mockBaseTarget) AssignSTMapSource(string, string) error               { return nil }
func (m *mockBaseTarget) AssignSTMapProgram(string) error                      { return nil }

// mockExtendedTarget extends mockBaseTarget with all optional applier interfaces.
type mockExtendedTarget struct {
	mockBaseTarget

	recordingApplied    bool
	recordingSnap       RecordingSnapshot
	destinationsApplied bool
	destinationSnaps    []DestinationSnapshot
	replayMarksApplied  bool
	replayMarksSnap     ReplayMarksSnapshot
	replayPlayerApplied bool
	replayPlayerSnap    ReplayPlayerSnapshot
	operatorsApplied    bool
	operatorSnaps       []OperatorSessionSnapshot
	playoutApplied      bool
	playoutSnaps        []PlayoutChannelSnapshot
	scte35Applied       bool
	scte35Snaps         []SCTE35ActiveEventSnapshot
	macroApplied        bool
	macroSnap           MacroExecutionSnapshot
}

func (m *mockExtendedTarget) ApplyRecording(snap RecordingSnapshot) error {
	m.recordingApplied = true
	m.recordingSnap = snap
	return nil
}

func (m *mockExtendedTarget) ApplyOutputDestinations(dests []DestinationSnapshot) error {
	m.destinationsApplied = true
	m.destinationSnaps = dests
	return nil
}

func (m *mockExtendedTarget) ApplyReplayMarks(marks ReplayMarksSnapshot) error {
	m.replayMarksApplied = true
	m.replayMarksSnap = marks
	return nil
}

func (m *mockExtendedTarget) ApplyReplayPlayer(player ReplayPlayerSnapshot) error {
	m.replayPlayerApplied = true
	m.replayPlayerSnap = player
	return nil
}

func (m *mockExtendedTarget) ApplyOperatorSessions(sessions []OperatorSessionSnapshot) error {
	m.operatorsApplied = true
	m.operatorSnaps = sessions
	return nil
}

func (m *mockExtendedTarget) ApplyPlayoutChannels(channels []PlayoutChannelSnapshot) error {
	m.playoutApplied = true
	m.playoutSnaps = channels
	return nil
}

func (m *mockExtendedTarget) ApplySCTE35Active(events []SCTE35ActiveEventSnapshot) error {
	m.scte35Applied = true
	m.scte35Snaps = events
	return nil
}

func (m *mockExtendedTarget) ApplyMacroExecution(exec MacroExecutionSnapshot) error {
	m.macroApplied = true
	m.macroSnap = exec
	return nil
}

func TestApplyFullSnapshot_CallsOptionalAppliers(t *testing.T) {
	target := &mockExtendedTarget{}
	snap := FullStateSnapshot{
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		Recording:     &RecordingSnapshot{Active: true, Engine: "engine-a"},
		OutputDestinations: []DestinationSnapshot{
			{ID: "d1", Config: DestinationSnapshotConfig{Type: "srt_caller"}, Active: true},
		},
		ReplayMarks:    &ReplayMarksSnapshot{Source: "cam1", MarkIn: 1000, MarkOut: 5000},
		ReplayPlayer:   &ReplayPlayerSnapshot{Source: "cam1", State: "playing", Speed: 0.5},
		MacroExecution: &MacroExecutionSnapshot{MacroID: "m1", StepIndex: 2, State: "running"},
		OperatorSessions: []OperatorSessionSnapshot{
			{OperatorID: "op-1", Name: "Alice", Role: "director"},
		},
		PlayoutChannels: []PlayoutChannelSnapshot{
			{ChannelIndex: 0, CurrentItemIndex: 3, ElapsedMs: 5000, SchedulerState: "playing"},
		},
		SCTE35Active: []SCTE35ActiveEventSnapshot{
			{EventID: 42, Type: "splice_insert", RemainingMs: 30000},
		},
	}

	warnings := ApplyFullSnapshot(context.Background(), snap, target)

	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	if !target.recordingApplied {
		t.Error("recording was not applied")
	}
	if !target.recordingSnap.Active {
		t.Error("recording snapshot data mismatch")
	}

	if !target.destinationsApplied {
		t.Error("destinations were not applied")
	}
	if len(target.destinationSnaps) != 1 || target.destinationSnaps[0].ID != "d1" {
		t.Error("destination snapshot data mismatch")
	}

	if !target.replayMarksApplied {
		t.Error("replay marks were not applied")
	}
	if target.replayMarksSnap.Source != "cam1" || target.replayMarksSnap.MarkIn != 1000 {
		t.Error("replay marks snapshot data mismatch")
	}

	if !target.replayPlayerApplied {
		t.Error("replay player was not applied")
	}
	if target.replayPlayerSnap.Speed != 0.5 || target.replayPlayerSnap.State != "playing" {
		t.Error("replay player snapshot data mismatch")
	}

	if !target.macroApplied {
		t.Error("macro execution was not applied")
	}
	if target.macroSnap.MacroID != "m1" || target.macroSnap.StepIndex != 2 {
		t.Error("macro snapshot data mismatch")
	}

	if !target.operatorsApplied {
		t.Error("operator sessions were not applied")
	}
	if len(target.operatorSnaps) != 1 || target.operatorSnaps[0].Name != "Alice" {
		t.Error("operator snapshot data mismatch")
	}

	if !target.playoutApplied {
		t.Error("playout channels were not applied")
	}
	if len(target.playoutSnaps) != 1 || target.playoutSnaps[0].SchedulerState != "playing" {
		t.Error("playout snapshot data mismatch")
	}

	if !target.scte35Applied {
		t.Error("scte-35 events were not applied")
	}
	if len(target.scte35Snaps) != 1 || target.scte35Snaps[0].EventID != 42 {
		t.Error("scte-35 snapshot data mismatch")
	}
}

func TestApplyFullSnapshot_SkipsWhenTargetDoesNotImplementOptional(t *testing.T) {
	target := &mockBaseTarget{}
	snap := FullStateSnapshot{
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		Recording:     &RecordingSnapshot{Active: true},
		OutputDestinations: []DestinationSnapshot{
			{ID: "d1", Config: DestinationSnapshotConfig{Type: "srt_caller"}, Active: true},
		},
		ReplayMarks:    &ReplayMarksSnapshot{Source: "cam1", MarkIn: 1000, MarkOut: 5000},
		ReplayPlayer:   &ReplayPlayerSnapshot{Source: "cam1", State: "playing", Speed: 0.5},
		MacroExecution: &MacroExecutionSnapshot{MacroID: "m1", StepIndex: 2, State: "running"},
		OperatorSessions: []OperatorSessionSnapshot{
			{OperatorID: "op-1", Name: "Alice", Role: "director"},
		},
		PlayoutChannels: []PlayoutChannelSnapshot{
			{ChannelIndex: 0, CurrentItemIndex: 3, ElapsedMs: 5000, SchedulerState: "playing"},
		},
		SCTE35Active: []SCTE35ActiveEventSnapshot{
			{EventID: 42, Type: "splice_insert", RemainingMs: 30000},
		},
	}

	// Should not panic — optional interfaces are simply skipped
	warnings := ApplyFullSnapshot(context.Background(), snap, target)

	// No warnings expected from optional sections that are skipped
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}
}

func TestApplyFullSnapshot_NilFieldsSkipApply(t *testing.T) {
	target := &mockExtendedTarget{}
	snap := FullStateSnapshot{
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		// All new fields are nil/empty
	}

	warnings := ApplyFullSnapshot(context.Background(), snap, target)

	if target.recordingApplied {
		t.Error("recording should not be applied when nil")
	}
	if target.destinationsApplied {
		t.Error("destinations should not be applied when nil")
	}
	if target.replayMarksApplied {
		t.Error("replay marks should not be applied when nil")
	}
	if target.replayPlayerApplied {
		t.Error("replay player should not be applied when nil")
	}
	if target.macroApplied {
		t.Error("macro should not be applied when nil")
	}
	if target.operatorsApplied {
		t.Error("operators should not be applied when nil")
	}
	if target.playoutApplied {
		t.Error("playout should not be applied when nil")
	}
	if target.scte35Applied {
		t.Error("scte-35 should not be applied when nil")
	}

	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

// mockFailingApplier implements optional interfaces but returns errors.
type mockFailingApplier struct {
	mockBaseTarget
}

func (m *mockFailingApplier) ApplyRecording(RecordingSnapshot) error {
	return errors.New("recording failed")
}

func (m *mockFailingApplier) ApplyOutputDestinations([]DestinationSnapshot) error {
	return errors.New("destinations failed")
}

func (m *mockFailingApplier) ApplyReplayMarks(ReplayMarksSnapshot) error {
	return errors.New("replay marks failed")
}

func (m *mockFailingApplier) ApplyReplayPlayer(ReplayPlayerSnapshot) error {
	return errors.New("replay player failed")
}

func (m *mockFailingApplier) ApplyOperatorSessions([]OperatorSessionSnapshot) error {
	return errors.New("operators failed")
}

func (m *mockFailingApplier) ApplyPlayoutChannels([]PlayoutChannelSnapshot) error {
	return errors.New("playout failed")
}

func (m *mockFailingApplier) ApplySCTE35Active([]SCTE35ActiveEventSnapshot) error {
	return errors.New("scte-35 failed")
}

func (m *mockFailingApplier) ApplyMacroExecution(MacroExecutionSnapshot) error {
	return errors.New("macro failed")
}

func TestApplyFullSnapshot_CollectsWarningsFromFailingAppliers(t *testing.T) {
	target := &mockFailingApplier{}
	snap := FullStateSnapshot{
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		Recording:     &RecordingSnapshot{Active: true},
		OutputDestinations: []DestinationSnapshot{
			{ID: "d1", Config: DestinationSnapshotConfig{Type: "srt_caller"}, Active: true},
		},
		ReplayMarks:    &ReplayMarksSnapshot{Source: "cam1", MarkIn: 1000, MarkOut: 5000},
		ReplayPlayer:   &ReplayPlayerSnapshot{Source: "cam1", State: "playing", Speed: 0.5},
		MacroExecution: &MacroExecutionSnapshot{MacroID: "m1", StepIndex: 2, State: "running"},
		OperatorSessions: []OperatorSessionSnapshot{
			{OperatorID: "op-1", Name: "Alice", Role: "director"},
		},
		PlayoutChannels: []PlayoutChannelSnapshot{
			{ChannelIndex: 0, CurrentItemIndex: 3, ElapsedMs: 5000, SchedulerState: "playing"},
		},
		SCTE35Active: []SCTE35ActiveEventSnapshot{
			{EventID: 42, Type: "splice_insert", RemainingMs: 30000},
		},
	}

	warnings := ApplyFullSnapshot(context.Background(), snap, target)

	expectedPrefixes := []string{
		"output destinations:",
		"recording:",
		"replay marks:",
		"replay player:",
		"playout channels:",
		"scte-35 events:",
		"operator sessions:",
		"macro execution:",
	}

	for _, prefix := range expectedPrefixes {
		found := false
		for _, w := range warnings {
			if len(w) >= len(prefix) && w[:len(prefix)] == prefix {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected warning with prefix %q in %v", prefix, warnings)
		}
	}
}

func TestFullStateSnapshot_EncoderField(t *testing.T) {
	snap := FullStateSnapshot{
		Version:       "1",
		ProgramSource: "cam1",
		Encoder:       "h264_nvenc",
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FullStateSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Encoder != "h264_nvenc" {
		t.Fatalf("expected encoder h264_nvenc, got %q", decoded.Encoder)
	}
}
