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

func TestGPUFrameReleaseHook(t *testing.T) {
	calls := 0
	f := &GPUFrame{Width: 64, Height: 64, releaseFn: func() { calls++ }}
	f.refs.Store(1) // owner reference

	f.Ref() // simulated in-flight reader
	f.Release()
	assert.Equal(t, 0, calls, "hook must not fire while references remain")

	f.Release() // owner drops last reference
	assert.Equal(t, 1, calls, "hook fires exactly once when refcount hits zero")
}
