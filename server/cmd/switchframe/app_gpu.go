package main

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
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

	// Initialize AI segmentation engine if model path is configured and backend is CUDA.
	var segAdapter gpu.SegmentationState
	if app.cfg.AIModelPath != "" && gs.ctx.Backend() == "cuda" {
		segEngine, segErr := gpu.NewSegmentationEngine(gs.ctx, app.cfg.AIModelPath)
		if segErr != nil {
			slog.Warn("AI segmentation unavailable", "error", segErr)
		} else {
			sourceMgr.SetSegmentationEngine(segEngine)
			app.segEngine = segEngine
			sa := &segmentationStateAdapter{
				engine:   segEngine,
				switcher: sw,
			}
			app.segAdapter = sa
			segAdapter = sa
			slog.Info("AI segmentation engine initialized", "model", app.cfg.AIModelPath)
		}
	}

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
	//   gpu_key → gpu_layout → gpu_compositor → gpu_ai_segment → gpu_stmap → raw_sinks → gpu_encode
	nodes := []gpu.GPUPipelineNode{
		gpu.NewGPUKeyNode(gs.ctx, gs.pool, keyAdapter),
		gpu.NewGPULayoutNode(gs.ctx, gs.pool, layoutAdapter),
		gpu.NewGPUCompositorNode(gs.ctx, compositorAdapter),
		gpu.NewGPUAISegmentNode(gs.ctx, gs.pool, segAdapter), // AI background replacement (nil on non-TensorRT)
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
	if err := gpu.CopyGPUFrame(frame, cached); err != nil {
		frame.Release()
		return fmt.Errorf("gpu pipeline: copy from cache failed: %w", err)
	}
	frame.PTS = pts

	if err := r.pipeline.Run(frame); err != nil {
		frame.Release()
		return err
	}
	frame.Release()
	return nil
}

func (r *gpuPipelineRunnerImpl) Snapshot() map[string]any {
	snap := r.pipeline.Snapshot()

	// Add source manager stats.
	if r.sourceManager != nil {
		snap["source_manager"] = r.sourceManager.Snapshot()
	}

	// Add backend info.
	if r.ctx != nil {
		snap["backend"] = r.ctx.Backend()
		snap["device"] = r.ctx.DeviceName()
	}

	return snap
}

func (r *gpuPipelineRunnerImpl) RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stinger *switcher.GPUStingerFrame) error {
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
		// Stinger: composite stinger overlay with per-pixel alpha onto
		// base source (A before cut point, B after).
		if stinger != nil && len(stinger.YUV) > 0 && len(stinger.Alpha) > 0 {
			// Upload stinger overlay YUV to GPU as NV12.
			overlayFrame, overlayErr := pool.Acquire()
			if overlayErr != nil {
				dst.Release()
				return fmt.Errorf("gpu transition stinger: acquire overlay: %w", overlayErr)
			}
			if err := gpu.Upload(ctx, overlayFrame, stinger.YUV, stinger.Width, stinger.Height); err != nil {
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: upload overlay: %w", err)
			}
			overlayFrame.Width = stinger.Width
			overlayFrame.Height = stinger.Height

			// Upload alpha plane as fake NV12: Y=alpha, UV=128 (neutral).
			alphaFrame, alphaErr := pool.Acquire()
			if alphaErr != nil {
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: acquire alpha: %w", alphaErr)
			}
			alphaYUV := make([]byte, stinger.Width*stinger.Height*3/2)
			copy(alphaYUV[:len(stinger.Alpha)], stinger.Alpha)
			cbOff := stinger.Width * stinger.Height
			for i := cbOff; i < len(alphaYUV); i++ {
				alphaYUV[i] = 128
			}
			if err := gpu.Upload(ctx, alphaFrame, alphaYUV, stinger.Width, stinger.Height); err != nil {
				alphaFrame.Release()
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: upload alpha: %w", err)
			}
			alphaFrame.Width = stinger.Width
			alphaFrame.Height = stinger.Height

			// GPU scale overlay and alpha to match base frame if dimensions differ.
			// Stingers may be stored at a different resolution than the pipeline
			// (e.g. 720p stinger PNGs with a 1080p pipeline).
			if stinger.Width != frameA.Width || stinger.Height != frameA.Height {
				scaledOverlay, scaleErr := pool.Acquire()
				if scaleErr != nil {
					alphaFrame.Release()
					overlayFrame.Release()
					dst.Release()
					return fmt.Errorf("gpu transition stinger: acquire scaled overlay: %w", scaleErr)
				}
				scaledOverlay.Width = frameA.Width
				scaledOverlay.Height = frameA.Height
				if err := gpu.ScaleBilinear(ctx, scaledOverlay, overlayFrame); err != nil {
					scaledOverlay.Release()
					alphaFrame.Release()
					overlayFrame.Release()
					dst.Release()
					return fmt.Errorf("gpu transition stinger: scale overlay: %w", err)
				}
				overlayFrame.Release()
				overlayFrame = scaledOverlay

				scaledAlpha, scaleErr := pool.Acquire()
				if scaleErr != nil {
					alphaFrame.Release()
					overlayFrame.Release()
					dst.Release()
					return fmt.Errorf("gpu transition stinger: acquire scaled alpha: %w", scaleErr)
				}
				scaledAlpha.Width = frameA.Width
				scaledAlpha.Height = frameA.Height
				if err := gpu.ScaleBilinear(ctx, scaledAlpha, alphaFrame); err != nil {
					scaledAlpha.Release()
					alphaFrame.Release()
					overlayFrame.Release()
					dst.Release()
					return fmt.Errorf("gpu transition stinger: scale alpha: %w", err)
				}
				alphaFrame.Release()
				alphaFrame = scaledAlpha
			}

			// Determine base source: A before cut point, B after.
			base := frameA
			if position >= stinger.CutPoint && frameB != nil {
				base = frameB
			}

			if err := gpu.BlendStinger(ctx, dst, base, overlayFrame, alphaFrame); err != nil {
				alphaFrame.Release()
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: %w", err)
			}
			alphaFrame.Release()
			overlayFrame.Release()
		} else {
			// No stinger data — fall back to mix blend.
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
			Alpha:      s.Alpha,
			ScaleMode:  s.ScaleMode,
			CropAnchor: s.CropAnchor,
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

// --- Segmentation State Adapter ---

// segmentationStateAdapter bridges SegmentationEngine + Switcher to the
// gpu.SegmentationState interface consumed by gpuAISegmentNode.
type segmentationStateAdapter struct {
	engine   *gpu.SegmentationEngine
	switcher *switcher.Switcher

	// Per-source configs, managed by the REST API (Phase 4).
	// For now, a simple map protected by a mutex.
	mu      sync.Mutex
	configs map[string]*gpu.AISegmentConfig
}

func (a *segmentationStateAdapter) HasEnabledSources() bool {
	if a.engine == nil {
		return false
	}
	// Check if the current program source has segmentation enabled.
	progKey := a.switcher.ProgramSource()
	if progKey == "" {
		return false
	}
	return a.engine.IsEnabled(progKey)
}

func (a *segmentationStateAdapter) ProgramSourceKey() string {
	return a.switcher.ProgramSource()
}

func (a *segmentationStateAdapter) MaskForSource(key string) *gpu.GPUFrame {
	if a.engine == nil {
		return nil
	}
	return a.engine.MaskForSource(key)
}

func (a *segmentationStateAdapter) ConfigForSource(key string) *gpu.AISegmentConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.configs == nil {
		return nil
	}
	return a.configs[key]
}

// SetConfig sets the AI segmentation configuration for a source.
// Called by the REST API (Phase 4) to configure background mode.
func (a *segmentationStateAdapter) SetConfig(key string, cfg *gpu.AISegmentConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.configs == nil {
		a.configs = make(map[string]*gpu.AISegmentConfig)
	}
	if cfg == nil {
		delete(a.configs, key)
	} else {
		a.configs[key] = cfg
	}
}

// DeleteConfig removes the AI segmentation configuration for a source.
func (a *segmentationStateAdapter) DeleteConfig(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.configs != nil {
		delete(a.configs, key)
	}
}

// --- AI Segment Manager Adapter ---

// aiSegmentManagerAdapter implements control.AISegmentManager.
// It bridges the GPU SegmentationEngine and segmentationStateAdapter
// to the REST API interface. Non-nil only when segEngine is active.
type aiSegmentManagerAdapter struct {
	engine  *gpu.SegmentationEngine
	adapter *segmentationStateAdapter
}

// Ensure interface compliance at compile time.
var _ control.AISegmentManager = (*aiSegmentManagerAdapter)(nil)

// IsAISegmentAvailable returns true when a working segmentation engine exists.
func (m *aiSegmentManagerAdapter) IsAISegmentAvailable() bool {
	return m.engine != nil
}

// EnableAISegment enables AI segmentation for a source and stores its config.
// The source resolution is not known at REST API call time — the segmentation
// engine will receive the actual dimensions on first IngestYUV call from the
// source manager. We store the config and defer EnableSource() until the
// source manager sees the first frame with real dimensions.
// The smoothing value is derived from edgeSmooth (0→0 smoothing, 1→0.9 smoothing).
func (m *aiSegmentManagerAdapter) EnableAISegment(source string, sensitivity, edgeSmooth float32, background string) error {
	if m.engine == nil {
		return gpu.ErrTensorRTNotAvailable
	}

	// Map edgeSmooth (0.0-1.0) to EMA alpha (0.0-0.9).
	// Higher edgeSmooth = heavier temporal filtering = smoother but more laggy.
	smoothing := edgeSmooth * 0.9

	// Store the REST config for the pipeline node to read.
	m.adapter.SetConfig(source, &gpu.AISegmentConfig{
		Background:  background,
		Sensitivity: sensitivity,
		EdgeSmooth:  edgeSmooth,
	})

	// Store pending config on the engine so the source manager can call
	// EnableSource with real frame dimensions on first IngestYUV.
	m.engine.SetPendingConfig(source, smoothing)

	return nil
}

// DisableAISegment stops AI segmentation for a source.
func (m *aiSegmentManagerAdapter) DisableAISegment(source string) {
	if m.engine != nil {
		m.engine.DisableSource(source)
	}
	m.adapter.DeleteConfig(source)
}

// GetAISegmentConfig returns the current AI segmentation config for a source.
func (m *aiSegmentManagerAdapter) GetAISegmentConfig(source string) (internal.AISegmentConfig, bool) {
	gpuCfg := m.adapter.ConfigForSource(source)
	if gpuCfg == nil {
		return internal.AISegmentConfig{}, false
	}
	enabled := m.engine != nil && m.engine.IsEnabled(source)
	return internal.AISegmentConfig{
		Enabled:     enabled,
		Sensitivity: gpuCfg.Sensitivity,
		EdgeSmooth:  gpuCfg.EdgeSmooth,
		Background:  gpuCfg.Background,
	}, true
}

// AllConfigs returns a snapshot of all per-source AI segmentation configs.
// Used by enrichState for ControlRoomState broadcast.
func (m *aiSegmentManagerAdapter) AllConfigs() map[string]internal.AISegmentConfig {
	m.adapter.mu.Lock()
	defer m.adapter.mu.Unlock()
	if len(m.adapter.configs) == 0 {
		return nil
	}
	out := make(map[string]internal.AISegmentConfig, len(m.adapter.configs))
	for key, gpuCfg := range m.adapter.configs {
		if gpuCfg == nil {
			continue
		}
		enabled := m.engine != nil && m.engine.IsEnabled(key)
		out[key] = internal.AISegmentConfig{
			Enabled:     enabled,
			Sensitivity: gpuCfg.Sensitivity,
			EdgeSmooth:  gpuCfg.EdgeSmooth,
			Background:  gpuCfg.Background,
		}
	}
	return out
}
