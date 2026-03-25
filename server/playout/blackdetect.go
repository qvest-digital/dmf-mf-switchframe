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
	if ySize == 0 || len(yuv) < ySize {
		return BlackScore{Score: 1.0}
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

// SceneChangeScore computes a normalized frame-to-frame Y-plane difference.
// It returns SAD / (pixelCount * 255), giving 0.0 for identical frames and 1.0
// for maximum difference. Only the Y plane (first w*h bytes) is examined.
func (d *BlackDetector) SceneChangeScore(yuvA, yuvB []byte, w, h int) float64 {
	ySize := w * h
	if ySize == 0 || len(yuvA) < ySize || len(yuvB) < ySize {
		return 0.0
	}

	yA := yuvA[:ySize]
	yB := yuvB[:ySize]

	var sad uint64
	for i := 0; i < ySize; i++ {
		diff := int(yA[i]) - int(yB[i])
		if diff < 0 {
			diff = -diff
		}
		sad += uint64(diff)
	}

	return float64(sad) / float64(ySize) / 255.0
}

// CandidateFrame represents a single frame being considered for a splice point.
type CandidateFrame struct {
	YUV    []byte
	PTS    int64
	Index  int
	Width  int
	Height int
}

// SpliceReason describes why a particular frame was chosen as a splice point.
type SpliceReason string

const (
	SpliceReasonBlack       SpliceReason = "black"
	SpliceReasonSceneChange SpliceReason = "scene_change"
	SpliceReasonScheduled   SpliceReason = "scheduled"
)

// SpliceFrameResult is the chosen splice frame and the reason it was selected.
type SpliceFrameResult struct {
	Index  int
	PTS    int64
	Reason SpliceReason
	Score  float64
}

// FindBestSpliceFrame selects the best splice point from a window of candidate frames.
//
// Priority order:
//  1. Black frame — the blackest frame among all candidates where IsBlack==true (lowest Score).
//  2. Scene change — the largest scene-change score above 0.15 between consecutive candidates.
//  3. Scheduled — fallback to the middle frame (len/2).
//
// Returns Index=-1 for empty candidates.
func (d *BlackDetector) FindBestSpliceFrame(candidates []CandidateFrame) SpliceFrameResult {
	if len(candidates) == 0 {
		return SpliceFrameResult{Index: -1}
	}

	// Priority 1: find the blackest frame among those that are actually black.
	bestBlackIdx := -1
	bestBlackScore := 2.0 // higher than any real score
	for i, c := range candidates {
		bs := d.AnalyzeFrame(c.YUV, c.Width, c.Height)
		if bs.IsBlack && bs.Score < bestBlackScore {
			bestBlackScore = bs.Score
			bestBlackIdx = i
		}
	}
	if bestBlackIdx >= 0 {
		c := candidates[bestBlackIdx]
		return SpliceFrameResult{
			Index:  c.Index,
			PTS:    c.PTS,
			Reason: SpliceReasonBlack,
			Score:  bestBlackScore,
		}
	}

	// Priority 2: largest scene change above threshold.
	const sceneChangeThreshold = 0.15
	bestSceneIdx := -1
	bestSceneScore := 0.0
	for i := 1; i < len(candidates); i++ {
		prev := candidates[i-1]
		cur := candidates[i]
		sc := d.SceneChangeScore(prev.YUV, cur.YUV, cur.Width, cur.Height)
		if sc > sceneChangeThreshold && sc > bestSceneScore {
			bestSceneScore = sc
			bestSceneIdx = i
		}
	}
	if bestSceneIdx >= 0 {
		c := candidates[bestSceneIdx]
		return SpliceFrameResult{
			Index:  c.Index,
			PTS:    c.PTS,
			Reason: SpliceReasonSceneChange,
			Score:  bestSceneScore,
		}
	}

	// Priority 3: fallback to middle frame.
	mid := len(candidates) / 2
	c := candidates[mid]
	return SpliceFrameResult{
		Index:  c.Index,
		PTS:    c.PTS,
		Reason: SpliceReasonScheduled,
	}
}
