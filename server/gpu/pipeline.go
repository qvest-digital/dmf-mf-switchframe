//go:build cgo && cuda

package gpu

/*
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/atomicutil"
)

// GPUPipelineNode is the processing unit for the GPU video pipeline.
// Mirrors switcher.PipelineNode but operates on GPU-resident NV12 frames.
//
// All nodes process frames in-place on the same GPUFrame — no intermediate
// copies between nodes. The frame stays in VRAM from decode through encode.
type GPUPipelineNode interface {
	// Name returns a human-readable identifier for debugging and metrics.
	Name() string

	// Configure is called once when the pipeline is built.
	Configure(width, height, pitch int) error

	// Active returns whether this node should be included in processing.
	Active() bool

	// ProcessGPU transforms the frame in-place on the GPU.
	ProcessGPU(frame *GPUFrame) error

	// Err returns the last error from ProcessGPU, or nil.
	Err() error

	// Latency reports estimated per-frame processing time.
	Latency() time.Duration

	// Close releases resources held by this node.
	Close() error
}

// GPUPipeline holds a configured GPU processing chain.
// Built via Build(), Run() called per-frame. Immutable once built.
type GPUPipeline struct {
	ctx         *Context
	pool        *FramePool
	allNodes    []GPUPipelineNode
	activeNodes []GPUPipelineNode
	width       int
	height      int

	// Per-node timing (nanoseconds)
	nodeTiming []atomic.Int64
	nodeMaxNs  []atomic.Int64

	// Aggregate metrics
	totalLatency time.Duration
	runCount     atomic.Int64
	lastRunNs    atomic.Int64
	maxRunNs     atomic.Int64

	// In-flight tracking for safe cleanup
	inflight sync.WaitGroup

	// Per-node bypass flags. Keyed by node Name(). When a flag is true,
	// Run() skips that node's ProcessGPU() call — the frame passes through
	// unchanged. Initialized by Build() for every node.
	bypassFlags map[string]*atomic.Bool

	// Per-node thumbnail capture (rate-limited, async JPEG encode).
	// Set via SetThumbnailStore(). Nil when no capture is active.
	thumbState atomic.Pointer[thumbnailState]
}

// NewGPUPipeline creates a GPU pipeline with the given context and frame pool.
func NewGPUPipeline(ctx *Context, pool *FramePool) *GPUPipeline {
	return &GPUPipeline{
		ctx:  ctx,
		pool: pool,
	}
}

// Pool returns the frame pool used by this pipeline.
func (p *GPUPipeline) Pool() *FramePool { return p.pool }

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
		// Include ALL nodes — each node checks Active() dynamically in ProcessGPU.
		p.activeNodes = append(p.activeNodes, n)
		p.totalLatency += n.Latency()
	}

	p.nodeTiming = make([]atomic.Int64, len(p.activeNodes))
	p.nodeMaxNs = make([]atomic.Int64, len(p.activeNodes))

	// Initialize per-node bypass flags.
	p.bypassFlags = make(map[string]*atomic.Bool, len(nodes))
	for _, n := range nodes {
		p.bypassFlags[n.Name()] = &atomic.Bool{}
	}

	names := make([]string, len(p.activeNodes))
	for i, n := range p.activeNodes {
		names[i] = n.Name()
	}
	slog.Info("gpu pipeline: built", "active_nodes", names, "width", width, "height", height)
	return nil
}

// Run processes a single GPU frame through all active nodes.
// The frame is modified in-place — all nodes operate on the same VRAM buffer.
func (p *GPUPipeline) Run(frame *GPUFrame) error {
	p.inflight.Add(1)
	defer p.inflight.Done()

	// Check if we should capture thumbnails this frame (rate-limited to 1/sec).
	ts := p.shouldCaptureThisFrame()

	start := time.Now().UnixNano()
	for i, node := range p.activeNodes {
		if flag, ok := p.bypassFlags[node.Name()]; ok && flag.Load() {
			p.nodeTiming[i].Store(0)
			continue
		}
		t0 := time.Now().UnixNano()
		if err := node.ProcessGPU(frame); err != nil {
			slog.Warn("gpu pipeline: node error", "node", node.Name(), "err", err)
			// Continue processing — don't abort pipeline on single node failure
		}
		dur := time.Now().UnixNano() - t0
		p.nodeTiming[i].Store(dur)
		atomicutil.UpdateMax(&p.nodeMaxNs[i], dur)

		// After each node, enqueue a GPU downsample to the scratch buffer.
		// The scale dispatch is pipelined — it does not block the next node.
		if ts != nil {
			ts.captureNode(p.ctx, i, frame)
		}
	}

	total := time.Now().UnixNano() - start
	p.lastRunNs.Store(total)
	p.runCount.Add(1)
	atomicutil.UpdateMax(&p.maxRunNs, total)

	// After the pipeline completes, readback all scratch buffers and
	// submit JPEG encode requests to the background goroutine.
	if ts != nil {
		ts.lastCapture = time.Now()
		ts.readbackAndEncode(p.ctx)
	}

	return nil
}

// RunWithUpload uploads a CPU planar frame to GPU, runs the pipeline,
// and returns the GPU frame. The upload kernel is selected based on the
// pool's frame format:
//   - NV12: expects YUV420P 8-bit (w*h*3/2 bytes)
//   - P210: expects YUV422P10LE (w*h*4 bytes)
func (p *GPUPipeline) RunWithUpload(yuv []byte, width, height int, pts int64) (*GPUFrame, error) {
	frame, err := p.pool.Acquire()
	if err != nil {
		return nil, fmt.Errorf("gpu pipeline: acquire frame: %w", err)
	}
	frame.PTS = pts

	if err := GPUUploadDispatch(p.ctx, frame, yuv, width, height); err != nil {
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
		bypassed := false
		if flag, ok := p.bypassFlags[n.Name()]; ok {
			bypassed = flag.Load()
		}
		entry := map[string]any{
			"name":       n.Name(),
			"last_ns":    p.nodeTiming[i].Load(),
			"max_ns":     p.nodeMaxNs[i].Load(),
			"latency_us": n.Latency().Microseconds(),
			"bypassed":   bypassed,
		}
		if err := n.Err(); err != nil {
			entry["last_error"] = err.Error()
		}
		nodes[i] = entry
	}
	return map[string]any{
		"gpu":              true,
		"active_nodes":     nodes,
		"total_nodes":      len(p.allNodes),
		"run_count":        p.runCount.Load(),
		"last_run_ns":      p.lastRunNs.Load(),
		"max_run_ns":       p.maxRunNs.Load(),
		"total_latency_us": p.totalLatency.Microseconds(),
	}
}

// SetBypass toggles the bypass flag for the named node. When bypassed,
// Run() skips the node's ProcessGPU() call — the frame passes through unchanged.
// Returns an error if the node name is protected or unknown.
func (p *GPUPipeline) SetBypass(nodeName string, bypass bool) error {
	protectedNodes := map[string]bool{
		"gpu_encode":   true,
		"gpu_raw_sink": true,
	}
	if protectedNodes[nodeName] {
		return fmt.Errorf("node %q is protected and cannot be bypassed", nodeName)
	}
	flag, ok := p.bypassFlags[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found in GPU pipeline", nodeName)
	}
	flag.Store(bypass)
	return nil
}

// BypassState returns the current bypass state for all nodes.
func (p *GPUPipeline) BypassState() map[string]bool {
	state := make(map[string]bool, len(p.bypassFlags))
	for name, flag := range p.bypassFlags {
		state[name] = flag.Load()
	}
	return state
}

// Wait blocks until all in-flight Run() calls complete.
func (p *GPUPipeline) Wait() { p.inflight.Wait() }

// Close waits for in-flight frames, then closes all nodes.
func (p *GPUPipeline) Close() error {
	p.inflight.Wait()
	p.closeThumbnails()
	var firstErr error
	for _, n := range p.allNodes {
		if err := n.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
