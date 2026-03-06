package output

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
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

	cm.mu.Lock()
	if time.Since(cm.lastUpdate) < cm.minInterval {
		cm.mu.Unlock()
		return
	}
	cm.mu.Unlock()

	if cm.decoderFactory == nil {
		return
	}

	// Lazy-init decoder
	cm.mu.Lock()
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
	cm.mu.Unlock()

	// Decode keyframe to YUV420
	yuv, w, h, err := decoder.Decode(frame.WireData)
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

	// Convert YUV420 to RGB
	rgb := make([]byte, dstW*dstH*3)
	transition.YUV420ToRGB(scaledYUV, dstW, dstH, rgb)

	// Create image.RGBA from RGB
	img := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for i := 0; i < dstW*dstH; i++ {
		img.SetRGBA(i%dstW, i/dstW, color.RGBA{
			R: rgb[i*3],
			G: rgb[i*3+1],
			B: rgb[i*3+2],
			A: 255,
		})
	}

	// JPEG encode
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		slog.Error("confidence monitor: JPEG encode error", "err", err)
		return
	}

	cm.mu.Lock()
	cm.latestJPEG = buf.Bytes()
	cm.lastUpdate = time.Now()
	cm.mu.Unlock()
}

// LatestThumbnail returns the most recent JPEG thumbnail, or nil if none
// has been generated yet.
func (cm *ConfidenceMonitor) LatestThumbnail() []byte {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.latestJPEG
}

// Close releases decoder resources.
func (cm *ConfidenceMonitor) Close() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.decoder != nil {
		cm.decoder.Close()
		cm.decoder = nil
	}
}
