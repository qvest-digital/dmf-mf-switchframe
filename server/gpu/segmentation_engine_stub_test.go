//go:build !cgo || !cuda || !tensorrt

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSegmentationEngineStubCreate(t *testing.T) {
	se, err := NewSegmentationEngine(nil, "/some/model.onnx")
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
	assert.Nil(t, se)
}

func TestSegmentationEngineStubNilSafe(t *testing.T) {
	var se *SegmentationEngine

	// All methods should be nil-safe.
	assert.False(t, se.IsEnabled("cam1"))
	assert.Nil(t, se.MaskForSource("cam1"))
	se.Segment("cam1", nil)
	se.DisableSource("cam1")
	se.Close()
}

func TestSegmentationEngineStubEnableSource(t *testing.T) {
	se := &SegmentationEngine{}
	err := se.EnableSource("cam1", 1920, 1080, 0.5)
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
}

func TestSegmentationEngineStubIsEnabled(t *testing.T) {
	se := &SegmentationEngine{}
	assert.False(t, se.IsEnabled("cam1"))
}

func TestSegmentationEngineStubMaskForSource(t *testing.T) {
	se := &SegmentationEngine{}
	assert.Nil(t, se.MaskForSource("cam1"))
}
