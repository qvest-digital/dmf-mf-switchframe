//go:build darwin

package gpu

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/atomicutil"
)

// GPUPipelineNode is the processing unit for the GPU video pipeline.
type GPUPipelineNode interface {
	Name() string
	Configure(width, height, pitch int) error
	Active() bool
	ProcessGPU(frame *GPUFrame) error
	Err() error
	Latency() time.Duration
	Close() error
}

// GPUPipeline holds a configured GPU processing chain.
type GPUPipeline struct {
	ctx         *Context
	pool        *FramePool
	allNodes    []GPUPipelineNode
	activeNodes []GPUPipelineNode
	width       int
	height      int

	nodeTiming []atomic.Int64
	nodeMaxNs  []atomic.Int64

	totalLatency time.Duration
	runCount     atomic.Int64
	lastRunNs    atomic.Int64
	maxRunNs     atomic.Int64

	inflight sync.WaitGroup
}

// NewGPUPipeline creates a GPU pipeline with the given context and frame pool.
func NewGPUPipeline(ctx *Context, pool *FramePool) *GPUPipeline {
	return &GPUPipeline{
		ctx:  ctx,
		pool: pool,
	}
}

// Build validates all nodes, filters active ones, and pre-computes latency.
func (p *GPUPipeline) Build(width, height, pitch int, nodes []GPUPipelineNode) error {
	p.width = width
	p.height = height
	p.allNodes = nodes
	p.activeNodes = p.activeNodes[:0]
	p.totalLatency = 0

	for _, n := range nodes {
		if err := n.Configure(width, height, pitch); err != nil {
			return fmt.Errorf("gpu pipeline: node %s: configure: %w", n.Name(), err)
		}
		if n.Active() {
			p.activeNodes = append(p.activeNodes, n)
			p.totalLatency += n.Latency()
		}
	}

	p.nodeTiming = make([]atomic.Int64, len(p.activeNodes))
	p.nodeMaxNs = make([]atomic.Int64, len(p.activeNodes))

	names := make([]string, len(p.activeNodes))
	for i, n := range p.activeNodes {
		names[i] = n.Name()
	}
	slog.Info("gpu pipeline: built", "active_nodes", names, "width", width, "height", height)
	return nil
}

// Run processes a single GPU frame through all active nodes.
func (p *GPUPipeline) Run(frame *GPUFrame) error {
	p.inflight.Add(1)
	defer p.inflight.Done()

	start := time.Now().UnixNano()
	for i, node := range p.activeNodes {
		t0 := time.Now().UnixNano()
		if err := node.ProcessGPU(frame); err != nil {
			slog.Warn("gpu pipeline: node error", "node", node.Name(), "err", err)
		}
		dur := time.Now().UnixNano() - t0
		p.nodeTiming[i].Store(dur)
		atomicutil.UpdateMax(&p.nodeMaxNs[i], dur)
	}

	total := time.Now().UnixNano() - start
	p.lastRunNs.Store(total)
	p.runCount.Add(1)
	atomicutil.UpdateMax(&p.maxRunNs, total)
	return nil
}

// RunWithUpload uploads a CPU YUV420p frame to GPU, runs the pipeline.
func (p *GPUPipeline) RunWithUpload(yuv []byte, width, height int, pts int64) (*GPUFrame, error) {
	frame, err := p.pool.Acquire()
	if err != nil {
		return nil, fmt.Errorf("gpu pipeline: acquire frame: %w", err)
	}
	frame.PTS = pts

	if err := Upload(p.ctx, frame, yuv, width, height); err != nil {
		frame.Release()
		return nil, fmt.Errorf("gpu pipeline: upload: %w", err)
	}

	if err := p.Run(frame); err != nil {
		frame.Release()
		return nil, err
	}

	return frame, nil
}

// Snapshot returns per-node timing for debug endpoint.
func (p *GPUPipeline) Snapshot() map[string]any {
	nodes := make([]map[string]any, len(p.activeNodes))
	for i, n := range p.activeNodes {
		entry := map[string]any{
			"name":       n.Name(),
			"last_ns":    p.nodeTiming[i].Load(),
			"max_ns":     p.nodeMaxNs[i].Load(),
			"latency_us": n.Latency().Microseconds(),
		}
		if err := n.Err(); err != nil {
			entry["last_error"] = err.Error()
		}
		nodes[i] = entry
	}
	return map[string]any{
		"gpu":              true,
		"backend":          "metal",
		"active_nodes":     nodes,
		"total_nodes":      len(p.allNodes),
		"run_count":        p.runCount.Load(),
		"last_run_ns":      p.lastRunNs.Load(),
		"max_run_ns":       p.maxRunNs.Load(),
		"total_latency_us": p.totalLatency.Microseconds(),
	}
}

// Wait blocks until all in-flight Run() calls complete.
func (p *GPUPipeline) Wait() { p.inflight.Wait() }

// Close waits for in-flight frames, then closes all nodes.
func (p *GPUPipeline) Close() error {
	p.inflight.Wait()
	var firstErr error
	for _, n := range p.allNodes {
		if err := n.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
