package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/mxl"
)

var (
	descMXLSkipped = prometheus.NewDesc("switchframe_mxl_reader_skipped_grains_total",
		"Grains an MXL input skipped because its consumer was behind, by essence.", []string{"source", "essence"}, nil)
	descMXLResyncs = prometheus.NewDesc("switchframe_mxl_reader_resyncs_total",
		"Times an MXL input jumped to the head after the ring overwrote its read position, losing what lay between, by essence.",
		[]string{"source", "essence"}, nil)
	descMXLLag = prometheus.NewDesc("switchframe_mxl_reader_lag_grains",
		"Grains an MXL input's video read position trails the head of its flow.", []string{"source"}, nil)
)

// mxlReaderCollector exposes what each MXL input's readers have lost, read
// at scrape time from counters the readers keep.
type mxlReaderCollector struct {
	sources func() []*mxl.Source
}

// Describe implements prometheus.Collector.
func (c *mxlReaderCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descMXLSkipped
	ch <- descMXLResyncs
	ch <- descMXLLag
}

// Collect implements prometheus.Collector.
func (c *mxlReaderCollector) Collect(ch chan<- prometheus.Metric) {
	for _, src := range c.sources() {
		source := metrics.SourceLabel(src.FlowName())
		for essence, st := range src.ReaderStats() {
			ch <- prometheus.MustNewConstMetric(descMXLSkipped, prometheus.CounterValue, float64(st.SkippedGrains), source, essence)
			ch <- prometheus.MustNewConstMetric(descMXLResyncs, prometheus.CounterValue, float64(st.Resyncs), source, essence)
			if essence == "video" {
				ch <- prometheus.MustNewConstMetric(descMXLLag, prometheus.GaugeValue, float64(st.LagGrains), source)
			}
		}
	}
}
