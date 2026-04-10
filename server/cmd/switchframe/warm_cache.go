package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/stmap"
)

// warmCache pre-generates persistent caches for all supported resolutions,
// then exits. Called during AMI build (Packer stage 22) so the cache files
// are baked into the image and don't need to be generated on first startup.
func warmCache(cfg AppConfig) error {
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("warm-cache: create cache dir: %w", err)
	}

	slog.Info("warm-cache: starting", "cacheDir", cacheDir)
	start := time.Now()

	// Pre-warm ST map animated effect caches for both 1080p and 720p.
	resolutions := []struct {
		w, h int
		name string
	}{
		{1920, 1080, "1080p"},
		{1280, 720, "720p"},
	}

	const defaultFrameCount = 90

	effects := stmap.AnimatedGeneratorInfoList()
	total := len(effects) * len(resolutions)
	done := 0

	for _, res := range resolutions {
		for _, info := range effects {
			params := make(map[string]float64, len(info.Params))
			for name, p := range info.Params {
				params[name] = p.Default
			}

			dir := filepath.Join(cacheDir, "stmap",
				info.Name+"_"+stmap.CacheKey(info.Name, params, res.w, res.h, defaultFrameCount))

			if stmap.DirComplete(dir, defaultFrameCount) {
				done++
				slog.Info("warm-cache: already cached",
					"effect", info.Name, "res", res.name,
					"progress", fmt.Sprintf("%d/%d", done, total))
				continue
			}

			anim, err := stmap.GenerateAnimated(info.Name, params, res.w, res.h, defaultFrameCount)
			if err != nil {
				slog.Warn("warm-cache: generate failed", "effect", info.Name, "res", res.name, "error", err)
				done++
				continue
			}
			anim.BuildProcessorsCached(dir)
			_ = anim.Close()
			done++
			slog.Info("warm-cache: cached",
				"effect", info.Name, "res", res.name,
				"progress", fmt.Sprintf("%d/%d", done, total))
		}
	}

	slog.Info("warm-cache: ST maps complete",
		"effects", len(effects),
		"resolutions", len(resolutions),
		"duration", time.Since(start).Round(time.Millisecond))

	// Pre-generate playout sample clips via ffmpeg.
	sampleDir := filepath.Join(cacheDir, "playout")
	slog.Info("warm-cache: generating playout sample clips", "dir", sampleDir)
	sampleStart := time.Now()
	clips := playout.GenerateSampleClips(sampleDir)
	slog.Info("warm-cache: playout samples complete",
		"clips", len(clips),
		"duration", time.Since(sampleStart).Round(time.Millisecond))

	// Pre-compile TensorRT .plan files (no-op on non-GPU builds).
	warmTensorRT(cfg.AIModelsDir)
	warmNeuralKeyPlan(cfg.NeuralKeyModelPath)

	slog.Info("warm-cache: all caches complete",
		"total_duration", time.Since(start).Round(time.Millisecond))
	return nil
}
