//go:build cgo && cuda && tensorrt

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/switchframe/server/gpu"
)

// warmTensorRT pre-compiles ONNX models to TensorRT .plan files for the
// current GPU architecture. Plans are GPU-specific but persist across
// restarts, saving 30-60s of ONNX→engine compilation on first startup.
func warmTensorRT(modelsDir string) {
	if modelsDir == "" {
		slog.Info("warm-cache: no --ai-models-dir, skipping TensorRT pre-compilation")
		return
	}

	// Initialize GPU context for TensorRT compilation.
	ctx, err := gpu.NewContext()
	if err != nil {
		slog.Warn("warm-cache: GPU not available, skipping TensorRT", "error", err)
		return
	}
	defer ctx.Close()

	slog.Info("warm-cache: pre-compiling TensorRT engines", "modelsDir", modelsDir)
	start := time.Now()

	// Find ONNX models in the models directory.
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		slog.Warn("warm-cache: failed to read models dir", "error", err)
		return
	}

	compiled := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".onnx" {
			continue
		}

		modelPath := filepath.Join(modelsDir, entry.Name())
		base := entry.Name()
		ext := filepath.Ext(base)
		planName := base[:len(base)-len(ext)] + ".plan"

		// Use the same cache directory as production.
		cacheDir := filepath.Join(os.Getenv("HOME"), ".switchframe", "models")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			slog.Warn("warm-cache: failed to create models cache dir", "error", err)
			continue
		}
		planPath := filepath.Join(cacheDir, planName)

		// Skip if plan already exists.
		if info, err := os.Stat(planPath); err == nil && info.Size() > 0 {
			slog.Info("warm-cache: TensorRT plan already cached",
				"model", base, "plan", planPath)
			compiled++
			continue
		}

		slog.Info("warm-cache: compiling TensorRT engine",
			"model", base, "plan", planPath)

		engine, err := gpu.NewTRTEngine(modelPath, gpu.TRTEngineOpts{
			MaxBatchSize:  1,
			UseFP16:       true,
			PlanCachePath: planPath,
		})
		if err != nil {
			slog.Warn("warm-cache: TensorRT compilation failed",
				"model", base, "error", err)
			continue
		}
		engine.Close()
		compiled++
		slog.Info("warm-cache: TensorRT engine compiled",
			"model", base, "plan", planPath)
	}

	slog.Info("warm-cache: TensorRT pre-compilation complete",
		"compiled", compiled, "duration", time.Since(start).Round(time.Millisecond))
}

// warmNeuralKeyPlan pre-compiles the CorridorKey neural key model.
// This uses a different plan naming convention (_neuralkey.plan suffix).
func warmNeuralKeyPlan(modelPath string) {
	if modelPath == "" {
		return
	}

	ctx, err := gpu.NewContext()
	if err != nil {
		slog.Warn("warm-cache: GPU not available for neural key", "error", err)
		return
	}
	defer ctx.Close()

	// Use standard production resolution for plan compilation.
	engine, err := gpu.NewNeuralKeyEngine(ctx, modelPath, 1920, 1080)
	if err != nil {
		slog.Warn("warm-cache: neural key plan compilation failed", "error", err)
		return
	}
	engine.Close()
	slog.Info("warm-cache: neural key plan compiled", "model", modelPath)
}
