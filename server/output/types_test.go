package output

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdapterState_String(t *testing.T) {
	require.Equal(t, "starting", string(StateStarting))
	require.Equal(t, "active", string(StateActive))
	require.Equal(t, "reconnecting", string(StateReconnecting))
	require.Equal(t, "stopped", string(StateStopped))
	require.Equal(t, "error", string(StateError))
}

func TestRecordingStatus_Defaults(t *testing.T) {
	s := RecordingStatus{}
	require.False(t, s.Active)
	require.Empty(t, s.Filename)
	require.Zero(t, s.BytesWritten)
}

func TestSRTOutputStatus_Defaults(t *testing.T) {
	s := SRTOutputStatus{}
	require.False(t, s.Active)
	require.Empty(t, s.Mode)
	require.Zero(t, s.Connections)
}
