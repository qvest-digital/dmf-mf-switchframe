package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/switchframe/server/playout"
)

// warmCache pre-generates persistent caches, then exits.
// Called during AMI build (Packer stage 22) so expensive artifacts are
// baked into the image. ST map LUTs are NOT included — they're ~28 GB
// and the background prewarmSTMapCache() goroutine generates them in
// ~3 min at runtime on first startup.
func warmCache(cfg AppConfig) error {
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	slog.Info("warm-cache: starting", "cacheDir", cacheDir)
	start := time.Now()

	// Pre-generate playout sample clips via ffmpeg (~30s).
	sampleDir := filepath.Join(cacheDir, "playout")
	slog.Info("warm-cache: generating playout sample clips", "dir", sampleDir)
	sampleStart := time.Now()
	clips := playout.GenerateSampleClips(sampleDir)
	slog.Info("warm-cache: playout samples complete",
		"clips", len(clips),
		"duration", time.Since(sampleStart).Round(time.Millisecond))

	// Pre-compile TensorRT .plan files (~7 min, no-op on non-GPU builds).
	warmTensorRT(cfg.AIModelsDir)
	warmNeuralKeyPlan(cfg.NeuralKeyModelPath)

	slog.Info("warm-cache: complete",
		"total_duration", time.Since(start).Round(time.Millisecond))
	return nil
}
