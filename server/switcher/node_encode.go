package switcher

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/metrics"
)

var _ PipelineNode = (*encodeNode)(nil)
var _ AsyncMetricsProvider = (*encodeNode)(nil)

// encodeWork holds a frame and encode parameters for async processing,
// or, with repeat set and no frame, a repeat of the last program frame at
// repeatPTS.
type encodeWork struct {
	frame     *ProcessingFrame
	forceIDR  bool
	repeat    bool
	repeatPTS int64
}

type encodeNode struct {
	codecs      *pipelineCodecs
	forceIDR    *atomic.Bool
	promMetrics *metrics.Metrics
	lastErr     atomic.Value // stores error; safe for concurrent Snapshot() reads

	// Output callback -- called with encoded H.264 frame.
	onEncoded func(frame *media.VideoFrame)
	// onRepeat sends the last program frame again at a shed tick's PTS.
	// It runs on the encode goroutine, after every frame queued before
	// the repeat has come out.
	onRepeat func(pts int64)

	// Diagnostic counter for hardware encoder warmup (nil frame returns)
	encodeNilCount *atomic.Int64

	// Async encode goroutine. When encodeCh is non-nil, Process() enqueues
	// work and returns immediately — the encode happens in a background
	// goroutine. This decouples encode latency from the pipeline loop,
	// allowing raw sinks (which run before encode) to deliver frames at
	// full rate even when encode is slow (e.g., stinger transitions).
	encodeCh  chan encodeWork
	wg        sync.WaitGroup
	closeOnce sync.Once

	// pred, when non-nil, receives the encode node this one follows on
	// the program, nil for none. The goroutine sends nothing before that
	// node's goroutine has ended: both feed the one program, and every
	// item left in the old queue is earlier than any in this one. done
	// closes when this node's goroutine ends.
	pred chan *encodeNode
	done chan struct{}

	// Diagnostic counter: frames dropped because the encoder goroutine
	// was still busy with the previous frame.
	encodeDropCount *atomic.Int64

	// Async encode timing — written by encodeLoop, read by AsyncMetrics().
	lastEncodeNs atomic.Int64 // most recent real encode duration (nanoseconds)
	maxEncodeNs  atomic.Int64 // peak encode duration (nanoseconds)
	encodeTotal  atomic.Int64 // total frames encoded successfully

	// encoder10bit is true when the encoder accepts YUV422P10LE input directly
	// (e.g., HEVC main422-10 or main10 profile). When true, 10-bit frames are
	// passed to the encoder without downconversion.
	encoder10bit bool

	// downconvertBuf is a preallocated buffer for 10-bit→8-bit conversion
	// when the encoder expects YUV420 8-bit. Allocated lazily on first use.
	downconvertBuf []byte
}

func (n *encodeNode) Name() string                          { return "h264-encode" }
func (n *encodeNode) Configure(format PipelineFormat) error { return nil }
func (n *encodeNode) Active() bool                          { return true }
func (n *encodeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}
func (n *encodeNode) Latency() time.Duration { return 10 * time.Millisecond }

// AsyncMetrics returns real encode timing measured inside the async goroutine.
// This is the actual H.264 encode wall time, not the near-zero Process() enqueue time.
func (n *encodeNode) AsyncMetrics() map[string]any {
	m := map[string]any{
		"encode_last_ns": n.lastEncodeNs.Load(),
		"encode_max_ns":  n.maxEncodeNs.Load(),
		"encode_total":   n.encodeTotal.Load(),
	}
	if n.encodeCh != nil {
		m["encode_queue_len"] = len(n.encodeCh)
	} else {
		m["encode_queue_len"] = 0
	}
	return m
}

// start launches the async encode goroutine. Must be called before Process()
// for async operation. If not called, Process() falls back to synchronous encode.
func (n *encodeNode) start() {
	// Buffer of 4: absorbs encode latency spikes without dropping.
	// At 30fps (33ms budget), this allows bursts where multiple frames
	// arrive before the encoder finishes the current one. Keeps latency
	// bounded to ~3 extra frames (100ms) in the worst case. Previously
	// 2, but software x264 on single-core VPS dropped 3-4% of frames.
	n.encodeCh = make(chan encodeWork, 4)
	n.done = make(chan struct{})
	n.wg.Add(1)
	go n.encodeLoop()
}

// Close stops the async encode goroutine and waits for pending work to drain.
// Safe to call multiple times (idempotent via sync.Once).
//
// Thread safety with Process(): Pipeline.Close() calls inflight.Wait() before
// closing nodes, guaranteeing no concurrent Process() call is in progress when
// Close() runs. This is enforced by the Pipeline.Run()/Close() contract.
func (n *encodeNode) Close() error {
	n.closeOnce.Do(func() {
		// A node never placed on the program follows nothing.
		n.follow(nil)
		if n.encodeCh != nil {
			close(n.encodeCh)
			n.wg.Wait()
		}
	})
	return nil
}

// encodeLoop processes encode work items from the channel.
// Runs in a dedicated goroutine for the lifetime of the node.
func (n *encodeNode) encodeLoop() {
	defer n.wg.Done()
	defer close(n.done)
	if n.pred != nil {
		if prev := <-n.pred; prev != nil {
			<-prev.done
		}
	}
	for work := range n.encodeCh {
		if work.repeat {
			n.onRepeat(work.repeatPTS)
			continue
		}
		n.processWorkItem(work)
	}
}

// follow hands the node the encode node it replaces on the program, nil
// for none. Only the first call counts; a node without pred ignores it.
func (n *encodeNode) follow(prev *encodeNode) {
	if n.pred == nil {
		return
	}
	select {
	case n.pred <- prev:
	default:
	}
}

// processWorkItem encodes a single frame with panic recovery.
// A panic in the encoder (e.g., cgo FFmpeg crash) must not kill the
// goroutine — that would silently disable all H.264 output.
func (n *encodeNode) processWorkItem(work encodeWork) {
	defer func() {
		if r := recover(); r != nil {
			n.lastErr.Store(fmt.Errorf("encode panic: %v", r))
			slog.Error("encode goroutine recovered from panic",
				"panic", r,
				"stack", string(debug.Stack()))
			// Invalidate the encoder — a panic (e.g., cgo crash) likely
			// left it in a corrupt state. Force recreation on next frame.
			n.codecs.invalidateEncoder()
		}
		work.frame.ReleaseYUV() // always release the async ref
	}()
	n.doEncode(work.frame, work.forceIDR)
}

// doEncode performs the actual encode with timing, metrics, and error handling.
// Called from processWorkItem (async) or Process (sync fallback).
//
// When the frame is 10-bit (YUV422P10LE) and the encoder accepts 10-bit input
// (e.g., HEVC main422-10 profile), the frame is passed directly. Otherwise,
// it is downconverted to 8-bit (YUV420P) before encoding. The downconvert
// buffer is reused across frames to avoid per-frame allocation.
func (n *encodeNode) doEncode(src *ProcessingFrame, forceIDR bool) {
	encSrc := src
	if src.Format == ColorYUV422_10bit && !n.encoder10bit {
		// Encoder expects 8-bit input — downconvert.
		dstSize := ColorYUV420_8bit.FrameSize(src.Width, src.Height)
		if len(n.downconvertBuf) < dstSize {
			n.downconvertBuf = make([]byte, dstSize)
		}
		if err := Downconvert422_10to420(src.Data, n.downconvertBuf[:dstSize], src.Width, src.Height); err == nil {
			encSrc = &ProcessingFrame{
				Data:       n.downconvertBuf[:dstSize],
				Format:     ColorYUV420_8bit,
				Width:      src.Width,
				Height:     src.Height,
				PTS:        src.PTS,
				DTS:        src.DTS,
				IsKeyframe: src.IsKeyframe,
				GroupID:    src.GroupID,
				Codec:      src.Codec,
			}
		}
		// If downconvert fails, fall through and let the encoder try with the original data.
	}

	encStart := time.Now().UnixNano()
	frame, err := n.codecs.encode(encSrc, forceIDR)
	encDur := time.Now().UnixNano() - encStart

	// Store real encode timing for debug snapshot (AsyncMetrics).
	n.lastEncodeNs.Store(encDur)
	atomicutil.UpdateMax(&n.maxEncodeNs, encDur)

	if n.promMetrics != nil {
		n.promMetrics.PipelineEncodeDuration.Observe(float64(encDur) / 1e9)
	}

	if err != nil {
		n.lastErr.Store(err)
		if n.promMetrics != nil {
			n.promMetrics.PipelineEncodeErrorsTotal.Inc()
		}
		return
	}
	if frame == nil {
		if n.encodeNilCount != nil {
			n.encodeNilCount.Add(1)
		}
		return
	}

	n.encodeTotal.Add(1)

	if n.promMetrics != nil {
		n.promMetrics.PipelineFramesProcessed.Inc()
	}
	if n.onEncoded != nil {
		n.onEncoded(frame)
	}
}

// queueRepeat queues a repeat of the last program frame at pts behind the
// frames already queued, so it reaches the program after them. It reports
// false when the node has no async goroutine, and the caller repeats
// itself. A repeat that finds the queue full is dropped like a frame the
// encoder is too busy for, and the tick goes without a frame.
func (n *encodeNode) queueRepeat(pts int64) bool {
	if n.encodeCh == nil || n.onRepeat == nil {
		return false
	}
	select {
	case n.encodeCh <- encodeWork{repeat: true, repeatPTS: pts}:
	default:
		if n.encodeDropCount != nil {
			n.encodeDropCount.Add(1)
		}
	}
	return true
}

func (n *encodeNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	forceIDR := src.IsKeyframe
	if n.forceIDR != nil {
		forceIDR = forceIDR || n.forceIDR.CompareAndSwap(true, false)
	}

	// Async path: enqueue work for the background goroutine. The queue
	// orders the program: every frame and repeat goes through it while the
	// goroutine runs, as one encoded here would overtake those queued. An
	// unmanaged frame (refs == nil, such as broadcast black) cannot be
	// Ref()'d past this call, so the queue gets a managed copy of it.
	if n.encodeCh != nil {
		work := src
		if src.Refs() > 0 {
			src.Ref() // +1 for async encode goroutine
		} else {
			work = managedCopy(src)
		}
		select {
		case n.encodeCh <- encodeWork{frame: work, forceIDR: forceIDR}:
			// queued for async encode
		default:
			// Encoder goroutine still busy — drop this frame from H.264 output.
			// Raw YUV sinks already received this frame (they run before encode
			// in the pipeline). H.264 viewers will experience the same drop they
			// would have seen from videoProcCh backpressure before this change.
			work.ReleaseYUV()
			if forceIDR && n.forceIDR != nil {
				// Re-arm so the next frame carries the IDR request.
				n.forceIDR.Store(true)
			}
			if n.encodeDropCount != nil {
				n.encodeDropCount.Add(1)
			}
		}
		return src
	}

	// Synchronous: no start() called, as in tests.
	n.doEncode(src, forceIDR)
	return src
}

// managedCopy returns a reference-counted copy of an unmanaged frame that
// owns its data, for a consumer that outlives the caller's hold on it.
func managedCopy(src *ProcessingFrame) *ProcessingFrame {
	cp := *src
	cp.Data = append([]byte(nil), src.Data...)
	cp.pool = nil
	cp.GPUData = nil
	cp.refs = nil
	cp.SetRefs(1)
	return &cp
}
