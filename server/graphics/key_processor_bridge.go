package graphics

import (
	"sync"
)

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

	// Per-source cached YUV for fill frames
	fillYUV map[string][]byte
}

// NewKeyProcessorBridge creates a bridge wrapping the given KeyProcessor.
func NewKeyProcessorBridge(kp *KeyProcessor) *KeyProcessorBridge {
	return &KeyProcessorBridge{
		kp:      kp,
		fillYUV: make(map[string][]byte),
	}
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
	cached := b.fillYUV[sourceKey]
	if len(cached) != yuvSize {
		cached = make([]byte, yuvSize)
	}
	copy(cached, yuv[:yuvSize])
	b.fillYUV[sourceKey] = cached
}

// RemoveFillSource removes the cached fill data for a source.
// Called when a source's key is removed or disabled.
func (b *KeyProcessorBridge) RemoveFillSource(source string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fillYUV, source)
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
	return len(b.fillYUV) > 0
}

// ProcessYUV applies upstream keys to a raw YUV420 buffer in-place.
// This is the codec-free processor used by the pipeline coordinator.
// When no keys are enabled or no fills are cached, returns yuv unchanged.
func (b *KeyProcessorBridge) ProcessYUV(yuv []byte, width, height int) []byte {
	if width%2 != 0 || height%2 != 0 || width <= 0 || height <= 0 {
		return yuv // YUV420 requires even positive dimensions
	}

	if !b.kp.HasEnabledKeys() {
		return yuv
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.fillYUV) == 0 {
		return yuv
	}

	b.kp.Process(yuv, b.fillYUV, width, height)
	return yuv
}

// Close releases all fill resources.
func (b *KeyProcessorBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fillYUV = make(map[string][]byte)
}
