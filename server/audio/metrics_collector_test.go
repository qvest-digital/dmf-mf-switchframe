package audio

import (
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/metrics"
)

func collectorValues(t *testing.T, c prometheus.Collector, name string) map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))
	families, err := reg.Gather()
	require.NoError(t, err)
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			key := ""
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += "/"
				}
				key += l.GetValue()
			}
			out[key] = metricValue(m)
		}
	}
	return out
}

func metricValue(m *dto.Metric) float64 {
	if m.Counter != nil {
		return m.GetCounter().GetValue()
	}
	return m.GetGauge().GetValue()
}

// Each strip reports its fader, mute and both channels' peaks, so an
// operator sees which source is muted or silent without opening the UI.
func TestMetricsCollector_Strips(t *testing.T) {
	m := newProduceTestMixer(t)
	m.AddChannel("cam1")
	m.AddChannel("cam2")
	require.NoError(t, m.SetLevel("cam1", -6))
	require.NoError(t, m.SetMuted("cam2", true))
	c := NewMetricsCollector(m)

	require.Equal(t, map[string]float64{"cam1": -6, "cam2": 0},
		collectorValues(t, c, "switchframe_audio_source_level_db"))
	require.Equal(t, map[string]float64{"cam1": 0, "cam2": 1},
		collectorValues(t, c, "switchframe_audio_source_muted"))
	peaks := collectorValues(t, c, "switchframe_audio_source_peak_dbfs")
	require.Equal(t, map[string]float64{"left/cam1": -96, "right/cam1": -96, "left/cam2": -96, "right/cam2": -96}, peaks,
		"no audio yet reads as silence")
}

// Program loudness carries all three R128 windows and the program peak
// both channels.
func TestMetricsCollector_Program(t *testing.T) {
	m := newProduceTestMixer(t)
	c := NewMetricsCollector(m)
	require.Empty(t, collectorValues(t, c, "switchframe_audio_loudness_lufs"),
		"silence has no loudness, not a sentinel")
	m.loudness.momentaryLUFSBits.Store(math.Float64bits(-23))
	m.loudness.shortTermLUFSBits.Store(math.Float64bits(-24.5))
	m.loudness.integratedLUFSBits.Store(math.Float64bits(-80))
	require.Equal(t, map[string]float64{"momentary": -23, "short_term": -24.5},
		collectorValues(t, c, "switchframe_audio_loudness_lufs"), "below the absolute gate is not reported")
	require.ElementsMatch(t, []string{"left", "right"}, keys(collectorValues(t, c, "switchframe_audio_program_peak_dbfs")))

	m.decodeErrors.Store(3)
	require.Equal(t, map[string]float64{"": 3}, collectorValues(t, c, "switchframe_audio_decode_errors_total"))
}

// The frame gap is the longest time between two ProduceSamples calls, the
// periods of program audio.
func TestMetricsCollector_MaxFrameGap(t *testing.T) {
	m := newProduceTestMixer(t)
	c := NewMetricsCollector(m)
	tl := newTimeline()
	_ = tl.produce(m, 1024)
	require.Equal(t, map[string]float64{"": 0}, collectorValues(t, c, "switchframe_audio_max_frame_gap_seconds"),
		"one period has no gap")
	time.Sleep(5 * time.Millisecond)
	_ = tl.produce(m, 1024)
	gap := collectorValues(t, c, "switchframe_audio_max_frame_gap_seconds")[""]
	require.GreaterOrEqual(t, gap, 0.005)

	m.ResetMaxInterFrameGap()
	require.Equal(t, map[string]float64{"": 0}, collectorValues(t, c, "switchframe_audio_max_frame_gap_seconds"))
}

// A source read more than a period off the program read point counts one
// realign per stretch it stays off.
func TestMetricsCollector_Realigns(t *testing.T) {
	m := mediaMixer(t)
	c := NewMetricsCollector(m)
	label := metrics.SourceLabel("mxl:a")
	tl := newTimeline()
	tl.push(m, "mxl:a", constPCM(1024, 0.25), 0)
	_ = tl.produce(m, 1024)
	require.Equal(t, map[string]float64{label: 0}, collectorValues(t, c, "switchframe_audio_source_realigns_total"),
		"audio on the read point is not realigned")

	// Audio stamped four periods ahead of the read point.
	tl.push(m, "mxl:a", constPCM(1024, 0.25), 5*1024)
	_ = tl.produce(m, 1024)
	_ = tl.produce(m, 1024)
	require.Equal(t, map[string]float64{label: 1}, collectorValues(t, c, "switchframe_audio_source_realigns_total"))
	require.Equal(t, int64(1), m.DebugSnapshot()["channels"].(map[string]any)["mxl:a"].(map[string]any)["realigns"])
}

func TestMetricsCollector_Lint(t *testing.T) {
	m := newProduceTestMixer(t)
	m.AddChannel("cam1")
	problems, err := testutil.CollectAndLint(NewMetricsCollector(m))
	require.NoError(t, err)
	require.Empty(t, problems)
}

func keys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestMetricsCollector_HidesSRTStreamIDs(t *testing.T) {
	m := newProduceTestMixer(t)
	m.AddChannel("srt:secret-stream-key")
	levels := collectorValues(t, NewMetricsCollector(m), "switchframe_audio_source_level_db")
	require.Equal(t, map[string]float64{metrics.SourceLabel("srt:secret-stream-key"): 0}, levels)
}
