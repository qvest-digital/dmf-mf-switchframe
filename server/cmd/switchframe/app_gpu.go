package main

import (
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/layout"
	"github.com/zsiec/switchframe/server/switcher"
)

// gpuState holds all GPU subsystem resources. Nil fields mean the GPU
// subsystem is not active and the CPU pipeline is used instead.
type gpuState struct {
	ctx           *gpu.Context
	pool          *gpu.FramePool
	pipeline      *gpu.GPUPipeline
	encoder       *gpu.GPUEncoder
	sourceManager *gpu.GPUSourceManager
}

// initGPU attempts to initialize the GPU pipeline. If the GPU is not
// available or initialization fails, logs a message and returns nil
// (CPU fallback). The caller should store the returned state and call
// closeGPU in cleanup.
func initGPU(sw *switcher.Switcher) *gpuState {
	ctx, err := gpu.NewContext()
	if err != nil {
		slog.Info("GPU not available, using CPU pipeline", "error", err)
		return nil
	}

	slog.Info("GPU detected",
		"backend", ctx.Backend(),
		"device", ctx.DeviceName(),
	)

	// Create GPU frame pool sized for the pipeline format.
	// Pool sizing: 9 cached source frames + 9 stmapTmp frames + 1 pipeline frame
	// + 1 stmap temp + 1 mask + 3 headroom = 24. Under-sizing causes pool
	// exhaustion under load when all sources are active with ST maps.
	pf := sw.PipelineFormat()
	pool, err := gpu.NewFramePool(ctx, pf.Width, pf.Height, 32)
	if err != nil {
		slog.Warn("GPU frame pool failed, falling back to CPU", "error", err)
		ctx.Close()
		return nil
	}
	ctx.SetPool(pool)

	return &gpuState{ctx: ctx, pool: pool}
}

// wireGPUPipeline creates the full GPU pipeline with all processing nodes
// and registers it on the switcher. Call after initGPU returns a non-nil state.
func wireGPUPipeline(gs *gpuState, sw *switcher.Switcher, app *App) {
	if gs == nil {
		return
	}

	pf := sw.PipelineFormat()

	// Create GPU encoder (VideoToolbox on macOS, NVENC on Linux).
	encoder, err := gpu.NewGPUEncoder(gs.ctx, pf.Width, pf.Height, pf.FPSNum, pf.FPSDen, 8_000_000)
	if err != nil {
		slog.Warn("GPU encoder failed, falling back to CPU pipeline", "error", err)
		return
	}
	gs.encoder = encoder

	// Create GPU pipeline.
	gpuPipeline := gpu.NewGPUPipeline(gs.ctx, gs.pool)
	gs.pipeline = gpuPipeline

	// Create GPU source manager for per-source upload, ST map, caching,
	// and (future) preview encoding. Sources auto-register on first
	// IngestYUV call from handleRawVideoFrame.
	sourceMgr := gpu.NewGPUSourceManager(gs.ctx, gs.pool, app.stmapRegistry)
	gs.sourceManager = sourceMgr

	// Register on switcher — gates CPU fill paths in handleRawVideoFrame.
	sw.SetGPUSourceManager(sourceMgr)

	// Create adapter types for state interfaces.
	keyAdapter := &keyBridgeAdapter{bridge: app.keyBridge, sourceMgr: sourceMgr}
	compositorAdapter := &compositorStateAdapter{compositor: app.compositor}
	layoutAdapter := &layoutStateAdapter{layout: app.layoutCompositor, sourceMgr: sourceMgr}

	// Raw sinks — GPU raw sink nodes download to CPU only when active.
	var gpuRawVideoSink atomic.Pointer[gpu.RawSinkFunc]
	var gpuRawPreviewSink atomic.Pointer[gpu.RawSinkFunc]

	// Wire the GPU raw sinks to the same underlying callbacks as the CPU sinks.
	// The app sets rawVideoSink/rawPreviewSink on the switcher for CPU path;
	// we mirror them to the GPU raw sink atomic pointers.
	app.gpuRawVideoSink = &gpuRawVideoSink
	app.gpuRawPreviewSink = &gpuRawPreviewSink

	// Bridge callback: GPU encode outputs Annex B H.264 (data, isIDR, pts).
	// Convert to AVC1 format (length-prefixed NALUs) before broadcasting,
	// matching the CPU pipeline's encode path (pipeline_codecs.go:208).
	// broadcastWithCaptions expects AVC1 and converts back to Annex B
	// for caption SEI injection.
	//
	// On IDR keyframes, extract SPS/PPS and set VideoInfo on the program
	// relay so the browser can configure its VideoDecoder. The CPU path
	// does this in pipeline_codecs.go via onVideoInfoChange.
	broadcastFn := sw.BroadcastWithCaptionsFunc()
	videoInfoCb := app.videoInfoCallback("gpu-pipeline")
	var gpuSPS, gpuPPS []byte
	gpuOnEncoded := func(data []byte, isIDR bool, pts int64) {
		avc1 := codec.AnnexBToAVC1Into(data, nil)

		// Extract SPS/PPS from IDR keyframes for VideoInfo.
		if isIDR {
			nalus := codec.ExtractNALUs(avc1)
			for _, nalu := range nalus {
				if len(nalu) == 0 {
					continue
				}
				naluType := nalu[0] & 0x1F
				switch naluType {
				case 7: // SPS
					sps := make([]byte, len(nalu))
					copy(sps, nalu)
					gpuSPS = sps
				case 8: // PPS
					pps := make([]byte, len(nalu))
					copy(pps, nalu)
					gpuPPS = pps
				}
			}
			if gpuSPS != nil && gpuPPS != nil {
				videoInfoCb(gpuSPS, gpuPPS, pf.Width, pf.Height)
			}
		}

		frame := &media.VideoFrame{
			PTS:        pts,
			IsKeyframe: isIDR,
			WireData:   avc1,
			Codec:      "H264",
		}
		if isIDR && gpuSPS != nil {
			frame.SPS = gpuSPS
			frame.PPS = gpuPPS
		}
		broadcastFn(frame)
	}

	// Build GPU pipeline node chain:
	//   gpu_key → gpu_layout → gpu_compositor → gpu_stmap → raw_sinks → gpu_encode
	nodes := []gpu.GPUPipelineNode{
		gpu.NewGPUKeyNode(gs.ctx, gs.pool, keyAdapter),
		gpu.NewGPULayoutNode(gs.ctx, gs.pool, layoutAdapter),
		gpu.NewGPUCompositorNode(gs.ctx, compositorAdapter),
		gpu.NewGPUSTMapNode(gs.ctx, gs.pool, app.stmapRegistry),
		gpu.NewGPURawSinkNode(gs.ctx, &gpuRawVideoSink),
		gpu.NewGPURawSinkNode(gs.ctx, &gpuRawPreviewSink),
		gpu.NewGPUEncodeNode(gs.ctx, encoder, sw.ForceNextIDRPtr(), gpuOnEncoded),
	}

	if err := gpuPipeline.Build(pf.Width, pf.Height, gs.pool.Pitch(), nodes); err != nil {
		slog.Warn("GPU pipeline build failed, falling back to CPU", "error", err)
		encoder.Close()
		gs.encoder = nil
		return
	}

	// Create the runner wrapper and register on the switcher.
	runner := &gpuPipelineRunnerImpl{
		pipeline:      gpuPipeline,
		sourceManager: sourceMgr,
		ctx:           gs.ctx,
	}
	sw.SetGPUPipeline(runner)

	slog.Info("GPU pipeline active",
		"backend", gs.ctx.Backend(),
		"device", gs.ctx.DeviceName(),
		"pool_frames", 32,
	)
}

// updateGPURawVideoSink mirrors the Switcher's raw video sink to the GPU pipeline.
// Must be called after every sw.SetRawVideoSink() call so the GPU raw sink node
// downloads frames to CPU and forwards them to the same callback.
func (a *App) updateGPURawVideoSink() {
	if a.gpuRawVideoSink == nil {
		return
	}
	cpuSink := a.sw.GetRawVideoSink()
	if cpuSink == nil {
		a.gpuRawVideoSink.Store(nil)
		return
	}
	fn := gpu.RawSinkFunc(func(yuv []byte, w, h int) {
		cpuSink(&switcher.ProcessingFrame{YUV: yuv, Width: w, Height: h})
	})
	a.gpuRawVideoSink.Store(&fn)
}

// updateGPURawPreviewSink mirrors the Switcher's raw preview sink to the GPU pipeline.
// Must be called after every sw.SetRawPreviewSink() call so the GPU raw sink node
// downloads frames to CPU and forwards them to the same callback.
func (a *App) updateGPURawPreviewSink() {
	if a.gpuRawPreviewSink == nil {
		return
	}
	cpuSink := a.sw.GetRawPreviewSink()
	if cpuSink == nil {
		a.gpuRawPreviewSink.Store(nil)
		return
	}
	fn := gpu.RawSinkFunc(func(yuv []byte, w, h int) {
		cpuSink(&switcher.ProcessingFrame{YUV: yuv, Width: w, Height: h})
	})
	a.gpuRawPreviewSink.Store(&fn)
}

// closeGPU releases all GPU resources.
func closeGPU(gs *gpuState) {
	if gs == nil {
		return
	}
	// Close source manager first — it holds GPU frames from the pool.
	if gs.sourceManager != nil {
		gs.sourceManager.Close()
	}
	if gs.pipeline != nil {
		gs.pipeline.Close()
	}
	if gs.encoder != nil {
		gs.encoder.Close()
	}
	if gs.pool != nil {
		gs.pool.Close()
	}
	if gs.ctx != nil {
		gs.ctx.Close()
	}
}

// --- GPU Pipeline Runner (implements switcher.GPUPipelineRunner) ---

type gpuPipelineRunnerImpl struct {
	pipeline      *gpu.GPUPipeline
	sourceManager *gpu.GPUSourceManager
	ctx           *gpu.Context
}

func (r *gpuPipelineRunnerImpl) RunWithUpload(yuv []byte, width, height int, pts int64) error {
	frame, err := r.pipeline.RunWithUpload(yuv, width, height, pts)
	if err != nil {
		return err
	}
	frame.Release()
	return nil
}

func (r *gpuPipelineRunnerImpl) RunFromCache(sourceKey string, pts int64) error {
	if r.sourceManager == nil {
		return fmt.Errorf("no GPU source manager")
	}
	cached := r.sourceManager.GetFrame(sourceKey)
	if cached == nil {
		return fmt.Errorf("no cached GPU frame for source %s", sourceKey)
	}
	defer cached.Release()

	// Acquire a fresh pipeline frame — the pipeline modifies frames in-place,
	// so we must not run it on the source cache frame directly.
	frame, err := r.pipeline.Pool().Acquire()
	if err != nil {
		return fmt.Errorf("gpu pipeline: acquire frame: %w", err)
	}

	// Copy NV12 data from cached source frame to pipeline frame.
	gpu.CopyGPUFrame(frame, cached)
	frame.PTS = pts

	if err := r.pipeline.Run(frame); err != nil {
		frame.Release()
		return err
	}
	frame.Release()
	return nil
}

func (r *gpuPipelineRunnerImpl) RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stingerAlpha []byte) error {
	if r.sourceManager == nil {
		return fmt.Errorf("no GPU source manager")
	}

	// Get FROM source frame from GPU cache.
	frameA := r.sourceManager.GetFrame(fromKey)
	if frameA == nil {
		return fmt.Errorf("no cached GPU frame for source %s", fromKey)
	}
	defer frameA.Release()

	// Get TO source frame from GPU cache (not needed for FTB/FTBReverse).
	var frameB *gpu.GPUFrame
	if transType != "ftb" && transType != "ftb_reverse" {
		frameB = r.sourceManager.GetFrame(toKey)
		if frameB == nil {
			return fmt.Errorf("no cached GPU frame for source %s", toKey)
		}
		defer frameB.Release()
	}

	// Acquire output frame for the blend result.
	pool := r.pipeline.Pool()
	dst, err := pool.Acquire()
	if err != nil {
		return fmt.Errorf("gpu transition: acquire blend frame: %w", err)
	}
	dst.PTS = pts

	// GPU blend based on transition type.
	ctx := r.ctx
	switch transType {
	case "mix":
		if err := gpu.BlendMix(ctx, dst, frameA, frameB, position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition mix: %w", err)
		}
	case "dip":
		// Dip = fade to black then from black.
		// Phase 1 (pos 0-0.5): fade A to black.
		// Phase 2 (pos 0.5-1.0): fade B from black.
		if position <= 0.5 {
			dipPos := position * 2.0
			if err := gpu.BlendFTB(ctx, dst, frameA, dipPos); err != nil {
				dst.Release()
				return fmt.Errorf("gpu transition dip phase1: %w", err)
			}
		} else {
			dipPos := (1.0 - position) * 2.0
			if err := gpu.BlendFTB(ctx, dst, frameB, dipPos); err != nil {
				dst.Release()
				return fmt.Errorf("gpu transition dip phase2: %w", err)
			}
		}
	case "ftb":
		if err := gpu.BlendFTB(ctx, dst, frameA, position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition ftb: %w", err)
		}
	case "ftb_reverse":
		if err := gpu.BlendFTB(ctx, dst, frameA, 1.0-position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition ftb_reverse: %w", err)
		}
	case "wipe":
		maskBuf, maskErr := pool.Acquire()
		if maskErr != nil {
			dst.Release()
			return fmt.Errorf("gpu transition wipe: acquire mask: %w", maskErr)
		}
		dir := gpu.WipeDirection(wipeDir)
		if err := gpu.BlendWipe(ctx, dst, frameA, frameB, maskBuf, position, dir, 4); err != nil {
			maskBuf.Release()
			dst.Release()
			return fmt.Errorf("gpu transition wipe: %w", err)
		}
		maskBuf.Release()
	case "stinger":
		// Stinger with per-pixel alpha plane. If alpha data is provided,
		// upload it to a GPU frame and use BlendStinger. Otherwise fall
		// back to mix blend.
		if stingerAlpha != nil && len(stingerAlpha) > 0 {
			alphaFrame, alphaErr := pool.Acquire()
			if alphaErr != nil {
				dst.Release()
				return fmt.Errorf("gpu transition stinger: acquire alpha: %w", alphaErr)
			}
			// Upload alpha plane to GPU frame's Y plane. The alpha data is
			// luma-resolution (width*height bytes). Upload fills Y, UV is
			// used as scratch by BlendStinger for downsampled chroma alpha.
			if err := gpu.Upload(ctx, alphaFrame, stingerAlpha, frameA.Width, frameA.Height); err != nil {
				alphaFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: upload alpha: %w", err)
			}

			// Determine base source: A before cut point, B after.
			// For now, use 0.5 as cut point (matches default stinger behavior).
			base := frameA
			overlay := frameB
			if position >= 0.5 && frameB != nil {
				base = frameB
				overlay = frameA
			}
			if overlay == nil {
				overlay = frameA
			}

			if err := gpu.BlendStinger(ctx, dst, base, overlay, alphaFrame); err != nil {
				alphaFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: %w", err)
			}
			alphaFrame.Release()
		} else {
			// No alpha data — fall back to mix blend.
			if err := gpu.BlendMix(ctx, dst, frameA, frameB, position); err != nil {
				dst.Release()
				return fmt.Errorf("gpu transition stinger fallback: %w", err)
			}
		}
	default:
		// Unknown type — use mix as fallback.
		if err := gpu.BlendMix(ctx, dst, frameA, frameB, position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition fallback: %w", err)
		}
	}

	// Run blended result through rest of GPU pipeline (key → layout →
	// compositor → stmap → raw sinks → encode).
	if err := r.pipeline.Run(dst); err != nil {
		dst.Release()
		return fmt.Errorf("gpu transition pipeline: %w", err)
	}
	dst.Release()
	return nil
}

// --- State Adapters (bridge package-local types to gpu interfaces) ---

// keyBridgeAdapter adapts graphics.KeyProcessorBridge to gpu.KeyBridge.
type keyBridgeAdapter struct {
	bridge    *graphics.KeyProcessorBridge
	sourceMgr *gpu.GPUSourceManager // nil until source manager is ready
}

func (a *keyBridgeAdapter) HasEnabledKeysWithFills() bool {
	return a.bridge.HasEnabledKeysWithFills()
}

func (a *keyBridgeAdapter) HasEnabledKeys() bool {
	return a.bridge.HasEnabledKeys()
}

func (a *keyBridgeAdapter) GPUFill(sourceKey string) *gpu.GPUFrame {
	if a.sourceMgr == nil {
		return nil
	}
	return a.sourceMgr.GetFrame(sourceKey)
}

func (a *keyBridgeAdapter) SnapshotEnabledKeys() []gpu.EnabledKeySnapshot {
	snaps := a.bridge.SnapshotEnabledKeys()
	if len(snaps) == 0 {
		return nil
	}
	result := make([]gpu.EnabledKeySnapshot, len(snaps))
	for i, s := range snaps {
		result[i] = gpu.EnabledKeySnapshot{
			SourceKey:      s.SourceKey,
			Type:           s.Type,
			KeyCb:          s.KeyCb,
			KeyCr:          s.KeyCr,
			Similarity:     s.Similarity,
			Smoothness:     s.Smoothness,
			SpillSuppress:  s.SpillSuppress,
			SpillReplaceCb: s.SpillReplaceCb,
			SpillReplaceCr: s.SpillReplaceCr,
			LowClip:        s.LowClip,
			HighClip:       s.HighClip,
			Softness:       s.Softness,
			FillYUV:        s.FillYUV,
			FillW:          s.FillW,
			FillH:          s.FillH,
		}
	}
	return result
}

// compositorStateAdapter adapts graphics.Compositor to gpu.CompositorState.
type compositorStateAdapter struct {
	compositor *graphics.Compositor
}

func (a *compositorStateAdapter) HasActiveLayers() bool {
	return a.compositor.HasActiveLayers()
}

func (a *compositorStateAdapter) SnapshotVisibleLayers() []gpu.VisibleLayerSnapshot {
	snaps := a.compositor.SnapshotVisibleLayers()
	if len(snaps) == 0 {
		return nil
	}
	result := make([]gpu.VisibleLayerSnapshot, len(snaps))
	for i, s := range snaps {
		result[i] = gpu.VisibleLayerSnapshot{
			ID:       s.ID,
			Rect:     gpu.Rect{X: s.RectX, Y: s.RectY, W: s.RectW, H: s.RectH},
			Alpha:    s.Alpha,
			Overlay:  s.Overlay,
			OverlayW: s.OverlayW,
			OverlayH: s.OverlayH,
			Gen:      s.Gen,
		}
	}
	return result
}

// layoutStateAdapter adapts layout.Compositor to gpu.LayoutState.
type layoutStateAdapter struct {
	layout    *layout.Compositor
	sourceMgr *gpu.GPUSourceManager // nil until source manager is ready
}

func (a *layoutStateAdapter) Active() bool {
	return a.layout.Active()
}

func (a *layoutStateAdapter) GPUFill(sourceKey string) *gpu.GPUFrame {
	if a.sourceMgr == nil {
		return nil
	}
	return a.sourceMgr.GetFrame(sourceKey)
}

func (a *layoutStateAdapter) SnapshotSlots() []gpu.SlotSnapshot {
	snaps := a.layout.SnapshotSlots()
	if len(snaps) == 0 {
		return nil
	}
	result := make([]gpu.SlotSnapshot, len(snaps))
	for i, s := range snaps {
		result[i] = gpu.SlotSnapshot{
			Index:     s.Index,
			Enabled:   s.Enabled,
			SourceKey: s.SourceKey,
			Rect:      gpu.Rect{X: s.RectX, Y: s.RectY, W: s.RectW, H: s.RectH},
			FillYUV:   s.FillYUV,
			FillW:     s.FillW,
			FillH:     s.FillH,
			FillPTS:   s.FillPTS,
			Border: gpu.BorderSnapshot{
				ColorY:    s.BorderColorY,
				ColorCb:   s.BorderColorCb,
				ColorCr:   s.BorderColorCr,
				Thickness: s.BorderThickness,
			},
			Alpha: s.Alpha,
		}
	}
	return result
}

// gpuNoOpPreviewEncoder is a no-op preview encoder used when GPU preview encoding
// is active. When passed as the MXL SourceConfig.PreviewEncoder, it causes
// encodeAndBroadcastVideo to delegate relay delivery to this (no-op) encoder
// and only use the CPU full-quality path for replay. The actual relay encoding
// is handled by the GPU source manager's PreviewConfig.OnPreview callback.
type gpuNoOpPreviewEncoder struct{}

func (gpuNoOpPreviewEncoder) Send(_ []byte, _, _ int, _ int64) {}
