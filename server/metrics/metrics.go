// Package metrics provides Prometheus metric definitions and a non-default
// registry for the Switchframe application.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// registry is a non-default Prometheus registry so Switchframe metrics don't
// collide with anything registered on prometheus.DefaultRegisterer.
var registry = prometheus.NewRegistry()

func init() {
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
	)
}

// GetRegistry returns the non-default Prometheus registry used by Switchframe.
func GetRegistry() *prometheus.Registry {
	return registry
}

// Handler returns an http.Handler that serves Prometheus metrics from the
// non-default registry with OpenMetrics enabled.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

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
	IDRGateDuration    prometheus.Histogram

	// Mixer
	FramesMixedTotal       prometheus.Counter
	EncodeErrorsTotal      prometheus.Counter
	PassthroughBypassTotal prometheus.Counter

	// Output
	RingbufOverflowsTotal prometheus.Counter
	SRTReconnectsTotal    prometheus.Counter
	RecordingBytesTotal   prometheus.Counter
	SRTBytesTotal         prometheus.Counter

	// CBR pacer
	CBRNullPacketsTotal prometheus.Counter
	CBRRealBytesTotal   prometheus.Counter
	CBRPadBytesTotal    prometheus.Counter
	CBRBurstTicksTotal  prometheus.Counter

	// Pipeline
	PipelineDecodeErrorsTotal prometheus.Counter
	PipelineEncodeErrorsTotal prometheus.Counter
	PipelineFramesProcessed   prometheus.Counter
	PipelineDecodeDuration    prometheus.Histogram
	PipelineEncodeDuration    prometheus.Histogram
	PipelineBlendDuration     prometheus.Histogram
	NodeProcessDuration       *prometheus.HistogramVec

	// Health
	SourceStatusChangesTotal *prometheus.CounterVec // labels: source, from_status, to_status

	// Command Queue (active-active)
	CmdQueueDepth           prometheus.Gauge
	CmdQueueDrainedTotal    prometheus.Counter
	CmdQueueLateTotal       prometheus.Counter
	CmdQueueStaleTotal      prometheus.Counter
	CmdQueueDuplicateTotal  prometheus.Counter
	CmdQueueClockJumpsTotal prometheus.Counter

	// Snapshot Sync (active-active)
	SnapshotApplyTotal    *prometheus.CounterVec // labels: result (success, partial, error, stale)
	SnapshotApplyDuration prometheus.Histogram
	SnapshotSizeBytes     prometheus.Histogram

	// Force-leader (operator-triggered epoch bump during partition)
	ForceLeaderTotal *prometheus.CounterVec // labels: operator, reason_class

	// Clock synchronization (engine-side PTP / system-clock detection)
	ClockSyncSynchronized  prometheus.Gauge     // 1 when clock is synchronized per the current detector, 0 otherwise
	ClockSyncWindowDriftNs prometheus.Gauge     // signed wall-vs-monotonic drift in nanoseconds over the sampler window (NOT a servo instantaneous)
	ClockSyncSource        *prometheus.GaugeVec // labels: source — 1 on the currently-active source, 0 on others
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

		// CBR pacer
		CBRNullPacketsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cbr",
			Name:      "null_packets_total",
			Help:      "Total null TS packets inserted by CBR pacer.",
		}),
		CBRRealBytesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cbr",
			Name:      "real_bytes_total",
			Help:      "Total real (non-null) bytes sent through CBR pacer.",
		}),
		CBRPadBytesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cbr",
			Name:      "pad_bytes_total",
			Help:      "Total null padding bytes inserted by CBR pacer.",
		}),
		CBRBurstTicksTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cbr",
			Name:      "burst_ticks_total",
			Help:      "Total tick cycles where real data exceeded CBR budget.",
		}),

		// Pipeline
		PipelineDecodeErrorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "decode_errors_total",
			Help:      "Total video pipeline decode errors (fallback to passthrough).",
		}),
		PipelineEncodeErrorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "encode_errors_total",
			Help:      "Total video pipeline encode errors (frame dropped).",
		}),
		PipelineFramesProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "frames_processed_total",
			Help:      "Total video frames processed through the YUV pipeline.",
		}),
		PipelineDecodeDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "decode_duration_seconds",
			Help:      "Video pipeline decode latency.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.02, 0.033, 0.05, 0.1},
		}),
		PipelineEncodeDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "encode_duration_seconds",
			Help:      "Video pipeline encode latency.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.02, 0.033, 0.05, 0.1},
		}),
		PipelineBlendDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "blend_duration_seconds",
			Help:      "Transition blend latency.",
			Buckets:   []float64{0.0001, 0.0005, 0.001, 0.002, 0.005, 0.01},
		}),
		NodeProcessDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Subsystem: "pipeline",
			Name:      "node_duration_seconds",
			Help:      "Per-node video processing duration.",
			Buckets:   []float64{0.00001, 0.0001, 0.001, 0.01, 0.1},
		}, []string{"node"}),

		// Health
		SourceStatusChangesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "switchframe",
			Name:      "source_status_changes_total",
			Help:      "Total source health status transitions.",
		}, []string{"source", "from_status", "to_status"}),

		// Command Queue (active-active)
		CmdQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "switchframe",
			Subsystem: "cmdqueue",
			Name:      "depth",
			Help:      "Current number of commands pending in the PTP-timed queue.",
		}),
		CmdQueueDrainedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cmdqueue",
			Name:      "drained_total",
			Help:      "Total commands drained (executed) from the queue.",
		}),
		CmdQueueLateTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cmdqueue",
			Name:      "late_total",
			Help:      "Total commands promoted to immediate execution (late but not stale).",
		}),
		CmdQueueStaleTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cmdqueue",
			Name:      "stale_total",
			Help:      "Total commands rejected as stale (too old to execute).",
		}),
		CmdQueueDuplicateTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cmdqueue",
			Name:      "duplicate_total",
			Help:      "Total commands rejected as duplicate (same command_seq).",
		}),
		CmdQueueClockJumpsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "cmdqueue",
			Name:      "clock_jumps_total",
			Help:      "Total backward clock jumps detected during drain.",
		}),

		// Snapshot Sync (active-active)
		SnapshotApplyTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "switchframe",
			Subsystem: "snapshot",
			Name:      "apply_total",
			Help:      "Total snapshot apply operations by result.",
		}, []string{"result"}),
		SnapshotApplyDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Subsystem: "snapshot",
			Name:      "apply_duration_seconds",
			Help:      "Duration of snapshot apply operations.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		}),
		SnapshotSizeBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "switchframe",
			Subsystem: "snapshot",
			Name:      "size_bytes",
			Help:      "Size of applied state snapshots in bytes.",
			Buckets:   []float64{1024, 4096, 16384, 65536, 262144, 1048576},
		}),

		ForceLeaderTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "switchframe",
			Name:      "force_leader_total",
			Help:      "Operator-triggered force-leader epoch bumps. 'operator' is the proxied browser-side attribution (or 'control-plane' for direct SRE calls); 'reason_class' is a closed enum (partition|recovery|maintenance|test|drill|unspecified|other) for coarse-grained ops dashboards — free-text reasons land in slog, not Prometheus labels.",
		}, []string{"operator", "reason_class"}),

		ClockSyncSynchronized: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "switchframe",
			Name:      "clock_sync_synchronized",
			Help:      "1 if the local clock is synchronized per the active detector (systemclock-monitored drift below threshold, or PTP when pmc integration lands), 0 otherwise. Older builds always reported 1.",
		}),
		ClockSyncWindowDriftNs: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "switchframe",
			Name:      "clock_sync_window_drift_ns",
			Help:      "Signed wall-vs-monotonic drift accumulated over the sampler window, in nanoseconds. NOT a servo instantaneous offset — it is an integral of the window. Negative if the wall clock ran slower than monotonic. Phase B's pmc-based detector will report a real instantaneous offset under a separate metric when it lands.",
		}),
		ClockSyncSource: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "switchframe",
			Name:      "clock_sync_source",
			Help:      "Labeled gauge, 1 on the currently-active clock-sync detection source (systemclock-initializing | systemclock-monitored | ptp), 0 on inactive sources. Lets dashboards pivot without re-encoding the source in label cardinality of other metrics.",
		}, []string{"source"}),
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
		m.PipelineDecodeErrorsTotal,
		m.PipelineEncodeErrorsTotal,
		m.PipelineFramesProcessed,
		m.PipelineDecodeDuration,
		m.PipelineEncodeDuration,
		m.PipelineBlendDuration,
		m.NodeProcessDuration,
		m.SourceStatusChangesTotal,
		m.CBRNullPacketsTotal,
		m.CBRRealBytesTotal,
		m.CBRPadBytesTotal,
		m.CBRBurstTicksTotal,
		m.CmdQueueDepth,
		m.CmdQueueDrainedTotal,
		m.CmdQueueLateTotal,
		m.CmdQueueStaleTotal,
		m.CmdQueueDuplicateTotal,
		m.CmdQueueClockJumpsTotal,
		m.SnapshotApplyTotal,
		m.SnapshotApplyDuration,
		m.SnapshotSizeBytes,
		m.ForceLeaderTotal,
		m.ClockSyncSynchronized,
		m.ClockSyncWindowDriftNs,
		m.ClockSyncSource,
	)

	return m
}
