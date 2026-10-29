package playout

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/metricstest"
)

// Every channel reports a one-hot state and its playlist position; the
// remaining time only while an item is on air.
func TestMetricsCollector_Channels(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(3), AllowedLocalPrefixes: []string{"/"}})
	defer m.Close()

	f := metricstest.Series(t, NewMetricsCollector(m, nil))
	state := f["switchframe_playout_channel_state"]
	require.Len(t, state, MaxChannels*len(channelStates))
	require.Equal(t, float64(1), state["1/stopped"])
	require.Equal(t, float64(0), state["1/on_air"])
	require.Equal(t, float64(1), state["2/stopped"])
	require.Equal(t, map[string]float64{"1": 0, "2": 0}, f["switchframe_playout_items"])
	require.NotContains(t, f, "switchframe_playout_item_remaining_seconds")
	require.NotContains(t, f, "switchframe_playout_cache_used_bytes", "no cache, no cache series")
}

func TestMetricsCollector_Cache(t *testing.T) {
	m := NewManager(ManagerConfig{AllowedLocalPrefixes: []string{"/"}})
	defer m.Close()
	cache, err := NewCache(CacheConfig{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)

	f := metricstest.Series(t, NewMetricsCollector(m, cache))
	require.Equal(t, map[string]float64{"": 1 << 20}, f["switchframe_playout_cache_max_bytes"])
	require.Equal(t, map[string]float64{"ready": 0, "fetching": 0, "error": 0}, f["switchframe_playout_cache_items"])
}
