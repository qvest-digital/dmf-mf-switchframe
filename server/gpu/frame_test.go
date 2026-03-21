//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGPUFramePlaneOffsets(t *testing.T) {
	frame := &GPUFrame{
		Width:  1920,
		Height: 1080,
		Pitch:  2048, // typical 256-aligned pitch for 1920
	}

	assert.Equal(t, 0, frame.YPlaneOffset())
	assert.Equal(t, 2048*1080, frame.UVPlaneOffset())
	assert.Equal(t, 2048*1080+2048*540, frame.NV12Size())
}

func TestGPUFrameReleaseNilSafe(t *testing.T) {
	var f *GPUFrame
	f.Release() // should not panic
}
