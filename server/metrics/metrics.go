// Package metrics provides Prometheus metric definitions and a non-default
// registry for the Switchframe application.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is a non-default Prometheus registry so Switchframe metrics don't
// collide with anything registered on prometheus.DefaultRegisterer.
var Registry = prometheus.NewRegistry()

func init() {
	Registry.MustRegister(collectors.NewGoCollector())
	Registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	Registry.MustRegister(
		FramesThroughput,
		TransitionsTotal,
		ActiveOutputs,
		AudioMixDuration,
		HTTPRequestsTotal,
		HTTPRequestDuration,
	)
}

// Handler returns an http.Handler that serves Prometheus metrics from the
// non-default Registry with OpenMetrics enabled.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// FramesThroughput counts video frames forwarded to program output, labeled by source.
var FramesThroughput = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "switchframe",
		Name:      "frames_total",
		Help:      "Video frames forwarded to program output, by source.",
	},
	[]string{"source"},
)

// TransitionsTotal counts completed transitions, labeled by type (mix, dip, ftb).
var TransitionsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "switchframe",
		Name:      "transitions_total",
		Help:      "Completed transitions by type.",
	},
	[]string{"type"},
)

// ActiveOutputs tracks the number of active output adapters, labeled by type
// (recording, srt_caller, srt_listener).
var ActiveOutputs = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "switchframe",
		Name:      "active_outputs",
		Help:      "Number of active output adapters by type.",
	},
	[]string{"type"},
)

// AudioMixDuration measures time spent in the audio mixer per AAC frame.
var AudioMixDuration = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Namespace: "switchframe",
		Subsystem: "audio",
		Name:      "mix_duration_seconds",
		Help:      "Time spent in the audio mixer per AAC frame.",
		Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 12),
	},
)

// HTTPRequestsTotal counts HTTP requests by method, path pattern, and status code.
var HTTPRequestsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "switchframe",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total HTTP requests by method, path pattern, and status code.",
	},
	[]string{"method", "pattern", "status"},
)

// HTTPRequestDuration measures HTTP request latency distribution.
var HTTPRequestDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "switchframe",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request latency distribution.",
		Buckets:   []float64{.0005, .001, .005, .01, .025, .05, .1, .25, .5},
	},
	[]string{"method", "pattern"},
)

// Metrics holds all Switchframe subsystem Prometheus metrics.
// Create with NewMetrics and pass the instance to subsystems that need
// to record observations. This avoids global state and makes testing easy.
type Metrics struct {
	// Switcher
	CutsTotal          prometheus.Counter
	TransitionsTotal   *prometheus.CounterVec // label: type
	IDRGateEventsTotal prometheus.Counter
	IDRGateDuration  prometheus.Histogram

	// Mixer
	FramesMixedTotal       prometheus.Counter
	EncodeErrorsTotal      prometheus.Counter
	PassthroughBypassTotal prometheus.Counter

	// Output
	RingbufOverflowsTotal prometheus.Counter
	SRTReconnectsTotal    prometheus.Counter
	RecordingBytesTotal   prometheus.Counter
	SRTBytesTotal         prometheus.Counter

	// Health
	SourceStatusChangesTotal *prometheus.CounterVec // labels: source, from_status, to_status
}

// NewMetrics creates and registers all Switchframe metrics on the given
// registerer. All metric names are prefixed with "switchframe_".
// Panics if any metric fails to register (e.g., duplicate registration).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		// Switcher
		CutsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Name:      "cuts_total",
			Help:      "Total number of hard cuts performed.",
		}),
		TransitionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "switchframe",
			Name:      "transitions_completed_total",
			Help:      "Total completed transitions by type (mix, dip, ftb).",
		}, []string{"type"}),
		IDRGateEventsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Name:      "idr_gate_events_total",
			Help:      "Total IDR gate events (frames gated until first keyframe after cut).",
		}),
		IDRGateDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Name:      "idr_gate_duration_seconds",
			Help:      "Duration of IDR gate wait in seconds.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		}),

		// Mixer
		FramesMixedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "mixer",
			Name:      "frames_mixed_total",
			Help:      "Total audio frames mixed (decoded, mixed, re-encoded).",
		}),
		EncodeErrorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "mixer",
			Name:      "encode_errors_total",
			Help:      "Total audio encode errors in the mixer.",
		}),
		PassthroughBypassTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "mixer",
			Name:      "passthrough_bypass_total",
			Help:      "Total audio frames bypassed via passthrough (single source at 0 dB).",
		}),

		// Output
		RingbufOverflowsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "output",
			Name:      "ringbuf_overflows_total",
			Help:      "Total SRT ring buffer overflow events.",
		}),
		SRTReconnectsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "output",
			Name:      "srt_reconnects_total",
			Help:      "Total SRT reconnection attempts.",
		}),
		RecordingBytesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "output",
			Name:      "recording_bytes_total",
			Help:      "Total bytes written to recording files.",
		}),
		SRTBytesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "output",
			Name:      "srt_bytes_total",
			Help:      "Total bytes sent over SRT connections.",
		}),

		// Health
		SourceStatusChangesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "switchframe",
			Name:      "source_status_changes_total",
			Help:      "Total source health status transitions.",
		}, []string{"source", "from_status", "to_status"}),
	}

	reg.MustRegister(
		m.CutsTotal,
		m.TransitionsTotal,
		m.IDRGateEventsTotal,
		m.IDRGateDuration,
		m.FramesMixedTotal,
		m.EncodeErrorsTotal,
		m.PassthroughBypassTotal,
		m.RingbufOverflowsTotal,
		m.SRTReconnectsTotal,
		m.RecordingBytesTotal,
		m.SRTBytesTotal,
		m.SourceStatusChangesTotal,
	)

	return m
}
