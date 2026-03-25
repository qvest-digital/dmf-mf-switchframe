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
	"unsafe"
)

// gpuNeuralKeyNode applies neural green screen keying on the GPU via
// TensorRT inference. It iterates all sources with type:"neural" keys,
// runs inference on each source's GPU-cached fill frame, and composites
// the keyed result onto the program frame — matching the upstream key
// pattern used by gpuKeyNode.
type gpuNeuralKeyNode struct {
	ctx    *Context
	pool   *FramePool
	state  NeuralKeyState
	engine *NeuralKeyEngine

	width, height int
	lastErr       atomic.Value
}

// NewGPUNeuralKeyNode creates a GPU neural key pipeline node.
func NewGPUNeuralKeyNode(ctx *Context, pool *FramePool, state NeuralKeyState, enginePath string) GPUPipelineNode {
	if state == nil || enginePath == "" {
		return nil
	}

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
	return nil
}

func (n *gpuNeuralKeyNode) Active() bool {
	return n.state != nil && n.state.HasNeuralKeys()
}

func (n *gpuNeuralKeyNode) Latency() time.Duration { return 15 * time.Millisecond }

func (n *gpuNeuralKeyNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// SetEngine attaches the TensorRT neural key engine to this node.
func (n *gpuNeuralKeyNode) SetEngine(engine *NeuralKeyEngine) {
	n.engine = engine
}

// ProcessGPU runs neural keying for all sources with type:"neural" keys.
// For each keyed source:
//  1. Get the source's GPU cached fill frame from the snapshot.
//  2. Run TRT inference on the fill frame (generates alpha + foreground).
//  3. Composite the result onto the program frame.
func (n *gpuNeuralKeyNode) ProcessGPU(frame *GPUFrame) error {
	if n.state == nil || !n.state.HasNeuralKeys() {
		return nil
	}
	if n.engine == nil {
		return nil
	}

	keys := n.state.SnapshotNeuralKeys()
	if len(keys) == 0 {
		return nil
	}

	for i := range keys {
		snap := &keys[i]
		if snap.FillFrame == nil {
			continue // source has no cached GPU frame yet
		}

		// Run inference on the fill frame and composite onto program.
		err := n.engine.InferAndComposite(
			frame,
			snap.FillFrame,
			snap.KeyColorCb, snap.KeyColorCr,
			snap.Similarity, snap.Smoothness,
			defaultCUDAStream,
		)
		// Release the ref acquired by GetFrame in SnapshotNeuralKeys.
		snap.FillFrame.Release()

		if err != nil {
			slog.Warn("gpu_neural_key: inference failed",
				"source", snap.SourceKey, "error", err)
			n.lastErr.Store(err)
			// Continue with remaining keys.
		}
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

// Ensure interface compliance for SetEngine type assertion.
var _ interface{ SetEngine(*NeuralKeyEngine) } = (*gpuNeuralKeyNode)(nil)

// unused import guard
var _ = unsafe.Pointer(nil)
