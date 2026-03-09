package graphics

import (
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
	onChange     func()               // called after SetKey/RemoveKey for pipeline rebuild
	spillWorkBuf []byte               // reused across frames for spill suppression copy
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

// SetKey configures an upstream key for a source.
func (kp *KeyProcessor) SetKey(source string, config KeyConfig) {
	kp.mu.Lock()
	kp.keys[source] = config
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
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	if len(kp.keys) == 0 {
		return bg
	}

	ySize := width * height
	uvWidth := width / 2
	uvSize := uvWidth * (height / 2)
	frameSize := ySize + 2*uvSize

	// Collect and sort source keys for deterministic compositing order.
	sortedKeys := make([]string, 0, len(kp.keys))
	for source := range kp.keys {
		sortedKeys = append(sortedKeys, source)
	}
	slices.Sort(sortedKeys)

	for _, source := range sortedKeys {
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

		// Generate alpha mask
		var mask []byte
		switch cfg.Type {
		case KeyTypeChroma:
			keyColor := YCbCr{Y: cfg.KeyColorY, Cb: cfg.KeyColorCb, Cr: cfg.KeyColorCr}
			spillCb, spillCr := cfg.SpillReplaceCb, cfg.SpillReplaceCr
			if spillCb == 0 && spillCr == 0 {
				spillCb, spillCr = 128, 128 // default to neutral
			}
			mask = ChromaKeyWithSpillColor(workFill, width, height, keyColor, cfg.Similarity, cfg.Smoothness, cfg.SpillSuppress, spillCb, spillCr)
		case KeyTypeLuma:
			mask = LumaKey(workFill, width, height, cfg.LowClip, cfg.HighClip, cfg.Softness)
		default:
			continue
		}

		if mask == nil {
			continue
		}

		// Composite fill onto background using the generated mask.
		// Y plane: per-pixel alpha blend at full resolution.
		for i := 0; i < ySize; i++ {
			alpha := float32(mask[i]) / 255.0
			if alpha < 1.0/255.0 {
				continue
			}
			invAlpha := 1.0 - alpha
			bg[i] = uint8(clampFloat(float32(bg[i])*invAlpha+float32(workFill[i])*alpha, 0, 255))
		}

		// Cb and Cr planes: average alpha over corresponding 2x2 luma block.
		// Each UV sample covers a 2x2 block of luma pixels. The alpha for the
		// chroma blend is the average of the 4 luma alphas in that block.
		// This matches the pattern in transition/blend.go BlendStinger.
		for py := 0; py < height/2; py++ {
			for px := 0; px < uvWidth; px++ {
				ly := py * 2
				lx := px * 2
				a00 := float32(mask[ly*width+lx])
				a01 := float32(mask[ly*width+lx+1])
				a10 := float32(mask[(ly+1)*width+lx])
				a11 := float32(mask[(ly+1)*width+lx+1])
				alpha := (a00 + a01 + a10 + a11) / (4.0 * 255.0)

				if alpha < 1.0/255.0 {
					continue
				}
				invAlpha := 1.0 - alpha

				uvIdx := py*uvWidth + px
				// Cb
				if ySize+uvIdx < frameSize {
					bg[ySize+uvIdx] = uint8(clampFloat(
						float32(bg[ySize+uvIdx])*invAlpha+float32(workFill[ySize+uvIdx])*alpha, 0, 255))
				}
				// Cr
				if ySize+uvSize+uvIdx < frameSize {
					bg[ySize+uvSize+uvIdx] = uint8(clampFloat(
						float32(bg[ySize+uvSize+uvIdx])*invAlpha+float32(workFill[ySize+uvSize+uvIdx])*alpha, 0, 255))
				}
			}
		}
	}

	return bg
}
