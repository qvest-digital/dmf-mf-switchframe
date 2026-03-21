//go:build darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeGPUCropRect_WiderSource(t *testing.T) {
	// 16:9 source into 4:3 slot — should crop horizontally.
	cropX, cropY, cropW, cropH := computeGPUCropRect(1920, 1080, 320, 240, [2]float64{0.5, 0.5})
	assert.Greater(t, cropW, 0, "crop width should be > 0")
	assert.Equal(t, 1080, cropH, "crop height should match source height")
	assert.Less(t, cropW, 1920, "crop width should be less than source width")
	// Verify even alignment
	assert.Equal(t, 0, cropX%2, "cropX must be even-aligned")
	assert.Equal(t, 0, cropY%2, "cropY must be even-aligned")
	assert.Equal(t, 0, cropW%2, "cropW must be even-aligned")
	assert.Equal(t, 0, cropH%2, "cropH must be even-aligned")
	// Verify aspect ratio match: cropW/cropH should be close to 320/240 = 4/3
	expectedCropW := int(float64(1080) * (320.0 / 240.0))
	expectedCropW &^= 1
	assert.Equal(t, expectedCropW, cropW)
	t.Logf("16:9 source → 4:3 slot: crop=(%d,%d,%d,%d)", cropX, cropY, cropW, cropH)
}

func TestComputeGPUCropRect_TallerSource(t *testing.T) {
	// 4:3 source into 16:9 slot — should crop vertically.
	cropX, cropY, cropW, cropH := computeGPUCropRect(640, 480, 320, 180, [2]float64{0.5, 0.5})
	assert.Equal(t, 640, cropW, "crop width should match source width")
	assert.Greater(t, cropH, 0, "crop height should be > 0")
	assert.Less(t, cropH, 480, "crop height should be less than source height")
	assert.Equal(t, 0, cropX%2, "cropX must be even-aligned")
	assert.Equal(t, 0, cropY%2, "cropY must be even-aligned")
	assert.Equal(t, 0, cropW%2, "cropW must be even-aligned")
	assert.Equal(t, 0, cropH%2, "cropH must be even-aligned")
	t.Logf("4:3 source → 16:9 slot: crop=(%d,%d,%d,%d)", cropX, cropY, cropW, cropH)
}

func TestComputeGPUCropRect_MatchingAspect(t *testing.T) {
	// Same aspect ratio — no crop needed.
	cropX, cropY, cropW, cropH := computeGPUCropRect(1920, 1080, 960, 540, [2]float64{0.5, 0.5})
	assert.Equal(t, 0, cropX)
	assert.Equal(t, 0, cropY)
	assert.Equal(t, 0, cropW, "no crop needed when aspect ratios match")
	assert.Equal(t, 0, cropH, "no crop needed when aspect ratios match")
}

func TestComputeGPUCropRect_AnchorTopLeft(t *testing.T) {
	// Crop with anchor at top-left corner.
	cropX, cropY, _, _ := computeGPUCropRect(1920, 1080, 320, 240, [2]float64{0.0, 0.0})
	assert.Equal(t, 0, cropX, "top-left anchor should have cropX=0")
	assert.Equal(t, 0, cropY, "top-left anchor should have cropY=0")
}

func TestComputeGPUCropRect_AnchorBottomRight(t *testing.T) {
	// Crop with anchor at bottom-right corner.
	cropX, _, cropW, _ := computeGPUCropRect(1920, 1080, 320, 240, [2]float64{1.0, 1.0})
	maxX := 1920 - cropW
	maxX &^= 1
	assert.Equal(t, maxX, cropX, "bottom-right anchor should place crop at max offset")
}

func TestComputeGPUCropRect_ZeroInputs(t *testing.T) {
	cropX, cropY, cropW, cropH := computeGPUCropRect(0, 0, 320, 240, [2]float64{0.5, 0.5})
	assert.Equal(t, 0, cropX)
	assert.Equal(t, 0, cropY)
	assert.Equal(t, 0, cropW)
	assert.Equal(t, 0, cropH)

	cropX, cropY, cropW, cropH = computeGPUCropRect(1920, 1080, 0, 0, [2]float64{0.5, 0.5})
	assert.Equal(t, 0, cropX)
	assert.Equal(t, 0, cropY)
	assert.Equal(t, 0, cropW)
	assert.Equal(t, 0, cropH)
}

func TestComputeGPUCropRect_EvenAlignment(t *testing.T) {
	// Use odd source dimensions to stress even-alignment.
	cropX, cropY, cropW, cropH := computeGPUCropRect(1921, 1081, 321, 241, [2]float64{0.5, 0.5})
	if cropW > 0 {
		assert.Equal(t, 0, cropX%2, "cropX must be even-aligned")
		assert.Equal(t, 0, cropY%2, "cropY must be even-aligned")
		assert.Equal(t, 0, cropW%2, "cropW must be even-aligned")
		assert.Equal(t, 0, cropH%2, "cropH must be even-aligned")
	}
}
