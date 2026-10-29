package output

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func gatherOutput(t *testing.T, c prometheus.Collector) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))
	families, err := reg.Gather()
	require.NoError(t, err)
	out := map[string]map[string]float64{}
	for _, f := range families {
		series := map[string]float64{}
		for _, m := range f.GetMetric() {
			key := ""
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += "/"
				}
				key += l.GetValue()
			}
			series[key] = seriesValue(m)
		}
		out[f.GetName()] = series
	}
	return out
}

func seriesValue(m *dto.Metric) float64 {
	if m.Counter != nil {
		return m.GetCounter().GetValue()
	}
	return m.GetGauge().GetValue()
}

// A destination reports its kind and name, never its address or URL, and
// a one-hot state, so a stopped output does not leave an active series.
func TestMetricsCollector_Destinations(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	id, err := m.AddDestination(DestinationConfig{
		Type: "rtmp", URL: "rtmp://ingest.example.com/live/secret-key", Name: "stream",
	})
	require.NoError(t, err)

	f := gatherOutput(t, NewMetricsCollector(m))
	require.Equal(t, map[string]float64{"rtmp/stream/" + id: 1}, f["switchframe_output_info"])
	require.Equal(t, map[string]float64{
		id + "/starting": 0, id + "/active": 0, id + "/reconnecting": 0, id + "/stopped": 1, id + "/error": 0,
	}, f["switchframe_output_state"])
	require.Equal(t, map[string]float64{id: 0}, f["switchframe_output_bytes_total"])
	require.NotContains(t, f, "switchframe_output_clients", "an RTMP output has no clients")

	for name, series := range f {
		for labels := range series {
			require.NotContains(t, labels, "secret-key", "%s exports the stream key", name)
			require.NotContains(t, labels, "ingest.example.com", "%s exports the address", name)
		}
	}
}

// The recorder's series follow it: active and growing while it runs, its
// size and duration gone once it stops.
func TestMetricsCollector_Recording(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	c := NewMetricsCollector(m)

	f := gatherOutput(t, c)
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_recording_active"])
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_recording_failed"])
	require.NotContains(t, f, "switchframe_recording_current_bytes")

	require.NoError(t, m.StartRecording(RecorderConfig{Dir: t.TempDir()}))
	m.DirectWriteVideo(makeKeyframe(90000))
	require.Eventually(t, func() bool {
		return gatherOutput(t, c)["switchframe_recording_current_bytes"][""] > 0
	}, 2*time.Second, 20*time.Millisecond)
	require.Equal(t, map[string]float64{"": 1}, gatherOutput(t, c)["switchframe_recording_active"])

	require.NoError(t, m.StopRecording())
	f = gatherOutput(t, c)
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_recording_active"])
	require.NotContains(t, f, "switchframe_recording_current_bytes")
}

func TestMetricsCollector_Lint(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	_, err := m.AddDestination(DestinationConfig{Type: "srt-listener", Port: 9000, Name: "contrib"})
	require.NoError(t, err)
	require.NoError(t, m.StartRecording(RecorderConfig{Dir: t.TempDir()}))
	problems, err := testutil.CollectAndLint(NewMetricsCollector(m))
	require.NoError(t, err)
	require.Empty(t, problems)
}
