package switcher

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/metrics"
)

// sourceHealthStatuses are the values switchframe_source_health is
// one-hot over, so a source's series exist whatever its state.
var sourceHealthStatuses = []SourceHealthStatus{SourceHealthy, SourceStale, SourceNoSignal, SourceOffline}

var (
	descProgramSource = prometheus.NewDesc("switchframe_program_source_info",
		"1 for the source on program.", []string{"source"}, nil)
	descPreviewSource = prometheus.NewDesc("switchframe_preview_source_info",
		"1 for the source on preview.", []string{"source"}, nil)
	descTransitionActive = prometheus.NewDesc("switchframe_transition_active",
		"1 while a transition runs.", nil, nil)
	descFTBActive = prometheus.NewDesc("switchframe_ftb_active",
		"1 while the program is faded to black.", nil, nil)
	descProgramFrames = prometheus.NewDesc("switchframe_program_frames_total",
		"Program frames sent to the outputs.", nil, nil)
	descProgramRepeated = prometheus.NewDesc("switchframe_program_repeated_frames_total",
		"Program frames re-sent because no new frame was ready in its slot.", nil, nil)
	descProgramDropped = prometheus.NewDesc("switchframe_program_dropped_frames_total",
		"Program frames dropped, by stage: proc (the processing queue was full), encode (the encoder was behind), stale (from a superseded program).",
		[]string{"stage"}, nil)
	descDeadlineViolations = prometheus.NewDesc("switchframe_pipeline_deadline_violations_total",
		"Program frames whose processing took longer than the frame budget.", nil, nil)
	descFrameBudget = prometheus.NewDesc("switchframe_pipeline_frame_budget_seconds",
		"Time the pipeline has per frame at the program rate.", nil, nil)
	descQueueDepth = prometheus.NewDesc("switchframe_pipeline_queue_depth",
		"Frames waiting for the processing pipeline.", nil, nil)
	descStageLatency = prometheus.NewDesc("switchframe_pipeline_stage_latency_seconds",
		"Latency of the most recent program frame in each stage, and end to end (stage total).",
		[]string{"stage"}, nil)
	descGPUFallback = prometheus.NewDesc("switchframe_gpu_fallback_frames_total",
		"Frames the GPU pipeline handed back to the CPU pipeline.", nil, nil)
	descLastProgramFrameAge = prometheus.NewDesc("switchframe_program_last_frame_age_seconds",
		"Time since the last program frame was sent.", nil, nil)

	descSourceInfo = prometheus.NewDesc("switchframe_source_info",
		"1 per source, with its type and display label.", []string{"source", "type", "label"}, nil)
	descSourceHealth = prometheus.NewDesc("switchframe_source_health",
		"1 for the source's health status, 0 for the others.", []string{"source", "status"}, nil)
	descSourceLastFrameAge = prometheus.NewDesc("switchframe_source_last_frame_age_seconds",
		"Time since the source last delivered a video frame.", []string{"source"}, nil)
	descSourceFrames = prometheus.NewDesc("switchframe_source_raw_frames_total",
		"Uncompressed video frames the source delivered, such as an MXL input's. A compressed source counts none here; its rate is switchframe_source_decode_fps.", []string{"source"}, nil)
	descSourceDecodeFPS = prometheus.NewDesc("switchframe_source_decode_fps",
		"Frames per second the decoder of a compressed source produces, averaged by the decoder.", []string{"source"}, nil)
	descSourceDecodeDrops = prometheus.NewDesc("switchframe_source_decode_drops_total",
		"Frames the source's decoder dropped.", []string{"source"}, nil)
	descSourceDecodeLatency = prometheus.NewDesc("switchframe_source_decode_latency_seconds",
		"Decode time of the source's most recent frame.", []string{"source"}, nil)
)

// MetricsCollector exposes the switcher's program and per-source state as
// Prometheus metrics, read at scrape time from what the switcher already
// keeps: no metric here costs the frame path anything.
type MetricsCollector struct {
	s *Switcher
}

// NewMetricsCollector returns a collector over s, to register on the
// registry /metrics serves.
func NewMetricsCollector(s *Switcher) *MetricsCollector {
	return &MetricsCollector{s: s}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descProgramSource, descPreviewSource, descTransitionActive, descFTBActive,
		descProgramFrames, descProgramRepeated, descProgramDropped, descDeadlineViolations,
		descFrameBudget, descQueueDepth, descStageLatency, descGPUFallback, descLastProgramFrameAge,
		descSourceInfo, descSourceHealth, descSourceLastFrameAge, descSourceFrames,
		descSourceDecodeFPS, descSourceDecodeDrops, descSourceDecodeLatency,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.s
	state := s.State()
	perf := s.PerfSample()

	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v), labels...)
	}
	flag := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	seconds := func(ns int64) float64 { return float64(ns) / 1e9 }

	if state.ProgramSource != "" {
		gauge(descProgramSource, 1, metrics.SourceLabel(state.ProgramSource))
	}
	if state.PreviewSource != "" {
		gauge(descPreviewSource, 1, metrics.SourceLabel(state.PreviewSource))
	}
	gauge(descTransitionActive, flag(state.InTransition))
	gauge(descFTBActive, flag(state.FTBActive))

	counter(descProgramFrames, s.videoBroadcastCount.Load())
	counter(descProgramRepeated, s.programRepeats.Load())
	counter(descProgramDropped, s.overloadDrops.Load(), "proc")
	counter(descProgramDropped, s.pipeEncodeDrop.Load(), "encode")
	counter(descDeadlineViolations, s.deadlineViolations.Load())
	counter(descGPUFallback, s.gpuFallbackFrames.Load())
	gauge(descFrameBudget, seconds(perf.FrameBudgetNs))
	gauge(descQueueDepth, float64(perf.QueueLen))
	gauge(descStageLatency, seconds(perf.DecodeQueueNs), "decode_queue")
	gauge(descStageLatency, seconds(perf.DecodeNs), "decode")
	gauge(descStageLatency, seconds(perf.SyncWaitNs), "sync_wait")
	gauge(descStageLatency, seconds(perf.ProcQueueNs), "proc_queue")
	gauge(descStageLatency, seconds(perf.E2ELastNs), "total")
	if last := s.lastBroadcastNano.Load(); last != 0 {
		gauge(descLastProgramFrameAge, time.Since(time.Unix(0, last)).Seconds())
	}

	for key, info := range state.Sources {
		source := metrics.SourceLabel(key)
		// A listener input is labelled after its stream id until an
		// operator names it, so its display label goes the same way.
		label := info.Label
		if source != key && label != "" && strings.Contains(key, label) {
			label = source
		}
		gauge(descSourceInfo, 1, source, info.Type, label)
		status := SourceHealthStatus(info.Status)
		for _, st := range sourceHealthStatuses {
			gauge(descSourceHealth, flag(st == status), source, string(st))
		}
		if ms := s.health.lastFrameAgoMs(key); ms >= 0 {
			gauge(descSourceLastFrameAge, float64(ms)/1e3, source)
		}
	}
	for key, src := range perf.Sources {
		source := metrics.SourceLabel(key)
		counter(descSourceFrames, src.RawFrameCount, source)
		gauge(descSourceDecodeFPS, src.AvgFPS, source)
		counter(descSourceDecodeDrops, src.DecodeDrops, source)
		gauge(descSourceDecodeLatency, seconds(src.DecodeLastNs), source)
	}
}
