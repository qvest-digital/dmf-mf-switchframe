package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetrics_RegistersWithoutPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}
}

func TestNewMetrics_AllMetricsGatherable(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	// Gathering should succeed with all metrics registered.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	// We expect at least the number of distinct metric names we registered.
	// CutsTotal, TransitionsTotal, IDRGateEventsTotal, IDRGateDurationMs,
	// FramesMixedTotal, EncodeErrorsTotal, PassthroughBypassTotal,
	// RingbufOverflowsTotal, SRTReconnectsTotal, RecordingBytesTotal,
	// SRTBytesTotal, SourceStatusChangesTotal = 12 metrics.
	// Note: CounterVec metrics only appear once they have been observed,
	// so we check for at least the scalar counters + histogram.
	wantNames := map[string]bool{
		"switchframe_cuts_total":                  false,
		"switchframe_idr_gate_events_total":       false,
		"switchframe_idr_gate_duration_ms":        false,
		"switchframe_mixer_frames_mixed_total":    false,
		"switchframe_mixer_encode_errors_total":   false,
		"switchframe_mixer_passthrough_bypass_total": false,
		"switchframe_output_ringbuf_overflows_total": false,
		"switchframe_output_srt_reconnects_total":   false,
		"switchframe_output_recording_bytes_total":   false,
		"switchframe_output_srt_bytes_total":         false,
	}

	for _, fam := range families {
		if _, ok := wantNames[fam.GetName()]; ok {
			wantNames[fam.GetName()] = true
		}
	}

	for name, found := range wantNames {
		if !found {
			t.Errorf("expected metric %q not found in gathered families", name)
		}
	}
}

func TestNewMetrics_DuplicateRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	// Registering the same metrics again on the same registry should panic.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration, got none")
		}
	}()
	_ = NewMetrics(reg)
}

func TestNewMetrics_CounterIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.CutsTotal.Inc()
	m.CutsTotal.Inc()
	m.CutsTotal.Inc()

	val := testutil.ToFloat64(m.CutsTotal)
	if val != 3 {
		t.Errorf("CutsTotal = %v, want 3", val)
	}
}

func TestNewMetrics_CounterVecLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.TransitionsTotal.WithLabelValues("mix").Inc()
	m.TransitionsTotal.WithLabelValues("dip").Inc()
	m.TransitionsTotal.WithLabelValues("mix").Inc()

	mixVal := testutil.ToFloat64(m.TransitionsTotal.WithLabelValues("mix"))
	dipVal := testutil.ToFloat64(m.TransitionsTotal.WithLabelValues("dip"))

	if mixVal != 2 {
		t.Errorf("TransitionsTotal{type=mix} = %v, want 2", mixVal)
	}
	if dipVal != 1 {
		t.Errorf("TransitionsTotal{type=dip} = %v, want 1", dipVal)
	}
}

func TestNewMetrics_SourceStatusChangesLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.SourceStatusChangesTotal.WithLabelValues("cam1", "healthy", "stale").Inc()
	m.SourceStatusChangesTotal.WithLabelValues("cam1", "stale", "no_signal").Inc()
	m.SourceStatusChangesTotal.WithLabelValues("cam2", "healthy", "offline").Inc()

	val := testutil.ToFloat64(m.SourceStatusChangesTotal.WithLabelValues("cam1", "healthy", "stale"))
	if val != 1 {
		t.Errorf("SourceStatusChanges{cam1,healthy,stale} = %v, want 1", val)
	}
}

func TestNewMetrics_IDRGateHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Observe some values and verify the histogram doesn't panic.
	m.IDRGateDurationMs.Observe(5)
	m.IDRGateDurationMs.Observe(25)
	m.IDRGateDurationMs.Observe(100)
	m.IDRGateDurationMs.Observe(500)

	// Gather and verify the histogram has the right bucket count.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	for _, fam := range families {
		if fam.GetName() == "switchframe_idr_gate_duration_ms" {
			metric := fam.GetMetric()
			if len(metric) == 0 {
				t.Fatal("no metric data for IDRGateDurationMs")
			}
			hist := metric[0].GetHistogram()
			if hist == nil {
				t.Fatal("expected histogram, got nil")
			}
			// We specified 8 bucket boundaries: {5, 10, 25, 50, 100, 250, 500, 1000}
			// Prometheus adds +Inf, so we get 8 explicit buckets.
			if got := len(hist.GetBucket()); got != 8 {
				t.Errorf("IDRGateDurationMs bucket count = %d, want 8", got)
			}
			if hist.GetSampleCount() != 4 {
				t.Errorf("IDRGateDurationMs sample count = %d, want 4", hist.GetSampleCount())
			}
			return
		}
	}
	t.Error("IDRGateDurationMs metric not found in gathered families")
}

func TestNewMetrics_SeparateRegistries(t *testing.T) {
	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()

	m1 := NewMetrics(reg1)
	m2 := NewMetrics(reg2)

	m1.CutsTotal.Inc()
	// m2 should be independent.
	val := testutil.ToFloat64(m2.CutsTotal)
	if val != 0 {
		t.Errorf("m2.CutsTotal = %v after m1.CutsTotal.Inc(), want 0", val)
	}
}
