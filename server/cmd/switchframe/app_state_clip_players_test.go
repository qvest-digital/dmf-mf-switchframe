package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clip"
)

// The browser reads clip players from the state broadcast, not from the clip
// manager. A field the conversion drops never reaches it: the loading state
// was dropped this way, so a clip one engine was still loading read as
// diverged from the peer that had finished, raising the resync banner. Every
// field the manager reports must reach the broadcast under the same name.
func TestClipPlayerInfosCarryEveryPlayerField(t *testing.T) {
	states := []clip.ClipPlayerState{
		{
			ID:              1,
			ClipID:          "0123456789abcdef0123456789abcdef",
			ClipName:        "bunny",
			State:           clip.StatePlaying,
			Speed:           0.5,
			Position:        0.25,
			Loop:            true,
			LoadingClipID:   "fedcba9876543210fedcba9876543210",
			LoadingClipName: "other",
		},
		{ID: 2, State: clip.StateEmpty},
	}

	want, err := json.Marshal(states)
	require.NoError(t, err)
	got, err := json.Marshal(clipPlayerInfos(states))
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}
