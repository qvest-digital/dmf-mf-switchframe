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

func TestSRTStatus_Defaults(t *testing.T) {
	s := SRTStatus{}
	require.False(t, s.Active)
	require.Empty(t, s.Mode)
	require.Zero(t, s.Connections)
}

func TestRTMPMaskURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"rtmp://a.rtmp.youtube.com/live2/xxxx-xxxx-xxxx", "rtmp://a.rtmp.youtube.com/live2/****"},
		{"rtmp://live.twitch.tv/app/live_123456", "rtmp://live.twitch.tv/app/****"},
		{"rtmps://ingest.facebook.com/rtmp/fb-key-12345", "rtmps://ingest.facebook.com/rtmp/****"},
		{"rtmp://host/app", "rtmp://host/app"},
		{"rtmp://host", "rtmp://host"},
		{"", ""},
	}
	for _, tt := range tests {
		got := rtmpMaskURL(tt.in)
		require.Equal(t, tt.want, got, "rtmpMaskURL(%q)", tt.in)
	}
}

func TestRTMPStatus_Defaults(t *testing.T) {
	t.Parallel()
	s := RTMPStatus{}
	require.False(t, s.Active)
	require.Empty(t, s.URL)
	require.Empty(t, s.State)
	require.Zero(t, s.BytesWritten)
	require.Empty(t, s.Error)
}
