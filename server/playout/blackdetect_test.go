package playout

import (
	"testing"
)

func TestBlackDetector_PerfectBlack(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 1920, 1080
	ySize := w * h
	// Full YUV420p frame: Y + U (w*h/4) + V (w*h/4)
	yuv := make([]byte, ySize+ySize/2)

	score := det.AnalyzeFrame(yuv, w, h)

	if !score.IsBlack {
		t.Errorf("expected IsBlack=true, got false")
	}
	if score.MeanLuma != 0 {
		t.Errorf("expected MeanLuma=0, got %f", score.MeanLuma)
	}
	if score.MaxLuma != 0 {
		t.Errorf("expected MaxLuma=0, got %d", score.MaxLuma)
	}
	if score.Score != 0.0 {
		t.Errorf("expected Score=0.0, got %f", score.Score)
	}
}

func TestBlackDetector_BrightFrame(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 320, 240
	ySize := w * h
	yuv := make([]byte, ySize+ySize/2)
	for i := 0; i < ySize; i++ {
		yuv[i] = 200
	}

	score := det.AnalyzeFrame(yuv, w, h)

	if score.IsBlack {
		t.Errorf("expected IsBlack=false, got true")
	}
	if score.MeanLuma < 199.5 || score.MeanLuma > 200.5 {
		t.Errorf("expected MeanLuma≈200, got %f", score.MeanLuma)
	}
	if score.MaxLuma != 200 {
		t.Errorf("expected MaxLuma=200, got %d", score.MaxLuma)
	}
	// Score should be 200/255 ≈ 0.784
	expectedScore := 200.0 / 255.0
	if score.Score < expectedScore-0.01 || score.Score > expectedScore+0.01 {
		t.Errorf("expected Score≈%f, got %f", expectedScore, score.Score)
	}
}

func TestBlackDetector_NearBlack(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 640, 480
	ySize := w * h
	yuv := make([]byte, ySize+ySize/2)
	for i := 0; i < ySize; i++ {
		yuv[i] = 10
	}

	score := det.AnalyzeFrame(yuv, w, h)

	if !score.IsBlack {
		t.Errorf("expected IsBlack=true for luma=10 (below default threshold 16), got false")
	}
	if score.MeanLuma < 9.5 || score.MeanLuma > 10.5 {
		t.Errorf("expected MeanLuma≈10, got %f", score.MeanLuma)
	}
	if score.MaxLuma != 10 {
		t.Errorf("expected MaxLuma=10, got %d", score.MaxLuma)
	}
}

func TestBlackDetector_CustomThresholds(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{
		MeanThreshold: 5,
		MaxThreshold:  10,
	})

	w, h := 320, 240
	ySize := w * h
	yuv := make([]byte, ySize+ySize/2)
	for i := 0; i < ySize; i++ {
		yuv[i] = 10
	}

	score := det.AnalyzeFrame(yuv, w, h)

	if score.IsBlack {
		t.Errorf("expected IsBlack=false for luma=10 with MeanThreshold=5/MaxThreshold=10, got true")
	}
}

// --- Task 2: SceneChangeScore tests ---

func TestBlackDetector_SceneChangeScore_IdenticalFrames(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 320, 240
	ySize := w * h
	frameA := make([]byte, ySize+ySize/2)
	frameB := make([]byte, ySize+ySize/2)
	for i := 0; i < ySize; i++ {
		frameA[i] = 128
		frameB[i] = 128
	}

	score := det.SceneChangeScore(frameA, frameB, w, h)

	if score != 0.0 {
		t.Errorf("expected score=0.0 for identical frames, got %f", score)
	}
}

func TestBlackDetector_SceneChangeScore_TotalChange(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 320, 240
	ySize := w * h
	frameA := make([]byte, ySize+ySize/2)
	frameB := make([]byte, ySize+ySize/2)
	// frameA all-0 (default), frameB all-255
	for i := 0; i < ySize; i++ {
		frameB[i] = 255
	}

	score := det.SceneChangeScore(frameA, frameB, w, h)

	if score != 1.0 {
		t.Errorf("expected score=1.0 for max difference, got %f", score)
	}
}

func TestBlackDetector_SceneChangeScore_PartialChange(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 320, 240
	ySize := w * h
	frameA := make([]byte, ySize+ySize/2)
	frameB := make([]byte, ySize+ySize/2)
	// Half the pixels differ by 128
	for i := 0; i < ySize/2; i++ {
		frameA[i] = 0
		frameB[i] = 128
	}
	// Other half identical
	for i := ySize / 2; i < ySize; i++ {
		frameA[i] = 100
		frameB[i] = 100
	}

	score := det.SceneChangeScore(frameA, frameB, w, h)

	// Expected: (half * 128) / (total * 255) = (ySize/2 * 128) / (ySize * 255)
	// = 128 / (2*255) = 128/510 ≈ 0.25098
	expected := 128.0 / 510.0
	if score < expected-0.01 || score > expected+0.01 {
		t.Errorf("expected score≈%f, got %f", expected, score)
	}
}

// --- Task 3: FindBestSpliceFrame tests ---

func TestBlackDetector_FindBestSpliceFrame_PicksBlack(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 64, 48
	ySize := w * h

	candidates := make([]CandidateFrame, 10)
	for i := range candidates {
		yuv := make([]byte, ySize+ySize/2)
		// All frames bright (luma=200) except index 4
		lumaVal := byte(200)
		if i == 4 {
			lumaVal = 0 // black
		}
		for j := 0; j < ySize; j++ {
			yuv[j] = lumaVal
		}
		candidates[i] = CandidateFrame{
			YUV:    yuv,
			PTS:    int64(i * 3000),
			Index:  i,
			Width:  w,
			Height: h,
		}
	}

	result := det.FindBestSpliceFrame(candidates)

	if result.Index != 4 {
		t.Errorf("expected Index=4, got %d", result.Index)
	}
	if result.Reason != SpliceReasonBlack {
		t.Errorf("expected Reason=%q, got %q", SpliceReasonBlack, result.Reason)
	}
	if result.PTS != 12000 {
		t.Errorf("expected PTS=12000, got %d", result.PTS)
	}
}

func TestBlackDetector_FindBestSpliceFrame_FallbackSceneChange(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 64, 48
	ySize := w * h

	candidates := make([]CandidateFrame, 10)
	for i := range candidates {
		yuv := make([]byte, ySize+ySize/2)
		// All frames at luma=100 except frame 6 jumps to 200
		lumaVal := byte(100)
		if i >= 6 {
			lumaVal = 200
		}
		for j := 0; j < ySize; j++ {
			yuv[j] = lumaVal
		}
		candidates[i] = CandidateFrame{
			YUV:    yuv,
			PTS:    int64(i * 3000),
			Index:  i,
			Width:  w,
			Height: h,
		}
	}

	result := det.FindBestSpliceFrame(candidates)

	if result.Index != 6 {
		t.Errorf("expected Index=6, got %d", result.Index)
	}
	if result.Reason != SpliceReasonSceneChange {
		t.Errorf("expected Reason=%q, got %q", SpliceReasonSceneChange, result.Reason)
	}
	if result.PTS != 18000 {
		t.Errorf("expected PTS=18000, got %d", result.PTS)
	}
	// Score should be 100/255 ≈ 0.392
	expectedScore := 100.0 / 255.0
	if result.Score < expectedScore-0.01 || result.Score > expectedScore+0.01 {
		t.Errorf("expected Score≈%f, got %f", expectedScore, result.Score)
	}
}

func TestBlackDetector_FindBestSpliceFrame_FallbackMiddle(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	w, h := 64, 48
	ySize := w * h

	candidates := make([]CandidateFrame, 10)
	for i := range candidates {
		yuv := make([]byte, ySize+ySize/2)
		// All frames identical bright
		for j := 0; j < ySize; j++ {
			yuv[j] = 200
		}
		candidates[i] = CandidateFrame{
			YUV:    yuv,
			PTS:    int64(i * 3000),
			Index:  i,
			Width:  w,
			Height: h,
		}
	}

	result := det.FindBestSpliceFrame(candidates)

	if result.Index != 5 {
		t.Errorf("expected Index=5 (middle of 10), got %d", result.Index)
	}
	if result.Reason != SpliceReasonScheduled {
		t.Errorf("expected Reason=%q, got %q", SpliceReasonScheduled, result.Reason)
	}
	if result.PTS != 15000 {
		t.Errorf("expected PTS=15000, got %d", result.PTS)
	}
}

func TestBlackDetector_FindBestSpliceFrame_Empty(t *testing.T) {
	det := NewBlackDetector(BlackDetectorConfig{})

	result := det.FindBestSpliceFrame(nil)

	if result.Index != -1 {
		t.Errorf("expected Index=-1 for empty candidates, got %d", result.Index)
	}
}
