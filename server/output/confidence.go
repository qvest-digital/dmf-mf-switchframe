package output

import (
	"bytes"
	"image"
	"image/jpeg"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/video"
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
	decoding       bool // true while a Decode is in flight; blocks concurrent Decode on the shared decoder

	decodeErrors atomic.Int64

	// bufMu serializes access to the cached intermediate buffers below.
	// In production the rate limiter (minInterval=1s) ensures at most one
	// IngestVideo is active, so this mutex virtually never contends.
	bufMu     sync.Mutex
	scaledBuf []byte
	rgbBuf    []byte
	rgbaBuf   *image.RGBA
	jpegBuf   bytes.Buffer
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

	// Single critical section: rate-limit check + decoder init + stamp
	// lastUpdate. This prevents TOCTOU where two goroutines both pass
	// the rate-limit check and decode concurrently.
	// The decoderFactory nil check must be inside the lock because Close()
	// sets it to nil under the lock — checking outside would race.
	cm.mu.Lock()
	if cm.decoderFactory == nil {
		cm.mu.Unlock()
		return
	}
	// The `decoding` gate blocks re-entry for the FULL decode duration, not
	// just minInterval from its start. lastUpdate is stamped at the start of
	// decode, so a decode that exceeds minInterval would otherwise let a
	// second IngestVideo pass the rate-limit check and call Decode
	// concurrently on the shared decoder — which most native decoders do not
	// support. The gate keeps Decode strictly serialized on one instance.
	if cm.decoding {
		cm.mu.Unlock()
		return
	}
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
	cm.decoding = true
	cm.inFlight.Add(1)
	cm.mu.Unlock()

	// Convert to Annex B synchronously — a cheap byte copy that also means
	// the background goroutine does not retain the caller's frame buffers.
	// Use codec-agnostic wire format conversion (identical for AVC and HEVC).
	annexB := codec.WireToAnnexBInto(frame.WireData, nil)
	if frame.Codec == "h265" {
		// HEVC: prepend VPS+SPS+PPS.
		annexB = codec.PrependVPSSPSPPSInto(frame.VPS, frame.SPS, frame.PPS, annexB, nil)
	} else {
		// H.264: prepend SPS+PPS only.
		annexB = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, annexB, nil)
	}

	// Decode, scale, and JPEG-encode on a background goroutine. IngestVideo
	// is invoked synchronously from the program video broadcast path
	// (Manager.DirectWriteVideo ← switcher sync loop): a 1080p keyframe
	// decode + JPEG encode can take 10-30ms, exceeding the 16.7-33ms frame
	// budget and tripping the sync loop's overload logic. The `decoding`
	// gate (cleared when the worker finishes) keeps Decode serialized on
	// the shared decoder, and inFlight lets Close join the worker.
	go cm.generateThumbnail(decoder, annexB)
}

// generateThumbnail runs the expensive decode → scale → JPEG encode pipeline
// on a background goroutine. The caller has already set cm.decoding and
// added to cm.inFlight; both are released here when the work completes.
func (cm *ConfidenceMonitor) generateThumbnail(decoder transition.VideoDecoder, annexB []byte) {
	defer func() {
		cm.mu.Lock()
		cm.decoding = false
		cm.mu.Unlock()
		cm.inFlight.Done()
	}()

	yuv, w, h, err := decoder.Decode(annexB)
	if err != nil {
		cm.decodeErrors.Add(1)
		slog.Warn("confidence monitor: decode error", "err", err)
		return
	}

	// Serialize access to cached intermediate buffers.
	cm.bufMu.Lock()
	defer cm.bufMu.Unlock()

	// Scale to thumbnail size if needed
	dstW, dstH := thumbWidth, thumbHeight
	var scaledYUV []byte
	if w == dstW && h == dstH {
		scaledYUV = yuv
	} else {
		scaledSize := video.YUV420FrameSize(dstW, dstH)
		if cap(cm.scaledBuf) < scaledSize {
			cm.scaledBuf = make([]byte, scaledSize)
		}
		cm.scaledBuf = cm.scaledBuf[:scaledSize]
		transition.ScaleYUV420(yuv, w, h, cm.scaledBuf, dstW, dstH)
		scaledYUV = cm.scaledBuf
	}

	// Convert YUV420 to RGB and build RGBA image via direct Pix access
	// (avoids per-pixel bounds checks from SetRGBA).
	rgbSize := dstW * dstH * 3
	if cap(cm.rgbBuf) < rgbSize {
		cm.rgbBuf = make([]byte, rgbSize)
	}
	cm.rgbBuf = cm.rgbBuf[:rgbSize]
	transition.YUV420ToRGBLimited(scaledYUV, dstW, dstH, cm.rgbBuf)

	if cm.rgbaBuf == nil || cm.rgbaBuf.Rect.Dx() != dstW || cm.rgbaBuf.Rect.Dy() != dstH {
		cm.rgbaBuf = image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	}
	pix := cm.rgbaBuf.Pix
	for i := 0; i < dstW*dstH; i++ {
		off := i * 4
		pix[off+0] = cm.rgbBuf[i*3]
		pix[off+1] = cm.rgbBuf[i*3+1]
		pix[off+2] = cm.rgbBuf[i*3+2]
		pix[off+3] = 255
	}

	cm.jpegBuf.Reset()
	if err := jpeg.Encode(&cm.jpegBuf, cm.rgbaBuf, &jpeg.Options{Quality: 70}); err != nil {
		slog.Error("confidence monitor: JPEG encode error", "err", err)
		return
	}

	// Copy JPEG bytes so jpegBuf can be reused on next frame.
	jpegCopy := make([]byte, cm.jpegBuf.Len())
	copy(jpegCopy, cm.jpegBuf.Bytes())

	cm.mu.Lock()
	cm.latestJPEG = jpegCopy
	cm.mu.Unlock()
}

// LatestThumbnail returns the most recent JPEG thumbnail, or nil if none
// has been generated yet.
func (cm *ConfidenceMonitor) LatestThumbnail() []byte {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.latestJPEG
}

// DecodeErrors returns the total number of decode errors since creation.
func (cm *ConfidenceMonitor) DecodeErrors() int64 {
	return cm.decodeErrors.Load()
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
