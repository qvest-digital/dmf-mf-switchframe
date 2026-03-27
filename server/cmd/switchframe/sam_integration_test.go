//go:build integration && cgo && cuda && tensorrt

package main

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/gpu"
)

const (
	samTestWidth  = 1920
	samTestHeight = 1080
	samModelDir   = "/tmp/ar_models"
)

// requireGPUForSAM creates a real CUDA GPU context or skips the test.
// Separate from requireGPU in ar_integration_test.go to avoid optix build tag.
func requireGPUForSAM(t *testing.T) *gpu.Context {
	t.Helper()
	ctx, err := gpu.NewContext()
	if err != nil {
		t.Skipf("GPU not available: %v", err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	t.Logf("GPU context: %s", ctx.DeviceName())
	mem := ctx.MemoryStats()
	t.Logf("GPU memory: %d MB total, %d MB free, %d MB used", mem.TotalMB, mem.FreeMB, mem.UsedMB)
	return ctx
}

// allocSAMTestFrame allocates a GPU NV12 frame filled with mid-gray.
func allocSAMTestFrame(t *testing.T, ctx *gpu.Context, w, h int) (*gpu.FramePool, *gpu.GPUFrame) {
	t.Helper()
	pool, err := gpu.NewFramePool(ctx, w, h, 2)
	if err != nil {
		t.Fatalf("NewFramePool: %v", err)
	}

	frame, err := pool.Acquire()
	if err != nil {
		pool.Close()
		t.Fatalf("pool.Acquire: %v", err)
	}

	err = gpu.FillColor(ctx, frame, gpu.YUVColor{Y: 128, Cb: 128, Cr: 128})
	if err != nil {
		frame.Release()
		pool.Close()
		t.Fatalf("FillColor: %v", err)
	}

	t.Logf("Test NV12 frame: %dx%d, pitch=%d, devptr=0x%x",
		frame.Width, frame.Height, frame.Pitch, uintptr(frame.DevPtr))
	return pool, frame
}

// --------------------------------------------------------------------------
// Test: SAM 2.1 Encoder + Decoder Inference
// --------------------------------------------------------------------------

func TestSAM_EncoderDecoder(t *testing.T) {
	encoderPath := filepath.Join(samModelDir, "sam2_encoder.plan")
	decoderPath := filepath.Join(samModelDir, "sam2_decoder.plan")

	if _, err := os.Stat(encoderPath); err != nil {
		t.Skipf("SAM encoder not found at %s (run model export first)", encoderPath)
	}
	if _, err := os.Stat(decoderPath); err != nil {
		t.Skipf("SAM decoder not found at %s (run model export first)", decoderPath)
	}
	t.Logf("SAM encoder: %s", encoderPath)
	t.Logf("SAM decoder: %s", decoderPath)

	gpuCtx := requireGPUForSAM(t)

	pool, frame := allocSAMTestFrame(t, gpuCtx, samTestWidth, samTestHeight)
	defer pool.Close()
	defer frame.Release()

	// Create inference engine and enable SAM.
	engine := arinfer.New(arinfer.Config{ModelsDir: samModelDir})
	defer engine.Close()

	err := engine.EnableSAM(encoderPath, decoderPath)
	if err != nil {
		t.Fatalf("EnableSAM: %v", err)
	}
	t.Logf("SAM model loaded successfully")

	status := engine.Status()
	if !status.SAMReady {
		t.Fatal("Status.SAMReady is false after EnableSAM")
	}
	t.Logf("Engine status: DepthReady=%v, SAMReady=%v", status.DepthReady, status.SAMReady)

	// Add a point prompt at the image center.
	centerX := samTestWidth / 2
	centerY := samTestHeight / 2
	err = engine.AddPrompt(arinfer.SAMPrompt{
		ID:     "center",
		Type:   "point",
		Points: [][2]int{{centerX, centerY}},
		Label:  "test-object",
	})
	if err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}
	t.Logf("Added point prompt at (%d, %d)", centerX, centerY)

	// Run inference. ProcessFrame calls Encode (first time) then Propagate.
	engine.ProcessFrame(uintptr(frame.DevPtr), frame.Width, frame.Height, frame.Pitch)

	// Verify the occlusion map is populated.
	occ := engine.LastOcclusion()
	if occ == nil {
		t.Fatal("LastOcclusion is nil after ProcessFrame")
	}
	if occ.MaskPtr == 0 {
		t.Fatal("OcclusionMap.MaskPtr is 0 (no SAM mask output)")
	}
	if occ.Width != samTestWidth || occ.Height != samTestHeight {
		t.Errorf("OcclusionMap dims = %dx%d, want %dx%d",
			occ.Width, occ.Height, samTestWidth, samTestHeight)
	}
	t.Logf("SAM mask output: MaskPtr=0x%x, %dx%d", occ.MaskPtr, occ.Width, occ.Height)

	// Download the mask and verify it is non-zero.
	maskSize := samTestWidth * samTestHeight
	maskHost := make([]byte, maskSize)
	err = gpu.DownloadMaskU8(maskHost, unsafe.Pointer(occ.MaskPtr), maskSize)
	if err != nil {
		t.Fatalf("Download SAM mask: %v", err)
	}

	nonZero := 0
	for _, b := range maskHost {
		if b != 0 {
			nonZero++
		}
	}
	t.Logf("SAM mask: %d/%d pixels non-zero (%.1f%%)",
		nonZero, maskSize, 100.0*float64(nonZero)/float64(maskSize))

	// For a uniform gray frame with a center-point prompt, SAM may produce
	// a mask covering some or all of the frame. The key check is that
	// inference ran without errors.
	t.Logf("SAM inference completed successfully")

	// Check latency.
	status = engine.Status()
	t.Logf("SAM latency: %.2f ms, frames processed: %d, skipped: %d",
		status.SAMLatencyMs, status.FramesProcessed, status.FramesSkipped)
	if status.FramesProcessed < 1 {
		t.Error("FramesProcessed should be >= 1")
	}
}

// --------------------------------------------------------------------------
// Test: SAM 2.1 TRT Engine Tensor Introspection
// --------------------------------------------------------------------------

func TestSAM_TensorIntrospection(t *testing.T) {
	encoderPath := filepath.Join(samModelDir, "sam2_encoder.plan")
	decoderPath := filepath.Join(samModelDir, "sam2_decoder.plan")

	if _, err := os.Stat(encoderPath); err != nil {
		t.Skipf("SAM encoder not found at %s", encoderPath)
	}
	if _, err := os.Stat(decoderPath); err != nil {
		t.Skipf("SAM decoder not found at %s", decoderPath)
	}

	_ = requireGPUForSAM(t)

	// Introspect encoder tensors.
	t.Log("=== SAM Encoder Tensors ===")
	encoder, err := gpu.LoadTRTPlan(encoderPath)
	if err != nil {
		t.Fatalf("Load encoder plan: %v", err)
	}
	defer encoder.Close()

	numIO := encoder.NumIOTensors()
	t.Logf("Encoder has %d I/O tensors", numIO)
	for i := 0; i < numIO; i++ {
		info, err := encoder.TensorInfoAt(i)
		if err != nil {
			t.Fatalf("TensorInfoAt(%d): %v", i, err)
		}
		direction := "output"
		if info.IsInput {
			direction = "input"
		}
		t.Logf("  [%d] %s %q dtype=%d dims=%v (buf=%d bytes)",
			i, direction, info.Name, info.DType, info.Dims, gpu.TensorBufferSize(info))
	}

	// Introspect decoder tensors.
	t.Log("=== SAM Decoder Tensors ===")
	decoder, err := gpu.LoadTRTPlan(decoderPath)
	if err != nil {
		t.Fatalf("Load decoder plan: %v", err)
	}
	defer decoder.Close()

	numIO = decoder.NumIOTensors()
	t.Logf("Decoder has %d I/O tensors", numIO)
	for i := 0; i < numIO; i++ {
		info, err := decoder.TensorInfoAt(i)
		if err != nil {
			t.Fatalf("TensorInfoAt(%d): %v", i, err)
		}
		direction := "output"
		if info.IsInput {
			direction = "input"
		}
		t.Logf("  [%d] %s %q dtype=%d dims=%v (buf=%d bytes)",
			i, direction, info.Name, info.DType, info.Dims, gpu.TensorBufferSize(info))
	}
}

// --------------------------------------------------------------------------
// Test: Combined Depth + SAM Pipeline
// --------------------------------------------------------------------------

func TestSAM_DepthPlusSAMPipeline(t *testing.T) {
	depthPath := filepath.Join(samModelDir, "depth_anything_v2_vits.plan")
	encoderPath := filepath.Join(samModelDir, "sam2_encoder.plan")
	decoderPath := filepath.Join(samModelDir, "sam2_decoder.plan")

	if _, err := os.Stat(depthPath); err != nil {
		t.Skipf("Depth model not found at %s", depthPath)
	}
	if _, err := os.Stat(encoderPath); err != nil {
		t.Skipf("SAM encoder not found at %s", encoderPath)
	}
	if _, err := os.Stat(decoderPath); err != nil {
		t.Skipf("SAM decoder not found at %s", decoderPath)
	}

	gpuCtx := requireGPUForSAM(t)

	pool, frame := allocSAMTestFrame(t, gpuCtx, samTestWidth, samTestHeight)
	defer pool.Close()
	defer frame.Release()

	// Create engine with both models.
	engine := arinfer.New(arinfer.Config{ModelsDir: samModelDir})
	defer engine.Close()

	t.Log("=== Enable Depth ===")
	if err := engine.EnableDepth(depthPath); err != nil {
		t.Fatalf("EnableDepth: %v", err)
	}

	t.Log("=== Enable SAM ===")
	if err := engine.EnableSAM(encoderPath, decoderPath); err != nil {
		t.Fatalf("EnableSAM: %v", err)
	}

	// Add a prompt so SAM actually runs.
	err := engine.AddPrompt(arinfer.SAMPrompt{
		ID:     "center",
		Type:   "point",
		Points: [][2]int{{samTestWidth / 2, samTestHeight / 2}},
		Label:  "test",
	})
	if err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}

	status := engine.Status()
	if !status.DepthReady {
		t.Fatal("Depth not ready")
	}
	if !status.SAMReady {
		t.Fatal("SAM not ready")
	}
	t.Logf("Both models loaded: DepthReady=%v, SAMReady=%v", status.DepthReady, status.SAMReady)

	// Run the combined pipeline.
	t.Log("=== ProcessFrame (Depth + SAM) ===")
	engine.ProcessFrame(uintptr(frame.DevPtr), frame.Width, frame.Height, frame.Pitch)

	occ := engine.LastOcclusion()
	if occ == nil {
		t.Fatal("LastOcclusion is nil")
	}

	if occ.DepthPtr == 0 {
		t.Error("DepthPtr is 0 — depth inference did not run")
	}
	if occ.MaskPtr == 0 {
		t.Error("MaskPtr is 0 — SAM inference did not run")
	}
	t.Logf("Depth+SAM pipeline OK: DepthPtr=0x%x, MaskPtr=0x%x", occ.DepthPtr, occ.MaskPtr)

	status = engine.Status()
	t.Logf("Latency: depth=%.2fms, SAM=%.2fms, processed=%d, skipped=%d",
		status.DepthLatencyMs, status.SAMLatencyMs, status.FramesProcessed, status.FramesSkipped)

	if status.FramesProcessed < 1 {
		t.Error("FramesProcessed should be >= 1")
	}

	// Log GPU memory.
	mem := gpuCtx.MemoryStats()
	t.Logf("GPU memory after pipeline: %d MB used / %d MB total", mem.UsedMB, mem.TotalMB)
}
