package playout

// BlackDetectorConfig holds thresholds for black frame detection.
type BlackDetectorConfig struct {
	MeanThreshold float64 // default: 16
	MaxThreshold  uint8   // default: 32
}

// BlackScore is the result of analyzing a single frame for blackness.
type BlackScore struct {
	MeanLuma float64
	MaxLuma  uint8
	IsBlack  bool
	Score    float64 // 0.0 = perfect black, 1.0 = max brightness
}

// BlackDetector analyzes YUV420p frames for blackness.
type BlackDetector struct {
	config BlackDetectorConfig
}

// NewBlackDetector creates a BlackDetector with the given config.
// Zero-value thresholds are replaced with defaults (MeanThreshold=16, MaxThreshold=32).
func NewBlackDetector(config BlackDetectorConfig) *BlackDetector {
	if config.MeanThreshold == 0 {
		config.MeanThreshold = 16
	}
	if config.MaxThreshold == 0 {
		config.MaxThreshold = 32
	}
	return &BlackDetector{config: config}
}

// AnalyzeFrame examines the Y plane of a YUV420p frame and returns a BlackScore.
// The Y plane is the first w*h bytes of yuv. The buffer must be at least w*h bytes.
func (d *BlackDetector) AnalyzeFrame(yuv []byte, w, h int) BlackScore {
	ySize := w * h
	if ySize == 0 {
		return BlackScore{IsBlack: true}
	}

	yPlane := yuv[:ySize]

	var sum uint64
	var maxLuma uint8
	for _, y := range yPlane {
		sum += uint64(y)
		if y > maxLuma {
			maxLuma = y
		}
	}

	meanLuma := float64(sum) / float64(ySize)
	score := meanLuma / 255.0
	isBlack := meanLuma < d.config.MeanThreshold && maxLuma < d.config.MaxThreshold

	return BlackScore{
		MeanLuma: meanLuma,
		MaxLuma:  maxLuma,
		IsBlack:  isBlack,
		Score:    score,
	}
}
