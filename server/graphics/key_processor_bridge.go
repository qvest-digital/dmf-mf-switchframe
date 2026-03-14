package graphics

import (
	"sync"
)

// ScaleFunc scales a YUV420 buffer from (srcW, srcH) to (dstW, dstH).
// The dst buffer is pre-allocated by the caller.
type ScaleFunc func(src []byte, srcW, srcH int, dst []byte, dstW, dstH int)

// fillEntry holds a cached fill frame and its dimensions.
type fillEntry struct {
	yuv           []byte
	width, height int
}

// KeyProcessorBridge wraps a KeyProcessor with fill-source capability.
//
// Fill sources (keyed inputs) are cached via IngestFillYUV.
// The pipeline coordinator calls ProcessYUV on the raw YUV program frame
// to apply all enabled keys via KeyProcessor.Process.
//
// When no keys are enabled, ProcessYUV returns the buffer unchanged (zero overhead).
type KeyProcessorBridge struct {
	mu sync.Mutex

	kp *KeyProcessor

	// Per-source cached YUV for fill frames (with dimensions for scaling).
	fills map[string]*fillEntry

	// Optional scaler for resolution mismatch between fill and program.
	scaleFunc ScaleFunc

	// Per-source scaled fill cache (reused across frames to avoid allocation).
	scaledFills map[string][]byte

	// Reusable fillMap passed to KeyProcessor.Process (avoids per-call map allocation).
	fillMap map[string][]byte
}

// NewKeyProcessorBridge creates a bridge wrapping the given KeyProcessor.
func NewKeyProcessorBridge(kp *KeyProcessor) *KeyProcessorBridge {
	return &KeyProcessorBridge{
		kp:          kp,
		fills:       make(map[string]*fillEntry),
		scaledFills: make(map[string][]byte),
		fillMap:     make(map[string][]byte),
	}
}

// SetScaleFunc sets the function used to scale fill frames when they don't
// match the program frame dimensions. Without a scaler, mismatched fills
// are silently skipped (same as before).
func (b *KeyProcessorBridge) SetScaleFunc(fn ScaleFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scaleFunc = fn
}

// IngestFillYUV stores pre-decoded YUV fill data for a keyed source.
// Used by the always-decode pipeline where frames arrive as raw YUV.
func (b *KeyProcessorBridge) IngestFillYUV(sourceKey string, yuv []byte, width, height int) {
	if !b.kp.HasEnabledKeys() {
		return
	}
	cfg, ok := b.kp.GetKey(sourceKey)
	if !ok || !cfg.Enabled {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	yuvSize := width * height * 3 / 2
	if len(yuv) < yuvSize {
		return
	}

	entry := b.fills[sourceKey]
	if entry == nil || len(entry.yuv) != yuvSize {
		entry = &fillEntry{yuv: make([]byte, yuvSize)}
	}
	copy(entry.yuv, yuv[:yuvSize])
	entry.width = width
	entry.height = height
	b.fills[sourceKey] = entry
}

// RemoveFillSource removes the cached fill data for a source.
// Called when a source's key is removed or disabled.
func (b *KeyProcessorBridge) RemoveFillSource(source string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fills, source)
	delete(b.scaledFills, source)
}

// HasEnabledKeysWithFills returns true if there are enabled keys AND cached
// fill frames. Used by the pipeline coordinator to decide whether to enter
// the slow decode/process/encode path.
func (b *KeyProcessorBridge) HasEnabledKeysWithFills() bool {
	if !b.kp.HasEnabledKeys() {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.fills) > 0
}

// ProcessYUV applies upstream keys to a raw YUV420 buffer in-place.
// This is the codec-free processor used by the pipeline coordinator.
// When no keys are enabled or no fills are cached, returns yuv unchanged.
// If a fill has different dimensions than the program frame and a ScaleFunc
// is set, the fill is scaled to match before compositing.
func (b *KeyProcessorBridge) ProcessYUV(yuv []byte, width, height int) []byte {
	if width%2 != 0 || height%2 != 0 || width <= 0 || height <= 0 {
		return yuv // YUV420 requires even positive dimensions
	}

	if !b.kp.HasEnabledKeys() {
		return yuv
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.fills) == 0 {
		return yuv
	}

	// Build the fills map for Process(), scaling any mismatched fills.
	// Reuse b.fillMap to avoid per-call map allocation: clear old entries,
	// then populate with current fills.
	targetSize := width*height*3/2
	for k := range b.fillMap {
		delete(b.fillMap, k)
	}
	for source, entry := range b.fills {
		if entry.width == width && entry.height == height {
			// Dimensions match — use directly.
			b.fillMap[source] = entry.yuv
		} else if b.scaleFunc != nil {
			// Scale fill to program dimensions.
			scaled := b.scaledFills[source]
			if len(scaled) != targetSize {
				scaled = make([]byte, targetSize)
				b.scaledFills[source] = scaled
			}
			b.scaleFunc(entry.yuv, entry.width, entry.height, scaled, width, height)
			b.fillMap[source] = scaled
		}
		// No scaleFunc and dimensions mismatch → skip (same as before).
	}

	if len(b.fillMap) == 0 {
		return yuv
	}

	b.kp.Process(yuv, b.fillMap, width, height)
	return yuv
}

// Close releases all fill resources.
func (b *KeyProcessorBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fills = make(map[string]*fillEntry)
	b.scaledFills = make(map[string][]byte)
}
