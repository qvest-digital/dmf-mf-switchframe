package graphics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	descLayerOnAir = prometheus.NewDesc("switchframe_graphics_layer_on_air",
		"1 while the graphics layer is on program. Layer names and templates are left out: the client names both freely.",
		[]string{"layer"}, nil)
	descLayersOnAir = prometheus.NewDesc("switchframe_graphics_layers_on_air",
		"Graphics layers on program.", nil, nil)
)

// MetricsCollector exposes the graphics layers as Prometheus metrics read
// at scrape time.
type MetricsCollector struct {
	c *Compositor
}

// NewMetricsCollector returns a collector over c, to register on the
// registry /metrics serves.
func NewMetricsCollector(c *Compositor) *MetricsCollector {
	return &MetricsCollector{c: c}
}

// Describe implements prometheus.Collector.
func (mc *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descLayerOnAir
	ch <- descLayersOnAir
}

// Collect implements prometheus.Collector.
func (mc *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	onAir := 0
	for _, l := range mc.c.Status().Layers {
		v := 0.0
		if l.Active {
			v = 1
			onAir++
		}
		ch <- prometheus.MustNewConstMetric(descLayerOnAir, prometheus.GaugeValue, v, strconv.Itoa(l.ID))
	}
	ch <- prometheus.MustNewConstMetric(descLayersOnAir, prometheus.GaugeValue, float64(onAir))
}
