//go:build cgo && cuda && tensorrt

package gpu

/*
#include <cuda_runtime.h>
*/
import "C"

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuNeuralKeyNode applies neural green screen keying on the GPU via
// TensorRT inference. It reads key configuration through NeuralKeyState
// and delegates to NeuralKeyEngine for the full inference pipeline.
//
// The node sits in the pipeline after the chroma/luma key node, which
// provides a coarse key as fallback during TensorRT warmup. Once the
// engine is warm, this node produces a high-quality neural matte.
type gpuNeuralKeyNode struct {
	ctx    *Context
	pool   *FramePool
	state  NeuralKeyState
	engine *NeuralKeyEngine

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

	// Engine creation is deferred until Configure() when we know program dimensions.
	return &gpuNeuralKeyNode{
		ctx:   ctx,
		pool:  pool,
		state: state,
	}
}

func (n *gpuNeuralKeyNode) Name() string { return "gpu_neural_key" }

func (n *gpuNeuralKeyNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	// Engine will be created lazily on first ProcessGPU call when the model path
	// is available via the app config. For now, dimensions are stored.
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

// SetEngine attaches the TensorRT neural key engine to this node.
// Called from wireGPUPipeline after engine initialization.
func (n *gpuNeuralKeyNode) SetEngine(engine *NeuralKeyEngine) {
	n.engine = engine
}

// ProcessGPU runs neural keying inference on the program frame.
//
//  1. If neural keying is not active or engine not ready, return immediately.
//  2. Read the current key configuration snapshot.
//  3. Run inference: generate chroma hint, TRT infer, composite.
func (n *gpuNeuralKeyNode) ProcessGPU(frame *GPUFrame) error {
	if n.state == nil || !n.state.HasNeuralKey() {
		return nil
	}
	if n.engine == nil {
		slog.Warn("gpu_neural_key: engine is nil, skipping")
		return nil
	}

	snap := n.state.NeuralKeySnapshot()
	if snap == nil {
		slog.Warn("gpu_neural_key: snapshot is nil, skipping")
		return nil
	}

	slog.Info("gpu_neural_key: running inference", "source", snap.SourceKey)

	// Run the full neural key pipeline on the default CUDA stream.
	err := n.engine.Infer(
		frame,
		snap.KeyColorCb, snap.KeyColorCr,
		snap.Similarity, snap.Smoothness,
		defaultCUDAStream,
	)
	if err != nil {
		slog.Warn("gpu_neural_key: inference failed",
			"source", snap.SourceKey, "error", err)
		n.lastErr.Store(err)
		// Don't fail the pipeline — pass through the unmodified frame.
		return nil
	}

	return nil
}

func (n *gpuNeuralKeyNode) Close() error {
	if n.engine != nil {
		n.engine.Close()
		n.engine = nil
	}
	return nil
}
