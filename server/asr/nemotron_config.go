package asr

// NemotronConfig configures the NVIDIA Nemotron ASR backend.
type NemotronConfig struct {
	// ModelDir is the directory containing ONNX model files.
	ModelDir string `json:"-"`

	// ModelsDir is the parent directory for model discovery.
	ModelsDir string `json:"-"`

	// ChunkMs is the inference interval in milliseconds during speech.
	// Controls how often the model re-transcribes the growing audio buffer
	// while speech is active. Default: 3000ms.
	ChunkMs int `json:"chunkMs"`

	// UseGPU enables the CUDA execution provider.
	UseGPU bool `json:"-"`

	// UseTensorRT enables the TensorRT execution provider.
	// Not yet implemented — hidden from API until wired.
	UseTensorRT bool `json:"-"`

	// Language is the recognition language (default: "en").
	Language string `json:"language"`
}
