package htmlgfx

import "github.com/prometheus/client_golang/prometheus"

var descFramesDelivered = prometheus.NewDesc("switchframe_htmlgfx_frames_delivered_total",
	"Frames the HTML5 graphics sidecar painted that reached the compositor on a graphics layer.",
	nil, nil)

// MetricsCollector exposes the HTML5 graphics manager as Prometheus metrics
// read at scrape time.
type MetricsCollector struct {
	m *Manager
}

// NewMetricsCollector returns a collector over m, to register on the
// registry /metrics serves.
func NewMetricsCollector(m *Manager) *MetricsCollector {
	return &MetricsCollector{m: m}
}

// Describe implements prometheus.Collector.
func (mc *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descFramesDelivered
}

// Collect implements prometheus.Collector.
func (mc *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(descFramesDelivered, prometheus.CounterValue, float64(mc.m.FramesDelivered()))
}
