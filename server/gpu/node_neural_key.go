//go:build cgo && cuda && tensorrt

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/neuralkey"
)

// gpuNeuralKeyNode applies neural green screen keying on the GPU via
// TensorRT inference. It reads key configuration through NeuralKeyState
// and delegates to the neuralkey.Engine for alpha matte generation.
//
// The node sits in the pipeline after the chroma/luma key node, which
// provides a coarse key as fallback during TensorRT warmup. Once the
// engine is warm, this node produces a high-quality neural matte.
type gpuNeuralKeyNode struct {
	ctx    *Context
	pool   *FramePool
	state  NeuralKeyState
	engine *neuralkey.Engine

	// Pre-allocated GPU scratch frames.
	hintBuf *GPUFrame // coarse alpha hint from chroma key

	width, height int
	lastErr       atomic.Value
}

// NewGPUNeuralKeyNode creates a GPU neural key pipeline node backed by
// a TensorRT inference engine loaded from the given ONNX model path.
// Returns nil if state is nil, enginePath is empty, or the engine fails
// to initialize (e.g., no GPU available).
func NewGPUNeuralKeyNode(ctx *Context, pool *FramePool, state NeuralKeyState, enginePath string) GPUPipelineNode {
	if state == nil || enginePath == "" {
		return nil
	}

	engine, err := neuralkey.NewEngine(enginePath, 1920, 1080)
	if err != nil {
		slog.Warn("neural key engine unavailable", "error", err)
		return nil
	}

	return &gpuNeuralKeyNode{
		ctx:    ctx,
		pool:   pool,
		state:  state,
		engine: engine,
	}
}

func (n *gpuNeuralKeyNode) Name() string { return "gpu_neural_key" }

func (n *gpuNeuralKeyNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height

	// Allocate or reallocate scratch buffer for the chroma hint mask.
	if n.hintBuf != nil {
		n.hintBuf.Release()
		n.hintBuf = nil
	}
	buf, err := n.pool.Acquire()
	if err != nil {
		return err
	}
	n.hintBuf = buf
	return nil
}

// Active returns true when a neural key state provider is attached and
// the current program source has neural keying enabled.
func (n *gpuNeuralKeyNode) Active() bool {
	return n.state != nil && n.state.HasNeuralKey()
}

func (n *gpuNeuralKeyNode) Latency() time.Duration { return 15 * time.Millisecond }

func (n *gpuNeuralKeyNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// ProcessGPU runs neural keying inference on the program frame.
//
//  1. If neural keying is not active, return immediately (fast path).
//  2. Read the current key configuration snapshot.
//  3. During TensorRT warmup, pass through (chroma key node provides fallback).
//  4. Run inference: generate chroma hint, infer alpha matte, composite.
func (n *gpuNeuralKeyNode) ProcessGPU(frame *GPUFrame) error {
	if n.state == nil || !n.state.HasNeuralKey() {
		return nil
	}

	snap := n.state.NeuralKeySnapshot()
	if snap == nil {
		return nil
	}

	// During TensorRT warmup, skip neural keying (pass through).
	// The chroma key node before us provides basic keying as fallback.
	if !n.engine.WarmupDone() {
		return nil
	}

	// Phase 2 inference pipeline:
	// 1. Generate chroma hint into hintBuf via ChromaKey CUDA kernel
	// 2. Call engine.Infer(frame.NV12, hintBuf.Y, w, h, pitch, alphaOut, fgOut, stream)
	// 3. Composite foreground onto program using alpha
	//
	// The C library (corridorkey.c) needs to be written first before
	// inference can run. This node is wired and ready for integration.

	return nil
}

func (n *gpuNeuralKeyNode) Close() error {
	if n.hintBuf != nil {
		n.hintBuf.Release()
		n.hintBuf = nil
	}
	if n.engine != nil {
		n.engine.Close()
	}
	return nil
}
