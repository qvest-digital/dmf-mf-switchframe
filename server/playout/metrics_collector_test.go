package playout

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
	require.NotContains(t, f, "switchframe_playout_item_index", "an empty playlist has no first item")

	require.NoError(t, m.LoadPlaylist(1, testPlaylist(clipItem("clip-1"))))
	f = metricstest.Series(t, NewMetricsCollector(m, nil))
	require.Equal(t, map[string]float64{"1": 0}, f["switchframe_playout_item_index"])
}

// Completed pods are those on the board in a terminal state, and deleting
// one lowers the count: a gauge, which a counter's reset handling would
// misread as new completions.
func TestMetricsCollector_PodsCompletedIsAGauge(t *testing.T) {
	m := NewManager(ManagerConfig{AllowedLocalPrefixes: []string{"/"}})
	defer m.Close()
	pb, err := m.PodBoard(1)
	require.NoError(t, err)
	var ids []string
	for i := 0; i < 2; i++ {
		pod, err := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
		require.NoError(t, err)
		pb.mu.Lock()
		pb.pods[pod.ID].State = []PodState{PodRejoined, PodDumpedOut}[i]
		pb.mu.Unlock()
		ids = append(ids, pod.ID)
	}
	c := NewMetricsCollector(m, nil)
	require.Equal(t, float64(2), metricstest.Series(t, c)["switchframe_playout_pods_completed"]["1"])
	require.NoError(t, pb.DeletePod(ids[0]))
	require.Equal(t, float64(1), metricstest.Series(t, c)["switchframe_playout_pods_completed"]["1"])
	require.Equal(t, dto.MetricType_GAUGE, familyType(t, c, "switchframe_playout_pods_completed"))
}

// familyType is the type of the collector's family name.
func familyType(t *testing.T, c prometheus.Collector, name string) dto.MetricType {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == name {
			return f.GetType()
		}
	}
	t.Fatalf("no family %s", name)
	return 0
}

func TestMetricsCollector_Cache(t *testing.T) {
	m := NewManager(ManagerConfig{AllowedLocalPrefixes: []string{"/"}})
	defer m.Close()
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 20})
	require.NoError(t, err)

	f := metricstest.Series(t, NewMetricsCollector(m, cache))
	require.Equal(t, map[string]float64{"": 1 << 20}, f["switchframe_playout_cache_max_bytes"])
	require.Equal(t, map[string]float64{"ready": 0, "pending": 0, "fetching": 0, "error": 0}, f["switchframe_playout_cache_items"])

	// An asset queued for a fetch is not ready yet.
	for uri, state := range map[string]AssetState{"a": AssetPending, "b": AssetFetching, "c": AssetCached, "d": AssetError} {
		require.NoError(t, cache.Put(&CacheEntry{AssetURI: uri, LocalPath: filepath.Join(dir, uri), State: state, LastAccessed: time.Now()}))
	}
	f = metricstest.Series(t, NewMetricsCollector(m, cache))
	require.Equal(t, map[string]float64{"ready": 1, "pending": 1, "fetching": 1, "error": 1}, f["switchframe_playout_cache_items"])
}
