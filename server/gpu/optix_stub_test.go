//go:build !cgo || !cuda || !optix

package gpu

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptixStubNewContext(t *testing.T) {
	ctx, err := NewOptixContext(nil)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, ctx)
}

func TestOptixStubContextClose(t *testing.T) {
	ctx := &OptixContext{}
	ctx.Close() // should not panic

	var nilCtx *OptixContext
	nilCtx.Close() // should not panic
}

func TestOptixStubCreateModule(t *testing.T) {
	ctx := &OptixContext{}
	mod, err := ctx.CreateModule([]byte("fake ptx"))
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, mod)
}

func TestOptixStubModuleClose(t *testing.T) {
	mod := &OptixModule{}
	mod.Close() // should not panic

	var nilMod *OptixModule
	nilMod.Close() // should not panic
}

func TestOptixStubCreatePipeline(t *testing.T) {
	ctx := &OptixContext{}
	pipeline, err := ctx.CreatePipeline(OptixPipelineConfig{
		Module:          &OptixModule{},
		RaygenEntry:     "__raygen__render",
		MissEntry:       "__miss__transparent",
		ClosestHitEntry: "__closesthit__pbr",
		MaxTraceDepth:   2,
	})
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, pipeline)
}

func TestOptixStubPipelineClose(t *testing.T) {
	p := &OptixPipeline{}
	p.Close() // should not panic

	var nilP *OptixPipeline
	nilP.Close() // should not panic
}

func TestOptixStubPipelineLaunch(t *testing.T) {
	p := &OptixPipeline{}
	err := p.Launch(OptixLaunchParams{
		Width:           1920,
		Height:          1080,
		SamplesPerPixel: 1,
	}, nil)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
}

func TestOptixStubBuildTriangleGAS(t *testing.T) {
	ctx := &OptixContext{}
	gas, err := ctx.BuildTriangleGAS(
		[]float32{0, 0, 0, 1, 0, 0, 0, 1, 0}, // triangle
		[]uint32{0, 1, 2},
		nil,
	)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, gas)
}

func TestOptixStubGASClose(t *testing.T) {
	gas := &OptixGAS{}
	gas.Close() // should not panic

	var nilGAS *OptixGAS
	nilGAS.Close() // should not panic
}

func TestOptixStubBuildIAS(t *testing.T) {
	ctx := &OptixContext{}
	ias, err := ctx.BuildIAS([]OptixInstance{{
		GAS:            &OptixGAS{},
		Transform:      [12]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0},
		SBTOffset:      0,
		VisibilityMask: 0xFF,
	}}, nil)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, ias)
}

func TestOptixStubIASUpdate(t *testing.T) {
	ias := &OptixIAS{}
	err := ias.Update([]OptixInstance{{
		GAS:            &OptixGAS{},
		Transform:      [12]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0},
		VisibilityMask: 0xFF,
	}}, nil)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
}

func TestOptixStubIASClose(t *testing.T) {
	ias := &OptixIAS{}
	ias.Close() // should not panic

	var nilIAS *OptixIAS
	nilIAS.Close() // should not panic
}

func TestOptixStubNewDenoiser(t *testing.T) {
	ctx := &OptixContext{}
	denoiser, err := NewOptixDenoiser(ctx, 1920, 1080, false)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, denoiser)
}

func TestOptixStubNewDenoiserTemporal(t *testing.T) {
	ctx := &OptixContext{}
	denoiser, err := NewOptixDenoiser(ctx, 1920, 1080, true)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
	assert.Nil(t, denoiser)
}

func TestOptixStubDenoiserDenoise(t *testing.T) {
	d := &OptixDenoiser{}
	var dummy1, dummy2 byte
	err := d.Denoise(
		unsafe.Pointer(&dummy1),
		unsafe.Pointer(&dummy2),
		nil,
		1920, 1080, nil,
	)
	require.ErrorIs(t, err, ErrOptiXNotAvailable)
}

func TestOptixStubDenoiserClose(t *testing.T) {
	d := &OptixDenoiser{}
	d.Close() // should not panic

	var nilD *OptixDenoiser
	nilD.Close() // should not panic
}

func TestOptixStubDenoiserAccessors(t *testing.T) {
	d := &OptixDenoiser{}
	assert.Equal(t, 0, d.Width())
	assert.Equal(t, 0, d.Height())
	assert.False(t, d.Temporal())

	var nilD *OptixDenoiser
	assert.Equal(t, 0, nilD.Width())
	assert.Equal(t, 0, nilD.Height())
	assert.False(t, nilD.Temporal())
}

func TestOptixStubPipelineConfigFields(t *testing.T) {
	config := OptixPipelineConfig{
		Module:          &OptixModule{},
		RaygenEntry:     "__raygen__render",
		MissEntry:       "__miss__transparent",
		ShadowMissEntry: "__miss__shadow",
		ClosestHitEntry: "__closesthit__pbr",
		AnyHitEntry:     "__anyhit__shadow",
		MaxTraceDepth:   2,
	}
	assert.NotNil(t, config.Module)
	assert.Equal(t, "__raygen__render", config.RaygenEntry)
	assert.Equal(t, "__miss__transparent", config.MissEntry)
	assert.Equal(t, "__miss__shadow", config.ShadowMissEntry)
	assert.Equal(t, "__closesthit__pbr", config.ClosestHitEntry)
	assert.Equal(t, "__anyhit__shadow", config.AnyHitEntry)
	assert.Equal(t, 2, config.MaxTraceDepth)
}

func TestOptixStubInstanceFields(t *testing.T) {
	identity := [12]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0}
	inst := OptixInstance{
		GAS:            &OptixGAS{},
		Transform:      identity,
		SBTOffset:      3,
		VisibilityMask: 0xFF,
	}
	assert.NotNil(t, inst.GAS)
	assert.Equal(t, identity, inst.Transform)
	assert.Equal(t, uint32(3), inst.SBTOffset)
	assert.Equal(t, uint32(0xFF), inst.VisibilityMask)
}

func TestOptixStubLaunchParamsFields(t *testing.T) {
	params := OptixLaunchParams{
		Width:           1920,
		Height:          1080,
		CameraPos:       [3]float32{0, 1, 5},
		CameraU:         [3]float32{1, 0, 0},
		CameraV:         [3]float32{0, 1, 0},
		CameraW:         [3]float32{0, 0, -1},
		SamplesPerPixel: 4,
		FrameNumber:     42,
		Lights:          nil,
		NumLights:       0,
	}
	assert.Equal(t, 1920, params.Width)
	assert.Equal(t, 1080, params.Height)
	assert.Equal(t, [3]float32{0, 1, 5}, params.CameraPos)
	assert.Equal(t, 4, params.SamplesPerPixel)
	assert.Equal(t, 42, params.FrameNumber)
	assert.Nil(t, params.Lights)
	assert.Equal(t, 0, params.NumLights)
}
