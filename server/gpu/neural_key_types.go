package gpu

// NeuralKeyState provides neural key configuration to the GPU pipeline node.
// Implementations must be safe for concurrent reads from the pipeline goroutine.
type NeuralKeyState interface {
	// HasNeuralKey returns true when the program source has type:"neural" key enabled.
	HasNeuralKey() bool
	// ProgramSourceKey returns the current program source key.
	ProgramSourceKey() string
	// NeuralKeySnapshot returns a deep copy of the current neural key config.
	NeuralKeySnapshot() *NeuralKeySnapshot
}

// NeuralKeySnapshot holds a deep copy of neural key parameters for one frame.
type NeuralKeySnapshot struct {
	SourceKey  string
	KeyColorY  uint8
	KeyColorCb uint8
	KeyColorCr uint8
	Similarity float32
	Smoothness float32
}
