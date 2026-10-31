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

	// In-flight tracking for safe cleanup (atomic swap drain).
	// An inflightGauge rather than a sync.WaitGroup: the guarded runner
	// (Switcher.runPipelineGuarded) registers from a zero count concurrently
	// with Wait — a pattern WaitGroup forbids (and the race detector flags)
	// but which is safe here thanks to the pipeline-pointer re-check after
	// registration.
	inflight inflightGauge

	// Per-node bypass flags. Keyed by node Name(). When a flag is true,
	// Run() skips that node's Process() call — the frame passes through
	// unchanged. Initialized by Build() for every node in allNodes.
	bypassFlags map[string]*atomic.Bool

	// bypassSet holds bypass state to restore during Build().
	// Set via RestoreBypass() before Build() to carry bypass flags
	// across pipeline rebuilds. Consumed and cleared by Build().
	bypassSet map[string]bool
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

	// Initialize per-node bypass flags for every node in allNodes.
	p.bypassFlags = make(map[string]*atomic.Bool, len(nodes))
	for _, n := range nodes {
		flag := &atomic.Bool{}
		// Restore bypass state from prior pipeline if available.
		if p.bypassSet != nil {
			if val, ok := p.bypassSet[n.Name()]; ok {
				flag.Store(val)
			}
		}
		p.bypassFlags[n.Name()] = flag
	}
	p.bypassSet = nil // consumed

	return nil
}

// RestoreBypass saves bypass state to apply during the next Build() call.
// Used to carry bypass flags across pipeline rebuilds. Must be called
// before Build(). Only restores flags for nodes that exist in the new pipeline.
func (p *Pipeline) RestoreBypass(state map[string]bool) {
	p.bypassSet = state
}

// asyncEncoder returns the pipeline's encode node when it encodes on a
// goroutine of its own, nil otherwise.
func (p *Pipeline) asyncEncoder() *encodeNode {
	for _, n := range p.activeNodes {
		if enc, ok := n.(*encodeNode); ok && enc.encodeCh != nil {
			return enc
		}
	}
	return nil
}

// SetMetrics sets the Prometheus metrics instance for per-node observations.
// Must be called after Build() but before Run(). Nil is safe (no observations).
func (p *Pipeline) SetMetrics(m *metrics.Metrics) {
	p.metrics = m
}

// SetBypass toggles the bypass flag for the named node. When bypassed,
// Run() skips the node's Process() call — the frame passes through unchanged.
// Returns an error if the node name is "gpu_encode" (protected) or unknown.
func (p *Pipeline) SetBypass(nodeName string, bypass bool) error {
	protectedNodes := map[string]bool{
		"gpu_encode":   true,
		"gpu_raw_sink": true,
	}
	if protectedNodes[nodeName] {
		return fmt.Errorf("node %q is protected and cannot be bypassed", nodeName)
	}
	flag, ok := p.bypassFlags[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found in pipeline", nodeName)
	}
	flag.Store(bypass)
	return nil
}

// BypassState returns the current bypass state for all nodes.
func (p *Pipeline) BypassState() map[string]bool {
	state := make(map[string]bool, len(p.bypassFlags))
	for name, flag := range p.bypassFlags {
		state[name] = flag.Load()
	}
	return state
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
		if flag, ok := p.bypassFlags[node.Name()]; ok && flag.Load() {
			p.nodeTiming[i].Store(0)
			continue
		}
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
		bypassed := false
		if flag, ok := p.bypassFlags[n.Name()]; ok {
			bypassed = flag.Load()
		}
		nodes[i] = map[string]any{
			"name":       n.Name(),
			"last_ns":    p.nodeTiming[i].Load(),
			"max_ns":     p.nodeMaxNs[i].Load(),
			"latency_us": n.Latency().Microseconds(),
			"bypassed":   bypassed,
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

// inflightGauge counts frames currently inside Run and lets Wait/Close block
// until the count drains to zero. Unlike sync.WaitGroup, Add(1) from a zero
// count is allowed to race Wait: Wait may then return before the racing
// frame finishes, which the caller compensates for by re-checking the
// current pipeline pointer after registering (see runPipelineGuarded) so a
// frame never runs on a pipeline whose drain already won.
type inflightGauge struct {
	mu sync.Mutex
	c  *sync.Cond
	n  int64
}

func (g *inflightGauge) Add(delta int64) {
	g.mu.Lock()
	g.n += delta
	if g.n < 0 {
		g.mu.Unlock()
		panic("switcher: pipeline in-flight counter went negative")
	}
	if g.n == 0 && g.c != nil {
		g.c.Broadcast()
	}
	g.mu.Unlock()
}

func (g *inflightGauge) Done() { g.Add(-1) }

// Wait blocks until the in-flight count is zero.
func (g *inflightGauge) Wait() {
	g.mu.Lock()
	for g.n != 0 {
		if g.c == nil {
			g.c = sync.NewCond(&g.mu)
		}
		g.c.Wait()
	}
	g.mu.Unlock()
}

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
