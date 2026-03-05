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
