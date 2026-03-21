package internal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestControlRoomState_STMap_JSON(t *testing.T) {
	state := ControlRoomState{
		STMap: &STMapState{
			Sources: map[string]string{"cam1": "barrel"},
			Program: &STMapProgramState{
				Map:   "heat_shimmer",
				Type:  "animated",
				Frame: 42,
			},
			Available: []string{"barrel", "heat_shimmer"},
		},
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.Contains(t, string(data), `"stmap"`)
	require.Contains(t, string(data), `"barrel"`)
	require.Contains(t, string(data), `"heat_shimmer"`)

	// Round-trip
	var decoded ControlRoomState
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	require.NotNil(t, decoded.STMap)
	require.Equal(t, "barrel", decoded.STMap.Sources["cam1"])
	require.NotNil(t, decoded.STMap.Program)
	require.Equal(t, "heat_shimmer", decoded.STMap.Program.Map)
	require.Equal(t, "animated", decoded.STMap.Program.Type)
	require.Equal(t, 42, decoded.STMap.Program.Frame)
	require.Equal(t, []string{"barrel", "heat_shimmer"}, decoded.STMap.Available)

	// Verify nil STMap is omitted
	state2 := ControlRoomState{}
	data2, _ := json.Marshal(state2)
	require.NotContains(t, string(data2), `"stmap"`)
}
