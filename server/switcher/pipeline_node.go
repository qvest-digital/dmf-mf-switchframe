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
