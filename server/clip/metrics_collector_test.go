package clip

import (
	"testing"
	"time"

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

// A playing clip reports its speed; one that stops playing drops it.
func TestMetricsCollector_PlayingSpeed(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 0.25, true)) // slow + loop: stays playing

	col := NewMetricsCollector(mgr)
	require.Eventually(t, func() bool {
		return metricstest.Series(t, col)["switchframe_clip_player_state"]["1/playing"] == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, map[string]float64{"1": 0.25}, metricstest.Series(t, col)["switchframe_clip_player_speed"])

	require.NoError(t, mgr.Pause(1))
	require.Eventually(t, func() bool {
		_, ok := metricstest.Series(t, col)["switchframe_clip_player_speed"]
		return !ok
	}, 2*time.Second, 10*time.Millisecond)
}
