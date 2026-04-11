package switcher

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// ThumbnailCache stores per-node JPEG thumbnails for the routing view.
type ThumbnailCache struct {
	mu      sync.RWMutex
	entries map[string]*thumbnailEntry

	requested atomic.Bool  // Set by API handler when client is polling
	lastPoll  atomic.Int64 // Unix ms of last poll — auto-disable after 5s
}

type thumbnailEntry struct {
	jpeg       []byte
	width      int
	height     int
	capturedAt time.Time
}

// ThumbnailInfo is the public read-only view of a cached thumbnail.
type ThumbnailInfo struct {
	JPEG       []byte
	Width      int
	Height     int
	CapturedAt time.Time
}

const (
	thumbnailWidth  = 160
	thumbnailHeight = 90
	pollTimeout     = 5 * time.Second
)

// NewThumbnailCache creates a new empty thumbnail cache.
func NewThumbnailCache() *ThumbnailCache {
	return &ThumbnailCache{
		entries: make(map[string]*thumbnailEntry),
	}
}

// Store saves a JPEG thumbnail for the given node ID.
func (tc *ThumbnailCache) Store(nodeID string, jpeg []byte, w, h int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.entries[nodeID] = &thumbnailEntry{
		jpeg:       jpeg,
		width:      w,
		height:     h,
		capturedAt: time.Now(),
	}
}

// Get retrieves a single thumbnail by node ID.
func (tc *ThumbnailCache) Get(nodeID string) (ThumbnailInfo, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	e, ok := tc.entries[nodeID]
	if !ok {
		return ThumbnailInfo{}, false
	}
	return ThumbnailInfo{
		JPEG:       e.jpeg,
		Width:      e.width,
		Height:     e.height,
		CapturedAt: e.capturedAt,
	}, true
}

// GetAll returns a snapshot of all cached thumbnails.
func (tc *ThumbnailCache) GetAll() map[string]ThumbnailInfo {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	result := make(map[string]ThumbnailInfo, len(tc.entries))
	for k, e := range tc.entries {
		result[k] = ThumbnailInfo{
			JPEG:       e.jpeg,
			Width:      e.width,
			Height:     e.height,
			CapturedAt: e.capturedAt,
		}
	}
	return result
}

// RequestCapture marks that a client is actively polling for thumbnails.
// Sets the requested flag and updates the last poll timestamp.
func (tc *ThumbnailCache) RequestCapture() {
	tc.requested.Store(true)
	tc.lastPoll.Store(time.Now().UnixMilli())
}

// ShouldCapture returns true if a client has requested capture AND the
// last poll was within the pollTimeout window (5s). This ensures capture
// stops automatically when clients disconnect.
func (tc *ThumbnailCache) ShouldCapture() bool {
	if !tc.requested.Load() {
		return false
	}
	elapsed := time.Since(time.UnixMilli(tc.lastPoll.Load()))
	return elapsed < pollTimeout
}

// ClearRequest clears the requested flag. Called after a capture cycle
// completes so the next cycle requires a fresh RequestCapture from the client.
func (tc *ThumbnailCache) ClearRequest() {
	tc.requested.Store(false)
}

// ScaleAndEncodeJPEG takes raw YUV420 planar data at (srcW x srcH), scales it
// to (dstW x dstH), converts to RGB, and encodes as JPEG at quality 60.
// Follows the same pattern as the confidence monitor (output/confidence.go).
func ScaleAndEncodeJPEG(yuv []byte, srcW, srcH, dstW, dstH int) ([]byte, error) {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return nil, fmt.Errorf("invalid dimensions: src=%dx%d dst=%dx%d", srcW, srcH, dstW, dstH)
	}

	// Scale YUV420 to destination size
	dstSize := video.YUV420FrameSize(dstW, dstH)
	var scaledYUV []byte
	if srcW == dstW && srcH == dstH {
		scaledYUV = yuv
	} else {
		scaledYUV = make([]byte, dstSize)
		transition.ScaleYUV420(yuv, srcW, srcH, scaledYUV, dstW, dstH)
	}

	// Convert YUV420 to RGB
	rgbSize := dstW * dstH * 3
	rgb := make([]byte, rgbSize)
	transition.YUV420ToRGB(scaledYUV, dstW, dstH, rgb)

	// Build RGBA image via direct Pix access (avoids per-pixel bounds checks)
	img := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	pix := img.Pix
	for i := 0; i < dstW*dstH; i++ {
		off := i * 4
		pix[off+0] = rgb[i*3]
		pix[off+1] = rgb[i*3+1]
		pix[off+2] = rgb[i*3+2]
		pix[off+3] = 255
	}

	// JPEG encode at quality 60 (lower than confidence monitor's 70 — these are tiny)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
