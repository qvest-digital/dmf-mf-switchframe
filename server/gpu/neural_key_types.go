package gpu

// NeuralKeyState provides neural key configuration to the GPU pipeline node.
// Unlike the original design which only checked the program source, this
// interface returns ALL sources with neural keys enabled — matching the
// upstream key pattern where keyed sources are composited onto program.
//
// Implementations must be safe for concurrent reads from the pipeline goroutine.
type NeuralKeyState interface {
	// HasNeuralKeys returns true when any source has type:"neural" key enabled.
	HasNeuralKeys() bool
	// SnapshotNeuralKeys returns a deep copy of all neural key configs,
	// each with a reference to the source's GPU cached fill frame.
	SnapshotNeuralKeys() []NeuralKeySnapshot
}

// NeuralKeySnapshot holds a deep copy of neural key parameters for one source.
type NeuralKeySnapshot struct {
	SourceKey  string
	KeyColorY  uint8
	KeyColorCb uint8
	KeyColorCr uint8
	Similarity float32
	Smoothness float32

	// FillFrame is the source's GPU-cached NV12 frame (from GPU source manager).
	// The neural key node runs inference on this frame and composites the result
	// onto the program frame. Nil if the source has no cached frame yet.
	FillFrame *GPUFrame
}
