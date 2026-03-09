package switcher

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Pipeline holds a configured, ready-to-run processing chain.
// Built via Build() on main goroutine. Run() called per-frame on pipeline goroutine.
// Immutable once built — reconfiguration creates a new Pipeline via atomic swap.
type Pipeline struct {
	allNodes    []PipelineNode // full node list (for Close, reconfigure)
	activeNodes []PipelineNode // filtered by Active() at build time
	format      PipelineFormat
	pool        *FramePool

	// Per-node timing (nanoseconds). Indexed by position in activeNodes.
	// Written by Run() (single-writer), read by Snapshot() (safe: atomic).
	nodeTiming []atomic.Int64
	nodeMaxNs  []atomic.Int64

	// Aggregate metrics
	totalLatency time.Duration
	runCount     atomic.Int64
	lastRunNs    atomic.Int64
	maxRunNs     atomic.Int64

	// In-flight tracking for safe cleanup (atomic swap drain)
	inflight sync.WaitGroup
}

// Build validates all nodes against the format, filters active nodes,
// and pre-computes total latency. Runs on main goroutine.
func (p *Pipeline) Build(format PipelineFormat, pool *FramePool, nodes []PipelineNode) error {
	p.format = format
	p.pool = pool
	p.allNodes = nodes
	p.activeNodes = p.activeNodes[:0]
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

// Run processes a single frame through all active nodes.
// Called on pipeline goroutine (single-threaded).
func (p *Pipeline) Run(frame *ProcessingFrame) *ProcessingFrame {
	p.inflight.Add(1)
	defer p.inflight.Done()

	start := time.Now().UnixNano()
	for i, node := range p.activeNodes {
		t0 := time.Now().UnixNano()
		frame = node.Process(nil, frame)
		dur := time.Now().UnixNano() - t0
		p.nodeTiming[i].Store(dur)
		updateAtomicMax(&p.nodeMaxNs[i], dur)
	}

	total := time.Now().UnixNano() - start
	p.lastRunNs.Store(total)
	p.runCount.Add(1)
	updateAtomicMax(&p.maxRunNs, total)
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
	}
	return map[string]any{
		"active_nodes":     nodes,
		"total_nodes":      len(p.allNodes),
		"run_count":        p.runCount.Load(),
		"last_run_ns":      p.lastRunNs.Load(),
		"max_run_ns":       p.maxRunNs.Load(),
		"total_latency_us": p.totalLatency.Microseconds(),
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
