//go:build cgo && cuda && tensorrt

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

// Well-known model filenames discovered from --models-dir.
const (
	segmentationModelFile = "rvm_mobilenetv3_fp32_fixed.onnx"
)

func main() {
	device := flag.Int("device", 0, "CUDA device index")
	modelsDir := flag.String("models-dir", "", "Path to directory with all AI model files (ONNX + TRT .plan)")
	trackingMapDir := flag.String("tracking-map-dir", "", "Path to studio map data for visual tracking")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: switchframe-ai [--device N] [--models-dir PATH] <socket-path>")
		os.Exit(2)
	}
	socketPath := flag.Arg(0)

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Initialize CUDA runtime on the selected device.
	if err := initCUDA(*device); err != nil {
		fmt.Fprintf(os.Stderr, "cuda init: %v\n", err)
		os.Exit(1)
	}
	slog.Info("cuda initialized", "device", *device)

	// Connect to host.
	w, err := sidecar.NewWorker(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	// Signal readiness to host BEFORE reading handshake.
	w.Ready()
	slog.Info("ready, waiting for gpu pool handshake")

	// Read GPU pool handshake from host.
	hs, err := sidecar.ReadGPUPoolHandshake(w.Conn())
	if err != nil {
		fmt.Fprintf(os.Stderr, "read handshake: %v\n", err)
		os.Exit(1)
	}
	slog.Info("gpu pool handshake received",
		"slots", hs.Config.SlotCount,
		"width", hs.Config.Width,
		"height", hs.Config.Height,
		"result_size", hs.Config.ResultSize,
	)

	// Import GPU pool.
	pool, err := sidecar.OpenCUDAGPUPoolWorker(
		hs.FrameHandles, hs.ResultHandles,
		hs.ReadyEvents, hs.DoneEvents,
		hs.Config, hs.ShmName,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("gpu pool opened")

	// Auto-discover segmentation model from models-dir.
	segModelPath := ""
	if *modelsDir != "" {
		candidate := filepath.Join(*modelsDir, segmentationModelFile)
		if _, err := os.Stat(candidate); err == nil {
			segModelPath = candidate
		}
	}

	// Create GPU context for TRT segmentation (needs ONNX → TRT compilation).
	var gpuCtx *gpu.Context
	if segModelPath != "" {
		var ctxErr error
		gpuCtx, ctxErr = gpu.NewContext()
		if ctxErr != nil {
			slog.Warn("GPU context creation failed, segmentation disabled", "error", ctxErr)
		}
	}

	// Set up signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		slog.Info("signal received, shutting down")
		cancel()
	}()

	// Create the AR inference engine (depth + SAM) if models dir is specified.
	var inferEngine *arinfer.Engine
	if *modelsDir != "" {
		poolCfg := pool.Config()
		format := arinfer.FormatNV12
		if poolCfg.Format == 1 { // P210
			format = arinfer.FormatP210
		}
		inferEngine = arinfer.New(arinfer.Config{
			ModelsDir:   *modelsDir,
			FrameFormat: format,
		})
		slog.Info("AR inference engine created", "modelsDir", *modelsDir, "format", format)
	}

	// Create the inference engine and start the frame processing loop.
	eng := newEngine(pool, w, gpuCtx, segModelPath, inferEngine, *modelsDir, *trackingMapDir)
	defer eng.close()
	eng.wg.Add(1)
	go eng.frameLoop(ctx)

	slog.Info("entering command loop", "modelsDir", *modelsDir, "segModel", segModelPath)

	// Run command loop until context cancelled or shutdown received.
	if err := w.Run(ctx, eng.handleMessage); err != nil {
		if ctx.Err() == nil {
			slog.Error("command loop error", "err", err)
			os.Exit(1)
		}
	}
	slog.Info("shutdown complete")
}
