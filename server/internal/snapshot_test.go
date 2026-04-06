package internal

import (
	"encoding/json"
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
			"cam1": {Label: "Camera 1", Position: 1},
			"cam2": {Label: "Camera 2", Position: 2},
			"cam3": {Label: "Replay", Position: 3},
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
