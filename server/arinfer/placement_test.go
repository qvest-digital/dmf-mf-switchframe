package arinfer

import (
	"encoding/json"
	"math"
	"testing"
)

func TestComputePlacement_CenterPixel(t *testing.T) {
	// A click at the exact center of the frame should produce X≈0, Y≈0.
	// With depthValue=1.0: metricDepth = 3.0/(1.0+0.01) ≈ 2.97
	result := ComputePlacement(960, 540, 1920, 1080, 1.0, 50, 36)

	if math.Abs(result.Position[0]) > 0.001 {
		t.Errorf("center pixel X = %f, want ~0", result.Position[0])
	}
	if math.Abs(result.Position[1]) > 0.001 {
		t.Errorf("center pixel Y = %f, want ~0", result.Position[1])
	}
	if result.Position[2] <= 0 {
		t.Errorf("center pixel Z = %f, want > 0", result.Position[2])
	}
	if result.Depth <= 0 {
		t.Errorf("depth = %f, want > 0", result.Depth)
	}
}

func TestComputePlacement_TopLeft(t *testing.T) {
	// Top-left corner (0,0): X should be negative (left), Y should be positive (up, since we flip).
	result := ComputePlacement(0, 0, 1920, 1080, 1.0, 50, 36)

	if result.Position[0] >= 0 {
		t.Errorf("top-left X = %f, want < 0", result.Position[0])
	}
	if result.Position[1] <= 0 {
		t.Errorf("top-left Y = %f, want > 0 (screen Y flipped)", result.Position[1])
	}
	if result.Position[2] <= 0 {
		t.Errorf("top-left Z = %f, want > 0", result.Position[2])
	}
}

func TestComputePlacement_BottomRight(t *testing.T) {
	// Bottom-right corner (1919,1079): X should be positive (right), Y should be negative (down, since we flip).
	result := ComputePlacement(1919, 1079, 1920, 1080, 1.0, 50, 36)

	if result.Position[0] <= 0 {
		t.Errorf("bottom-right X = %f, want > 0", result.Position[0])
	}
	if result.Position[1] >= 0 {
		t.Errorf("bottom-right Y = %f, want < 0 (screen Y flipped)", result.Position[1])
	}
	if result.Position[2] <= 0 {
		t.Errorf("bottom-right Z = %f, want > 0", result.Position[2])
	}
}

func TestComputePlacement_DefaultFocalLength(t *testing.T) {
	// Pass zero values for focal/sensor — should use defaults (50mm / 36mm).
	resultDefault := ComputePlacement(960, 540, 1920, 1080, 1.0, 0, 0)
	resultExplicit := ComputePlacement(960, 540, 1920, 1080, 1.0, 50, 36)

	if resultDefault.Depth != resultExplicit.Depth {
		t.Errorf("default depth = %f, explicit depth = %f, want equal", resultDefault.Depth, resultExplicit.Depth)
	}
	for i := 0; i < 3; i++ {
		if resultDefault.Position[i] != resultExplicit.Position[i] {
			t.Errorf("default position[%d] = %f, explicit = %f, want equal", i, resultDefault.Position[i], resultExplicit.Position[i])
		}
	}
}

func TestComputePlacement_NegativeFocalLength(t *testing.T) {
	// Negative focal/sensor should be clamped to defaults.
	result := ComputePlacement(960, 540, 1920, 1080, 1.0, -10, -5)
	resultDefault := ComputePlacement(960, 540, 1920, 1080, 1.0, 50, 36)

	if result.Depth != resultDefault.Depth {
		t.Errorf("negative focal depth = %f, default = %f, want equal", result.Depth, resultDefault.Depth)
	}
}

func TestComputePlacement_HighDepthValue(t *testing.T) {
	// High inverse depth value (closer object) should produce smaller metric depth.
	near := ComputePlacement(960, 540, 1920, 1080, 10.0, 50, 36)
	far := ComputePlacement(960, 540, 1920, 1080, 0.5, 50, 36)

	if near.Depth >= far.Depth {
		t.Errorf("near depth (%f) should be less than far depth (%f)", near.Depth, far.Depth)
	}
}

func TestComputePlacement_ZeroDepthValue(t *testing.T) {
	// Zero depth value should not panic (protected by +0.01 in denominator).
	result := ComputePlacement(960, 540, 1920, 1080, 0.0, 50, 36)

	if math.IsNaN(result.Depth) || math.IsInf(result.Depth, 0) {
		t.Errorf("zero depth value produced NaN/Inf: %f", result.Depth)
	}
	if result.Depth <= 0 {
		t.Errorf("depth = %f, want > 0", result.Depth)
	}
}

func TestComputePlacement_DifferentResolutions(t *testing.T) {
	// Center pixel at different resolutions should still be near (0,0,z).
	resolutions := [][2]int{{1920, 1080}, {1280, 720}, {3840, 2160}}
	for _, res := range resolutions {
		w, h := res[0], res[1]
		result := ComputePlacement(w/2, h/2, w, h, 1.0, 50, 36)
		if math.Abs(result.Position[0]) > 0.001 {
			t.Errorf("res %dx%d: center X = %f, want ~0", w, h, result.Position[0])
		}
		if math.Abs(result.Position[1]) > 0.001 {
			t.Errorf("res %dx%d: center Y = %f, want ~0", w, h, result.Position[1])
		}
	}
}

func TestPlacementResult_JSON(t *testing.T) {
	result := PlacementResult{
		Position: [3]float64{1.5, -0.3, 2.97},
		Depth:    2.97,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded PlacementResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	for i := 0; i < 3; i++ {
		if math.Abs(decoded.Position[i]-result.Position[i]) > 0.001 {
			t.Errorf("position[%d] = %f, want %f", i, decoded.Position[i], result.Position[i])
		}
	}
	if math.Abs(decoded.Depth-result.Depth) > 0.001 {
		t.Errorf("depth = %f, want %f", decoded.Depth, result.Depth)
	}
}

func TestComputePlacement_Symmetry(t *testing.T) {
	// Points equidistant from center should have symmetric X/Y magnitudes.
	left := ComputePlacement(460, 540, 1920, 1080, 1.0, 50, 36)
	right := ComputePlacement(1460, 540, 1920, 1080, 1.0, 50, 36)

	// Both are 500px from center horizontally. X should be opposite signs, same magnitude.
	if math.Abs(math.Abs(left.Position[0])-math.Abs(right.Position[0])) > 0.001 {
		t.Errorf("X magnitudes differ: left=%f, right=%f", left.Position[0], right.Position[0])
	}
	if left.Position[0] >= 0 {
		t.Errorf("left X = %f, want < 0", left.Position[0])
	}
	if right.Position[0] <= 0 {
		t.Errorf("right X = %f, want > 0", right.Position[0])
	}
}
