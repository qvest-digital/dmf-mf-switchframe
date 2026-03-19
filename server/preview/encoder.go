// Package preview provides per-source preview encoding for browser multiview.
//
// Each source gets its own Encoder goroutine that scales incoming raw YUV420
// frames to a lower resolution and encodes them with a lightweight x264 preset.
// The encoded H.264 stream is broadcast to a per-source MoQ relay so browsers
// can subscribe to individual preview feeds.
//
// The Encoder uses a newest-wins drop policy: if the encode goroutine falls
// behind, older frames are silently discarded so the preview always shows the
// most recent frame. This is the correct behavior for a monitoring preview --
// latency matters more than every-frame delivery.
package preview

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// Relay is the subset of distribution.Relay used by the preview encoder.
type Relay interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
	SetVideoInfo(info distribution.VideoInfo)
}

// Config configures a preview encoder instance.
type Config struct {
	SourceKey     string // Source identifier (e.g. "cam1", "srt:feed1")
	Width         int    // Preview output width (e.g. 854)
	Height        int    // Preview output height (e.g. 480)
	Bitrate       int    // Target bitrate in bps (e.g. 500_000)
	FPSNum        int    // Frame rate numerator (e.g. 30)
	FPSDen        int    // Frame rate denominator (e.g. 1)
	Relay         Relay  // MoQ relay for broadcast
	FrameInterval int    // Encode every Nth frame (1=all, 2=half rate, etc). 0 treated as 1.
}

// Stats tracks preview encoder performance counters using atomic operations.
type Stats struct {
	FramesIn      atomic.Int64
	FramesOut     atomic.Int64
	FramesDropped atomic.Int64
	EncodeErrors  atomic.Int64
	LastEncodeNs  atomic.Int64 // last encode duration in nanoseconds
	AvgEncodeNs   atomic.Int64 // exponential moving average
}

// StatsSnapshot is the JSON-serializable view of encoder stats.
type StatsSnapshot struct {
	FramesIn      int64   `json:"framesIn"`
	FramesOut     int64   `json:"framesOut"`
	FramesDropped int64   `json:"framesDropped"`
	EncodeErrors  int64   `json:"encodeErrors"`
	LastEncodeMs  float64 `json:"lastEncodeMs"`
	AvgEncodeMs   float64 `json:"avgEncodeMs"`
}

// encodeJob carries a YUV420 frame to the encode goroutine.
type encodeJob struct {
	yuv     []byte
	w       int
	h       int
	pts     int64
	release func([]byte) // optional: called after yuv is no longer needed
}

// Encoder scales and encodes preview frames for a single source.
// Each source gets its own Encoder instance -- they run completely
// independently with no shared state.
type Encoder struct {
	cfg           Config
	ch            chan encodeJob
	done          chan struct{}
	stopOnce      sync.Once
	stopped       atomic.Bool
	forceIDR      atomic.Bool
	stats         Stats
	frameInterval int // resolved from Config (min 1)
}

// ForceKeyframe forces the next encoded frame to be an IDR keyframe.
// Call on source cuts to prevent P-frame artifacts from the old scene
// smearing into the new one.
func (e *Encoder) ForceKeyframe() {
	e.forceIDR.Store(true)
}

// NewEncoder creates and starts a preview encoder goroutine.
// The goroutine runs until Stop() is called.
func NewEncoder(cfg Config) (*Encoder, error) {
	frameInterval := cfg.FrameInterval
	if frameInterval < 1 {
		frameInterval = 1
	}
	e := &Encoder{
		cfg:           cfg,
		ch:            make(chan encodeJob, 4),
		done:          make(chan struct{}),
		frameInterval: frameInterval,
	}
	go e.loop()
	return e, nil
}

// Send submits a raw YUV420 frame for preview encoding.
// Non-blocking with newest-wins drop policy: if the channel is full,
// the oldest frame is drained and replaced with the new one.
// The YUV data is deep-copied so the caller can reuse or release the buffer.
func (e *Encoder) Send(yuv []byte, w, h int, pts int64) {
	if e.stopped.Load() {
		return
	}

	e.stats.FramesIn.Add(1)

	// Deep copy -- caller may reuse the buffer.
	cp := make([]byte, len(yuv))
	copy(cp, yuv)

	job := encodeJob{yuv: cp, w: w, h: h, pts: pts}

	// Guard against channel close race with Stop().
	defer func() { _ = recover() }()

	select {
	case e.ch <- job:
		return
	default:
	}

	// Channel full -- drain one (newest-wins) and send.
	e.stats.FramesDropped.Add(1)
	select {
	case dropped := <-e.ch:
		// Release the dropped frame's buffer if it was submitted via SendOwned.
		if dropped.release != nil {
			dropped.release(dropped.yuv)
		}
	default:
	}
	select {
	case e.ch <- job:
	default:
		// Channel closed or race -- drop silently.
	}
}

// SendOwned submits a raw YUV420 frame for preview encoding, taking
// ownership of the buffer (no deep copy). The optional release callback
// is called after the buffer is no longer needed (after scaling), or
// immediately if the frame is dropped due to channel overflow.
func (e *Encoder) SendOwned(yuv []byte, w, h int, pts int64, release func([]byte)) {
	if e.stopped.Load() {
		if release != nil {
			release(yuv)
		}
		return
	}

	e.stats.FramesIn.Add(1)

	job := encodeJob{yuv: yuv, w: w, h: h, pts: pts, release: release}

	// Guard against channel close race with Stop().
	defer func() {
		if r := recover(); r != nil {
			if release != nil {
				release(yuv)
			}
		}
	}()

	// Try non-blocking send.
	select {
	case e.ch <- job:
		return
	default:
	}

	// Channel full -- drain one (newest-wins) and send.
	e.stats.FramesDropped.Add(1)
	select {
	case dropped := <-e.ch:
		// Release the dropped frame's buffer.
		if dropped.release != nil {
			dropped.release(dropped.yuv)
		}
	default:
	}
	select {
	case e.ch <- job:
	default:
		// Channel closed or race -- release the buffer we failed to send.
		if release != nil {
			release(yuv)
		}
	}
}

// Stop signals the encode goroutine to exit and waits for it to finish.
// Safe to call multiple times.
func (e *Encoder) Stop() {
	e.stopOnce.Do(func() {
		e.stopped.Store(true)
		close(e.ch)
	})
	<-e.done
}

// loop is the encode goroutine. It reads from the channel, scales, encodes,
// and broadcasts to the relay. All encoder/buffer state is goroutine-local.
func (e *Encoder) loop() {
	defer close(e.done)

	var (
		encoder   transition.VideoEncoder
		groupID   atomic.Uint32
		infoSent  bool
		scaledYUV []byte // persistent scale buffer (reused across frames)
		encYUV    []byte // persistent encoder input buffer (reused)
		boxBuf    []byte // persistent box-shrink intermediate (amd64 only)
		lastSrcW  int
		lastSrcH  int
	)

	targetW := e.cfg.Width
	targetH := e.cfg.Height
	targetSize := targetW * targetH * 3 / 2

	var frameCount int64

	for job := range e.ch {
		w, h, pts := job.w, job.h, job.pts

		// Frame skip: only encode every Nth frame.
		frameCount++
		if e.frameInterval > 1 && frameCount%int64(e.frameInterval) != 1 {
			if job.release != nil {
				job.release(job.yuv)
			}
			continue
		}

		// Source resolution changed -- recreate encoder.
		if encoder != nil && (w != lastSrcW || h != lastSrcH) {
			encoder.Close()
			encoder = nil
			infoSent = false
		}
		lastSrcW = w
		lastSrcH = h

		// Lazy encoder creation.
		if encoder == nil {
			enc, err := codec.NewPreviewEncoder(targetW, targetH, e.cfg.Bitrate, e.cfg.FPSNum, e.cfg.FPSDen)
			if err != nil {
				slog.Error("preview: encoder init failed",
					"key", e.cfg.SourceKey, "error", err)
				if job.release != nil {
					job.release(job.yuv)
				}
				continue
			}
			encoder = enc
		}

		// Scale to target resolution if needed.
		var frameYUV []byte
		if w == targetW && h == targetH {
			// No scaling needed -- use directly.
			frameYUV = job.yuv
		} else {
			// Grow scaledYUV buffer if needed (reused across frames).
			if cap(scaledYUV) < targetSize {
				scaledYUV = make([]byte, targetSize)
			}
			scaledYUV = scaledYUV[:targetSize]
			transition.ScaleYUV420Preview(job.yuv, w, h, scaledYUV, targetW, targetH, &boxBuf)
			frameYUV = scaledYUV
		}

		// Copy into encoder buffer (encoder may hold a reference).
		if cap(encYUV) < targetSize {
			encYUV = make([]byte, targetSize)
		}
		encYUV = encYUV[:targetSize]
		copy(encYUV, frameYUV)

		// Release the source buffer now that it's been copied/scaled.
		if job.release != nil {
			job.release(job.yuv)
		}

		// Encode. Force IDR if requested (source cut).
		forceIDR := e.forceIDR.Swap(false)
		encStart := time.Now()
		encoded, isKeyframe, err := encoder.Encode(encYUV, pts, forceIDR)
		encDur := time.Since(encStart).Nanoseconds()
		e.stats.LastEncodeNs.Store(encDur)
		// EMA: avg = avg*0.9 + sample*0.1
		oldAvg := e.stats.AvgEncodeNs.Load()
		if oldAvg == 0 {
			e.stats.AvgEncodeNs.Store(encDur)
		} else {
			newAvg := int64(float64(oldAvg)*0.9 + float64(encDur)*0.1)
			e.stats.AvgEncodeNs.Store(newAvg)
		}
		if err != nil || len(encoded) == 0 {
			if err != nil {
				e.stats.EncodeErrors.Add(1)
			}
			continue
		}
		e.stats.FramesOut.Add(1)

		// Convert Annex B -> AVC1 for MoQ wire format.
		// SPS/PPS are inline on keyframes (no GLOBAL_HEADER flag).
		avc1 := codec.AnnexBToAVC1(encoded)
		if len(avc1) == 0 {
			continue
		}

		if isKeyframe {
			groupID.Add(1)
		}

		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
			GroupID:    groupID.Load(),
		}

		// Extract SPS/PPS from AVC1 data for the frame metadata.
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7: // SPS
					frame.SPS = nalu
				case 8: // PPS
					frame.PPS = nalu
				}
			}

			// Set VideoInfo on first keyframe so browsers can configure decoder.
			if !infoSent && frame.SPS != nil && frame.PPS != nil {
				avcC := moq.BuildAVCDecoderConfig(frame.SPS, frame.PPS)
				if avcC != nil {
					e.cfg.Relay.SetVideoInfo(distribution.VideoInfo{
						Codec:         codec.ParseSPSCodecString(frame.SPS),
						Width:         targetW,
						Height:        targetH,
						DecoderConfig: avcC,
					})
					slog.Info("preview: VideoInfo set",
						"key", e.cfg.SourceKey,
						"width", targetW,
						"height", targetH,
					)
					infoSent = true
				}
			}
		}

		e.cfg.Relay.BroadcastVideo(frame)
	}

	// Channel closed -- clean up encoder.
	if encoder != nil {
		encoder.Close()
	}
}

// GetStats returns a JSON-friendly snapshot of encoder performance metrics.
func (e *Encoder) GetStats() StatsSnapshot {
	return StatsSnapshot{
		FramesIn:      e.stats.FramesIn.Load(),
		FramesOut:     e.stats.FramesOut.Load(),
		FramesDropped: e.stats.FramesDropped.Load(),
		EncodeErrors:  e.stats.EncodeErrors.Load(),
		LastEncodeMs:  float64(e.stats.LastEncodeNs.Load()) / 1e6,
		AvgEncodeMs:   float64(e.stats.AvgEncodeNs.Load()) / 1e6,
	}
}

// DebugSnapshot implements debug.SnapshotProvider for registration
// with the debug collector.
func (e *Encoder) DebugSnapshot() map[string]any {
	s := e.GetStats()
	return map[string]any{
		"framesIn":      s.FramesIn,
		"framesOut":     s.FramesOut,
		"framesDropped": s.FramesDropped,
		"encodeErrors":  s.EncodeErrors,
		"lastEncodeMs":  s.LastEncodeMs,
		"avgEncodeMs":   s.AvgEncodeMs,
	}
}
