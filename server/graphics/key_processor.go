package graphics

import (
	"log/slog"
	"slices"
	"sync"
)

// KeyType identifies the keying algorithm.
type KeyType string

const (
	KeyTypeChroma KeyType = "chroma"
	KeyTypeLuma   KeyType = "luma"
)

// KeyConfig describes the key configuration for a source.
type KeyConfig struct {
	Type    KeyType `json:"type"`
	Enabled bool    `json:"enabled"`

	// Chroma key params
	KeyColorY      uint8   `json:"keyColorY"`
	KeyColorCb     uint8   `json:"keyColorCb"`
	KeyColorCr     uint8   `json:"keyColorCr"`
	Similarity     float32 `json:"similarity"`
	Smoothness     float32 `json:"smoothness"`
	SpillSuppress  float32 `json:"spillSuppress"`
	SpillReplaceCb uint8   `json:"spillReplaceCb,omitempty"`
	SpillReplaceCr uint8   `json:"spillReplaceCr,omitempty"`

	// Luma key params
	LowClip  float32 `json:"lowClip"`
	HighClip float32 `json:"highClip"`
	Softness float32 `json:"softness"`

	// FillSource specifies which source provides the fill layer.
	// If empty, the source itself is used as both fill and key.
	FillSource string `json:"fillSource,omitempty"`
}

// KeyProcessor manages per-source upstream key configurations and applies
// them during frame processing. Keys are composited in source-key order
// (sorted) onto the background (program) frame.
//
// The processor is safe for concurrent use from multiple goroutines.
type KeyProcessor struct {
	mu           sync.RWMutex
	keys         map[string]KeyConfig // source key → config
	sortedKeys   []string             // cached sorted key list, rebuilt on mutation
	onChange     func()               // called after SetKey/RemoveKey for pipeline rebuild
	spillWorkBuf []byte               // reused across frames for spill suppression copy
	maskBuf      []byte               // reused luma-resolution mask buffer (width*height)
	chromaMaskBuf []byte              // reused chroma-resolution mask buffer (w/2*h/2)
}

// NewKeyProcessor creates a new key processor with no keys configured.
func NewKeyProcessor() *KeyProcessor {
	return &KeyProcessor{
		keys: make(map[string]KeyConfig),
	}
}

// OnChange registers a callback invoked after any key mutation (SetKey, RemoveKey).
// Used by the Switcher to trigger pipeline rebuild on key configuration changes.
func (kp *KeyProcessor) OnChange(fn func()) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.onChange = fn
}

// rebuildSortedKeys rebuilds the cached sorted key list from the keys map.
// Caller must hold kp.mu.
func (kp *KeyProcessor) rebuildSortedKeys() {
	// Reuse the existing slice backing array when capacity is sufficient.
	if cap(kp.sortedKeys) >= len(kp.keys) {
		kp.sortedKeys = kp.sortedKeys[:0]
	} else {
		kp.sortedKeys = make([]string, 0, len(kp.keys))
	}
	for k := range kp.keys {
		kp.sortedKeys = append(kp.sortedKeys, k)
	}
	slices.Sort(kp.sortedKeys)
}

// SetKey configures an upstream key for a source.
func (kp *KeyProcessor) SetKey(source string, config KeyConfig) {
	kp.mu.Lock()
	kp.keys[source] = config
	kp.rebuildSortedKeys()
	fn := kp.onChange
	kp.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// GetKey returns the key configuration for a source.
func (kp *KeyProcessor) GetKey(source string) (KeyConfig, bool) {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	cfg, ok := kp.keys[source]
	return cfg, ok
}

// RemoveKey removes the key configuration for a source.
func (kp *KeyProcessor) RemoveKey(source string) {
	kp.mu.Lock()
	delete(kp.keys, source)
	kp.rebuildSortedKeys()
	fn := kp.onChange
	kp.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// HasEnabledKeys returns true if any keys are configured and enabled.
// Used by the bridge for fast-path bypass when no keying is active.
func (kp *KeyProcessor) HasEnabledKeys() bool {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	for _, cfg := range kp.keys {
		if cfg.Enabled {
			return true
		}
	}
	return false
}

// EnabledSources returns the set of source keys that have enabled upstream keys.
func (kp *KeyProcessor) EnabledSources() map[string]KeyConfig {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	result := make(map[string]KeyConfig)
	for source, cfg := range kp.keys {
		if cfg.Enabled {
			result[source] = cfg
		}
	}
	return result
}

// Process applies all enabled upstream keys to the background frame.
// fills maps source keys to their YUV420 fill data. The background frame
// is modified in-place and returned.
//
// For each enabled key:
//  1. Generate alpha mask from the fill frame (chroma or luma)
//  2. Composite fill onto background using the mask
//
// If no keys are configured or enabled, the background is returned unchanged.
func (kp *KeyProcessor) Process(bg []byte, fills map[string][]byte, width, height int) []byte {
	if width%2 != 0 || height%2 != 0 {
		slog.Warn("skipping key processing: YUV420 requires even dimensions", "width", width, "height", height)
		return bg
	}

	kp.mu.Lock()
	defer kp.mu.Unlock()

	if len(kp.keys) == 0 {
		return bg
	}

	ySize := width * height
	uvWidth := width / 2
	uvSize := uvWidth * (height / 2)
	frameSize := ySize + 2*uvSize

	// Use cached sorted key list for deterministic compositing order
	// (rebuilt on SetKey/RemoveKey, avoids per-frame allocation + sort).
	for _, source := range kp.sortedKeys {
		cfg := kp.keys[source]
		if !cfg.Enabled {
			continue
		}

		fill, ok := fills[source]
		if !ok || len(fill) < frameSize {
			continue
		}

		// Only copy the fill when chroma key with spill suppression needs to modify it.
		// Luma keys and chroma keys without spill work on the original, saving ~3MB per frame.
		var workFill []byte
		if cfg.Type == KeyTypeChroma && cfg.SpillSuppress > 0 {
			if cap(kp.spillWorkBuf) < len(fill) {
				kp.spillWorkBuf = make([]byte, len(fill))
			}
			workFill = kp.spillWorkBuf[:len(fill)]
			copy(workFill, fill)
		} else {
			workFill = fill
		}

		// Generate alpha mask using pre-allocated buffers to avoid per-frame allocation.
		// Ensure buffers are large enough for current resolution.
		if cap(kp.maskBuf) < ySize {
			kp.maskBuf = make([]byte, ySize)
		}
		if cap(kp.chromaMaskBuf) < uvSize {
			kp.chromaMaskBuf = make([]byte, uvSize)
		}

		var mask []byte
		switch cfg.Type {
		case KeyTypeChroma:
			keyColor := YCbCr{Y: cfg.KeyColorY, Cb: cfg.KeyColorCb, Cr: cfg.KeyColorCr}
			spillCb, spillCr := cfg.SpillReplaceCb, cfg.SpillReplaceCr
			if spillCb == 0 && spillCr == 0 {
				spillCb, spillCr = 128, 128 // default to neutral
			}
			mask = ChromaKeyWithSpillColorInto(kp.maskBuf, kp.chromaMaskBuf, workFill, width, height, keyColor, cfg.Similarity, cfg.Smoothness, cfg.SpillSuppress, spillCb, spillCr)
		case KeyTypeLuma:
			mask = LumaKeyInto(kp.maskBuf, workFill, width, height, cfg.LowClip, cfg.HighClip, cfg.Softness)
		default:
			continue
		}

		if mask == nil {
			continue
		}

		// Composite fill onto background using the generated mask.
		// Uses integer fixed-point arithmetic with SIMD acceleration
		// (arm64 NEON / amd64 SSE2). All blend paths in the codebase
		// use the same pattern: w = alpha + (alpha>>7) maps 0-255 → 0-256,
		// result = (bg*(256-w) + fill*w + 128) >> 8.

		// Y plane: per-pixel alpha blend at full resolution.
		blendMaskY(&bg[0], &workFill[0], &mask[0], ySize)

		// Chroma planes: downsample mask to chroma resolution (average
		// 2x2 luma block), then blend Cb and Cr with the same kernel.
		chromaMask := kp.chromaMaskBuf[:uvSize]
		downsampleMask2x2(chromaMask, mask, width, height)

		blendMaskY(&bg[ySize], &workFill[ySize], &chromaMask[0], uvSize)
		blendMaskY(&bg[ySize+uvSize], &workFill[ySize+uvSize], &chromaMask[0], uvSize)
	}

	return bg
}

// downsampleMask2x2 averages each 2x2 block of the luma-resolution mask
// into one chroma-resolution sample. dst must be at least (width/2)*(height/2)
// bytes. src must be at least width*height bytes.
//
// Used to convert per-pixel luma alpha masks to chroma resolution for
// YUV420 blending, matching the pattern in transition/blend.go BlendStinger.
func downsampleMask2x2(dst, src []byte, width, height int) {
	chromaWidth := width / 2
	chromaHeight := height / 2
	for py := 0; py < chromaHeight; py++ {
		row0 := py * 2 * width
		row1 := row0 + width
		dstOff := py * chromaWidth
		for px := 0; px < chromaWidth; px++ {
			lx := px * 2
			sum := int(src[row0+lx]) + int(src[row0+lx+1]) +
				int(src[row1+lx]) + int(src[row1+lx+1])
			dst[dstOff+px] = byte((sum + 2) >> 2)
		}
	}
}
