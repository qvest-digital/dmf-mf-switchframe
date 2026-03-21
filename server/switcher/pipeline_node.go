package switcher

import "time"

// AsyncMetricsProvider is optionally implemented by pipeline nodes that
// perform work asynchronously (after Process() returns). The returned map
// is merged into the node's Snapshot() entry, giving debug tools access to
// the real work duration instead of the near-zero enqueue time measured by
// the pipeline loop.
type AsyncMetricsProvider interface {
	AsyncMetrics() map[string]any
}

// GPUPipelineRunner is the interface for running the full GPU video pipeline.
// The implementation lives in the gpu package (wrapped by the app layer).
// When set on the Switcher, frames are routed through the GPU pipeline
// instead of the CPU PipelineNode chain.
type GPUPipelineRunner interface {
	// RunWithUpload uploads a CPU YUV420p frame to GPU, runs all GPU nodes
	// (key, layout, compositor, stmap, raw sinks, encode), releases the GPU
	// frame, and returns. The encode callback has already been called with
	// the H.264 output by the time this returns.
	RunWithUpload(yuv []byte, width, height int, pts int64) error

	// RunFromCache retrieves a pre-uploaded GPU frame from the source cache
	// (GPUSourceManager), copies it to a pipeline frame, and runs the GPU
	// pipeline — skipping the CPU→GPU upload entirely. Returns an error if
	// the source has no cached frame, in which case the caller should fall
	// back to RunWithUpload.
	RunFromCache(sourceKey string, pts int64) error

	// RunTransition blends two source frames on GPU and runs the result
	// through the rest of the GPU pipeline (key → layout → compositor →
	// stmap → raw sinks → encode). Both source frames are read from the
	// GPU source cache. transType is "mix", "dip", "wipe", "ftb",
	// "ftb_reverse", or "stinger". wipeDir is an int matching gpu.WipeDirection.
	// position is 0.0 (all A) to 1.0 (all B). stingerAlpha carries
	// the per-pixel alpha plane for stinger transitions (nil otherwise).
	// Returns an error if either source frame is not cached, in which case
	// the caller should fall back to the CPU transition path.
	RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stingerAlpha []byte) error
}

// GPUSourceManagerIface provides GPU source frame management.
// Implemented by gpu.GPUSourceManager in the app layer.
// When set on the Switcher, handleRawVideoFrame routes YUV frames through
// GPU upload + ST map + cache instead of CPU fill paths (IngestFillYUV,
// IngestSourceFrame).
type GPUSourceManagerIface interface {
	IngestYUV(sourceKey string, yuv []byte, w, h int, pts int64)
}

// PipelineNode is the fundamental processing unit in the video pipeline.
//
// Lifecycle:
//   - Configure() runs once when the pipeline is built or reconfigured.
//     May allocate, acquire locks, or fail. Runs on main goroutine.
//   - Process() runs on every frame on the pipeline goroutine.
//     Must not allocate, must not block, must not acquire contested locks.
//   - Active() is checked during pipeline build to filter inactive nodes.
//     Must be safe for concurrent reads (atomic or lock-free).
//
// Contract: Process receives src (current frame). In-place nodes modify src
// and return it. Passthrough nodes return src unmodified. The dst parameter
// is reserved for future nodes needing a separate output buffer (e.g.,
// scaling to different resolution).
type PipelineNode interface {
	// Name returns a human-readable identifier for debugging and metrics.
	Name() string

	// Configure is called once when the pipeline is built or reconfigured.
	// Receives the pipeline format. Returns error if the node cannot operate.
	Configure(format PipelineFormat) error

	// Active returns whether this node should be included in processing.
	// Inactive nodes are skipped entirely (zero overhead). Must be safe
	// for concurrent reads.
	Active() bool

	// Process transforms the frame. Called per-frame on pipeline goroutine.
	// Must not allocate, must not block.
	// Returns output frame (src for in-place, dst if separate buffer used).
	Process(dst, src *ProcessingFrame) *ProcessingFrame

	// Err returns the last error from Process(), or nil. Checked by
	// monitoring, not on hot path. Nodes log their own errors.
	Err() error

	// Latency reports estimated per-frame processing time. Used for
	// pipeline latency reporting and automatic lip-sync calculation.
	Latency() time.Duration

	// Close releases resources held by this node.
	Close() error
}
