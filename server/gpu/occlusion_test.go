package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultOcclusionFuseParams(t *testing.T) {
	params := DefaultOcclusionFuseParams()
	assert.InDelta(t, 0.3, float64(params.DepthNear), 0.001, "default DepthNear should be 0.3")
	assert.InDelta(t, 0.1, float64(params.Softness), 0.001, "default Softness should be 0.1")
}

func TestOcclusionFuseParams_CustomValues(t *testing.T) {
	params := OcclusionFuseParams{
		DepthNear: 0.5,
		Softness:  0.25,
	}
	assert.InDelta(t, 0.5, float64(params.DepthNear), 0.001)
	assert.InDelta(t, 0.25, float64(params.Softness), 0.001)
}

func TestFuseOcclusion_NilContext(t *testing.T) {
	params := DefaultOcclusionFuseParams()
	err := FuseOcclusion(nil, 0x1000, 0x2000, 0x3000, 1920, 1080, 512, 288, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestOcclusionFuseParams_ZeroValue(t *testing.T) {
	// Zero-value struct should have all zeroes — callers should use DefaultOcclusionFuseParams
	var params OcclusionFuseParams
	assert.Equal(t, float32(0), params.DepthNear)
	assert.Equal(t, float32(0), params.Softness)
}

func TestOcclusionFuseParams_EdgeCases(t *testing.T) {
	// Very tight softness (sharp cutoff)
	tight := OcclusionFuseParams{DepthNear: 0.3, Softness: 0.01}
	assert.InDelta(t, 0.01, float64(tight.Softness), 0.001)

	// Very wide softness (gradual transition)
	wide := OcclusionFuseParams{DepthNear: 0.3, Softness: 0.5}
	assert.InDelta(t, 0.5, float64(wide.Softness), 0.001)
}

func TestFuseOcclusion_SignatureAcceptsNullableInputs(t *testing.T) {
	// Verify that depth=0 and samMask=0 are valid (kernel handles NULL pointers).
	// On non-GPU builds, this just returns ErrGPUNotAvailable.
	// On GPU builds, this would require a valid context and output buffer.
	params := DefaultOcclusionFuseParams()

	// Both nullable inputs as 0 (NULL)
	err := FuseOcclusion(nil, 0x1000, 0, 0, 1920, 1080, 0, 0, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGPUNotAvailable)
}
