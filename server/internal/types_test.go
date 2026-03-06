// server/internal/types_test.go
package internal_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal"
)

func TestControlRoomStateJSON(t *testing.T) {
	t.Parallel()
	state := internal.ControlRoomState{
		ProgramSource:        "camera1",
		PreviewSource:        "camera2",
		TransitionType:       "cut",
		TransitionDurationMs: 0,
		TransitionPosition:   0.0,
		InTransition:         false,
		TallyState:           map[string]string{"camera1": "program", "camera2": "preview"},
		Sources:              map[string]internal.SourceInfo{"camera1": {Key: "camera1", Status: "healthy"}},
		Seq:                  1,
		Timestamp:            1709500000000,
	}

	data, err := json.Marshal(state)
	require.NoError(t, err, "marshal")

	var decoded internal.ControlRoomState
	require.NoError(t, json.Unmarshal(data, &decoded), "unmarshal")

	require.Equal(t, "camera1", decoded.ProgramSource)
	require.Equal(t, "program", decoded.TallyState["camera1"])
	require.Equal(t, "healthy", decoded.Sources["camera1"].Status)
}

func TestSourceInfoHealthStatus(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			si := internal.SourceInfo{Key: "cam1", Status: tt.status}
			data, err := json.Marshal(si)
			require.NoError(t, err)
			var decoded internal.SourceInfo
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Equal(t, tt.status, decoded.Status)
		})
	}
}

func TestAudioChannelJSON(t *testing.T) {
	t.Parallel()
	ch := internal.AudioChannel{
		Level: -6.0,
		Muted: false,
		AFV:   true,
	}

	data, err := json.Marshal(ch)
	require.NoError(t, err)

	var decoded internal.AudioChannel
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, ch, decoded)
}

func TestControlRoomStateAudioFields(t *testing.T) {
	t.Parallel()
	state := internal.ControlRoomState{
		ProgramSource: "cam1",
		AudioChannels: map[string]internal.AudioChannel{
			"cam1": {Level: 0, Muted: false, AFV: true},
			"cam2": {Level: -12, Muted: true, AFV: false},
		},
		MasterLevel: -3.0,
		ProgramPeak: [2]float64{-6.0, -8.0},
	}

	data, err := json.Marshal(state)
	require.NoError(t, err)

	var decoded internal.ControlRoomState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, state.AudioChannels, decoded.AudioChannels)
	require.Equal(t, state.MasterLevel, decoded.MasterLevel)
	require.Equal(t, state.ProgramPeak, decoded.ProgramPeak)
}
