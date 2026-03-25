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
