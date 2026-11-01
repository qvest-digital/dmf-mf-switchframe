package switcher

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/metrics"
)

// gather collects c into a fresh registry and returns its families by name.
func gather(t *testing.T, c prometheus.Collector) map[string]*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))
	families, err := reg.Gather()
	require.NoError(t, err)
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		byName[f.GetName()] = f
	}
	return byName
}

// values maps a family's series, keyed by their label values joined with
// "/", to their values.
func values(f *dto.MetricFamily) map[string]float64 {
	out := map[string]float64{}
	if f == nil {
		return out
	}
	for _, m := range f.GetMetric() {
		key := ""
		for i, l := range m.GetLabel() {
			if i > 0 {
				key += "/"
			}
			key += l.GetValue()
		}
		switch {
		case m.Gauge != nil:
			out[key] = m.GetGauge().GetValue()
		case m.Counter != nil:
			out[key] = m.GetCounter().GetValue()
		}
	}
	return out
}

// Program and preview are one series each, carrying the source in a label,
// so a state timeline shows what was on air when.
func TestMetricsCollector_ProgramAndPreview(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	f := gather(t, NewMetricsCollector(sw))
	require.Equal(t, map[string]float64{"cam1": 1}, values(f["switchframe_program_source_info"]))
	require.Equal(t, map[string]float64{"cam2": 1}, values(f["switchframe_preview_source_info"]))
	require.Equal(t, map[string]float64{"": 0}, values(f["switchframe_transition_active"]))
	require.Equal(t, map[string]float64{"": 0}, values(f["switchframe_ftb_active"]))
}

// Each source reports every health status, 1 on its own and 0 on the
// rest, so a source changing state never leaves a stale series behind.
func TestMetricsCollector_SourceHealthIsOneHot(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	sw.health.recordFrame("cam1", time.Now())
	sw.health.checkForChanges()

	health := values(gather(t, NewMetricsCollector(sw))["switchframe_source_health"])
	for key := range health {
		if !strings.HasPrefix(key, "cam") {
			delete(health, key) // the built-in sources
		}
	}
	require.Equal(t, map[string]float64{
		"cam1/healthy": 1, "cam1/stale": 0, "cam1/no_signal": 0, "cam1/offline": 0,
		"cam2/healthy": 0, "cam2/stale": 0, "cam2/no_signal": 0, "cam2/offline": 1,
	}, health)
}

// The counters are the switcher's own, read at scrape time.
func TestMetricsCollector_ProgramCounters(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	sw.videoBroadcastCount.Store(500)
	sw.programRepeats.Store(7)
	sw.overloadDrops.Store(1)
	sw.pipeEncodeDrop.Store(2)
	sw.deadlineViolations.Store(4)
	sw.frameBudgetNs.Store(int64(20 * time.Millisecond))

	f := gather(t, NewMetricsCollector(sw))
	require.Equal(t, map[string]float64{"": 500}, values(f["switchframe_program_frames_total"]))
	require.Equal(t, map[string]float64{"": 7}, values(f["switchframe_program_repeated_frames_total"]))
	require.Equal(t, map[string]float64{"proc": 1, "encode": 2},
		values(f["switchframe_program_dropped_frames_total"]))
	require.Equal(t, map[string]float64{"": 4}, values(f["switchframe_pipeline_deadline_violations_total"]))
	require.Equal(t, map[string]float64{"": 0.02}, values(f["switchframe_pipeline_frame_budget_seconds"]))
}

// A source's last frame age grows while it delivers nothing; a source that
// never delivered has no age at all rather than a made-up one.
func TestMetricsCollector_SourceLastFrameAge(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	sw.health.recordFrame("cam1", time.Now().Add(-3*time.Second))

	age := values(gather(t, NewMetricsCollector(sw))["switchframe_source_last_frame_age_seconds"])
	require.InDelta(t, 3, age["cam1"], 0.5)
	require.NotContains(t, age, "cam2")
}

// The gap between program frames lands in the histogram, so a stall is
// visible as a count above two frame periods even between scrapes.
func TestSwitcher_ProgramFrameIntervalHistogram(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	pm := metrics.NewMetrics(prometheus.NewRegistry())
	sw.SetMetrics(pm)

	sw.trackBroadcastInterval() // the first frame has no gap
	sw.lastBroadcastNano.Store(time.Now().Add(-300 * time.Millisecond).UnixNano())
	sw.trackBroadcastInterval()

	var m dto.Metric
	require.NoError(t, pm.ProgramFrameInterval.Write(&m))
	require.Equal(t, uint64(1), m.GetHistogram().GetSampleCount())
	require.InDelta(t, 0.3, m.GetHistogram().GetSampleSum(), 0.05)
}

// The collector passes the registry's own consistency checks.
func TestMetricsCollector_Lint(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	sw.RegisterSource("cam1", newTestRelay())
	problems, err := testutil.CollectAndLint(NewMetricsCollector(sw))
	require.NoError(t, err)
	require.Empty(t, problems)
}

// An SRT listener input is keyed by its publisher's stream id; no series
// carries it, in any label.
func TestMetricsCollector_HidesSRTStreamIDs(t *testing.T) {
	sw := NewTestSwitcher(t, distribution.NewRelay())
	key := "srt:secret-stream-key"
	sw.RegisterSource(key, newTestRelay())
	require.NoError(t, sw.SetLabel(context.Background(), key, "secret-stream-key"))
	sw.health.recordFrame(key, time.Now())
	require.NoError(t, sw.Cut(context.Background(), key))

	for name, f := range gather(t, NewMetricsCollector(sw)) {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				require.NotContains(t, l.GetValue(), "secret", "%s{%s}", name, l.GetName())
			}
		}
	}
	require.Equal(t, map[string]float64{metrics.SourceLabel(key): 1},
		values(gather(t, NewMetricsCollector(sw))["switchframe_program_source_info"]))
}
