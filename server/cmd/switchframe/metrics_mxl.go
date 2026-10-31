package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/mxl"
)

var (
	descMXLSkipped = prometheus.NewDesc("switchframe_mxl_reader_skipped_grains_total",
		"Video grains an MXL input skipped because its consumer was behind.", []string{"source"}, nil)
	descMXLResyncs = prometheus.NewDesc("switchframe_mxl_reader_resyncs_total",
		"Times an MXL input jumped to the head after the ring overwrote its read position, losing what lay between, by essence.",
		[]string{"source", "essence"}, nil)
	descMXLLag = prometheus.NewDesc("switchframe_mxl_reader_lag_grains",
		"Grains an MXL input's video read position trails the head of its flow.", []string{"source"}, nil)

	descMXLAudioOverlaps = prometheus.NewDesc("switchframe_mxl_writer_audio_overlaps_total",
		"Program audio batches the MXL output dropped because they overlapped audio already written.", nil, nil)
	descMXLVideoIndexDrops = prometheus.NewDesc("switchframe_mxl_writer_video_index_drops_total",
		"Program video frames the MXL output dropped because the grain index of their PTS was not after the last grain written.", nil, nil)
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
			ch <- prometheus.MustNewConstMetric(descMXLResyncs, prometheus.CounterValue, float64(st.Resyncs), source, essence)
			// Only the video reader skips ahead and tracks its lag.
			if essence == "video" {
				ch <- prometheus.MustNewConstMetric(descMXLSkipped, prometheus.CounterValue, float64(st.SkippedGrains), source)
				ch <- prometheus.MustNewConstMetric(descMXLLag, prometheus.GaugeValue, float64(st.LagGrains), source)
			}
		}
	}
}

// mxlWriterCollector exposes what the program MXL output dropped for
// landing on media time already written, read at scrape time from counters
// its writer keeps. It exposes nothing without an MXL output.
type mxlWriterCollector struct {
	writer *mxl.Writer
}

// Describe implements prometheus.Collector.
func (c *mxlWriterCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descMXLAudioOverlaps
	ch <- descMXLVideoIndexDrops
}

// Collect implements prometheus.Collector.
func (c *mxlWriterCollector) Collect(ch chan<- prometheus.Metric) {
	if c.writer == nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(descMXLAudioOverlaps, prometheus.CounterValue, float64(c.writer.AudioOverlaps()))
	ch <- prometheus.MustNewConstMetric(descMXLVideoIndexDrops, prometheus.CounterValue, float64(c.writer.VideoIndexDrops()))
}
