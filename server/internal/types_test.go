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

func TestNeuralKeyStateJSON(t *testing.T) {
	t.Parallel()
	nk := internal.NeuralKeyState{Available: true}

	data, err := json.Marshal(nk)
	require.NoError(t, err)
	require.JSONEq(t, `{"available":true}`, string(data))

	var decoded internal.NeuralKeyState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.True(t, decoded.Available)
}

func TestControlRoomStateNeuralKeyField(t *testing.T) {
	t.Parallel()
	state := internal.ControlRoomState{
		ProgramSource: "cam1",
		Sources:       map[string]internal.SourceInfo{},
		NeuralKey:     &internal.NeuralKeyState{Available: true},
		Seq:           42,
	}

	data, err := json.Marshal(state)
	require.NoError(t, err)

	var decoded internal.ControlRoomState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.NeuralKey)
	require.True(t, decoded.NeuralKey.Available)

	// Verify omitempty: nil NeuralKey should not appear in JSON.
	stateNoKey := internal.ControlRoomState{ProgramSource: "cam2"}
	data2, err := json.Marshal(stateNoKey)
	require.NoError(t, err)
	require.NotContains(t, string(data2), "neuralKey")
}

func TestAROcclusionStateJSON(t *testing.T) {
	t.Parallel()

	ar := internal.AROcclusionState{
		Enabled:         true,
		DepthReady:      true,
		SAMReady:        false,
		Prompts:         []internal.ARPromptInfo{},
		DepthLatencyMs:  4.2,
		SAMLatencyMs:    0,
		FramesProcessed: 1500,
		FramesSkipped:   3,
	}

	data, err := json.Marshal(ar)
	require.NoError(t, err)

	// Prompts should be [] not null.
	require.Contains(t, string(data), `"prompts":[]`)

	var decoded internal.AROcclusionState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.True(t, decoded.Enabled)
	require.True(t, decoded.DepthReady)
	require.False(t, decoded.SAMReady)
	require.Equal(t, 4.2, decoded.DepthLatencyMs)
	require.Equal(t, int64(1500), decoded.FramesProcessed)
	require.Equal(t, int64(3), decoded.FramesSkipped)
	require.NotNil(t, decoded.Prompts, "prompts should be non-nil empty slice")
	require.Empty(t, decoded.Prompts)
}

func TestAROcclusionStateWithPrompts(t *testing.T) {
	t.Parallel()

	ar := internal.AROcclusionState{
		Enabled:    true,
		DepthReady: true,
		SAMReady:   true,
		Prompts: []internal.ARPromptInfo{
			{ID: "p1", Label: "Presenter", Type: "point"},
			{ID: "p2", Label: "Desk", Type: "box"},
		},
		DepthLatencyMs:  3.1,
		SAMLatencyMs:    8.7,
		FramesProcessed: 500,
	}

	data, err := json.Marshal(ar)
	require.NoError(t, err)

	var decoded internal.AROcclusionState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Prompts, 2)
	require.Equal(t, "p1", decoded.Prompts[0].ID)
	require.Equal(t, "Presenter", decoded.Prompts[0].Label)
	require.Equal(t, "point", decoded.Prompts[0].Type)
	require.Equal(t, "Desk", decoded.Prompts[1].Label)
	require.Equal(t, "box", decoded.Prompts[1].Type)
}

func TestControlRoomStateAROcclusionField(t *testing.T) {
	t.Parallel()

	// Present: should appear in JSON.
	state := internal.ControlRoomState{
		ProgramSource: "cam1",
		Sources:       map[string]internal.SourceInfo{},
		AROcclusion: &internal.AROcclusionState{
			Enabled:    true,
			DepthReady: true,
			Prompts:    []internal.ARPromptInfo{},
		},
		Seq: 99,
	}

	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.Contains(t, string(data), `"arOcclusion"`)

	var decoded internal.ControlRoomState
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.AROcclusion)
	require.True(t, decoded.AROcclusion.Enabled)
	require.True(t, decoded.AROcclusion.DepthReady)

	// Nil: should be omitted from JSON.
	stateNil := internal.ControlRoomState{ProgramSource: "cam2"}
	data2, err := json.Marshal(stateNil)
	require.NoError(t, err)
	require.NotContains(t, string(data2), "arOcclusion")
}
