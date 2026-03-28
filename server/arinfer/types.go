// Package arinfer provides the AI inference engine for AR graphics,
// managing depth estimation and segmentation models. It produces
// occlusion maps that the AR compositor uses to place virtual graphics
// behind real-world objects in the scene.
package arinfer

import "errors"

// Sentinel errors for the inference engine.
var (
	// ErrNotReady indicates the engine has not finished initializing.
	ErrNotReady = errors.New("arinfer: engine not ready")

	// ErrModelMissing indicates a required model file was not found.
	ErrModelMissing = errors.New("arinfer: model file not found")

	// ErrPromptFailed indicates a SAM prompt operation failed.
	ErrPromptFailed = errors.New("arinfer: SAM prompt failed")

	// ErrDisabled indicates the AR inference feature is disabled.
	ErrDisabled = errors.New("arinfer: feature disabled")
)

// FrameFormat identifies the pixel format of GPU frames for preprocessing dispatch.
type FrameFormat int

const (
	// FormatNV12 is 8-bit 4:2:0 semi-planar (uint8 Y plane + interleaved UV).
	FormatNV12 FrameFormat = 0

	// FormatP210 is 10-bit 4:2:2 semi-planar (uint16 Y plane + interleaved UV).
	FormatP210 FrameFormat = 1
)

// Config holds initialization parameters for the inference engine.
type Config struct {
	// ModelsDir is the path to the directory containing model files.
	ModelsDir string

	// FrameFormat specifies the GPU frame pixel format for preprocessing.
	// Defaults to FormatNV12 (0) if not set.
	FrameFormat FrameFormat

	// DepthEnabled enables depth estimation on startup.
	DepthEnabled bool

	// SAMEnabled enables segmentation on startup.
	SAMEnabled bool

	// MaxInferenceMs is the maximum milliseconds per frame before skipping.
	// Defaults to 20 if zero.
	MaxInferenceMs int
}

// Status reports the current state of the inference engine.
type Status struct {
	DepthReady      bool    `json:"depthReady"`
	SAMReady        bool    `json:"samReady"`
	DepthLatencyMs  float64 `json:"depthLatencyMs"`
	SAMLatencyMs    float64 `json:"samLatencyMs"`
	FramesProcessed int64   `json:"framesProcessed"`
	FramesSkipped   int64   `json:"framesSkipped"`
	GPUMemUsedMB    float64 `json:"gpuMemUsedMB"`
}

// OcclusionMap holds GPU device pointers and metadata for the per-pixel
// occlusion confidence and depth maps produced by the inference engine.
type OcclusionMap struct {
	// MaskPtr is a CUDA device pointer to the uint8 per-pixel confidence map.
	MaskPtr uintptr

	// DepthPtr is a CUDA device pointer to the float16 depth map.
	DepthPtr uintptr

	// Width and Height are the occlusion map dimensions (matches input frame).
	Width  int
	Height int

	// DepthW and DepthH are the depth map native resolution dimensions.
	DepthW int
	DepthH int

	// Stale is true if this map was reused from a previous frame
	// (inference was skipped due to budget constraints).
	Stale bool
}

// SAMPrompt defines a user-specified segmentation prompt sent to SAM 2.
type SAMPrompt struct {
	// ID uniquely identifies this prompt.
	ID string `json:"id"`

	// Type is the prompt type: "point" or "box".
	Type string `json:"type"`

	// Points holds (x, y) coordinates for point prompts.
	Points [][2]int `json:"points,omitempty"`

	// Box holds bounding box coordinates as [x1, y1, x2, y2] for box prompts.
	Box [4]int `json:"box,omitempty"`

	// Label is a human-readable label for the segmented object.
	Label string `json:"label"`
}
