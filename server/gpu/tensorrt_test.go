//go:build cgo && cuda && tensorrt

package gpu

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func trtModelPath(t *testing.T) string {
	t.Helper()
	// Use the segmentation model that's already available on the GPU server.
	path := os.Getenv("TRT_TEST_MODEL_PATH")
	if path == "" {
		path = "/opt/switchframe/models/rvm_mobilenetv3_fp32.onnx"
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("TRT test model not found at %s (set TRT_TEST_MODEL_PATH)", path)
	}
	return path
}

func TestTRTEngineCreate(t *testing.T) {
	modelPath := trtModelPath(t)
	ctx := testContext(t)
	_ = ctx // ensure GPU is available

	planPath := t.TempDir() + "/test.plan"

	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	require.NoError(t, err)
	require.NotNil(t, engine)
	defer engine.Close()

	assert.Greater(t, engine.InputSize(), 0, "input size should be > 0")
	assert.Greater(t, engine.OutputSize(), 0, "output size should be > 0")
	assert.Greater(t, engine.NumIOTensors(), 0, "should have I/O tensors")

	// Plan file should have been written.
	_, err = os.Stat(planPath)
	assert.NoError(t, err, "plan file should exist")
}

func TestTRTEngineLoadPlan(t *testing.T) {
	modelPath := trtModelPath(t)
	ctx := testContext(t)
	_ = ctx

	planPath := t.TempDir() + "/test.plan"

	// Build first.
	engine1, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	require.NoError(t, err)
	engine1.Close()

	// Load from plan using LoadTRTPlan.
	engine2, err := LoadTRTPlan(planPath)
	require.NoError(t, err)
	require.NotNil(t, engine2)
	defer engine2.Close()

	assert.Greater(t, engine2.InputSize(), 0)
	assert.Greater(t, engine2.OutputSize(), 0)
}

func TestTRTEngineLoadPlanFromMemory(t *testing.T) {
	modelPath := trtModelPath(t)
	ctx := testContext(t)
	_ = ctx

	planPath := t.TempDir() + "/test.plan"

	// Build first.
	engine1, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	require.NoError(t, err)
	engine1.Close()

	// Read plan into memory.
	planData, err := os.ReadFile(planPath)
	require.NoError(t, err)

	// Load from memory.
	engine2, err := LoadTRTPlanFromMemory(planData)
	require.NoError(t, err)
	require.NotNil(t, engine2)
	defer engine2.Close()

	assert.Greater(t, engine2.InputSize(), 0)
	assert.Greater(t, engine2.OutputSize(), 0)
}

func TestTRTEngineLoadPlanFromMemoryEmpty(t *testing.T) {
	engine, err := LoadTRTPlanFromMemory(nil)
	require.Error(t, err)
	assert.Nil(t, engine)
	assert.Contains(t, err.Error(), "empty plan data")

	engine, err = LoadTRTPlanFromMemory([]byte{})
	require.Error(t, err)
	assert.Nil(t, engine)
}

func TestTRTLoadPlanNotFound(t *testing.T) {
	engine, err := LoadTRTPlan("/nonexistent/model.plan")
	require.Error(t, err)
	assert.Nil(t, engine)
	assert.Contains(t, err.Error(), "plan file not found")
}

func TestTRTEngineContext(t *testing.T) {
	modelPath := trtModelPath(t)
	ctx := testContext(t)
	_ = ctx

	planPath := t.TempDir() + "/test.plan"
	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	require.NoError(t, err)
	defer engine.Close()

	// Create execution context.
	trtCtx, err := engine.NewContext()
	require.NoError(t, err)
	require.NotNil(t, trtCtx)
	defer trtCtx.Close()

	// Create a second context (multi-context support).
	trtCtx2, err := engine.NewContext()
	require.NoError(t, err)
	require.NotNil(t, trtCtx2)
	defer trtCtx2.Close()
}

func TestTRTEngineTensorInfo(t *testing.T) {
	modelPath := trtModelPath(t)
	ctx := testContext(t)
	_ = ctx

	planPath := t.TempDir() + "/test.plan"
	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	require.NoError(t, err)
	defer engine.Close()

	numIO := engine.NumIOTensors()
	assert.Greater(t, numIO, 0)

	// Query each tensor.
	foundInput := false
	foundOutput := false
	for i := 0; i < numIO; i++ {
		info, infoErr := engine.TensorInfoAt(i)
		require.NoError(t, infoErr)
		assert.NotEmpty(t, info.Name)
		assert.Greater(t, len(info.Dims), 0)
		if info.IsInput {
			foundInput = true
		} else {
			foundOutput = true
		}
	}
	assert.True(t, foundInput, "should have at least one input tensor")
	assert.True(t, foundOutput, "should have at least one output tensor")

	// Out of range index.
	_, err = engine.TensorInfoAt(numIO)
	assert.Error(t, err)
}

func TestTRTDTypeSizeGPU(t *testing.T) {
	_ = testContext(t)

	assert.Equal(t, 4, TRTDTypeSize(0))  // kFLOAT
	assert.Equal(t, 2, TRTDTypeSize(1))  // kHALF
	assert.Equal(t, 1, TRTDTypeSize(2))  // kINT8
	assert.Equal(t, 4, TRTDTypeSize(3))  // kINT32
	assert.Equal(t, 1, TRTDTypeSize(4))  // kBOOL
	assert.Equal(t, 8, TRTDTypeSize(8))  // kINT64 (TRT 10.x: enum value 8)
	assert.Equal(t, 0, TRTDTypeSize(99)) // unknown
}

func TestTRTTensorBufferSize(t *testing.T) {
	_ = testContext(t)

	info := TensorInfo{
		Name:    "input",
		IsInput: true,
		DType:   0, // float32 = 4 bytes
		Dims:    []int{1, 3, 256, 256},
	}
	// 1 * 3 * 256 * 256 * 4 = 786432
	assert.Equal(t, 786432, TensorBufferSize(info))

	// Half precision.
	info.DType = 1 // half = 2 bytes
	assert.Equal(t, 393216, TensorBufferSize(info))

	// Dynamic dim treated as 1.
	info.DType = 0
	info.Dims = []int{-1, 3, 256, 256}
	assert.Equal(t, 786432, TensorBufferSize(info))

	// Unknown dtype returns 0.
	info.DType = 99
	assert.Equal(t, 0, TensorBufferSize(info))
}

func TestTRTEngineHandle(t *testing.T) {
	modelPath := trtModelPath(t)
	ctx := testContext(t)
	_ = ctx

	planPath := t.TempDir() + "/test.plan"
	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	require.NoError(t, err)
	defer engine.Close()

	handle := engine.Handle()
	assert.NotNil(t, handle, "handle should be non-nil for a loaded engine")
}

func TestTRTEngineNilEngine(t *testing.T) {
	var engine *TRTEngine
	assert.Equal(t, 0, engine.InputSize())
	assert.Equal(t, 0, engine.OutputSize())
	assert.Equal(t, 0, engine.NumIOTensors())
	assert.Nil(t, engine.Handle())
	engine.Close() // should not panic
}

func TestTRTContextNilContext(t *testing.T) {
	var ctx *TRTContext
	err := ctx.Infer(nil, nil, 1, nil)
	assert.Error(t, err)
	ctx.Close() // should not panic
}

func TestTRTNewEngineONNXNotFound(t *testing.T) {
	engine, err := NewTRTEngine("/nonexistent/model.onnx", TRTEngineOpts{})
	require.Error(t, err)
	assert.Nil(t, engine)
	assert.Contains(t, err.Error(), "ONNX file not found")
}
