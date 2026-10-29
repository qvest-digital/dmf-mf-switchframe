package clip

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/metricstest"
)

// Every player reports a one-hot state, and position only once a clip is
// in it. Clip names stay out: they are operator text.
func TestMetricsCollector_Players(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	f := metricstest.Series(t, NewMetricsCollector(mgr))
	state := f["switchframe_clip_player_state"]
	require.Equal(t, float64(1), state["1/loaded"])
	require.Equal(t, float64(0), state["1/empty"])
	require.Equal(t, float64(1), state["2/empty"])
	require.Len(t, state, MaxPlayers*len(playerStates))
	require.Contains(t, f["switchframe_clip_player_position_ratio"], "1")
	require.NotContains(t, f["switchframe_clip_player_position_ratio"], "2")
	require.NotContains(t, f, "switchframe_clip_player_speed", "nothing plays")
}
