package arinfer

// Inferrer is the interface for AR inference engines that produce
// occlusion maps (depth + segmentation) for AR graphics compositing.
// Implemented by Engine (in-process TensorRT) and InferenceProxy
// (sidecar delegation via CUDA IPC).
//
// The switcher's program frame callback invokes ProcessFrame to feed
// frames into the inference pipeline. The AR compositor pipeline node
// reads LastOcclusion for the latest result (lock-free atomic read).
type Inferrer interface {
	// ProcessFrame runs depth + SAM inference on the current program frame.
	// Called from the switcher's frame routing with the GPU device pointer.
	ProcessFrame(framePtr uintptr, w, h, pitch int)

	// LastOcclusion returns the most recently computed occlusion map.
	// Returns nil if no inference has been performed. Lock-free.
	LastOcclusion() *OcclusionMap

	// IsActive returns true if any inference model is loaded.
	IsActive() bool

	// Hook returns a ProcessFrameHook for wiring into the switcher.
	Hook() ProcessFrameHook

	// EnableDepth loads and enables the depth estimation model.
	EnableDepth(planPath string) error

	// DisableDepth tears down the depth estimation model.
	DisableDepth()

	// EnableSAM loads and enables the SAM 2 segmentation model.
	EnableSAM(encoderPlanPath, decoderPlanPath string) error

	// DisableSAM tears down the SAM 2 segmentation model.
	DisableSAM()

	// AddPrompt registers a new segmentation prompt with the SAM model.
	AddPrompt(p SAMPrompt) error

	// RemovePrompt removes a segmentation prompt by ID.
	RemovePrompt(id string) error

	// Prompts returns a copy of the active SAM prompts.
	Prompts() []SAMPrompt

	// Status returns a snapshot of the engine's current state.
	Status() Status

	// DepthAtPixel returns the depth value at the given pixel coordinates.
	DepthAtPixel(x, y, frameW, frameH int) (float64, error)

	// Close shuts down the engine and releases all resources.
	Close()
}

// Compile-time check that Engine implements Inferrer.
var _ Inferrer = (*Engine)(nil)
