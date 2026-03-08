package output

import (
	"bytes"
	"image"
	"image/jpeg"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

const (
	thumbWidth  = 320
	thumbHeight = 180
)

// ConfidenceMonitor generates low-rate JPEG thumbnails from program video
// for operator confidence monitoring. It decodes keyframes at most once
// per second, scales to thumbnail size, and stores the latest JPEG.
type ConfidenceMonitor struct {
	mu             sync.RWMutex
	inFlight       sync.WaitGroup
	latestJPEG     []byte
	lastUpdate     time.Time
	minInterval    time.Duration
	decoderFactory transition.DecoderFactory
	decoder        transition.VideoDecoder
}

// NewConfidenceMonitor creates a confidence monitor. The decoderFactory
// is used to create a video decoder for H.264 keyframe decoding.
// Pass nil for environments without codec support (thumbnail will be unavailable).
func NewConfidenceMonitor(decoderFactory transition.DecoderFactory) *ConfidenceMonitor {
	return &ConfidenceMonitor{
		minInterval:    time.Second, // 1fps
		decoderFactory: decoderFactory,
	}
}

// IngestVideo processes a video frame. Only keyframes are decoded, and
// at most one thumbnail is generated per minInterval.
func (cm *ConfidenceMonitor) IngestVideo(frame *media.VideoFrame) {
	if !frame.IsKeyframe {
		return
	}
	if cm.decoderFactory == nil {
		return
	}

	// Single critical section: rate-limit check + decoder init + stamp
	// lastUpdate. This prevents TOCTOU where two goroutines both pass
	// the rate-limit check and decode concurrently.
	cm.mu.Lock()
	if time.Since(cm.lastUpdate) < cm.minInterval {
		cm.mu.Unlock()
		return
	}
	if cm.decoder == nil {
		dec, err := cm.decoderFactory()
		if err != nil {
			cm.mu.Unlock()
			slog.Error("confidence monitor: failed to create decoder", "err", err)
			return
		}
		cm.decoder = dec
	}
	decoder := cm.decoder
	// Stamp now to prevent re-entry while we decode outside the lock.
	cm.lastUpdate = time.Now()
	cm.inFlight.Add(1)
	cm.mu.Unlock()
	defer cm.inFlight.Done()

	// Decode, scale, and JPEG-encode outside the lock to avoid blocking
	// the viewer goroutine or LatestThumbnail readers.
	annexB := codec.AVC1ToAnnexBInto(frame.WireData, nil)
	if frame.IsKeyframe {
		annexB = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, annexB, nil)
	}
	yuv, w, h, err := decoder.Decode(annexB)
	if err != nil {
		slog.Debug("confidence monitor: decode error", "err", err)
		return
	}

	// Scale to thumbnail size if needed
	dstW, dstH := thumbWidth, thumbHeight
	var scaledYUV []byte
	if w == dstW && h == dstH {
		scaledYUV = yuv
	} else {
		scaledYUV = make([]byte, dstW*dstH*3/2)
		transition.ScaleYUV420(yuv, w, h, scaledYUV, dstW, dstH)
	}

	// Convert YUV420 to RGB and build RGBA image via direct Pix access
	// (avoids per-pixel bounds checks from SetRGBA).
	rgb := make([]byte, dstW*dstH*3)
	transition.YUV420ToRGB(scaledYUV, dstW, dstH, rgb)

	img := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	pix := img.Pix
	for i := 0; i < dstW*dstH; i++ {
		off := i * 4
		pix[off+0] = rgb[i*3]
		pix[off+1] = rgb[i*3+1]
		pix[off+2] = rgb[i*3+2]
		pix[off+3] = 255
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		slog.Error("confidence monitor: JPEG encode error", "err", err)
		return
	}

	cm.mu.Lock()
	cm.latestJPEG = buf.Bytes()
	cm.mu.Unlock()
}

// LatestThumbnail returns the most recent JPEG thumbnail, or nil if none
// has been generated yet.
func (cm *ConfidenceMonitor) LatestThumbnail() []byte {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.latestJPEG
}

// Close releases decoder resources. Safe to call multiple times.
// After Close, IngestVideo becomes a no-op.
func (cm *ConfidenceMonitor) Close() {
	cm.mu.Lock()
	cm.decoderFactory = nil // prevent re-creation after close
	cm.mu.Unlock()

	// Wait for any in-flight decode operations to finish before
	// destroying the decoder, preventing use-after-close.
	cm.inFlight.Wait()

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.decoder != nil {
		cm.decoder.Close()
		cm.decoder = nil
	}
}
