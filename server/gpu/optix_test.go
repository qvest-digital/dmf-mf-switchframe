//go:build cgo && cuda && optix

package gpu

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptixContextCreate(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	// Get the CUDA context for OptiX init.
	// OptiX needs a CUcontext, which we get from the GPU context.
	optCtx, err := NewOptixContext(nil) // NULL = use current CUDA context
	require.NoError(t, err)
	require.NotNil(t, optCtx)
	defer optCtx.Close()
}

func TestOptixContextNilClose(t *testing.T) {
	var ctx *OptixContext
	ctx.Close() // should not panic
}

func TestOptixModuleCreateEmpty(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	optCtx, err := NewOptixContext(nil)
	require.NoError(t, err)
	defer optCtx.Close()

	// Empty PTX should fail.
	mod, err := optCtx.CreateModule(nil)
	require.Error(t, err)
	assert.Nil(t, mod)

	mod, err = optCtx.CreateModule([]byte{})
	require.Error(t, err)
	assert.Nil(t, mod)
}

func TestOptixGASBuildInvalidVertices(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	optCtx, err := NewOptixContext(nil)
	require.NoError(t, err)
	defer optCtx.Close()

	// Empty vertices should fail.
	gas, err := optCtx.BuildTriangleGAS(nil, nil, nil)
	require.Error(t, err)
	assert.Nil(t, gas)

	// Non-divisible by 3 should fail.
	gas, err = optCtx.BuildTriangleGAS([]float32{1, 2}, nil, nil)
	require.Error(t, err)
	assert.Nil(t, gas)
}

func TestOptixIASBuildEmpty(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	optCtx, err := NewOptixContext(nil)
	require.NoError(t, err)
	defer optCtx.Close()

	// Empty instances should fail.
	ias, err := optCtx.BuildIAS(nil, nil)
	require.Error(t, err)
	assert.Nil(t, ias)

	ias, err = optCtx.BuildIAS([]OptixInstance{}, nil)
	require.Error(t, err)
	assert.Nil(t, ias)
}

func TestOptixDenoiserInvalidDimensions(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	optCtx, err := NewOptixContext(nil)
	require.NoError(t, err)
	defer optCtx.Close()

	d, err := NewOptixDenoiser(optCtx, 0, 0, false)
	require.Error(t, err)
	assert.Nil(t, d)

	d, err = NewOptixDenoiser(optCtx, -1, 1080, false)
	require.Error(t, err)
	assert.Nil(t, d)
}

func TestOptixDenoiserNilContext(t *testing.T) {
	d, err := NewOptixDenoiser(nil, 1920, 1080, false)
	require.Error(t, err)
	assert.Nil(t, d)
}

func TestOptixPipelineNilModule(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	optCtx, err := NewOptixContext(nil)
	require.NoError(t, err)
	defer optCtx.Close()

	p, err := optCtx.CreatePipeline(OptixPipelineConfig{
		Module:          nil,
		RaygenEntry:     "__raygen__test",
		MissEntry:       "__miss__test",
		ClosestHitEntry: "__closesthit__test",
	})
	require.Error(t, err)
	assert.Nil(t, p)
}

func TestOptixPipelineLaunchNil(t *testing.T) {
	var p *OptixPipeline
	err := p.Launch(OptixLaunchParams{}, nil)
	require.Error(t, err)
}

func TestOptixDenoiserDenoiseNil(t *testing.T) {
	var d *OptixDenoiser
	err := d.Denoise(nil, nil, nil, 1920, 1080, nil)
	require.Error(t, err)
}

func TestOptixDenoiserDenoiseNilPointers(t *testing.T) {
	gpuCtx := testContext(t)
	_ = gpuCtx

	optCtx, err := NewOptixContext(nil)
	require.NoError(t, err)
	defer optCtx.Close()

	d, err := NewOptixDenoiser(optCtx, 1920, 1080, false)
	if err != nil {
		t.Skipf("Denoiser creation failed (expected without OptiX SDK): %v", err)
	}
	defer d.Close()

	err = d.Denoise(nil, unsafe.Pointer(uintptr(0x1000)), nil, 1920, 1080, nil)
	require.Error(t, err)
}

func TestOptixIASUpdateNil(t *testing.T) {
	var ias *OptixIAS
	err := ias.Update(nil, nil)
	require.Error(t, err)
}

func TestOptixDenoiserAccessors(t *testing.T) {
	var d *OptixDenoiser
	assert.Equal(t, 0, d.Width())
	assert.Equal(t, 0, d.Height())
	assert.False(t, d.Temporal())
}
