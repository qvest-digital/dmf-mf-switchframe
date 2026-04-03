package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/layout"
	"github.com/zsiec/switchframe/server/sidecar"
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
	previewNode   gpu.PreviewEncodeForceIDR // non-nil when GPU program preview encode is active
	fruc          *gpu.FRUC                 // FRUC for GPU-accelerated replay motion blur (nil if unavailable)
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
	poolFormat := gpuPoolFormatForColor(pf.Color.ColorFormat)
	var pool *gpu.FramePool
	if poolFormat == gpu.GPUFormatP210 {
		pool, err = gpu.NewP210FramePool(ctx, pf.Width, pf.Height, 32)
	} else {
		pool, err = gpu.NewFramePool(ctx, pf.Width, pf.Height, 32)
	}
	if err != nil {
		slog.Warn("GPU frame pool failed, falling back to CPU", "error", err)
		_ = ctx.Close()
		return nil
	}
	slog.Info("GPU frame pool created", "format", poolFormat, "width", pf.Width, "height", pf.Height)
	ctx.SetPool(pool)

	return &gpuState{ctx: ctx, pool: pool}
}

// gpuPoolFormatForColor maps a pipeline color format to the corresponding GPU
// frame pool format. Professional 10-bit 4:2:2 uses P210; everything else
// uses NV12 (8-bit 4:2:0).
func gpuPoolFormatForColor(cf switcher.PipelineColorFormat) gpu.GPUFrameFormat {
	if cf == switcher.ColorYUV422_10bit {
		return gpu.GPUFormatP210
	}
	return gpu.GPUFormatNV12
}

// wireGPUPipeline creates the full GPU pipeline with all processing nodes
// and registers it on the switcher. Call after initGPU returns a non-nil state.
func wireGPUPipeline(gs *gpuState, sw *switcher.Switcher, app *App) {
	if gs == nil {
		return
	}

	pf := sw.PipelineFormat()

	// Create GPU encoder (VideoToolbox on macOS, NVENC on Linux).
	// Codec-aware: creates HEVC encoder for professional/standard-hevc profiles.
	encoder, err := gpu.NewGPUEncoderForCodec(gs.ctx, pf.Width, pf.Height, pf.FPSNum, pf.FPSDen,
		8_000_000, pf.Color.Codec, pf.Color.ColorFormat)
	if err != nil {
		slog.Warn("GPU encoder failed, falling back to CPU pipeline", "error", err)
		return
	}
	gs.encoder = encoder
	slog.Info("GPU encoder created",
		"codec", pf.Color.Codec,
		"colorFormat", pf.Color.ColorFormat,
		"isHEVC", encoder.IsHEVC(),
	)

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

	// Initialize AI segmentation via sidecar (the only supported path).
	var segAdapter gpu.SegmentationState
	if app.cfg.AISidecarBin != "" && gs.ctx.Backend() == "cuda" {
		// Sidecar mode: spawn AI sidecar, share FramePool via CUDA IPC.
		segProxy := app.initAISidecar(gs, sw)
		if segProxy != nil {
			sourceMgr.SetSegmenter(segProxy)
			app.segEngine = segProxy
			sa := &segmentationStateAdapter{
				engine:   segProxy,
				switcher: sw,
			}
			app.segAdapter = sa
			segAdapter = sa
		}
	}

	// Initialize neural key adapter and engine if model path is configured.
	var neuralKeyAdapter gpu.NeuralKeyState
	var neuralKeyEngine *gpu.NeuralKeyEngine
	if app.cfg.NeuralKeyModelPath != "" && gs.ctx.Backend() == "cuda" {
		nkAdapter := &neuralKeyStateAdapter{
			keyer:     app.keyProcessor,
			sourceMgr: sourceMgr,
		}
		neuralKeyAdapter = nkAdapter

		nkEngine, nkErr := gpu.NewNeuralKeyEngine(gs.ctx, app.cfg.NeuralKeyModelPath, pf.Width, pf.Height)
		if nkErr != nil {
			slog.Warn("Neural key engine unavailable", "error", nkErr)
		} else {
			neuralKeyEngine = nkEngine
			slog.Info("Neural key engine initialized", "model", app.cfg.NeuralKeyModelPath)
		}
	}

	// Wire per-source color corrector into GPU source manager.
	if app.sourceCorrectors != nil {
		sourceMgr.SetSourceColorGrade(app.sourceCorrectors)
		slog.Info("per-source color grading wired to GPU source manager")
	}

	// Create adapter types for state interfaces.
	keyAdapter := &keyBridgeAdapter{bridge: app.keyBridge, sourceMgr: sourceMgr}
	compositorAdapter := &compositorStateAdapter{compositor: app.compositor}
	layoutAdapter := &layoutStateAdapter{layout: app.layoutCompositor, sourceMgr: sourceMgr}

	// Raw sinks — GPU raw sink nodes download to CPU only when active.
	var gpuRawVideoSink atomic.Pointer[gpu.RawSinkFunc]

	// Wire the GPU raw video sink to the same underlying callback as the CPU sink.
	// The app sets rawVideoSink on the switcher for CPU path;
	// we mirror it to the GPU raw sink atomic pointer.
	app.gpuRawVideoSink = &gpuRawVideoSink

	// Shared caption SEI cache: consumed once per frame by the program encode
	// callback, read by the async preview encode callback. Both outputs get
	// the same caption data without double-consuming.
	var lastCaptionSEI atomic.Pointer[[]byte]

	// injectCaptionSEI inserts the given SEI NALU into wire-format data
	// (4-byte length-prefixed NALUs). Codec-aware: dispatches to HEVC VCL
	// detection when the pipeline uses HEVC.
	pipelineCodec := pf.Color.Codec
	injectCaptionSEI := func(wire []byte, sei []byte) []byte {
		if len(sei) == 0 {
			return wire
		}
		var codecArg string
		if pipelineCodec == switcher.CodecHEVC {
			codecArg = "h265"
		}
		annexB := codec.WireToAnnexBInto(wire, nil)
		inserted := caption.InsertSEIBeforeVCLInto(sei, annexB, nil, codecArg)
		return codec.AnnexBToWireInto(inserted, nil)
	}

	// injectPreviewCaptionSEI inserts an AVC-format SEI NALU into AVC1 data.
	// Preview is always H.264 regardless of pipeline codec.
	injectPreviewCaptionSEI := func(avc1 []byte, sei []byte) []byte {
		if len(sei) == 0 {
			return avc1
		}
		annexB := codec.AVC1ToAnnexBInto(avc1, nil)
		inserted := caption.InsertSEIBeforeVCLInto(sei, annexB, nil)
		return codec.AnnexBToAVC1Into(inserted, nil)
	}

	// Bridge callback: GPU encode outputs Annex B H.264 (data, isIDR, pts).
	// Consumes caption pairs once per frame, caches the SEI NALU for the
	// preview encoder, injects into the program output, and broadcasts.
	broadcastToProgram := sw.BroadcastToProgramFunc()
	videoInfoCb := app.videoInfoCallback("gpu-pipeline")
	var gpuVPS, gpuSPS, gpuPPS []byte
	// pic_timing frame counter for the GPU encode path.
	var gpuPicTimingFrame int64
	// PTS delta diagnostic: detect jitter introduced by GPU encode.
	var gpuLastEncodedPTS int64
	gpuOnEncoded := func(data []byte, isIDR bool, pts int64) {
		// Diagnostic: verify GPU-encoded PTS deltas are nominal.
		if prev := gpuLastEncodedPTS; prev != 0 {
			delta := pts - prev
			if delta != 3003 && delta != 3004 && delta != 3753 && delta != 3754 {
				slog.Warn("gpuOnEncoded: non-nominal PTS delta",
					"delta", delta, "pts", pts, "prevPts", prev)
			}
		}
		gpuLastEncodedPTS = pts
		// Convert Annex B to wire format (4-byte length prefix).
		// AVC1 and HVC1 use the same wire encoding.
		wire := codec.AnnexBToWireInto(data, nil)

		// Consume caption pairs (once per frame) and cache SEI for preview.
		// ConsumeForFrameWithVANC always returns at least a null pair to
		// maintain continuous ATSC A/53 cc_data presence in the stream.
		if app.captionMgr != nil {
			pairs := app.captionMgr.ConsumeForFrameWithVANC()
			var codecStr string
			if pipelineCodec == switcher.CodecHEVC {
				codecStr = "h265"
			}
			// Build combined pic_timing + caption SEI for broadcast
			// decoder compliance, or caption-only SEI if pic_timing
			// is not configured on the switcher.
			var sei []byte
			if ptCfg := sw.PicTimingConfig(); ptCfg != nil {
				var tc *caption.Timecode
				if ptCfg.TimecodeEnabled {
					t := caption.UTCToTimecode(time.Now().UTC(), ptCfg.FPSNum, ptCfg.FPSDen)
					tc = &t
				}
				sei = caption.BuildPicTimingAndCaptionSEI(pairs, *ptCfg, gpuPicTimingFrame, tc, codecStr)
				gpuPicTimingFrame++
			} else {
				sei = caption.BuildSEINALU(pairs, codecStr)
			}
			// Only cache SEI for preview when AVC — HEVC SEI NALUs
			// have a different header format and cannot be injected
			// into the AVC preview stream.
			if pipelineCodec != switcher.CodecHEVC {
				lastCaptionSEI.Store(&sei)
			} else {
				lastCaptionSEI.Store(nil)
			}
			wire = injectCaptionSEI(wire, sei)
		}

		// Extract parameter sets from IDR keyframes for VideoInfo.
		if isIDR {
			nalus := codec.ExtractNALUs(wire)
			if pipelineCodec == switcher.CodecHEVC {
				for _, nalu := range nalus {
					if len(nalu) < 2 {
						continue
					}
					nalType := codec.HEVCNALUType(nalu[0])
					switch nalType {
					case codec.HEVCNALVPS:
						gpuVPS = append([]byte{}, nalu...)
					case codec.HEVCNALSPS:
						gpuSPS = append([]byte{}, nalu...)
					case codec.HEVCNALPPS:
						gpuPPS = append([]byte{}, nalu...)
					}
				}
				if gpuVPS != nil && gpuSPS != nil && gpuPPS != nil {
					videoInfoCb(gpuVPS, gpuSPS, gpuPPS, pf.Width, pf.Height)
				}
			} else {
				for _, nalu := range nalus {
					if len(nalu) == 0 {
						continue
					}
					switch nalu[0] & 0x1F {
					case 7: // SPS
						gpuSPS = append([]byte{}, nalu...)
					case 8: // PPS
						gpuPPS = append([]byte{}, nalu...)
					}
				}
				if gpuSPS != nil && gpuPPS != nil {
					videoInfoCb(nil, gpuSPS, gpuPPS, pf.Width, pf.Height)
				}
			}
		}

		frameCodec := "h264"
		if pipelineCodec == switcher.CodecHEVC {
			frameCodec = "h265"
		}

		frame := &media.VideoFrame{
			PTS:        pts,
			IsKeyframe: isIDR,
			WireData:   wire,
			Codec:      frameCodec,
		}
		if isIDR {
			frame.VPS = gpuVPS
			frame.SPS = gpuSPS
			frame.PPS = gpuPPS
		}
		broadcastToProgram(frame)
	}

	// GPU preview encode callback: Annex B H.264 → AVC1 → inject cached
	// caption SEI → broadcast on program-preview relay. Reads the SEI NALU
	// cached by gpuOnEncoded (same frame's captions, no double-consume).
	previewW, previewH := 1280, 720
	previewBitrate := 3_000_000
	var previewGroupID atomic.Uint32
	var previewSPS, previewPPS []byte
	var previewVideoInfoSent bool
	var previewEncodeNode gpu.GPUPipelineNode
	if app.programPreviewRelay != nil {
		gpuOnPreviewEncoded := func(data []byte, isIDR bool, pts int64) {
			avc1 := codec.AnnexBToAVC1Into(data, nil)

			// Inject cached caption SEI (produced by program encode callback).
			// Only available when pipeline is AVC — HEVC SEI NALUs have a
			// different header format and cannot be injected into AVC preview.
			if seiPtr := lastCaptionSEI.Load(); seiPtr != nil {
				avc1 = injectPreviewCaptionSEI(avc1, *seiPtr)
			}

			if isIDR {
				previewGroupID.Add(1)
			}
			frame := &media.VideoFrame{
				PTS: pts, DTS: pts, IsKeyframe: isIDR,
				WireData: avc1, Codec: "h264", GroupID: previewGroupID.Load(),
			}
			if isIDR {
				for _, nalu := range codec.ExtractNALUs(avc1) {
					if len(nalu) == 0 {
						continue
					}
					switch nalu[0] & 0x1F {
					case 7:
						sps := make([]byte, len(nalu))
						copy(sps, nalu)
						previewSPS = sps
						frame.SPS = sps
					case 8:
						pps := make([]byte, len(nalu))
						copy(pps, nalu)
						previewPPS = pps
						frame.PPS = pps
					}
				}
				if !previewVideoInfoSent && previewSPS != nil && previewPPS != nil {
					avcC := app.buildAVCConfig(previewSPS, previewPPS)
					if avcC != nil {
						app.programPreviewRelay.SetVideoInfo(app.buildVideoInfo(previewSPS, avcC, previewW, previewH))
						slog.Info("gpu-pipeline: program preview VideoInfo set", "w", previewW, "h", previewH)
						previewVideoInfoSent = true
					}
				}
			}
			app.programPreviewRelay.BroadcastVideo(frame)
		}
		previewEncodeNode = gpu.NewGPUPreviewEncodeNode(gs.ctx, previewW, previewH, previewBitrate,
			pf.FPSNum, pf.FPSDen, gpuOnPreviewEncoded)
	}

	// Color grade state adapter for GPU pipeline node.
	var colorGradeAdapter gpu.ColorGradeState
	if app.colorGradeCtrl != nil {
		colorGradeAdapter = newColorGradeStateAdapter(app.colorGradeCtrl)
	}

	// Build GPU pipeline node chain:
	//   gpu_key → [gpu_neural_key] → gpu_layout → gpu_compositor → [gpu_ai_segment] → gpu_colorgrade → gpu_stmap → raw_sink → gpu_preview_encode → gpu_encode
	// Some nodes return nil on unsupported builds (e.g. AI segment on non-TensorRT).
	neuralKeyNode := gpu.NewGPUNeuralKeyNode(gs.ctx, gs.pool, neuralKeyAdapter, app.cfg.NeuralKeyModelPath)
	// Inject TensorRT engine into the neural key node if available.
	if neuralKeyEngine != nil && neuralKeyNode != nil {
		if nkn, ok := neuralKeyNode.(interface{ SetEngine(*gpu.NeuralKeyEngine) }); ok {
			nkn.SetEngine(neuralKeyEngine)
		}
	}

	dskNode := gpu.NewGPUCompositorNode(gs.ctx, compositorAdapter)

	candidateNodes := []gpu.GPUPipelineNode{
		gpu.NewGPUKeyNode(gs.ctx, gs.pool, keyAdapter),
		neuralKeyNode,
		gpu.NewGPULayoutNode(gs.ctx, gs.pool, layoutAdapter),
		dskNode,
		gpu.NewGPUAISegmentNode(gs.ctx, gs.pool, segAdapter),
		gpu.NewGPUColorGradeNode(gs.ctx, colorGradeAdapter),
		gpu.NewGPUSTMapNode(gs.ctx, gs.pool, app.stmapRegistry),
		gpu.NewGPURawSinkNode(gs.ctx, &gpuRawVideoSink),
		previewEncodeNode,
		gpu.NewGPUEncodeNode(gs.ctx, encoder, sw.ForceNextIDRPtr(), gpuOnEncoded),
	}
	// Filter nil nodes (stub builds return nil for unsupported features).
	nodes := make([]gpu.GPUPipelineNode, 0, len(candidateNodes))
	for _, n := range candidateNodes {
		if n != nil {
			nodes = append(nodes, n)
		}
	}

	if err := gpuPipeline.Build(pf.Width, pf.Height, gs.pool.Pitch(), nodes); err != nil {
		slog.Warn("GPU pipeline build failed, falling back to CPU", "error", err)
		encoder.Close()
		gs.encoder = nil
		return
	}

	// Store preview node reference for ForceIDR on source cuts.
	if previewEncodeNode != nil {
		if pn, ok := previewEncodeNode.(gpu.PreviewEncodeForceIDR); ok {
			gs.previewNode = pn
		}
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
		cpuSink(&switcher.ProcessingFrame{Data: yuv, Width: w, Height: h})
	})
	a.gpuRawVideoSink.Store(&fn)
}

// wireGPUReplay creates a GPU FRUC instance and wires it into the replay
// manager for GPU-accelerated motion-blur slow-motion. No-op if the GPU
// is unavailable or the replay manager hasn't been created yet.
func (a *App) wireGPUReplay() {
	if a.gpuState == nil || a.gpuState.ctx == nil || a.replayMgr == nil {
		return
	}

	pf := a.sw.PipelineFormat()
	fruc, err := gpu.NewFRUC(a.gpuState.ctx, pf.Width, pf.Height)
	if err != nil {
		slog.Warn("GPU FRUC init failed, replay will use CPU path", "error", err)
		return
	}

	a.gpuState.fruc = fruc
	a.replayMgr.SetGPUReplay(a.gpuState.ctx, fruc, a.gpuState.pool)
	slog.Info("GPU motion-blur replay enabled",
		"hasNVOFA", fruc.HasNVOFA(),
		"width", pf.Width,
		"height", pf.Height,
		"shutterAngle", "180deg")
}

// initAISidecar spawns the AI sidecar process, exports the FramePool via
// CUDA IPC, sends the handshake, and creates a segmentation proxy. Returns nil
// if the sidecar binary is not found or initialization fails (non-fatal —
// production continues without AI features).
func (a *App) initAISidecar(gs *gpuState, sw *switcher.Switcher) *gpu.SegmentationProxy {
	binPath := a.cfg.AISidecarBin

	// Verify binary exists.
	if _, err := os.Stat(binPath); err != nil {
		slog.Info("AI sidecar binary not found, AI features disabled", "path", binPath)
		return nil
	}

	pf := sw.PipelineFormat()

	// Export FramePool device pointers for zero-copy IPC sharing.
	framePtrs := gs.pool.AllDevPtrs()
	if len(framePtrs) == 0 {
		slog.Warn("AI sidecar: no frame pool device pointers to export")
		return nil
	}

	// Create the shared VRAM pool with IPC handles.
	shmName := "/switchframe-ai-pool"
	pool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width:  pf.Width,
		Height: pf.Height,
		Pitch:  gs.pool.Pitch(),
		Format: int(gs.pool.Format()),
	}, shmName)
	if err != nil {
		slog.Warn("AI sidecar: GPU pool creation failed", "error", err)
		return nil
	}
	a.aiSidecarPool = pool

	// Create sidecar host (proxies created after spawn with the host reference).
	var segProxy *gpu.SegmentationProxy

	host := sidecar.NewHost(sidecar.Config{
		Binary:       binPath,
		ReadyTimeout: 120 * time.Second, // TRT engine build from ONNX can take 60+ seconds on first run
		BuildArgs: func(socketDir string) []string {
			socketPath := filepath.Join(socketDir, "control.sock")
			args := []string{}
			if a.cfg.AIModelsDir != "" {
				args = append(args, "--models-dir", a.cfg.AIModelsDir)
			}
			args = append(args, socketPath)
			return args
		},
		OnRestart: func(gen int64) {
			slog.Info("AI sidecar restarted, recovering state", "generation", gen)
			if segProxy != nil {
				segProxy.RecoverStaleSlots()
			}

			// Re-send GPU pool handshake via Host.Send (thread-safe).
			hs := &sidecar.GPUPoolHandshake{
				Config:       pool.Config(),
				ShmName:      shmName,
				FrameHandles: pool.FrameHandles(),
				ReadyEvents:  pool.ReadyEventHandles(),
				DoneEvents:   pool.DoneEventHandles(),
			}
			if hsErr := a.aiSidecarHost.Send(&sidecar.Message{
				Cmd:  "gpu_pool_init",
				Data: hs,
			}); hsErr != nil {
				slog.Error("AI sidecar: re-send handshake failed", "error", hsErr)
				return
			}

			// Re-enable sources/state lost during sidecar restart.
			if segProxy != nil {
				segProxy.ReenableSources()
			}
		},
		OnStatus: func(s sidecar.Status) {
			if !s.Alive && s.LastError != "" {
				slog.Warn("AI sidecar status", "alive", s.Alive, "error", s.LastError)
			}
		},
	})
	a.aiSidecarHost = host

	// Spawn the sidecar.
	if err := host.Start(context.Background()); err != nil {
		slog.Warn("AI sidecar: failed to start", "error", err)
		_ = pool.Close()
		a.aiSidecarPool = nil
		a.aiSidecarHost = nil
		return nil
	}

	// Send handshake via Host.Send (writeMu-protected).
	hs := &sidecar.GPUPoolHandshake{
		Config:       pool.Config(),
		ShmName:      shmName,
		FrameHandles: pool.FrameHandles(),
		ReadyEvents:  pool.ReadyEventHandles(),
		DoneEvents:   pool.DoneEventHandles(),
	}
	if err := host.Send(&sidecar.Message{Cmd: "gpu_pool_init", Data: hs}); err != nil {
		slog.Warn("AI sidecar: handshake failed", "error", err)
		_ = host.Close()
		_ = pool.Close()
		a.aiSidecarPool = nil
		a.aiSidecarHost = nil
		return nil
	}

	// Create segmentation proxy now that host is live and connected.
	segProxy = gpu.NewSegmentationProxy(host, pool)

	// Wire OnMessage to dispatch to the segmentation proxy.
	host.OnMessage = func(msg *sidecar.Message) {
		segProxy.HandleMessage(msg)
	}

	slog.Info("AI sidecar initialized",
		"binary", binPath,
		"pool_slots", len(framePtrs),
		"frame_size", fmt.Sprintf("%dx%d", pf.Width, pf.Height),
	)

	return segProxy
}

// closeGPU releases all GPU resources.
func closeGPU(gs *gpuState) {
	if gs == nil {
		return
	}
	// Close FRUC first — it holds CUDA/NVOFA resources.
	if gs.fruc != nil {
		gs.fruc.Close()
	}
	// Close source manager — it holds GPU frames from the pool.
	if gs.sourceManager != nil {
		gs.sourceManager.Close()
	}
	if gs.pipeline != nil {
		_ = gs.pipeline.Close()
	}
	if gs.encoder != nil {
		gs.encoder.Close()
	}
	if gs.pool != nil {
		gs.pool.Close()
	}
	if gs.ctx != nil {
		_ = gs.ctx.Close()
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

// previewRelayDebugProvider exposes program-preview relay viewer stats
// and GPU preview encode counters in the debug snapshot.
type previewRelayDebugProvider struct {
	app *App
}

func (p *previewRelayDebugProvider) DebugSnapshot() map[string]any {
	result := make(map[string]any)

	// Relay viewer stats (videoSent, videoDropped, bytesSent per viewer).
	if relay := p.app.programPreviewRelay; relay != nil {
		viewers := relay.ViewerStatsAll()
		viewerList := make([]map[string]any, 0, len(viewers))
		var totalSent, totalDropped int64
		for _, v := range viewers {
			viewerList = append(viewerList, map[string]any{
				"id":            v.ID,
				"video_sent":    v.VideoSent,
				"video_dropped": v.VideoDropped,
				"audio_sent":    v.AudioSent,
				"audio_dropped": v.AudioDropped,
				"bytes_sent":    v.BytesSent,
				"last_video_ts": v.LastVideoTsMS,
			})
			totalSent += v.VideoSent
			totalDropped += v.VideoDropped
		}
		result["viewer_count"] = len(viewers)
		result["total_video_sent"] = totalSent
		result["total_video_dropped"] = totalDropped
		result["viewers"] = viewerList
	}

	// GPU preview encode node counters.
	if gs := p.app.gpuState; gs != nil && gs.previewNode != nil {
		if dn, ok := gs.previewNode.(interface{ DiagSnapshot() map[string]any }); ok {
			result["gpu_encode"] = dn.DiagSnapshot()
		}
	}

	return result
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
	// All blend calls use dispatch functions that route P210 vs NV12 automatically.
	ctx := r.ctx
	switch transType {
	case "mix":
		if err := gpu.GPUBlendMixDispatch(ctx, dst, frameA, frameB, position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition mix: %w", err)
		}
	case "dip":
		// Dip = fade to black then from black.
		// Phase 1 (pos 0-0.5): fade A to black.
		// Phase 2 (pos 0.5-1.0): fade B from black.
		if position <= 0.5 {
			dipPos := position * 2.0
			if err := gpu.GPUBlendFTBDispatch(ctx, dst, frameA, dipPos); err != nil {
				dst.Release()
				return fmt.Errorf("gpu transition dip phase1: %w", err)
			}
		} else {
			dipPos := (1.0 - position) * 2.0
			if err := gpu.GPUBlendFTBDispatch(ctx, dst, frameB, dipPos); err != nil {
				dst.Release()
				return fmt.Errorf("gpu transition dip phase2: %w", err)
			}
		}
	case "ftb":
		if err := gpu.GPUBlendFTBDispatch(ctx, dst, frameA, position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition ftb: %w", err)
		}
	case "ftb_reverse":
		if err := gpu.GPUBlendFTBDispatch(ctx, dst, frameA, 1.0-position); err != nil {
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
		if err := gpu.GPUBlendWipeDispatch(ctx, dst, frameA, frameB, maskBuf, position, dir, 4); err != nil {
			maskBuf.Release()
			dst.Release()
			return fmt.Errorf("gpu transition wipe: %w", err)
		}
		maskBuf.Release()
	case "stinger":
		// Stinger: composite stinger overlay with per-pixel alpha onto
		// base source (A before cut point, B after).
		if stinger != nil && len(stinger.YUV) > 0 && len(stinger.Alpha) > 0 {
			// Upload stinger overlay YUV to GPU.
			// Stinger YUV is always 8-bit YUV420P. For P210 pools, promote
			// via UploadYUV420PToP210. For NV12 pools, use standard Upload.
			overlayFrame, overlayErr := pool.Acquire()
			if overlayErr != nil {
				dst.Release()
				return fmt.Errorf("gpu transition stinger: acquire overlay: %w", overlayErr)
			}
			var uploadErr error
			if overlayFrame.Format == gpu.GPUFormatP210 {
				uploadErr = gpu.UploadYUV420PToP210(ctx, overlayFrame, stinger.YUV, stinger.Width, stinger.Height)
			} else {
				uploadErr = gpu.Upload(ctx, overlayFrame, stinger.YUV, stinger.Width, stinger.Height)
			}
			if uploadErr != nil {
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: upload overlay: %w", uploadErr)
			}
			overlayFrame.Width = stinger.Width
			overlayFrame.Height = stinger.Height

			// Upload alpha plane as fake YUV420P: Y=alpha, UV=128 (neutral).
			alphaFrame, alphaErr := pool.Acquire()
			if alphaErr != nil {
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: acquire alpha: %w", alphaErr)
			}
			alphaYUV := make([]byte, switcher.ColorYUV420_8bit.FrameSize(stinger.Width, stinger.Height))
			copy(alphaYUV[:len(stinger.Alpha)], stinger.Alpha)
			cbOff := stinger.Width * stinger.Height
			for i := cbOff; i < len(alphaYUV); i++ {
				alphaYUV[i] = 128
			}
			if alphaFrame.Format == gpu.GPUFormatP210 {
				uploadErr = gpu.UploadYUV420PToP210(ctx, alphaFrame, alphaYUV, stinger.Width, stinger.Height)
			} else {
				uploadErr = gpu.Upload(ctx, alphaFrame, alphaYUV, stinger.Width, stinger.Height)
			}
			if uploadErr != nil {
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
				if err := gpu.GPUScaleDispatch(ctx, scaledOverlay, overlayFrame); err != nil {
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
				if err := gpu.GPUScaleDispatch(ctx, scaledAlpha, alphaFrame); err != nil {
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

			if err := gpu.GPUBlendStingerDispatch(ctx, dst, base, overlayFrame, alphaFrame); err != nil {
				alphaFrame.Release()
				overlayFrame.Release()
				dst.Release()
				return fmt.Errorf("gpu transition stinger: %w", err)
			}
			alphaFrame.Release()
			overlayFrame.Release()
		} else {
			// No stinger data — fall back to mix blend.
			if err := gpu.GPUBlendMixDispatch(ctx, dst, frameA, frameB, position); err != nil {
				dst.Release()
				return fmt.Errorf("gpu transition stinger fallback: %w", err)
			}
		}
	default:
		// Unknown type — use mix as fallback.
		if err := gpu.GPUBlendMixDispatch(ctx, dst, frameA, frameB, position); err != nil {
			dst.Release()
			return fmt.Errorf("gpu transition fallback: %w", err)
		}
	}

	// Diagnostic: verify blend produced non-zero output.
	{
		probe := make([]byte, 64)
		if err := gpu.ProbeGPUFrame(r.ctx, dst, probe); err == nil {
			nonZero := 0
			for _, b := range probe {
				if b != 0 {
					nonZero++
				}
			}
			slog.Info("gpu transition: blend probe",
				"type", transType, "pos", fmt.Sprintf("%.3f", position),
				"nonZeroBytes", nonZero, "dstFormat", dst.Format,
				"dstW", dst.Width, "dstH", dst.Height, "dstPitch", dst.Pitch,
				"aW", frameA.Width, "aH", frameA.Height, "aPitch", frameA.Pitch,
				"first8", fmt.Sprintf("%02x %02x %02x %02x %02x %02x %02x %02x",
					probe[0], probe[1], probe[2], probe[3], probe[4], probe[5], probe[6], probe[7]))
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

// segmentationStateAdapter bridges Segmenter + Switcher to the
// gpu.SegmentationState interface consumed by gpuAISegmentNode.
type segmentationStateAdapter struct {
	engine   gpu.Segmenter
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

// --- Neural Key State Adapter ---

// neuralKeyStateAdapter bridges KeyProcessor + Switcher + GPUSourceManager
// to gpu.NeuralKeyState. Returns all sources with type:"neural" keys, each
// with a reference to the source's GPU cached fill frame.
type neuralKeyStateAdapter struct {
	keyer     *graphics.KeyProcessor
	sourceMgr *gpu.GPUSourceManager
}

func (a *neuralKeyStateAdapter) HasNeuralKeys() bool {
	for _, cfg := range a.keyer.EnabledSources() {
		if cfg.Type == graphics.KeyTypeNeural {
			return true
		}
	}
	return false
}

func (a *neuralKeyStateAdapter) SnapshotNeuralKeys() []gpu.NeuralKeySnapshot {
	var result []gpu.NeuralKeySnapshot
	for key, cfg := range a.keyer.EnabledSources() {
		if cfg.Type != graphics.KeyTypeNeural {
			continue
		}
		var fill *gpu.GPUFrame
		if a.sourceMgr != nil {
			fill = a.sourceMgr.GetFrame(key)
		}
		result = append(result, gpu.NeuralKeySnapshot{
			SourceKey:  key,
			KeyColorY:  cfg.KeyColorY,
			KeyColorCb: cfg.KeyColorCb,
			KeyColorCr: cfg.KeyColorCr,
			Similarity: cfg.Similarity,
			Smoothness: cfg.Smoothness,
			FillFrame:  fill,
		})
	}
	return result
}

// --- AI Segment Manager Adapter ---

// aiSegmentManagerAdapter implements control.AISegmentManager.
// It bridges the GPU Segmenter and segmentationStateAdapter
// to the REST API interface. Non-nil only when segEngine is active.
type aiSegmentManagerAdapter struct {
	engine  gpu.Segmenter
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
