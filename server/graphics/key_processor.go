package graphics

import (
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
	KeyColorY     uint8   `json:"keyColorY"`
	KeyColorCb    uint8   `json:"keyColorCb"`
	KeyColorCr    uint8   `json:"keyColorCr"`
	Similarity    float32 `json:"similarity"`
	Smoothness    float32 `json:"smoothness"`
	SpillSuppress float32 `json:"spillSuppress"`

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
	mu   sync.RWMutex
	keys map[string]KeyConfig // source key → config
}

// NewKeyProcessor creates a new key processor with no keys configured.
func NewKeyProcessor() *KeyProcessor {
	return &KeyProcessor{
		keys: make(map[string]KeyConfig),
	}
}

// SetKey configures an upstream key for a source.
func (kp *KeyProcessor) SetKey(source string, config KeyConfig) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.keys[source] = config
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
	defer kp.mu.Unlock()
	delete(kp.keys, source)
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

	for source, cfg := range kp.keys {
		if !cfg.Enabled {
			continue
		}

		fill, ok := fills[source]
		if !ok || len(fill) < frameSize {
			continue
		}

		// Make a working copy of fill for chroma key (spill suppression modifies it)
		fillCopy := make([]byte, len(fill))
		copy(fillCopy, fill)

		// Generate alpha mask
		var mask []byte
		switch cfg.Type {
		case KeyTypeChroma:
			keyColor := YCbCr{Y: cfg.KeyColorY, Cb: cfg.KeyColorCb, Cr: cfg.KeyColorCr}
			mask = ChromaKey(fillCopy, width, height, keyColor, cfg.Similarity, cfg.Smoothness, cfg.SpillSuppress)
		case KeyTypeLuma:
			mask = LumaKey(fillCopy, width, height, cfg.LowClip, cfg.HighClip, cfg.Softness)
		default:
			continue
		}

		if mask == nil {
			continue
		}

		// Composite fill onto background using the generated mask.
		// Alpha mask has one byte per pixel; blend in YUV420 space.
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				pixIdx := row*width + col
				alpha := float32(mask[pixIdx]) / 255.0

				// Skip fully transparent pixels (key area, don't composite)
				if alpha < 1.0/255.0 {
					continue
				}

				invAlpha := 1.0 - alpha

				// Blend Y (luma)
				bgY := float32(bg[pixIdx])
				fgY := float32(fillCopy[pixIdx])
				bg[pixIdx] = uint8(clampFloat(bgY*invAlpha+fgY*alpha, 0, 255))

				// Blend Cb, Cr (chroma) — at quarter resolution
				uvIdx := (row/2)*uvWidth + (col / 2)
				if ySize+uvIdx < frameSize && ySize+uvSize+uvIdx < frameSize {
					bgCb := float32(bg[ySize+uvIdx])
					fgCb := float32(fillCopy[ySize+uvIdx])
					bg[ySize+uvIdx] = uint8(clampFloat(bgCb*invAlpha+fgCb*alpha, 0, 255))

					bgCr := float32(bg[ySize+uvSize+uvIdx])
					fgCr := float32(fillCopy[ySize+uvSize+uvIdx])
					bg[ySize+uvSize+uvIdx] = uint8(clampFloat(bgCr*invAlpha+fgCr*alpha, 0, 255))
				}
			}
		}
	}

	return bg
}
