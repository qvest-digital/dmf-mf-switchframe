# Video Processing Pipeline Architecture

## Overview

The Switchframe video processing pipeline transforms decoded YUV420 frames
from source decoders into H.264 program output. It replaces the original
inline procedural processing with an explicit, node-based architecture that
enables runtime reconfiguration without frame drops.

The pipeline operates on a simple principle: **immutable once built, atomic
swap to reconfigure**. Each `Pipeline` instance is constructed on the main
goroutine, then executed on the dedicated video processing goroutine. When
configuration changes (compositor on/off, key add/remove, sink
connect/disconnect), a new pipeline is built and atomically swapped in. The
old pipeline drains in-flight frames in a background goroutine before
closing.

### Key Properties

- **Zero-allocation hot path**: `Pipeline.Run()` iterates a slice of active
  nodes. No maps, no channels, no interface dispatch beyond the node
  `Process()` call.
- **Single-writer timing**: Per-node nanosecond timing stored via atomic
  stores. Read safely by `Snapshot()` from any goroutine.
- **Active() filtering at build time**: Inactive nodes are excluded from the
  `activeNodes` slice during `Build()`, not checked per-frame. Zero overhead
  for disabled features.
- **In-place processing**: Most nodes modify the YUV buffer and return the
  same `ProcessingFrame`. No intermediate copies except where semantically
  required (raw sink deep-copy, encode output).
- **Pool-managed memory**: YUV buffers are acquired from and returned to a
  `FramePool` with LIFO free list, achieving >99% hit rate vs 19% with
  `sync.Pool`.

## Architecture Evolution

```
Before (implicit pipeline):
  broadcastProcessedFromPF()     ─ deep-copy, keying, compositing, enqueue
  enqueueVideoWork()             ─ async handoff via videoProcCh
  videoProcessingLoop()          ─ goroutine: dequeue + encode
  encodeAndBroadcastTransition() ─ raw sinks, encode, broadcast

After (explicit pipeline):
  broadcastProcessed()           ─ wrap YUV → ProcessingFrame, enqueue
  enqueueVideoWork()             ─ async handoff via videoProcCh (unchanged)
  videoProcessingLoop()          ─ goroutine: dequeue + Pipeline.Run()
  Pipeline.Run()                 ─ iterate activeNodes: key → comp → sinks → encode
```

The refactor was structured as independently shippable phases:

| Phase | Focus | Key Outcome |
|-------|-------|-------------|
| 0 | Memory fixes | AnnexB buffer reuse, GOMEMLIMIT, LockOSThread |
| 1 | FramePool | Mutex-guarded LIFO free list replacing sync.Pool |
| 2 | PipelineNode interface | 7-method contract for processing nodes |
| 3 | Pipeline builder | Pipeline struct with Build/Run/Snapshot |
| 4 | Atomic swap | Runtime reconfiguration without frame drops |
| 6 | Instrumentation | Per-node Prometheus histogram, lip-sync hint |


## Frame Flow

```
sourceDecoder (per-source, H.264 → YUV420)
  │
  ├─ FrameSynchronizer (optional, aligns multi-source timing)
  │   or
  ├─ delayBuffer (per-source, 0-500ms lip-sync correction)
  │
  ▼
handleRawVideoFrame()
  │
  ├─ health.recordFrame()
  ├─ updateFrameStats()
  ├─ fillIngestor → keyBridge (cache raw YUV for upstream keying)
  │
  ├─ [transition active] → TransitionEngine.IngestRawFrame()
  │   └─ blend output → broadcastProcessed() → enqueueVideoWork()
  │
  └─ [program source] → enqueueVideoWork()
          │
          ▼
     videoProcCh (buffered channel, 8 frames)
          │
          ▼
     videoProcessingLoop() goroutine
          │
          ▼
     Pipeline.Run(frame)
          │
          ├─ upstreamKeyNode   ─ chroma/luma keying (in-place)
          ├─ compositorNode    ─ DSK graphics overlay (in-place)
          ├─ rawSinkNode       ─ MXL output (deep-copy + callback)
          ├─ rawSinkNode       ─ raw program monitor (deep-copy + callback)
          └─ encodeNode        ─ H.264 encode → broadcastOwnedToProgram()
                                  │
                                  ▼
                            programRelay.BroadcastVideo()
```


## PipelineNode Interface

```go
type PipelineNode interface {
    Name() string                           // human-readable identifier
    Configure(PipelineFormat) error         // runs once at build time
    Active() bool                           // checked at build time to filter
    Process(dst, src *ProcessingFrame) *ProcessingFrame  // hot path
    Err() error                             // last error for monitoring
    Latency() time.Duration                 // estimated processing time
    Close() error                           // resource cleanup
}
```

### Design Rationale

**`Name()`** — Used as Prometheus histogram label (`node` dimension) and in
`Snapshot()` for debug endpoints. Must be stable across rebuilds.

**`Configure(PipelineFormat)`** — Runs on the main goroutine during
`Pipeline.Build()`. May allocate, acquire locks, or return errors. The
pipeline format (resolution, frame rate) is passed so nodes can prepare
format-dependent resources.

**`Active()`** — Checked once during `Build()` to filter the `activeNodes`
slice. Not checked per-frame. This enables zero-overhead bypass of disabled
features. Must be safe for concurrent reads (typically reads an atomic or
calls a lock-free predicate).

**`Process(dst, src)`** — The hot path. Called per-frame on the pipeline
goroutine (single-threaded). Contract: must not allocate, must not block,
must not acquire contested locks. In-place nodes modify `src.YUV` and return
`src`. The `dst` parameter is reserved for future nodes needing a separate
output buffer (e.g., scaling to a different resolution).

**`Err()`** — Returns the last error from `Process()`. Checked by monitoring
(debug snapshot), not on the hot path. Nodes log their own errors; this
provides a structured view for diagnostics.

**`Latency()`** — Estimated per-frame processing time. Summed across active
nodes to compute `Pipeline.TotalLatency()`, used for automatic lip-sync
compensation.

**`Close()`** — Resource cleanup. Called during pipeline drain after all
in-flight `Run()` calls complete.


## Node Implementations

### upstreamKeyNode

Wraps `graphics.KeyProcessorBridge.ProcessYUV()`. Applies per-source
chroma/luma keying to the program frame.

| Property | Value |
|----------|-------|
| File | `node_upstream_key.go` |
| Name | `"upstream-key"` |
| Active when | Bridge has enabled keys with cached fill frames |
| Processing | In-place: modifies `src.YUV` via `bridge.ProcessYUV()` |
| Latency | 100μs estimated |

### compositorNode

Wraps `graphics.Compositor.ProcessYUV()`. Overlays RGBA graphics (DSK) onto
the program frame.

| Property | Value |
|----------|-------|
| File | `node_compositor.go` |
| Name | `"compositor"` |
| Active when | `compositor.IsActive()` returns true |
| Processing | In-place: modifies `src.YUV` via `compositor.ProcessYUV()` |
| Latency | 200μs estimated |

### rawSinkNode

Wraps a `*atomic.Pointer[RawVideoSink]`. Deep-copies the frame and delivers
it to an external consumer (MXL output or raw program monitor). Two
instances are created with different names for observability.

| Property | Value |
|----------|-------|
| File | `node_raw_sink.go` |
| Names | `"raw-sink-mxl"`, `"raw-sink-monitor"` |
| Active when | Sink pointer is non-nil |
| Processing | Deep-copy via `src.DeepCopy()`, invoke sink callback |
| Latency | 50μs estimated |

### encodeNode

Wraps `pipelineCodecs.encode()`. Encodes the YUV420 frame to H.264 AVC1.
Always active — the encode step is mandatory for program output.

| Property | Value |
|----------|-------|
| File | `node_encode.go` |
| Name | `"h264-encode"` |
| Active when | Always |
| Processing | Encode via `codecs.encode()`, invoke `onEncoded` callback |
| Latency | 10ms estimated (hardware encoder) |
| Error tracking | `lastErr` atomic.Value for `Snapshot()` reads |
| Metrics | Observes `PipelineEncodeDuration`, increments `PipelineEncodeErrorsTotal` and `PipelineFramesProcessed` |

The encode node also handles force-IDR logic: if the source frame is a
keyframe or `forceNextIDR` is set (after a cut or transition start), the
encoder forces an IDR output.


## Pipeline Struct

```go
type Pipeline struct {
    allNodes    []PipelineNode    // full node list (for Close, reconfigure)
    activeNodes []PipelineNode    // filtered by Active() at build time
    format      PipelineFormat
    pool        *FramePool

    metrics     *metrics.Metrics  // Prometheus (optional, nil-safe)

    // Per-node timing (nanoseconds). Indexed by position in activeNodes.
    nodeTiming  []atomic.Int64    // last run duration
    nodeMaxNs   []atomic.Int64    // max duration since pipeline creation

    // Aggregate metrics
    totalLatency time.Duration    // sum of active nodes' Latency()
    runCount     atomic.Int64     // total Run() invocations
    lastRunNs    atomic.Int64     // most recent Run() total duration
    maxRunNs     atomic.Int64     // max Run() total duration

    epoch        uint64           // monotonically increasing, set at build time
    inflight     sync.WaitGroup   // in-flight Run() tracking for safe drain
}
```

### Build

`Pipeline.Build(format, pool, nodes)` runs on the main goroutine:

1. Calls `Configure(format)` on every node. Returns error if any fails.
2. Filters nodes where `Active()` returns true into `activeNodes`.
3. Sums active nodes' `Latency()` into `totalLatency`.
4. Allocates per-node atomic timing arrays sized to `len(activeNodes)`.

After `Build()`, `SetMetrics(m)` wires Prometheus (nil is safe). The
pipeline is then stored via `pipeline.Store(p)` or `swapPipeline(p)`.

### Run

`Pipeline.Run(frame)` runs on the video processing goroutine (single-threaded):

```
inflight.Add(1)
defer inflight.Done()

start := now()
for i, node := range activeNodes {
    t0 := now()
    frame = node.Process(nil, frame)
    dur := now() - t0
    nodeTiming[i].Store(dur)         // atomic — safe for Snapshot() reads
    updateAtomicMax(&nodeMaxNs[i], dur)
    if metrics != nil {
        metrics.NodeProcessDuration.WithLabelValues(node.Name()).Observe(dur)
    }
}
total := now() - start
lastRunNs.Store(total)
runCount.Add(1)
updateAtomicMax(&maxRunNs, total)
```

### Snapshot

`Pipeline.Snapshot()` returns a `map[string]any` for the debug endpoint:

```json
{
  "active_nodes": [
    {"name": "upstream-key", "last_ns": 45000, "max_ns": 120000, "latency_us": 100},
    {"name": "compositor",   "last_ns": 98000, "max_ns": 250000, "latency_us": 200},
    {"name": "h264-encode",  "last_ns": 8500000, "max_ns": 12000000, "latency_us": 10000,
     "last_error": "encoder returned nil frame"}
  ],
  "total_nodes": 5,
  "epoch": 3,
  "run_count": 14523,
  "last_run_ns": 8643000,
  "max_run_ns": 12370000,
  "total_latency_us": 10350,
  "lip_sync_hint_us": -10983
}
```

The `lip_sync_hint_us` value is `(totalLatency - aacFrameDuration)` where
`aacFrameDuration = 1024/48000 s ≈ 21.333ms`. A negative value means video
processing is faster than one AAC frame — audio leads video. A positive
value means video processing is slower — video leads audio.

### Wait and Close

`Pipeline.Wait()` blocks until all in-flight `Run()` calls complete (via
`sync.WaitGroup`).

`Pipeline.Close()` calls `Wait()` then closes all nodes (from `allNodes`,
not just `activeNodes`). Returns the first error encountered.


## FramePool

The `FramePool` replaces `sync.Pool` for YUV420 buffer management. The
original `sync.Pool` achieved only 19% hit rate because the GC drains pool
entries every ~570ms (with `GOGC=400`). The replacement uses a
mutex-guarded LIFO free list that is immune to GC drain.

```go
type FramePool struct {
    mu      sync.Mutex
    free    [][]byte    // stack of available buffers (LIFO for cache warmth)
    bufSize int         // YUV420 buffer size (width * height * 3/2)
    cap     int         // total capacity (pre-allocated count)
    hits    uint64      // diagnostic counters
    misses  uint64
}
```

### Sizing

At 1080p (1920×1080), each YUV420 buffer is `1920 × 1080 × 3/2 = 3,110,400
bytes` (~3MB). The pool pre-allocates 32 buffers (~97MB). The in-flight
budget accounts for:

| Consumer | Buffers |
|----------|---------|
| 4 source decoder outputs | 4 |
| DeepCopy in broadcastProcessed | 1 |
| videoProcCh (being encoded) | 1 |
| Raw sink copies (MXL + monitor) | 2 |
| Frame sync retained references | 2-3 |
| FRC retained frames | 2 |
| **Total typical** | **~12-14** |

The 32-buffer pool provides ~2x headroom. If all buffers are in use,
`Acquire()` falls back to `make()` (logged as a pool miss).

### Format Change Handling

When `SetPipelineFormat()` changes the resolution, a new `FramePool` is
created at the new dimensions. The old pool drains naturally — in-flight
frames release their buffers to the old pool, but wrong-sized buffers are
discarded by `Release()` (cap check). No explicit synchronization needed.

### ProcessingFrame Integration

`ProcessingFrame` carries a `pool *FramePool` reference:

- `DeepCopy()` acquires a buffer from the pool for the copy
- `ReleaseYUV()` returns the buffer to the pool after encode
- Both are nil-safe: falls back to `make()` / no-op for tests

```go
func (pf *ProcessingFrame) DeepCopy() *ProcessingFrame {
    cp := *pf
    if pf.pool != nil {
        cp.YUV = pf.pool.Acquire()
    } else {
        cp.YUV = make([]byte, len(pf.YUV))
    }
    copy(cp.YUV, pf.YUV)
    return &cp
}
```


## Atomic Swap

The core reconfiguration primitive is `swapPipeline()`:

```go
func (s *Switcher) swapPipeline(newPipeline *Pipeline) {
    old := s.pipeline.Swap(newPipeline)  // atomic.Pointer swap
    if old == nil {
        return
    }
    s.drainWg.Add(1)
    go func() {
        defer s.drainWg.Done()
        old.Wait()    // drain in-flight Run() calls
        old.Close()   // close all nodes
    }()
}
```

The `pipeline` field is an `atomic.Pointer[Pipeline]`. The swap is a single
atomic store. The old pipeline is drained and closed in a background
goroutine tracked by `drainWg` for orderly shutdown.

### Rebuild Triggers

Seven sources trigger `rebuildPipeline()`:

| Trigger | How |
|---------|-----|
| `SetCompositor(c)` | Direct call after storing compositor reference |
| `SetKeyBridge(kb)` | Direct call after storing key bridge reference |
| `SetRawVideoSink(sink)` | Direct call after atomic store |
| `SetRawMonitorSink(sink)` | Direct call after atomic store |
| Compositor state change | `OnStateChange(fn)` callback wired in `app.go` |
| Key processor change | `OnChange(fn)` callback wired in `app.go` |
| `SetPipelineFormat(f)` | Rebuilds with new format, pool, and frame budget |

`rebuildPipeline()` builds a fresh Pipeline from current state:

```go
func (s *Switcher) rebuildPipeline() {
    // 1. Capture state under read lock
    s.mu.RLock()
    hasPipeCodecs := s.pipeCodecs != nil
    prom := s.promMetrics
    nodes := s.buildNodeList()  // fresh node slice from current state
    s.mu.RUnlock()

    // 2. Build new pipeline (outside lock)
    p := &Pipeline{}
    p.Build(format, s.framePool, nodes)
    p.SetMetrics(prom)
    p.epoch = s.pipelineEpoch.Add(1)

    // 3. Atomic swap (old drains in background)
    s.swapPipeline(p)
}
```

### Pipeline Epoch

Each pipeline is assigned a monotonically increasing epoch at build time:

```go
p.epoch = s.pipelineEpoch.Add(1)
```

The epoch is exposed in `Pipeline.Snapshot()` for downstream consumers (SRT
output, recording, confidence monitor) to detect pipeline changes. This
enables responses like forcing a keyframe, starting a new recording segment,
or refreshing stream metadata.

### Shutdown

```go
// In Switcher.Close():
if p := s.pipeline.Swap(nil); p != nil {
    p.Wait()
    p.Close()
}
s.drainWg.Wait()  // wait for background drain goroutines
```

The pipeline is swapped to nil (preventing new `Run()` calls), then drained
and closed synchronously. `drainWg.Wait()` ensures all background drain
goroutines from previous swaps have completed.


## Instrumentation

### Per-Node Prometheus Histogram

```go
NodeProcessDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
    Namespace: "switchframe",
    Subsystem: "pipeline",
    Name:      "node_duration_seconds",
    Help:      "Per-node video processing duration.",
    Buckets:   []float64{0.00001, 0.0001, 0.001, 0.01, 0.1},
}, []string{"node"})
```

Bucket boundaries span 5 orders of magnitude (10μs to 100ms), covering:
- Upstream key / compositor: 50-200μs typical
- Raw sinks: 30-100μs (dominated by memcpy)
- H.264 encode: 5-15ms typical (hardware), 15-40ms (software)

The `node` label is the node's `Name()` string. Observation happens inside
`Pipeline.Run()` after each `Process()` call. Nil-safe: when `metrics` is
nil (e.g., in tests), no observation occurs.

### Lip-Sync Hint

The lip-sync hint quantifies the video-audio timing relationship:

```
lip_sync_hint = totalVideoLatency - aacFrameDuration
```

Where:
- `totalVideoLatency` = sum of active nodes' `Latency()` (estimated
  per-frame processing time)
- `aacFrameDuration` = 1024 samples / 48000 Hz = 21.333ms (one AAC-LC
  frame)

| Hint Value | Meaning |
|------------|---------|
| Negative | Video processing completes before one AAC frame is ready. Audio leads. |
| Zero | Video and audio are perfectly synchronized. |
| Positive | Video processing takes longer than one AAC frame. Video leads. |

The hint is logged at pipeline build time and exposed in `Snapshot()` for
the debug endpoint. It provides the raw data needed for automatic audio
delay compensation in future work.

### Build-Time Logging

Every pipeline build or rebuild logs its configuration:

```
INFO pipeline built  epoch=3  active_nodes=3  total_latency=10.35ms  lip_sync_hint=-10.983ms
```


## PipelineFormat

The `PipelineFormat` struct defines the global video pipeline format:

```go
type PipelineFormat struct {
    Width  int    // Horizontal resolution (e.g. 1920)
    Height int    // Vertical resolution (e.g. 1080)
    FPSNum int    // Frame rate numerator (e.g. 30000)
    FPSDen int    // Frame rate denominator (e.g. 1001)
    Name   string // Human-readable name (e.g. "1080p29.97")
}
```

Frame rate is expressed as a rational number for broadcast correctness
(e.g., 30000/1001 for 29.97fps NTSC). Standard presets cover 720p through
4K UHD at common broadcast frame rates. The format drives:

- FramePool buffer sizing (`Width × Height × 3/2`)
- Frame budget for deadline monitoring (`FPSDen × 1s / FPSNum`)
- Frame synchronizer tick rate
- Encoder bitrate/fps parameters
- Node `Configure()` calls (format-dependent resources)


## Performance Characteristics

### Memory Allocation Reduction

| Metric | Before (sync.Pool) | After (FramePool) |
|--------|--------------------|--------------------|
| YUV allocation rate | 546 MB/sec | ~50 MB/sec |
| GC frequency | 1.8/sec | <0.5/sec |
| Pool hit rate | 19% | >99% |

### Pipeline Overhead

The pipeline abstraction itself adds negligible overhead. Live profiling
shows keying, compositing, and blending consume **<0.3% of CPU** — below
the pprof sampling threshold. The dominant costs are:

- H.264 encode: 10.6% of CPU (hardware encoder) / ~30% (software)
- FRC motion estimation: 2.4% of CPU
- Pipeline node dispatch: unmeasurable (slice iteration + interface call)

### Timing Budget (1080p30)

| Node | Typical | Budget |
|------|---------|--------|
| upstream-key | 50-200μs | 33ms frame budget |
| compositor | 100-300μs | |
| raw-sink-mxl | 30-100μs (memcpy) | |
| raw-sink-monitor | 30-100μs (memcpy) | |
| h264-encode | 5-15ms (HW) / 15-40ms (SW) | |
| **Total** | **~6-16ms (HW)** | **33ms** |


## Future Evolution

### Automatic Lip-Sync Compensation

The lip-sync hint provides the raw data. Future work: feed the hint into the
audio mixer's per-channel delay buffer to automatically compensate for video
processing latency. When the pipeline is rebuilt (new nodes activated,
format change), the compensation adjusts automatically.

### Per-Destination Pipelines

Currently all output destinations share a single encode. Future work: allow
per-destination encoding parameters (bitrate, resolution, codec profile).
Each SRT destination could get its own `encodeNode` configured for the
target platform.

### Dynamic Node Insertion

The `PipelineNode` interface supports inserting new processing stages
(color correction, burn-in timecode, watermarking) without modifying
existing nodes. New nodes are added to `buildNodeList()` and participate in
the standard Build/Run/Swap lifecycle.

### Hardware Encoder Selection

The `Configure(PipelineFormat)` method provides a hook for nodes to select
hardware-specific resources at build time. Future work: `encodeNode` could
select between NVENC, VA-API, VideoToolbox, and software encoding based on
the format and available hardware, with automatic fallback on encoder
failure.

### Node DAG (Future)

The current pipeline is a linear chain. The research phase explored DAG
(directed acyclic graph) compositors used in VFX tools (Nuke, Fusion).
The linear chain was chosen for the initial implementation because the
current processing stages are inherently sequential (key before compositor
before encode). The `PipelineNode` interface is compatible with a future
DAG scheduler — nodes would declare input/output ports and the scheduler
would topologically sort and parallelize where possible.


## File Reference

| File | Purpose |
|------|---------|
| `switcher/pipeline_node.go` | `PipelineNode` interface definition |
| `switcher/pipeline_loop.go` | `Pipeline` struct: Build, Run, Snapshot, Wait, Close |
| `switcher/node_upstream_key.go` | Upstream chroma/luma key node |
| `switcher/node_compositor.go` | DSK graphics compositor node |
| `switcher/node_raw_sink.go` | Raw video sink node (MXL, monitor) |
| `switcher/node_encode.go` | H.264 encode node |
| `switcher/frame_pool.go` | FramePool: mutex-guarded LIFO free list |
| `switcher/processing_frame.go` | ProcessingFrame: YUV carrier with pool integration |
| `switcher/format.go` | PipelineFormat: resolution/fps presets |
| `switcher/pipeline_codecs.go` | Encoder-only codec pool (wrapped by encodeNode) |
| `switcher/switcher.go` | Integration: buildNodeList, BuildPipeline, swapPipeline, rebuildPipeline |
| `metrics/metrics.go` | NodeProcessDuration HistogramVec definition |
