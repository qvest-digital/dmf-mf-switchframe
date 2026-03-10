package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewMetrics_RegistersWithoutPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	require.NotNil(t, m, "NewMetrics returned nil")
}

func TestNewMetrics_AllMetricsGatherable(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	// Gathering should succeed with all metrics registered.
	families, err := reg.Gather()
	require.NoError(t, err, "Gather failed")

	// We expect at least the number of distinct metric names we registered.
	// CutsTotal, TransitionsTotal, IDRGateEventsTotal, IDRGateDuration,
	// FramesMixedTotal, EncodeErrorsTotal, PassthroughBypassTotal,
	// RingbufOverflowsTotal, SRTReconnectsTotal, RecordingBytesTotal,
	// SRTBytesTotal, SourceStatusChangesTotal = 12 metrics.
	// Note: CounterVec metrics only appear once they have been observed,
	// so we check for at least the scalar counters + histogram.
	wantNames := map[string]bool{
		"switchframe_cuts_total":                     false,
		"switchframe_idr_gate_events_total":          false,
		"switchframe_idr_gate_duration_seconds":      false,
		"switchframe_mixer_frames_mixed_total":       false,
		"switchframe_mixer_encode_errors_total":      false,
		"switchframe_mixer_passthrough_bypass_total": false,
		"switchframe_output_ringbuf_overflows_total": false,
		"switchframe_output_srt_reconnects_total":    false,
		"switchframe_output_recording_bytes_total":   false,
		"switchframe_output_srt_bytes_total":         false,
	}

	for _, fam := range families {
		if _, ok := wantNames[fam.GetName()]; ok {
			wantNames[fam.GetName()] = true
		}
	}

	for name, found := range wantNames {
		require.True(t, found, "expected metric %q not found in gathered families", name)
	}
}

func TestNewMetrics_DuplicateRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	// Registering the same metrics again on the same registry should panic.
	require.Panics(t, func() {
		_ = NewMetrics(reg)
	}, "expected panic on duplicate registration")
}

func TestNewMetrics_CounterIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.CutsTotal.Inc()
	m.CutsTotal.Inc()
	m.CutsTotal.Inc()

	val := testutil.ToFloat64(m.CutsTotal)
	require.Equal(t, float64(3), val, "CutsTotal")
}

func TestNewMetrics_CounterVecLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.TransitionsTotal.WithLabelValues("mix").Inc()
	m.TransitionsTotal.WithLabelValues("dip").Inc()
	m.TransitionsTotal.WithLabelValues("mix").Inc()

	mixVal := testutil.ToFloat64(m.TransitionsTotal.WithLabelValues("mix"))
	dipVal := testutil.ToFloat64(m.TransitionsTotal.WithLabelValues("dip"))

	require.Equal(t, float64(2), mixVal, "TransitionsTotal{type=mix}")
	require.Equal(t, float64(1), dipVal, "TransitionsTotal{type=dip}")
}

func TestNewMetrics_SourceStatusChangesLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.SourceStatusChangesTotal.WithLabelValues("cam1", "healthy", "stale").Inc()
	m.SourceStatusChangesTotal.WithLabelValues("cam1", "stale", "no_signal").Inc()
	m.SourceStatusChangesTotal.WithLabelValues("cam2", "healthy", "offline").Inc()

	val := testutil.ToFloat64(m.SourceStatusChangesTotal.WithLabelValues("cam1", "healthy", "stale"))
	require.Equal(t, float64(1), val, "SourceStatusChanges{cam1,healthy,stale}")
}

func TestNewMetrics_IDRGateHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Observe some values in seconds and verify the histogram doesn't panic.
	m.IDRGateDuration.Observe(0.005)
	m.IDRGateDuration.Observe(0.025)
	m.IDRGateDuration.Observe(0.1)
	m.IDRGateDuration.Observe(0.5)

	// Gather and verify the histogram has the right bucket count.
	families, err := reg.Gather()
	require.NoError(t, err, "Gather failed")

	for _, fam := range families {
		if fam.GetName() == "switchframe_idr_gate_duration_seconds" {
			metric := fam.GetMetric()
			require.NotEmpty(t, metric, "no metric data for IDRGateDuration")
			hist := metric[0].GetHistogram()
			require.NotNil(t, hist, "expected histogram, got nil")
			// We specified 8 bucket boundaries: {5, 10, 25, 50, 100, 250, 500, 1000}
			// Prometheus adds +Inf, so we get 8 explicit buckets.
			require.Equal(t, 8, len(hist.GetBucket()), "IDRGateDuration bucket count")
			require.Equal(t, uint64(4), hist.GetSampleCount(), "IDRGateDuration sample count")
			return
		}
	}
	require.Fail(t, "IDRGateDuration metric not found in gathered families")
}

func TestMetrics_NodeProcessDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	require.NotNil(t, m.NodeProcessDuration)

	// Observe a value and verify it doesn't panic.
	m.NodeProcessDuration.WithLabelValues("h264-encode").Observe(0.001)

	// Verify the metric is registered and gatherable.
	families, err := reg.Gather()
	require.NoError(t, err)

	var found bool
	for _, f := range families {
		if f.GetName() == "switchframe_pipeline_node_duration_seconds" {
			found = true
		}
	}
	require.True(t, found, "NodeProcessDuration should be registered")
}

func TestNewMetrics_SeparateRegistries(t *testing.T) {
	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()

	m1 := NewMetrics(reg1)
	m2 := NewMetrics(reg2)

	m1.CutsTotal.Inc()
	// m2 should be independent.
	val := testutil.ToFloat64(m2.CutsTotal)
	require.Equal(t, float64(0), val, "m2.CutsTotal after m1.CutsTotal.Inc()")
}
