package audio

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	descLoudness = prometheus.NewDesc("switchframe_audio_loudness_lufs",
		"Program loudness per EBU R128, by window: momentary (400 ms), short_term (3 s) and integrated. Absent below -70 LUFS, the BS.1770 absolute gate, as in silence.",
		[]string{"window"}, nil)
	descProgramPeak = prometheus.NewDesc("switchframe_audio_program_peak_dbfs",
		"Program peak level of the most recent frame, per channel; -96 is silence.", []string{"channel"}, nil)
	descChannelPeak = prometheus.NewDesc("switchframe_audio_source_peak_dbfs",
		"Peak level of a source's most recent frame, per channel; -96 is silence.", []string{"source", "channel"}, nil)
	descChannelLevel = prometheus.NewDesc("switchframe_audio_source_level_db",
		"A source's fader level.", []string{"source"}, nil)
	descChannelMuted = prometheus.NewDesc("switchframe_audio_source_muted",
		"1 while a source is muted.", []string{"source"}, nil)
	descMixCycle = prometheus.NewDesc("switchframe_audio_mix_cycle_seconds",
		"Time the most recent mix cycle took.", nil, nil)
	descMaxFrameGap = prometheus.NewDesc("switchframe_audio_max_frame_gap_seconds",
		"Longest gap between program audio frames since the mixer started.", nil, nil)
	descDecodeErrors = prometheus.NewDesc("switchframe_audio_decode_errors_total",
		"Source audio frames the mixer failed to decode.", nil, nil)
)

// loudnessAbsoluteGate is the BS.1770 absolute gate, in LUFS.
const loudnessAbsoluteGate = -70

// MetricsCollector exposes the mixer's program loudness, levels and errors
// as Prometheus metrics, read at scrape time from what the mixer already
// keeps for its meters.
type MetricsCollector struct {
	m *Mixer
}

// NewMetricsCollector returns a collector over m, to register on the
// registry /metrics serves.
func NewMetricsCollector(m *Mixer) *MetricsCollector {
	return &MetricsCollector{m: m}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descLoudness, descProgramPeak, descChannelPeak, descChannelLevel, descChannelMuted,
		descMixCycle, descMaxFrameGap, descDecodeErrors,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	perf := c.m.PerfSample()
	for window, lufs := range map[string]float64{
		"momentary": perf.MomentaryLUFS, "short_term": perf.ShortTermLUFS, "integrated": perf.IntegratedLUFS,
	} {
		// Below the BS.1770 absolute gate there is no loudness to report:
		// the meter holds a sentinel for silence, which a graph would plot
		// as a value.
		if lufs >= loudnessAbsoluteGate {
			gauge(descLoudness, lufs, window)
		}
	}
	gauge(descMixCycle, float64(perf.MixCycleLastNs)/1e9)
	gauge(descMaxFrameGap, float64(perf.MaxInterFrameGapNs)/1e9)
	ch <- prometheus.MustNewConstMetric(descDecodeErrors, prometheus.CounterValue, float64(perf.DecodeErrors))

	peak := c.m.ProgramPeak()
	gauge(descProgramPeak, peak[0], "left")
	gauge(descProgramPeak, peak[1], "right")

	for source, st := range c.m.ChannelStates() {
		gauge(descChannelPeak, st.PeakL, source, "left")
		gauge(descChannelPeak, st.PeakR, source, "right")
		gauge(descChannelLevel, st.Level, source)
		muted := 0.0
		if st.Muted {
			muted = 1
		}
		gauge(descChannelMuted, muted, source)
	}
}
