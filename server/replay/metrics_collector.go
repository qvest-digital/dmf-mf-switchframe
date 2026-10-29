package replay

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/metrics"
)

// replayStates are the values switchframe_replay_state is one-hot over.
var replayStates = []PlayerState{PlayerIdle, PlayerLoading, PlayerPlaying, PlayerPaused}

var (
	descReplayState = prometheus.NewDesc("switchframe_replay_state",
		"1 for the replay player's state, 0 for the others.", []string{"state"}, nil)
	descBufferSeconds = prometheus.NewDesc("switchframe_replay_buffer_seconds",
		"Media a source's replay buffer holds.", []string{"source"}, nil)
	descBufferBytes = prometheus.NewDesc("switchframe_replay_buffer_bytes",
		"Bytes a source's replay buffer holds.", []string{"source"}, nil)
)

// MetricsCollector exposes the replay player and its per-source buffers
// as Prometheus metrics read at scrape time.
type MetricsCollector struct {
	m *Manager
}

// NewMetricsCollector returns a collector over m, to register on the
// registry /metrics serves.
func NewMetricsCollector(m *Manager) *MetricsCollector {
	return &MetricsCollector{m: m}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descReplayState
	ch <- descBufferSeconds
	ch <- descBufferBytes
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	st := c.m.Status()
	for _, s := range replayStates {
		v := 0.0
		if s == st.State {
			v = 1
		}
		gauge(descReplayState, v, string(s))
	}
	for _, b := range st.Buffers {
		source := metrics.SourceLabel(b.Source)
		gauge(descBufferSeconds, b.DurationSecs, source)
		gauge(descBufferBytes, float64(b.BytesUsed), source)
	}
}
