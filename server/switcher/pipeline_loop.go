package switcher

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/metrics"
)

// aacFrameDuration is the duration of one AAC-LC frame at 48kHz (1024 samples).
// Used for lip-sync hint calculation: 1024/48000 s ≈ 21.333ms.
var aacFrameDuration = time.Second * 1024 / 48000

// Pipeline holds a configured, ready-to-run processing chain.
// Built via Build() on main goroutine. Run() called per-frame on pipeline goroutine.
// Immutable once built — reconfiguration creates a new Pipeline via atomic swap.
type Pipeline struct {
	allNodes    []PipelineNode // full node list (for Close, reconfigure)
	activeNodes []PipelineNode // filtered by Active() at build time
	format      PipelineFormat
	pool        *FramePool

	// Prometheus metrics (optional). Set via SetMetrics() after Build().
	metrics *metrics.Metrics

	// Per-node timing (nanoseconds). Indexed by position in activeNodes.
	// Written by Run() (single-writer), read by Snapshot() (safe: atomic).
	nodeTiming []atomic.Int64
	nodeMaxNs  []atomic.Int64

	// Aggregate metrics
	totalLatency time.Duration
	runCount     atomic.Int64
	lastRunNs    atomic.Int64
	maxRunNs     atomic.Int64

	// Pipeline epoch — set at build time, exposed in Snapshot().
	// Monotonically increasing across rebuilds for downstream change detection.
	epoch uint64

	// In-flight tracking for safe cleanup (atomic swap drain)
	inflight sync.WaitGroup
}

// PipelineEpoch captures the pipeline's identity at a point in time.
// Downstream consumers (SRT output, recording, confidence monitor) can compare
// epochs to detect pipeline changes and respond (force keyframe, start new segment).
type PipelineEpoch struct {
	Format    PipelineFormat
	Epoch     uint64
	StartPTS  int64
	NodeNames []string
}

// Build validates all nodes against the format, filters active nodes,
// and pre-computes total latency. Runs on main goroutine.
func (p *Pipeline) Build(format PipelineFormat, pool *FramePool, nodes []PipelineNode) error {
	p.format = format
	p.pool = pool
	p.allNodes = nodes
	p.activeNodes = p.activeNodes[:0] // Safe: new Pipeline created per build; not reused on live pipeline.
	p.totalLatency = 0

	for _, n := range nodes {
		if err := n.Configure(format); err != nil {
			return fmt.Errorf("node %s: configure: %w", n.Name(), err)
		}
		if n.Active() {
			p.activeNodes = append(p.activeNodes, n)
			p.totalLatency += n.Latency()
		}
	}

	p.nodeTiming = make([]atomic.Int64, len(p.activeNodes))
	p.nodeMaxNs = make([]atomic.Int64, len(p.activeNodes))
	return nil
}

// SetMetrics sets the Prometheus metrics instance for per-node observations.
// Must be called after Build() but before Run(). Nil is safe (no observations).
func (p *Pipeline) SetMetrics(m *metrics.Metrics) {
	p.metrics = m
}

// Run processes a single frame through all active nodes.
// Called on pipeline goroutine (single-threaded).
//
// MakeWritable ensures the pipeline owns its YUV buffer before any node
// modifies it in-place. Source frames delivered via shallow copy (Ref) from
// frame_sync remain untouched — critical for PIP fill cache correctness.
func (p *Pipeline) Run(frame *ProcessingFrame) *ProcessingFrame {
	p.inflight.Add(1)
	defer p.inflight.Done()

	// Ensure exclusive ownership before in-place processing.
	// No-op when frame is already sole owner (unmanaged or refs==1).
	frame.MakeWritable(p.pool)

	start := time.Now().UnixNano()
	for i, node := range p.activeNodes {
		t0 := time.Now().UnixNano()
		frame = node.Process(nil, frame)
		dur := time.Now().UnixNano() - t0
		p.nodeTiming[i].Store(dur)
		atomicutil.UpdateMax(&p.nodeMaxNs[i], dur)
		if p.metrics != nil {
			p.metrics.NodeProcessDuration.WithLabelValues(node.Name()).Observe(float64(dur) / 1e9)
		}
	}

	total := time.Now().UnixNano() - start
	p.lastRunNs.Store(total)
	p.runCount.Add(1)
	atomicutil.UpdateMax(&p.maxRunNs, total)
	return frame
}

// TotalLatency returns sum of all active nodes' reported latencies.
// Used for automatic audio delay compensation (lip-sync).
func (p *Pipeline) TotalLatency() time.Duration {
	return p.totalLatency
}

// Snapshot returns per-node timing for debug endpoint.
func (p *Pipeline) Snapshot() map[string]any {
	nodes := make([]map[string]any, len(p.activeNodes))
	for i, n := range p.activeNodes {
		nodes[i] = map[string]any{
			"name":       n.Name(),
			"last_ns":    p.nodeTiming[i].Load(),
			"max_ns":     p.nodeMaxNs[i].Load(),
			"latency_us": n.Latency().Microseconds(),
		}
		if err := n.Err(); err != nil {
			nodes[i]["last_error"] = err.Error()
		}
		// Merge async metrics for nodes that do work after Process() returns
		// (e.g., encodeNode's real H.264 encode timing).
		if amp, ok := n.(AsyncMetricsProvider); ok {
			for k, v := range amp.AsyncMetrics() {
				nodes[i][k] = v
			}
		}
	}
	return map[string]any{
		"active_nodes":     nodes,
		"total_nodes":      len(p.allNodes),
		"epoch":            p.epoch,
		"run_count":        p.runCount.Load(),
		"last_run_ns":      p.lastRunNs.Load(),
		"max_run_ns":       p.maxRunNs.Load(),
		"total_latency_us": p.totalLatency.Microseconds(),
		"lip_sync_hint_us": (p.totalLatency - aacFrameDuration).Microseconds(),
	}
}

// Wait blocks until all in-flight Run() calls complete.
func (p *Pipeline) Wait() { p.inflight.Wait() }

// Close waits for in-flight frames, then closes all nodes.
func (p *Pipeline) Close() error {
	p.inflight.Wait()
	var firstErr error
	for _, n := range p.allNodes {
		if err := n.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
