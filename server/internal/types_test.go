// server/internal/types_test.go
package internal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestControlRoomStateJSON(t *testing.T) {
	state := ControlRoomState{
		ProgramSource:        "camera1",
		PreviewSource:        "camera2",
		TransitionType:       "cut",
		TransitionDurationMs: 0,
		TransitionPosition:   0.0,
		InTransition:         false,
		TallyState:           map[string]string{"camera1": "program", "camera2": "preview"},
		Sources:              map[string]SourceInfo{"camera1": {Key: "camera1", Status: "healthy"}},
		Seq:                  1,
		Timestamp:            1709500000000,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ControlRoomState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ProgramSource != "camera1" {
		t.Errorf("ProgramSource = %q, want %q", decoded.ProgramSource, "camera1")
	}
	if decoded.TallyState["camera1"] != "program" {
		t.Errorf("TallyState[camera1] = %q, want %q", decoded.TallyState["camera1"], "program")
	}
	if decoded.Sources["camera1"].Status != "healthy" {
		t.Errorf("Sources[camera1].Status = %q, want %q", decoded.Sources["camera1"].Status, "healthy")
	}
}

func TestSourceInfoHealthStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{"healthy", "healthy", "healthy"},
		{"stale", "stale", "stale"},
		{"no_signal", "no_signal", "no_signal"},
		{"offline", "offline", "offline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			si := SourceInfo{Key: "cam1", Status: tt.status}
			data, _ := json.Marshal(si)
			var decoded SourceInfo
			_ = json.Unmarshal(data, &decoded)
			if decoded.Status != tt.status {
				t.Errorf("got %q, want %q", decoded.Status, tt.status)
			}
		})
	}
}

func TestAudioChannelJSON(t *testing.T) {
	ch := AudioChannel{
		Level: -6.0,
		Muted: false,
		AFV:   true,
	}

	data, err := json.Marshal(ch)
	require.NoError(t, err)

	var decoded AudioChannel
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, ch, decoded)
}

func TestControlRoomStateAudioFields(t *testing.T) {
	state := ControlRoomState{
		ProgramSource: "cam1",
		AudioChannels: map[string]AudioChannel{
			"cam1": {Level: 0, Muted: false, AFV: true},
			"cam2": {Level: -12, Muted: true, AFV: false},
		},
		MasterLevel: -3.0,
		ProgramPeak: [2]float64{-6.0, -8.0},
	}

	data, err := json.Marshal(state)
	require.NoError(t, err)

	var decoded ControlRoomState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, state.AudioChannels, decoded.AudioChannels)
	require.Equal(t, state.MasterLevel, decoded.MasterLevel)
	require.Equal(t, state.ProgramPeak, decoded.ProgramPeak)
}
