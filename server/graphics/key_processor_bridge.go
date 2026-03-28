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

	// Per-source cached YUV420 8-bit fill frames (with dimensions for scaling).
	fills map[string]*fillEntry

	// Per-source cached YUV422P10LE 10-bit fill frames.
	fills10 map[string]*fillEntry10

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

	yuvSize := yuvFrameSize(width, height)
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

// fillEntry10 holds a cached 10-bit fill frame and its dimensions.
type fillEntry10 struct {
	yuv           []byte
	width, height int
}

// IngestFillYUV10 stores pre-decoded YUV422P10LE fill data for a keyed source.
// Used by the always-decode pipeline when the pipeline color format is 10-bit.
func (b *KeyProcessorBridge) IngestFillYUV10(sourceKey string, yuv []byte, width, height int) {
	if !b.kp.HasEnabledKeys() {
		return
	}
	cfg, ok := b.kp.GetKey(sourceKey)
	if !ok || !cfg.Enabled {
		return
	}
	_ = cfg

	b.mu.Lock()
	defer b.mu.Unlock()

	yuvSize := yuv422p10FrameSize(width, height)
	if len(yuv) < yuvSize {
		return
	}

	if b.fills10 == nil {
		b.fills10 = make(map[string]*fillEntry10)
	}

	entry := b.fills10[sourceKey]
	if entry == nil || len(entry.yuv) != yuvSize {
		entry = &fillEntry10{yuv: make([]byte, yuvSize)}
	}
	copy(entry.yuv, yuv[:yuvSize])
	entry.width = width
	entry.height = height
	b.fills10[sourceKey] = entry
}

// RemoveFillSource removes the cached fill data for a source.
// Called when a source's key is removed or disabled.
func (b *KeyProcessorBridge) RemoveFillSource(source string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fills, source)
	delete(b.scaledFills, source)
	if b.fills10 != nil {
		delete(b.fills10, source)
	}
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

// HasEnabledKeys returns true if any keys are configured and enabled,
// regardless of whether CPU fill frames have been cached. Used by the GPU
// key node where fills come from the GPU source manager cache rather than
// the CPU bridge's fills map.
func (b *KeyProcessorBridge) HasEnabledKeys() bool {
	return b.kp.HasEnabledKeys()
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
	targetSize := yuvFrameSize(width, height)
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

// EnabledKeySnap is a deep-copied snapshot of one enabled key's state.
// Used by the GPU key node to avoid holding locks during GPU dispatch.
type EnabledKeySnap struct {
	SourceKey      string
	Type           string // "chroma" or "luma"
	KeyCb, KeyCr   uint8
	Similarity     float32
	Smoothness     float32
	SpillSuppress  float32
	SpillReplaceCb uint8
	SpillReplaceCr uint8
	LowClip        float32
	HighClip       float32
	Softness       float32
	FillYUV        []byte // YUV420p fill frame (deep copy)
	FillW, FillH   int
}

// SnapshotEnabledKeys returns a deep-copied snapshot of all enabled keys
// and their fill frames. The GPU key node calls this once per frame to get
// all needed state, then dispatches GPU kernels without holding any locks.
//
// Lock ordering: acquires b.mu then b.kp.mu.RLock (same order as ProcessYUV).
func (b *KeyProcessorBridge) SnapshotEnabledKeys() []EnabledKeySnap {
	if !b.kp.HasEnabledKeys() {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.fills) == 0 {
		return nil
	}

	// Read key configs under kp lock.
	b.kp.mu.RLock()
	defer b.kp.mu.RUnlock()

	var result []EnabledKeySnap
	for _, source := range b.kp.sortedKeys {
		cfg := b.kp.keys[source]
		if !cfg.Enabled {
			continue
		}
		entry, ok := b.fills[source]
		if !ok {
			continue
		}

		yuvSize := yuvFrameSize(entry.width, entry.height)
		if len(entry.yuv) < yuvSize {
			continue
		}

		// Deep-copy fill frame.
		fillCopy := make([]byte, yuvSize)
		copy(fillCopy, entry.yuv[:yuvSize])

		result = append(result, EnabledKeySnap{
			SourceKey:      source,
			Type:           string(cfg.Type),
			KeyCb:          cfg.KeyColorCb,
			KeyCr:          cfg.KeyColorCr,
			Similarity:     cfg.Similarity,
			Smoothness:     cfg.Smoothness,
			SpillSuppress:  cfg.SpillSuppress,
			SpillReplaceCb: cfg.SpillReplaceCb,
			SpillReplaceCr: cfg.SpillReplaceCr,
			LowClip:        cfg.LowClip,
			HighClip:       cfg.HighClip,
			Softness:       cfg.Softness,
			FillYUV:        fillCopy,
			FillW:          entry.width,
			FillH:          entry.height,
		})
	}

	return result
}

// HasEnabledKeysWithFills10 returns true if there are enabled keys AND cached
// 10-bit fill frames. Used by the pipeline coordinator for 10-bit format.
func (b *KeyProcessorBridge) HasEnabledKeysWithFills10() bool {
	if !b.kp.HasEnabledKeys() {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.fills10) > 0
}

// ProcessYUV10 applies upstream keys to a raw YUV422P10LE buffer in-place.
// This is the 10-bit equivalent of ProcessYUV. Generates key masks using
// ChromaKey10/LumaKey10 and blends using keyMaskBlend10Row.
//
// When no keys are enabled or no 10-bit fills are cached, returns yuv unchanged.
func (b *KeyProcessorBridge) ProcessYUV10(yuv []byte, width, height int) []byte {
	if width%2 != 0 || width <= 0 || height <= 0 {
		return yuv
	}

	if !b.kp.HasEnabledKeys() {
		return yuv
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.fills10) == 0 {
		return yuv
	}

	frameSize := yuv422p10FrameSize(width, height)
	if len(yuv) < frameSize {
		return yuv
	}

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height

	// Read key configs under kp lock.
	b.kp.mu.RLock()
	defer b.kp.mu.RUnlock()

	for _, source := range b.kp.sortedKeys {
		cfg := b.kp.keys[source]
		if !cfg.Enabled {
			continue
		}
		if cfg.Type == KeyTypeNeural {
			continue
		}

		entry, ok := b.fills10[source]
		if !ok {
			continue
		}
		fillSize := yuv422p10FrameSize(entry.width, entry.height)
		if len(entry.yuv) < fillSize || entry.width != width || entry.height != height {
			continue // dimension mismatch — skip (scaling not yet implemented for 10-bit keys)
		}

		fill := entry.yuv

		// Generate 8-bit alpha mask from the 10-bit fill.
		var mask []byte
		switch cfg.Type {
		case KeyTypeChroma:
			// Convert 8-bit key color to 10-bit.
			targetCb := uint16(cfg.KeyColorCb) * 4
			targetCr := uint16(cfg.KeyColorCr) * 4
			// Convert similarity/smoothness from 0.0-1.0 to chroma distance in 10-bit space.
			// 10-bit chroma range is ~0-1023, tolerance is a distance threshold.
			tolerance := int(cfg.Similarity * 400) // scale to 10-bit range
			softness := int(cfg.Smoothness * 200)
			mask = ChromaKey10(fill, width, height, targetCb, targetCr, tolerance, softness)
		case KeyTypeLuma:
			// Convert 0.0-1.0 clips to 10-bit Y thresholds.
			lowThresh := uint16(cfg.LowClip * 1023)
			highThresh := uint16(cfg.HighClip * 1023)
			mask = LumaKey10(fill, width, height, lowThresh, highThresh)
		default:
			continue
		}

		if mask == nil {
			continue
		}

		// For chroma keying, the mask marks key area as 255 (transparent/keyed).
		// We need to invert: mask=255 → alpha=0 (transparent, show bg).
		// The keyMaskBlend10Row expects: mask[i] = blend weight of fill.
		// ChromaKey10: 255 = keyed (transparent), 0 = opaque (show fill).
		// So we need to invert: blendMask = 255 - chromaMask.
		if cfg.Type == KeyTypeChroma {
			for i := range mask {
				mask[i] = 255 - mask[i]
			}
		}
		// LumaKey10: 255 = opaque (keep), 0 = transparent (keyed).
		// This is already the blend weight of fill. No inversion needed.

		// Blend Y plane using keyMaskBlend10Row (SIMD on amd64/arm64).
		// Process in chunks the kernel can handle.
		keyMaskBlend10Row(&yuv[0], &fill[0], &mask[0], ySize)

		// Downsample mask to chroma resolution: average 2 horizontal pixels (4:2:2).
		chromaMask := make([]byte, chromaSize)
		for py := 0; py < height; py++ {
			for cx := 0; cx < chromaW; cx++ {
				lx := cx * 2
				a0 := int(mask[py*width+lx])
				a1 := int(mask[py*width+lx+1])
				chromaMask[py*chromaW+cx] = byte((a0 + a1 + 1) >> 1)
			}
		}

		// Blend Cb plane.
		cbOff := ySize * 2
		fillCbOff := ySize * 2
		keyMaskBlend10Row(&yuv[cbOff], &fill[fillCbOff], &chromaMask[0], chromaSize)

		// Blend Cr plane.
		crOff := cbOff + chromaSize*2
		fillCrOff := fillCbOff + chromaSize*2
		keyMaskBlend10Row(&yuv[crOff], &fill[fillCrOff], &chromaMask[0], chromaSize)
	}

	return yuv
}

// Close releases all fill resources.
func (b *KeyProcessorBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fills = make(map[string]*fillEntry)
	b.scaledFills = make(map[string][]byte)
	b.fills10 = nil
}
