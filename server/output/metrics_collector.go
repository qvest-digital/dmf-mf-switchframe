package output

import (
	"github.com/prometheus/client_golang/prometheus"
)

// outputStates are the values switchframe_output_state is one-hot over.
var outputStates = []AdapterState{StateStarting, StateActive, StateReconnecting, StateStopped, StateError}

var (
	descOutputInfo = prometheus.NewDesc("switchframe_output_info",
		"1 per output, with its kind and display name. The legacy single SRT output is output \"srt\". No address, URL or stream key is exported.",
		[]string{"output", "kind", "name"}, nil)
	descOutputState = prometheus.NewDesc("switchframe_output_state",
		"1 for the output's state, 0 for the others.", []string{"output", "state"}, nil)
	descOutputBytes = prometheus.NewDesc("switchframe_output_bytes_total",
		"Bytes the output sent since it last started.", []string{"output"}, nil)
	descOutputDropped = prometheus.NewDesc("switchframe_output_dropped_packets_total",
		"TS packets the output dropped because it could not keep up.", []string{"output"}, nil)
	descOutputOverflows = prometheus.NewDesc("switchframe_output_overflows_total",
		"Times an SRT caller's reconnect buffer overflowed, losing what it held.", []string{"output"}, nil)
	descOutputClients = prometheus.NewDesc("switchframe_output_clients",
		"Clients connected to an SRT listener output.", []string{"output"}, nil)

	descRecordingActive = prometheus.NewDesc("switchframe_recording_active",
		"1 while recording.", nil, nil)
	descRecordingFailed = prometheus.NewDesc("switchframe_recording_failed",
		"1 while the current or last recording is in error.", nil, nil)
	descRecordingBytes = prometheus.NewDesc("switchframe_recording_current_bytes",
		"Bytes the current recording has written.", nil, nil)
	descRecordingDuration = prometheus.NewDesc("switchframe_recording_current_duration_seconds",
		"How long the current recording has run.", nil, nil)
	descRecordingDropped = prometheus.NewDesc("switchframe_recording_dropped_packets_total",
		"TS packets the current recording dropped because the disk could not keep up.", nil, nil)
)

// MetricsCollector exposes every output's state and throughput, and the
// recorder's, as Prometheus metrics read at scrape time.
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
	for _, d := range []*prometheus.Desc{
		descOutputInfo, descOutputState, descOutputBytes, descOutputDropped, descOutputOverflows,
		descOutputClients, descRecordingActive, descRecordingFailed, descRecordingBytes,
		descRecordingDuration, descRecordingDropped,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
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
	output := func(id, kind, name, state string, bytes, dropped, overflows int64, clients int) {
		gauge(descOutputInfo, 1, id, kind, name)
		for _, st := range outputStates {
			gauge(descOutputState, flag(string(st) == state), id, string(st))
		}
		counter(descOutputBytes, bytes, id)
		counter(descOutputDropped, dropped, id)
		counter(descOutputOverflows, overflows, id)
		if kind == "srt-listener" {
			gauge(descOutputClients, float64(clients), id)
		}
	}

	for _, d := range c.m.ListDestinations() {
		output(d.ID, d.Config.Type, d.Config.Name, d.State, d.BytesWritten, d.DroppedPackets, d.OverflowCount, d.Connections)
	}
	if srt := c.m.SRTOutputStatus(); srt.Active {
		output("srt", "srt-"+srt.Mode, "", srt.State, srt.BytesWritten, srt.DroppedPackets, srt.OverflowCount, srt.Connections)
	}

	rec := c.m.RecordingStatus()
	gauge(descRecordingActive, flag(rec.Active))
	gauge(descRecordingFailed, flag(rec.Error != ""))
	if rec.Active {
		gauge(descRecordingBytes, float64(rec.BytesWritten))
		gauge(descRecordingDuration, rec.DurationSecs)
		counter(descRecordingDropped, rec.DroppedPackets)
	}
}
