//go:build !cgo || !cuda || !tensorrt

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTRTStubNewEngine(t *testing.T) {
	engine, err := NewTRTEngine("/some/model.onnx", TRTEngineOpts{})
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
	assert.Nil(t, engine)
}

func TestTRTStubNewEngineV2(t *testing.T) {
	engine, err := NewTRTEngineV2("/some/model.onnx", TRTEngineOptsV2{})
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
	assert.Nil(t, engine)
}

func TestTRTStubLoadPlan(t *testing.T) {
	engine, err := LoadTRTPlan("/some/model.plan")
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
	assert.Nil(t, engine)
}

func TestTRTStubLoadPlanFromMemory(t *testing.T) {
	engine, err := LoadTRTPlanFromMemory([]byte("fake plan data"))
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
	assert.Nil(t, engine)
}

func TestTRTStubEngineNewContext(t *testing.T) {
	engine := &TRTEngine{}
	ctx, err := engine.NewContext()
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
	assert.Nil(t, ctx)
}

func TestTRTStubEngineSizes(t *testing.T) {
	engine := &TRTEngine{}
	assert.Equal(t, 0, engine.InputSize())
	assert.Equal(t, 0, engine.OutputSize())
	assert.Equal(t, 0, engine.NumIOTensors())
}

func TestTRTStubEngineTensorInfo(t *testing.T) {
	engine := &TRTEngine{}
	_, err := engine.TensorInfoAt(0)
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
}

func TestTRTStubEngineHandle(t *testing.T) {
	engine := &TRTEngine{}
	assert.Nil(t, engine.Handle())

	var nilEngine *TRTEngine
	assert.Nil(t, nilEngine.Handle())
}

func TestTRTStubEngineClose(t *testing.T) {
	engine := &TRTEngine{}
	engine.Close() // should not panic

	var nilEngine *TRTEngine
	nilEngine.Close() // should not panic
}

func TestTRTStubContextInfer(t *testing.T) {
	ctx := &TRTContext{}
	err := ctx.Infer(nil, nil, 1, nil)
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
}

func TestTRTStubContextInferMulti(t *testing.T) {
	ctx := &TRTContext{}
	err := ctx.InferMulti([]TRTBinding{{Name: "test"}}, nil)
	require.ErrorIs(t, err, ErrTensorRTNotAvailable)
}

func TestTRTStubContextClose(t *testing.T) {
	ctx := &TRTContext{}
	ctx.Close() // should not panic

	var nilCtx *TRTContext
	nilCtx.Close() // should not panic
}

func TestTRTStubDTypeSize(t *testing.T) {
	assert.Equal(t, 0, TRTDTypeSize(0))
	assert.Equal(t, 0, TRTDTypeSize(1))
	assert.Equal(t, 0, TRTDTypeSize(99))
}

func TestTRTStubTensorBufferSize(t *testing.T) {
	info := TensorInfo{
		Name:    "input",
		IsInput: true,
		DType:   0, // float32
		Dims:    []int{1, 3, 256, 256},
	}
	assert.Equal(t, 0, TensorBufferSize(info))
}

func TestTRTStubTensorInfoFields(t *testing.T) {
	info := TensorInfo{
		Name:    "output",
		IsInput: false,
		DType:   1,
		Dims:    []int{1, 1, 256, 256},
	}
	assert.Equal(t, "output", info.Name)
	assert.False(t, info.IsInput)
	assert.Equal(t, 1, info.DType)
	assert.Equal(t, []int{1, 1, 256, 256}, info.Dims)
}

func TestTRTStubBindingFields(t *testing.T) {
	binding := TRTBinding{
		Name:   "input",
		DevPtr: nil,
		Dims:   []int{1, 3, 512, 512},
	}
	assert.Equal(t, "input", binding.Name)
	assert.Nil(t, binding.DevPtr)
	assert.Equal(t, []int{1, 3, 512, 512}, binding.Dims)
}

func TestTRTStubEngineOptsFields(t *testing.T) {
	opts := TRTEngineOpts{
		MaxBatchSize:  4,
		UseFP16:       true,
		UseINT8:       false,
		PlanCachePath: "/tmp/model.plan",
	}
	assert.Equal(t, 4, opts.MaxBatchSize)
	assert.True(t, opts.UseFP16)
	assert.False(t, opts.UseINT8)
	assert.Equal(t, "/tmp/model.plan", opts.PlanCachePath)
}

func TestTRTStubEngineOptsV2Fields(t *testing.T) {
	opts := TRTEngineOptsV2{
		MaxBatchSize:  1,
		MaxSeqLen:     512,
		MaxEncSeqLen:  1024,
		UseFP16:       true,
		UseINT8:       false,
		PlanCachePath: "/tmp/model_v2.plan",
	}
	assert.Equal(t, 1, opts.MaxBatchSize)
	assert.Equal(t, 512, opts.MaxSeqLen)
	assert.Equal(t, 1024, opts.MaxEncSeqLen)
	assert.True(t, opts.UseFP16)
	assert.Equal(t, "/tmp/model_v2.plan", opts.PlanCachePath)
}
