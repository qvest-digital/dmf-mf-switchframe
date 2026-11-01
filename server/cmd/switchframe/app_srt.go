package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
	srtgo "github.com/zsiec/srtgo"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/preview"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// srtSourceState tracks an active SRT source and its associated resources
// (encoder, relay) for clean shutdown and reconnection.
type srtSourceState struct {
	source       *srt.Source
	relay        *distribution.Relay
	videoEncoder transition.VideoEncoder // set by relay goroutine, for stats
	audioEncoder audio.Encoder           // set by audio goroutine, for stats
	previewEnc   *preview.Encoder        // set when preview proxy is enabled

	// Relay path drop counters (incremented in decode callbacks).
	relayVideoDrops *atomic.Int64
	relayAudioDrops *atomic.Int64

	// anchor places the connection's timestamps on the engine timeline.
	anchor *clock.TrackingAnchor

	// oversizeLogged gates a one-time warning when a decoded frame exceeds the
	// frame pool's buffer size, which forces per-frame heap fallback allocations
	// on the hot ingest path (a source/pipeline resolution or format mismatch).
	oversizeLogged atomic.Bool
}

// srtVideoJob carries a decoded YUV frame between the decode callback and
// the relay/pipeline/replay goroutines.
type srtVideoJob struct {
	yuv []byte
	w   int
	h   int
	pts int64
}

// srtAudioJob carries decoded PCM audio between the decode callback and
// the audio goroutine.
type srtAudioJob struct {
	pcm        []float32
	pts        int64
	sampleRate int
	channels   int
}

// srtReplayGPUJob is a lightweight signal for the GPU replay encode goroutine.
// No YUV data — the GPU frame is read from the source manager's atomic pointer.
type srtReplayGPUJob struct {
	pts  int64
	w, h int
}

// srtRelayContext groups the shared state passed from wireSRTSource to the
// relay helper methods. This avoids passing 10+ individual parameters.
type srtRelayContext struct {
	key       string
	relay     *distribution.Relay
	state     *srtSourceState
	framePool *switcher.FramePool
	pf        switcher.PipelineFormat

	// gpu is the GPU state snapshot taken when the source was wired (nil when
	// the GPU subsystem is inactive). The replay/preview goroutines must keep
	// using the snapshot their registrations were made against, not whatever
	// state a later format-change rebuild installs.
	gpu *gpuState

	// anchor places the connection's timestamps on the engine timeline.
	anchor *clock.TrackingAnchor

	// Channels connecting decode callbacks to goroutines.
	relayVideoCh  chan srtVideoJob
	replayVideoCh chan srtVideoJob // nil when replay is not active
	audioCh       chan srtAudioJob

	// Replay viewer (nil when replay is not active or wired via relay).
	replayViewer interface {
		SendVideo(frame *media.VideoFrame)
		SendAudio(frame *media.AudioFrame)
	}
}

// initSRT initializes the SRT store, stats manager, caller, and optionally the
// listener. Called after initSubsystems, before initAPI. The store, stats, and
// caller are always initialized so the SRT pull API works without --srt-listen.
// The listener is only created when --srt-listen is non-empty.
func (a *App) initSRT() error {
	// Initialize the active sources map.
	a.srtSources = make(map[string]*srtSourceState)

	// Create store for persisting source configs.
	storePath := a.statePath("srt_sources.json")
	store, err := srt.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("srt: create store: %w", err)
	}
	a.srtStore = store
	slog.Info("srt store initialized", "path", storePath)

	// Create stats manager for tracking per-source SRT metrics.
	statsMgr := srt.NewStatsManager()
	a.srtStats = statsMgr
	a.debugCollector.Register("srt", statsMgr)
	if a.appMetrics != nil {
		registerCollectors(metrics.GetRegistry(), srt.NewMetricsCollector(statsMgr))
	}
	a.debugCollector.Register("relay_drops", &relayDropProvider{app: a})
	a.debugCollector.Register("srt_anchors", srtAnchorProvider{app: a})
	if a.appMetrics != nil {
		registerCollectors(metrics.GetRegistry(), srtAnchorCollector{app: a})
	}

	// Wire stats manager into output manager so SRT output adapters
	// get ConnStats entries for monitoring via /debug/snapshot.
	if a.outputMgr != nil {
		a.outputMgr.SetSRTStatsManager(statsMgr)
	}

	latency := time.Duration(a.cfg.SRTLatencyMs) * time.Millisecond

	// Create caller (outbound pulls with auto-reconnect).
	// Always available — does not require --srt-listen.
	caller := srt.NewCaller(srt.CallerConfig{
		Store:          store,
		DefaultLatency: latency,
		OnSource:       a.onSRTCallerSource,
	})
	a.srtCaller = caller

	// Create listener only when --srt-listen is configured.
	if a.cfg.SRTListen != "" {
		listener, err := srt.NewListener(srt.ListenerConfig{
			Addr:           a.cfg.SRTListen,
			Store:          store,
			DefaultLatency: latency,
			OnSource:       a.onSRTListenerSource,
		})
		if err != nil {
			return fmt.Errorf("srt: create listener: %w", err)
		}
		a.srtListener = listener
	}

	slog.Info("srt initialized",
		"listen", a.cfg.SRTListen,
		"latencyMs", a.cfg.SRTLatencyMs,
	)
	return nil
}

// onSRTListenerSource is the callback for a new SRT push connection accepted
// by the listener. Fire-and-forget -- the Source manages its own lifecycle.
// Closes the connection on any error path to prevent resource leaks.
func (a *App) onSRTListenerSource(cfg srt.SourceConfig, conn *srtgo.Conn) {
	// The listener incremented activeCount before invoking this callback. On the
	// happy path the slot is released by src.OnStopped when the decode loop ends.
	// But Source.Start creates the decoder (and can fail with no goroutines
	// launched) before OnStopped can ever fire, and wireSRTSource may return nil,
	// so any terminal path here that doesn't reach OnStopped must release the
	// slot itself — otherwise activeCount leaks and eventually wedges MaxSources.
	src := a.wireSRTSource(cfg, conn)
	if src == nil {
		_ = conn.Close()
		if a.srtListener != nil {
			a.srtListener.ReleaseSource()
		}
		return
	}
	if err := src.Start(a.srtCtx); err != nil {
		slog.Error("srt: failed to start source", "key", cfg.Key, "error", err)
		_ = conn.Close()
		// OnStopped never fires on its own when Start fails (no decode
		// goroutine was launched), so run its cleanup explicitly: it closes
		// the fan-out channels, removes GPU/replay registrations and the
		// srtSources entry, and releases the listener slot (cfg.Mode is
		// ModeListener here, so no extra ReleaseSource call is needed).
		failSRTSourceStart(src, cfg.Key)
		return
	}
}

// onSRTCallerSource is the callback for a successful outbound SRT pull
// connection. Returns a done channel that closes when the source stops,
// triggering reconnection in the Caller's connect loop.
// Uses sync.Once to prevent double-close of the done channel.
func (a *App) onSRTCallerSource(cfg srt.SourceConfig, conn *srtgo.Conn) <-chan struct{} {
	doneCh := make(chan struct{})
	var closeOnce sync.Once

	src := a.wireSRTSource(cfg, conn)
	if src == nil {
		_ = conn.Close()
		close(doneCh)
		return doneCh
	}

	// Wrap the OnStopped callback to also close the done channel.
	// This must be done BEFORE Start() to avoid a race.
	origOnStopped := src.OnStopped
	src.OnStopped = func(key string) {
		if origOnStopped != nil {
			origOnStopped(key)
		}
		closeOnce.Do(func() { close(doneCh) })
	}

	if err := src.Start(a.srtCtx); err != nil {
		slog.Error("srt: failed to start source", "key", cfg.Key, "error", err)
		_ = conn.Close()
		// OnStopped never fires on its own when Start fails (no decode
		// goroutine was launched). failSRTSourceStart invokes the wrapped
		// OnStopped above, which runs wireSRTSource's cleanup (closing the
		// fan-out channels, removing GPU/replay registrations and the
		// srtSources entry) AND closes doneCh to trigger reconnection.
		failSRTSourceStart(src, cfg.Key)
		return doneCh
	}

	return doneCh
}

// failSRTSourceStart cleans up an SRT source whose Start() returned an error.
//
// srt.Source.Start fails before launching any goroutine (decoder creation is
// the first step), so the decode-goroutine teardown that normally invokes
// OnStopped never runs — and Stop() does not call OnStopped either.
// Everything wireSRTSource set up before Start is reclaimed only by
// OnStopped: the fan-out goroutines parked on their job channels (and the
// encoders they own), the GPU source-manager and replay registrations, the
// srtSources map entry, and (for listener mode) the listener slot. Invoke it
// explicitly here, exactly once, or all of it leaks on every failed
// connection attempt.
func failSRTSourceStart(src *srt.Source, key string) {
	src.Stop() // release the conn and any partially initialized state
	if src.OnStopped != nil {
		src.OnStopped(key)
	}
}

// wireSRTSource creates an SRT Source and wires it into the switcher/mixer/relay.
// Returns the Source with callbacks configured but NOT started. The caller is
// responsible for calling src.Start() after any additional callback wrapping.
//
// Follows the same fan-out pattern as initMXL (app.go) and startMXLDemo (app_mxl_demo.go):
//   - Clean up existing source if reconnecting (same streamID)
//   - Keep a source already registered (bindSRTSource), else register it
//     with the switcher (IngestRawVideo path) and the mixer (IngestPCM path)
//   - Create browser relay for MoQ subscribers
//   - Wire OnRawVideo -> ProcessingFrame -> IngestRawVideo + encode -> relay.BroadcastVideo
//   - Wire OnRawAudio -> IngestPCM + encode -> relay.BroadcastAudio
//   - Wire OnStopped -> cleanup + stats update
func (a *App) wireSRTSource(cfg srt.SourceConfig, conn *srtgo.Conn) *srt.Source {
	key := cfg.Key

	// Clean up existing source if reconnecting with the same key.
	// Copy old state and release lock before calling Stop() — Stop() blocks
	// on wg.Wait() and must not be called under srtSourcesMu.
	a.srtSourcesMu.Lock()
	oldState, hadOld := a.srtSources[key]
	if hadOld {
		delete(a.srtSources, key)
	}
	a.srtSourcesMu.Unlock()

	if hadOld {
		slog.Info("SRT source reconnecting, cleaning up old source", "key", key)
		oldState.source.Stop() // triggers OnStopped which closes channels → goroutines clean up
	}

	anchor := a.bindSRTSource(key)

	// Create browser relay for MoQ viewing.
	relay := a.server.RegisterStream(key)

	// Create or get stats tracker.
	cs := a.srtStats.GetOrCreate(key)
	remoteAddr := ""
	if conn.RemoteAddr() != nil {
		remoteAddr = conn.RemoteAddr().String()
	}
	cs.SetConnected(remoteAddr, cfg.LatencyMs)

	// Apply label from config.
	if cfg.Label != "" {
		_ = a.sw.SetLabel(context.Background(), key, cfg.Label)
	}

	// Snapshot the GPU state once for the whole wiring: every registration,
	// closure, and goroutine below must operate against the SAME state, and
	// the OnStopped cleanup must remove registrations from the manager they
	// were made on even if a format-change rebuild swapped in a new one.
	gs := a.gpu()

	// Pass hardware device context for NVDEC when CUDA GPU is active.
	if gs != nil && gs.sourceManager != nil {
		cfg.HWDeviceCtx = codec.HWDeviceCtx()
	}

	// Create source orchestrator.
	src := srt.NewSource(cfg, conn, cs, slog.Default())

	// Relay path drop counters (incremented in decode callbacks, exposed in debug snapshots).
	var relayVideoDrops, relayAudioDrops atomic.Int64

	// Source state for tracking encoder lifecycle and cleanup.
	state := &srtSourceState{
		source:          src,
		relay:           relay,
		relayVideoDrops: &relayVideoDrops,
		relayAudioDrops: &relayAudioDrops,
		anchor:          anchor,
	}

	// === FULLY ASYNC ARCHITECTURE ===
	//
	// The FFmpeg decode goroutine runs a single-threaded decode loop:
	// av_read_frame → decode → callback → av_read_frame → ...
	// If ANY callback blocks, the entire decode loop stalls — no video
	// OR audio frames are produced. This causes bursty frame delivery
	// with 100-1500ms gaps.
	//
	// Solution: The decode callbacks do NOTHING blocking. They deep-copy
	// the data and send to buffered channels. Three separate goroutines
	// handle the heavy work:
	//   1. Pipeline goroutine: IngestRawVideo (program path)
	//   2. Relay video goroutine: H.264 encode + BroadcastVideo
	//   3. Audio goroutine: IngestPCM + AAC encode + BroadcastAudio
	//
	// PTS linearization runs in the callback (fast, no allocation).

	pf := a.sw.PipelineFormat()
	framePool := a.sw.GetFramePool()

	pipelineCh := make(chan srtVideoJob, 4)   // → IngestRawVideo
	relayVideoCh := make(chan srtVideoJob, 4) // → H.264 encode + relay
	audioCh := make(chan srtAudioJob, 8)      // → IngestPCM + AAC encode + relay

	// Replay channel: when preview proxy is active and replay is configured,
	// a separate full-quality encode goroutine feeds the replay viewer directly.
	var replayVideoCh chan srtVideoJob
	var replayViewer interface {
		SendVideo(frame *media.VideoFrame)
		SendAudio(frame *media.AudioFrame)
	}

	// Shared PTS linearizer for video and audio. Uses a single offset so
	// both streams stay aligned after PTS jumps (SRT source loop/reconnect).
	ptsLin := srt.NewPTSLinearizer()

	// --- Goroutine 1: Pipeline ingest (program path) ---
	a.startSRTPipelineIngest(key, anchor, pipelineCh, framePool)

	// --- Preview proxy encoder (replaces full-quality relay encode) ---
	var previewEnc *preview.Encoder
	var gpuPreviewActive bool
	if a.cfg.PreviewProxy && gs != nil && gs.sourceManager != nil {
		// GPU preview encoding — source manager handles it in IngestYUV.
		// Register the source with a PreviewConfig so the GPU source manager
		// creates a per-source GPU preview encoder. The OnPreview callback
		// converts Annex B to AVC1 and broadcasts to the browser relay.
		pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
		var groupID atomic.Uint32
		var videoInfoSent bool
		gs.sourceManager.RegisterSource(key, pf.Width, pf.Height, &gpu.PreviewConfig{
			Width:   pw,
			Height:  ph,
			Bitrate: a.cfg.PreviewBitrate,
			FPSNum:  pf.FPSNum,
			FPSDen:  pf.FPSDen,
			OnPreview: func(data []byte, isIDR bool, pts int64) {
				avc1 := codec.AnnexBToAVC1(data)
				if isIDR {
					groupID.Add(1)
				}
				frame := &media.VideoFrame{
					PTS: pts, DTS: pts, IsKeyframe: isIDR,
					WireData: avc1, Codec: "h264", GroupID: groupID.Load(),
				}
				if isIDR {
					for _, nalu := range codec.ExtractNALUs(avc1) {
						if len(nalu) == 0 {
							continue
						}
						switch nalu[0] & 0x1F {
						case 7:
							frame.SPS = nalu
						case 8:
							frame.PPS = nalu
						}
					}
					if !videoInfoSent && frame.SPS != nil && frame.PPS != nil {
						avcC := moq.BuildAVCDecoderConfig(frame.SPS, frame.PPS)
						if avcC != nil {
							relay.SetVideoInfo(distribution.VideoInfo{
								Codec: codec.ParseSPSCodecString(frame.SPS),
								Width: pw, Height: ph, DecoderConfig: avcC,
							})
							slog.Info("SRT source: GPU preview VideoInfo set", "key", key, "w", pw, "h", ph)
							videoInfoSent = true
						}
					}
				}
				relay.BroadcastVideo(frame)
			},
		})
		gpuPreviewActive = true
		slog.Info("srt: GPU preview encoder registered", "key", key)
	} else if a.cfg.PreviewProxy {
		// CPU preview encoder fallback.
		var err error
		previewEnc, err = preview.NewEncoder(a.sourcePreviewConfig(key, relay, pf.FPSNum, pf.FPSDen))
		if err != nil {
			slog.Error("srt: preview encoder failed, falling back to full quality", "key", key, "error", err)
		} else {
			state.previewEnc = previewEnc
		}
	}

	// When preview proxy is active and replay is configured, set up dual-encode:
	// preview encoder feeds relay (browsers), full-quality encode feeds replay.
	if (previewEnc != nil || gpuPreviewActive) && a.replayMgr != nil {
		if err := a.replayMgr.AddSource(key); err != nil {
			slog.Warn("srt: could not add replay source", "key", key, "err", err)
		} else if v := a.replayMgr.Viewer(key); v != nil {
			replayViewer = v
			replayVideoCh = make(chan srtVideoJob, 4)
		}
	}

	// Build the shared relay context used by all helper methods.
	rc := &srtRelayContext{
		key:           key,
		relay:         relay,
		state:         state,
		framePool:     framePool,
		pf:            pf,
		gpu:           gs,
		anchor:        anchor,
		relayVideoCh:  relayVideoCh,
		replayVideoCh: replayVideoCh,
		audioCh:       audioCh,
		replayViewer:  replayViewer,
	}

	// --- Goroutine 2: Relay video encode ---
	// When GPU preview is active, the GPU source manager handles preview
	// encoding via IngestYUV, so we just drain the relay channel to release
	// frame pool buffers. When CPU preview is enabled, the preview.Encoder
	// handles scaling and encoding. Otherwise, use full-quality encode.
	var replayGPUCh chan srtReplayGPUJob
	if gpuPreviewActive {
		a.startSRTVideoRelayGPUDrain(rc)
	} else if previewEnc != nil {
		a.startSRTVideoRelayPreview(rc, previewEnc)
	} else {
		a.startSRTVideoRelayFullQuality(rc)
	}

	// --- GPU replay encode (NVDEC + NVENC, zero CPU-copy) ---
	if gs != nil && gs.sourceManager != nil && cfg.HWDeviceCtx != nil && replayViewer != nil {
		replayGPUCh = make(chan srtReplayGPUJob, 2)
		a.startSRTReplayGPUEncode(rc, replayGPUCh)
	}

	// --- CPU replay full-quality encode (only when preview proxy + replay) ---
	// Skipped when GPU replay encode is active — NVDEC path handles replay.
	if replayVideoCh != nil && replayGPUCh == nil {
		a.startSRTReplayCPUEncode(rc)
	}

	// --- Goroutine 3: Audio (mixer + relay encode) ---
	a.startSRTAudioRelay(rc)

	// --- Register preview encoder with debug collector ---
	if previewEnc != nil {
		a.debugCollector.Register("preview:"+key, previewEnc)
	}

	// --- Decode callbacks: ZERO blocking work ---
	// Only: linearize PTS + deep copy + non-blocking channel send.

	src.OnRawVideo = func(sourceKey string, yuv []byte, w, h int, pts int64) {
		pts = ptsLin.Linearize(pts, srt.StreamVideo)

		// Surface persistent size mismatch: if the decoded frame is larger than
		// the pool buffer, each of the three fan-out copies below falls back to a
		// fresh heap allocation every frame, defeating the off-GC-heap pool. Log
		// once per source (rate-limited via CAS) so the misconfiguration is
		// visible instead of only showing up as silent pool misses + GC pressure.
		if framePool != nil && len(yuv) > framePool.BufSize() && state.oversizeLogged.CompareAndSwap(false, true) {
			slog.Warn("srt: decoded frame exceeds frame pool buffer size; falling back to per-frame heap allocation (check source vs pipeline resolution/format)",
				"key", key, "frameBytes", len(yuv), "poolBufSize", framePool.BufSize(), "w", w, "h", h)
		}

		// Pipeline and relay get SEPARATE buffers to prevent wrong-frame
		// corruption if the relay falls behind. Pipeline buffer is owned by
		// ProcessingFrame lifecycle; relay buffer is released by the relay
		// goroutine after copying to encoderYUV.

		// Pipeline path: pool buffer with ProcessingFrame lifecycle.
		var pipelineBuf []byte
		if framePool != nil && len(yuv) <= framePool.BufSize() {
			pipelineBuf = framePool.Acquire()[:len(yuv)]
		} else {
			pipelineBuf = make([]byte, len(yuv))
		}
		copy(pipelineBuf, yuv)

		select {
		case pipelineCh <- srtVideoJob{yuv: pipelineBuf, w: w, h: h, pts: pts}:
		default:
			if framePool != nil {
				framePool.Release(pipelineBuf)
			}
		}

		// Relay path: separate pool buffer, released after copy to encoderYUV.
		var relayBuf []byte
		if framePool != nil && len(yuv) <= framePool.BufSize() {
			relayBuf = framePool.Acquire()[:len(yuv)]
		} else {
			relayBuf = make([]byte, len(yuv))
		}
		copy(relayBuf, yuv)

		select {
		case relayVideoCh <- srtVideoJob{yuv: relayBuf, w: w, h: h, pts: pts}:
		default:
			relayVideoDrops.Add(1)
			if framePool != nil {
				framePool.Release(relayBuf)
			}
		}

		// Replay path: separate pool buffer for full-quality encode.
		if replayVideoCh != nil {
			var replayBuf []byte
			if framePool != nil && len(yuv) <= framePool.BufSize() {
				replayBuf = framePool.Acquire()[:len(yuv)]
			} else {
				replayBuf = make([]byte, len(yuv))
			}
			copy(replayBuf, yuv)

			select {
			case replayVideoCh <- srtVideoJob{yuv: replayBuf, w: w, h: h, pts: pts}:
			default:
				if framePool != nil {
					framePool.Release(replayBuf)
				}
			}
		}
	}

	// --- NVDEC zero-copy GPU callback ---
	// When NVDEC is active, the C decode loop calls goOnVideoFrameGPU instead
	// of goOnVideoFrame. This callback receives a CUDA device pointer to the
	// NV12 surface in VRAM and copies it directly into a GPU pool frame,
	// bypassing CPU entirely. PTS linearization still runs here (fast, no alloc).
	if gs != nil && gs.sourceManager != nil && cfg.HWDeviceCtx != nil {
		srcMgr := gs.sourceManager
		pool := srcMgr.Pool()

		src.OnRawVideoGPU = func(sourceKey string, yDevPtr, uvDevPtr uintptr, pitch, w, h int, pts int64, bitDepth int) {
			pts = ptsLin.Linearize(pts, srt.StreamVideo)

			// Acquire pool frame for device-to-device copy.
			frame, err := pool.Acquire()
			if err != nil {
				return // pool exhausted, drop frame
			}

			// NVDEC produces NV12 (8-bit) or P016 (10-bit) device frames.
			// When the pool is P210 (professional mode), we need to:
			// 1. Copy NVDEC output into a temporary NV12 frame
			// 2. Convert NV12 → P210 into the pool frame
			// When the pool is NV12 (standard mode), direct copy suffices.
			if pool.Format() == gpu.GPUFormatP210 {
				// Need an NV12 temp frame for the NVDEC copy.
				// Use the source manager's NV12 temp pool via a helper.
				nv12Tmp, nv12Err := srcMgr.AcquireNV12Tmp(sourceKey, w, h)
				if nv12Err != nil {
					frame.Release()
					return
				}
				if bitDepth > 8 {
					if err := gpu.CopyP016ToNV12FromDevice(nv12Tmp, yDevPtr, uvDevPtr, pitch, w, h); err != nil {
						nv12Tmp.Release()
						frame.Release()
						return
					}
				} else {
					if err := gpu.CopyNV12FromDevice(nv12Tmp, yDevPtr, uvDevPtr, pitch, w, h); err != nil {
						nv12Tmp.Release()
						frame.Release()
						return
					}
				}
				// Convert NV12 → P210 on GPU. Use the snapshot ctx, NOT the
				// live a.gpu() state: pool frames belong to the snapshot's
				// pool, and mixing a rebuilt context with old-pool frames
				// would touch destroyed CUDA streams.
				if err := gpu.ConvertNV12ToP210(gs.ctx, frame, nv12Tmp); err != nil {
					nv12Tmp.Release()
					frame.Release()
					return
				}
				nv12Tmp.Release()
			} else {
				// Standard mode: direct NV12 copy into NV12 pool frame.
				if bitDepth > 8 {
					if err := gpu.CopyP016ToNV12FromDevice(frame, yDevPtr, uvDevPtr, pitch, w, h); err != nil {
						frame.Release()
						return
					}
				} else {
					if err := gpu.CopyNV12FromDevice(frame, yDevPtr, uvDevPtr, pitch, w, h); err != nil {
						frame.Release()
						return
					}
				}
			}

			// The frame is cached and synchronized under one engine-timeline
			// PTS: the tick that shows it looks the cached frame up by it.
			mediaPTS := anchor.PTS(pts)
			srcMgr.IngestGPUFrame(sourceKey, frame, mediaPTS)

			// Notify the switcher so health monitoring and frame sync see this
			// source as alive. The ProcessingFrame has nil YUV — IngestYUV
			// returns early for nil buffers, and the GPU source manager already
			// has the frame from IngestGPUFrame above.
			a.sw.IngestRawVideo(sourceKey, &switcher.ProcessingFrame{
				Width:  w,
				Height: h,
				PTS:    mediaPTS,
				DTS:    mediaPTS,
			})

			// Fan out to GPU replay encode (NVENC from VRAM).
			if replayGPUCh != nil {
				select {
				case replayGPUCh <- srtReplayGPUJob{pts: pts, w: w, h: h}:
				default:
					// Drop if replay encode can't keep up.
				}
			}
		}

		slog.Info("SRT source: NVDEC zero-copy path enabled", "key", key)
	}

	src.OnRawAudio = func(sourceKey string, pcm []float32, pts int64, sampleRate, channels int) {
		pts = ptsLin.Linearize(pts, srt.StreamAudio)

		pcmCopy := make([]float32, len(pcm))
		copy(pcmCopy, pcm)
		select {
		case audioCh <- srtAudioJob{pcm: pcmCopy, pts: pts, sampleRate: sampleRate, channels: channels}:
		default:
			relayAudioDrops.Add(1)
		}
	}

	// Wire stopped callback: close channels (goroutines clean up their encoders).
	src.OnStopped = func(sourceKey string) {
		cs.SetDisconnected()
		slog.Info("SRT source disconnected", "key", sourceKey)

		// Close all async channels — goroutines drain and clean up.
		close(pipelineCh)
		close(relayVideoCh)
		close(audioCh)
		if replayVideoCh != nil {
			close(replayVideoCh)
		}
		if replayGPUCh != nil {
			close(replayGPUCh)
		}

		a.releaseSRTConnection(sourceKey, cfg.Mode)

		// Don't unregister from switcher -- leave as "no_signal" for reconnect.
		// The health monitor will mark it stale/no_signal automatically.
	}

	// Track in active sources map.
	a.srtSourcesMu.Lock()
	a.srtSources[key] = state
	a.srtSourcesMu.Unlock()

	// Register replay viewer on the source relay (same pattern as app_streams.go).
	// Skip if already wired directly via dual-encode mode above.
	if a.replayMgr != nil && replayViewer == nil {
		if err := a.replayMgr.AddSource(key); err != nil {
			slog.Warn("srt: could not add replay source", "key", key, "err", err)
		} else if v := a.replayMgr.Viewer(key); v != nil {
			relay.AddViewer(v)
		}
	}

	slog.Info("SRT source wired",
		"key", key,
		"mode", cfg.Mode,
		"streamID", cfg.StreamID,
		"remote", remoteAddr,
	)

	return src
}

// bindSRTSource makes key a source of the switcher and the mixer for a new
// connection and returns the anchor that places the connection's
// timestamps on the engine timeline. A key already registered, by an
// earlier connection or as a pre-registered caller, stays registered: a
// reconnect is not a removal. The source keeps its program and preview
// selection, its delay and position, its last frame, which the program
// holds until the new connection's first, and its GPU entry. What belongs
// to the connection starts afresh: the anchor, whose placement the new
// timestamps would otherwise inherit, and the audio the mixer holds.
func (a *App) bindSRTSource(key string) *clock.TrackingAnchor {
	a.srtSourcesMu.Lock()
	if t := a.srtGone[key]; t != nil {
		t.Stop()
		delete(a.srtGone, key)
	}
	a.srtSourcesMu.Unlock()
	if !a.sw.HasSource(key) {
		a.sw.RegisterSRTSource(key)
	}
	if !a.mixer.ResetChannelInput(key) {
		a.mixer.AddChannel(key)
		_ = a.mixer.SetAFV(key, true)
	}
	return &clock.TrackingAnchor{}
}

// srtSourceReleaseAfter is how long after its connection ended a source
// keeps its frames: as long as the program freezes on its last frame.
var srtSourceReleaseAfter = switcher.SourceLossTimeout

// releaseSRTConnection releases what a stopped SRT connection held: its
// entry in the active sources, its listener slot, its replay viewer and
// its GPU preview encoder, which has nothing more to encode and would
// otherwise still be open when a new connection opens its own. The source
// stays registered, shown as no signal, and keeps its frames: the program
// holds its last frame until a new connection replaces it, or for
// srtSourceReleaseAfter, after which it shows black and the frames are
// released. A source whose caller never comes back keeps only its
// registration, so sources come and go without holding frames or encode
// sessions.
func (a *App) releaseSRTConnection(sourceKey string, mode string) {
	// Before the entry goes: a new connection under the key finds it,
	// stops this one and waits for this release before it opens a preview
	// encoder of its own, which a stop after the delete could close.
	a.sw.StopSourcePreview(sourceKey)

	a.srtSourcesMu.Lock()
	delete(a.srtSources, sourceKey)
	if a.srtGone == nil {
		a.srtGone = make(map[string]*time.Timer)
	}
	if t := a.srtGone[sourceKey]; t != nil {
		t.Stop()
	}
	var gone *time.Timer
	gone = time.AfterFunc(srtSourceReleaseAfter, func() {
		// Held across the release, so a new connection binds the key
		// only after its frames are gone, never in between.
		a.srtSourcesMu.Lock()
		defer a.srtSourcesMu.Unlock()
		if a.srtGone[sourceKey] != gone {
			return
		}
		delete(a.srtGone, sourceKey)
		a.sw.ReleaseSourceFrames(sourceKey)
	})
	a.srtGone[sourceKey] = gone
	a.srtSourcesMu.Unlock()

	// Release listener slot so new connections can be accepted
	// when MaxSources is set.
	if mode == srt.ModeListener && a.srtListener != nil {
		a.srtListener.ReleaseSource()
	}

	// Remove replay viewer from the source relay.
	if a.replayMgr != nil {
		a.replayMgr.RemoveSource(sourceKey)
	}
}

// startSRTPipelineIngest starts a goroutine that reads decoded YUV frames from
// pipelineCh and delivers them to the switcher's IngestRawVideo path. Each frame
// is wrapped in a ProcessingFrame with pool-aware lifecycle.
func (a *App) startSRTPipelineIngest(key string, anchor *clock.TrackingAnchor, pipelineCh <-chan srtVideoJob, framePool *switcher.FramePool) {
	go func() {
		for job := range pipelineCh {
			job.pts = anchor.PTS(job.pts)
			a.sw.IngestRawVideo(key, newSRTPipelineFrame(job, framePool))
		}
	}()
}

// newSRTPipelineFrame wraps a decoded SRT video job in a managed, pool-backed
// ProcessingFrame for ingest into the switcher's frame-sync ring.
//
// SetRefs(1) is required, not optional: the ring retains the frame (and serves
// it as the freeze-repeat lastRawVideo) across ticks while sync-loop consumers
// take and release their own references. On an unmanaged frame Ref() is a
// no-op while ReleaseYUV frees immediately (see ProcessingFrame.Ref), so a
// consumer-side release would recycle the pool buffer out from under the ring
// (use-after-release) and the ring's own release would double-free it.
func newSRTPipelineFrame(job srtVideoJob, framePool *switcher.FramePool) *switcher.ProcessingFrame {
	pfr := &switcher.ProcessingFrame{
		Data:   job.yuv,
		Width:  job.w,
		Height: job.h,
		PTS:    job.pts,
		DTS:    job.pts,
		Codec:  "h264",
	}
	if framePool != nil {
		pfr.SetPool(framePool)
	}
	pfr.SetRefs(1)
	return pfr
}

// startSRTVideoRelayGPUDrain starts a goroutine that drains the relay video
// channel without encoding. Used when the GPU source manager handles preview
// encoding via IngestYUV — we just release pool buffers.
func (a *App) startSRTVideoRelayGPUDrain(rc *srtRelayContext) {
	go func() {
		for job := range rc.relayVideoCh {
			if rc.framePool != nil {
				rc.framePool.Release(job.yuv)
			}
		}
	}()
}

// startSRTVideoRelayPreview starts a goroutine that feeds decoded YUV frames
// to the CPU preview encoder for low-bitrate browser relay.
func (a *App) startSRTVideoRelayPreview(rc *srtRelayContext, previewEnc *preview.Encoder) {
	go func() {
		for job := range rc.relayVideoCh {
			release := func(buf []byte) {
				if rc.framePool != nil {
					rc.framePool.Release(buf)
				}
			}
			previewEnc.SendOwned(job.yuv, job.w, job.h, job.pts, release)
		}
		previewEnc.Stop()
	}()
}

// startSRTVideoRelayFullQuality starts a goroutine that encodes YUV frames at
// the configured relay resolution and broadcasts them to the browser relay.
// Used when neither GPU preview nor CPU preview proxy is active.
func (a *App) startSRTVideoRelayFullQuality(rc *srtRelayContext) {
	relayW, relayH := parseRelayResolution(a.cfg.RelayResolution)

	var (
		videoEncoder  transition.VideoEncoder
		groupID       atomic.Uint32
		videoInfoSent bool
		scaledYUV     []byte // persistent scale buffer
		encoderYUV    []byte
		lastVideoW    int
		lastVideoH    int
	)
	go func() {
		for job := range rc.relayVideoCh {
			if rc.relay == nil {
				continue
			}
			w, h, pts := job.w, job.h, job.pts

			// Determine encode dimensions: scale or use source.
			encW, encH := relayW, relayH
			if encW == 0 || encH == 0 {
				encW, encH = w, h // "source" mode: no scaling
			}

			// Resolution change → recreate encoder.
			if videoEncoder != nil && (w != lastVideoW || h != lastVideoH) {
				videoEncoder.Close()
				videoEncoder = nil
				rc.state.videoEncoder = nil
				videoInfoSent = false
			}

			// Lazy encoder creation (ultrafast/baseline for low CPU).
			if videoEncoder == nil {
				enc, err := codec.NewPreviewEncoder(encW, encH, a.cfg.RelayBitrate, rc.pf.FPSNum, rc.pf.FPSDen)
				if err != nil {
					slog.Error("srt: relay encoder init failed", "key", rc.key, "error", err)
					continue
				}
				videoEncoder = enc
				rc.state.videoEncoder = enc
				lastVideoW = w
				lastVideoH = h
			}

			// Scale if needed, then copy into encoder buffer.
			var frameYUV []byte
			if w == encW && h == encH {
				frameYUV = job.yuv
			} else {
				targetSize := switcher.ColorYUV420_8bit.FrameSize(encW, encH)
				if cap(scaledYUV) < targetSize {
					scaledYUV = make([]byte, targetSize)
				}
				scaledYUV = scaledYUV[:targetSize]
				transition.ScaleYUV420(job.yuv, w, h, scaledYUV, encW, encH)
				frameYUV = scaledYUV
			}

			needed := len(frameYUV)
			if cap(encoderYUV) < needed {
				encoderYUV = make([]byte, needed)
			}
			encoderYUV = encoderYUV[:needed]
			copy(encoderYUV, frameYUV)
			if rc.framePool != nil {
				rc.framePool.Release(job.yuv)
			}

			encoded, isKeyframe, err := videoEncoder.Encode(encoderYUV, pts, false)
			if err != nil || len(encoded) == 0 {
				continue
			}

			avc1 := codec.AnnexBToAVC1(encoded)
			if isKeyframe {
				groupID.Add(1)
			}

			frame := &media.VideoFrame{
				PTS: pts, DTS: pts, IsKeyframe: isKeyframe,
				WireData: avc1, Codec: "h264", GroupID: groupID.Load(),
			}

			if isKeyframe {
				for _, nalu := range codec.ExtractNALUs(avc1) {
					if len(nalu) == 0 {
						continue
					}
					switch nalu[0] & 0x1F {
					case 7:
						frame.SPS = nalu
					case 8:
						frame.PPS = nalu
					}
				}
				if !videoInfoSent && frame.SPS != nil && frame.PPS != nil {
					avcC := moq.BuildAVCDecoderConfig(frame.SPS, frame.PPS)
					if avcC != nil {
						rc.relay.SetVideoInfo(distribution.VideoInfo{
							Codec: codec.ParseSPSCodecString(frame.SPS),
							Width: encW, Height: encH, DecoderConfig: avcC,
						})
						slog.Info("SRT source: relay VideoInfo set", "key", rc.key, "w", encW, "h", encH)
						videoInfoSent = true
					}
				}
			}
			rc.relay.BroadcastVideo(frame)
		}
		if videoEncoder != nil {
			videoEncoder.Close()
			videoEncoder = nil
		}
	}()
}

// startSRTReplayGPUEncode starts a goroutine that reads GPU-resident NV12 frames
// from the source manager and NVENC-encodes them directly from VRAM for replay.
// Used when NVDEC is active (OnRawVideo never fires, so CPU replay starves).
func (a *App) startSRTReplayGPUEncode(rc *srtRelayContext, replayGPUCh <-chan srtReplayGPUJob) {
	srcMgr := rc.gpu.sourceManager

	go func() {
		var (
			gpuEnc         *gpu.GPUEncoder
			groupID        atomic.Uint32
			lastW, lastH   int
			replayNV12Tmp  *gpu.GPUFrame // temp NV12 frame for P210→NV12 convert
			replayNV12Pool *gpu.FramePool
		)
		defer func() {
			if replayNV12Tmp != nil {
				replayNV12Tmp.Release()
			}
			if replayNV12Pool != nil {
				replayNV12Pool.Close()
			}
			if gpuEnc != nil {
				gpuEnc.Close()
			}
		}()

		for job := range replayGPUCh {
			// Resolution change → recreate encoder and NV12 temp.
			if gpuEnc != nil && (job.w != lastW || job.h != lastH) {
				gpuEnc.Close()
				gpuEnc = nil
				if replayNV12Tmp != nil {
					replayNV12Tmp.Release()
					replayNV12Tmp = nil
				}
				if replayNV12Pool != nil {
					replayNV12Pool.Close()
					replayNV12Pool = nil
				}
			}

			if gpuEnc == nil {
				enc, err := gpu.NewGPUEncoder(rc.gpu.ctx, job.w, job.h, rc.pf.FPSNum, rc.pf.FPSDen, 6_000_000)
				if err != nil {
					slog.Error("srt: GPU replay encoder init failed", "key", rc.key, "error", err)
					continue
				}
				gpuEnc = enc
				lastW, lastH = job.w, job.h
			}

			// Load current GPU frame (atomic, lock-free read).
			gpuFrame := srcMgr.CurrentFrame(rc.key)
			if gpuFrame == nil {
				continue
			}

			// If source frame is P210, convert to NV12 before H.264 encode.
			// Replay always outputs H.264 for browser compatibility.
			encFrame := gpuFrame
			if gpuFrame.Format == gpu.GPUFormatP210 {
				if replayNV12Tmp == nil {
					tmp, tmpErr := gpu.NewFramePool(rc.gpu.ctx, job.w, job.h, 2)
					if tmpErr != nil {
						continue
					}
					replayNV12Pool = tmp
					f, fErr := tmp.Acquire()
					if fErr != nil {
						continue
					}
					replayNV12Tmp = f
				}
				if err := gpu.ScaleP210ToNV12On(rc.gpu.ctx, replayNV12Tmp, gpuFrame, nil); err != nil {
					continue
				}
				replayNV12Tmp.PTS = gpuFrame.PTS
				encFrame = replayNV12Tmp
			}

			// NVENC encode directly from VRAM.
			data, isIDR, err := gpuEnc.EncodeGPU(encFrame, false)
			if err != nil || len(data) == 0 {
				continue
			}

			avc1 := codec.AnnexBToAVC1(data)
			if isIDR {
				groupID.Add(1)
			}

			vf := &media.VideoFrame{
				PTS:        job.pts,
				DTS:        job.pts,
				IsKeyframe: isIDR,
				GroupID:    groupID.Load(),
				WireData:   avc1,
				Codec:      "h264",
			}

			// Extract SPS/PPS from IDR frames for decoder init.
			if isIDR {
				for _, nalu := range codec.ExtractNALUs(avc1) {
					if len(nalu) == 0 {
						continue
					}
					switch nalu[0] & 0x1F {
					case 7:
						vf.SPS = nalu
					case 8:
						vf.PPS = nalu
					}
				}
			}

			rc.replayViewer.SendVideo(vf)
		}
	}()
}

// startSRTReplayCPUEncode starts a goroutine that encodes YUV frames at full
// quality for the replay system. Used when preview proxy is active (so the relay
// gets low-bitrate preview) but replay needs full-quality H.264.
// Skipped when GPU replay encode is active (NVDEC path handles replay).
func (a *App) startSRTReplayCPUEncode(rc *srtRelayContext) {
	go func() {
		var (
			videoEncoder transition.VideoEncoder
			groupID      atomic.Uint32
			encoderYUV   []byte
			lastW, lastH int
		)
		replayEncFactory := encoderFactory()
		for job := range rc.replayVideoCh {
			w, h, pts := job.w, job.h, job.pts

			// Resolution change → recreate encoder.
			if videoEncoder != nil && (w != lastW || h != lastH) {
				videoEncoder.Close()
				videoEncoder = nil
			}

			if videoEncoder == nil {
				enc, err := replayEncFactory(w, h, 6_000_000, rc.pf.FPSNum, rc.pf.FPSDen)
				if err != nil {
					slog.Error("srt: replay encoder init failed", "key", rc.key, "error", err)
					if rc.framePool != nil {
						rc.framePool.Release(job.yuv)
					}
					continue
				}
				videoEncoder = enc
				lastW = w
				lastH = h
			}

			needed := len(job.yuv)
			if cap(encoderYUV) < needed {
				encoderYUV = make([]byte, needed)
			}
			encoderYUV = encoderYUV[:needed]
			copy(encoderYUV, job.yuv)
			if rc.framePool != nil {
				rc.framePool.Release(job.yuv)
			}

			encoded, isKeyframe, err := videoEncoder.Encode(encoderYUV, pts, false)
			if err != nil || len(encoded) == 0 {
				continue
			}

			avc1 := codec.AnnexBToAVC1(encoded)
			if isKeyframe {
				groupID.Add(1)
			}

			frame := &media.VideoFrame{
				PTS: pts, DTS: pts, IsKeyframe: isKeyframe,
				WireData: avc1, Codec: "h264", GroupID: groupID.Load(),
			}

			if isKeyframe {
				for _, nalu := range codec.ExtractNALUs(avc1) {
					if len(nalu) == 0 {
						continue
					}
					switch nalu[0] & 0x1F {
					case 7:
						frame.SPS = nalu
					case 8:
						frame.PPS = nalu
					}
				}
			}
			rc.replayViewer.SendVideo(frame)
		}
		if videoEncoder != nil {
			videoEncoder.Close()
		}
	}()
}

// ingestSRTAudio hands an SRT source's decoded audio to the mixer. The
// stream's own PTS, placed on the timeline by the tracking anchor its video
// shares, keep the audio continuous however the decoder bunches it; the
// anchor follows a sender clock that drifts.
func (a *App) ingestSRTAudio(key string, anchor *clock.TrackingAnchor, job srtAudioJob) {
	a.mixer.IngestPCM(key, job.pcm, anchor.PTS(job.pts), job.channels)
}

// startSRTAudioRelay starts a goroutine that delivers decoded PCM audio to the
// mixer and encodes it as AAC for the browser relay and replay system.
func (a *App) startSRTAudioRelay(rc *srtRelayContext) {
	const aacFrameSamples = 1024
	var (
		audioEncoder audio.Encoder
		audioBuf     []float32
		audioPTS     int64
	)
	go func() {
		for job := range rc.audioCh {
			a.ingestSRTAudio(rc.key, rc.anchor, job)

			// Encode for relay.
			if rc.relay == nil {
				continue
			}

			if audioEncoder == nil {
				enc, err := audio.NewFDKEncoder(job.sampleRate, job.channels)
				if err != nil {
					slog.Error("srt: audio encoder init failed", "key", rc.key, "error", err)
					continue
				}
				audioEncoder = enc
				rc.state.audioEncoder = enc
			}

			if len(audioBuf) == 0 {
				audioPTS = job.pts
			}
			audioBuf = append(audioBuf, job.pcm...)

			chunkSize := aacFrameSamples * job.channels
			for len(audioBuf) >= chunkSize {
				chunk := audioBuf[:chunkSize]
				encoded, err := audioEncoder.Encode(chunk)
				if err != nil {
					audioBuf = audioBuf[chunkSize:]
					continue
				}
				if len(encoded) > 0 {
					af := &media.AudioFrame{
						PTS: audioPTS, Data: encoded,
						SampleRate: job.sampleRate, Channels: job.channels,
					}
					rc.relay.BroadcastAudio(af)
					if rc.replayViewer != nil {
						rc.replayViewer.SendAudio(af)
					}
				}
				audioPTS += int64(aacFrameSamples) * 90000 / int64(job.sampleRate)
				audioBuf = audioBuf[chunkSize:]
			}
		}
		if audioEncoder != nil {
			_ = audioEncoder.Close()
			audioEncoder = nil
		}
	}()
}

// startSRT starts the SRT listener and restores persisted caller configs.
// Called from Run() after all subsystems are initialized. Stores the app
// context for use by source callbacks.
func (a *App) startSRT(ctx context.Context) {
	// Store context for SRT source callbacks (onSRTListenerSource, onSRTCallerSource).
	a.srtCtx = ctx

	if a.srtListener != nil {
		a.bgWG.Add(1)
		go func() {
			defer a.bgWG.Done()
			if err := a.srtListener.Run(ctx); err != nil {
				slog.Error("SRT listener exited with error", "error", err)
			}
		}()
		slog.Info("SRT listener started", "addr", a.cfg.SRTListen)
	}
	if a.srtCaller != nil {
		// Pre-register stored caller sources so they appear immediately
		// in the UI while connections are being established.
		for _, cfg := range a.srtStore.List() {
			if cfg.Mode != srt.ModeCaller {
				continue
			}
			a.sw.RegisterSRTSource(cfg.Key)
			a.mixer.AddChannel(cfg.Key)
			_ = a.mixer.SetAFV(cfg.Key, true)
			if cfg.Label != "" {
				_ = a.sw.SetLabel(ctx, cfg.Key, cfg.Label)
			}
			if cfg.Position > 0 {
				_ = a.sw.SetSourcePosition(cfg.Key, cfg.Position)
			}
			a.srtStats.Create(cfg.Key, srt.ModeCaller, cfg.StreamID, cfg.LatencyMs)
		}
		a.srtCaller.RestoreFromStore(ctx)
	}
}

// stopSRTSources stops all active SRT sources and cleans up their
// encoders, on shutdown and before a GPU rebuild. Each stop ends a
// connection, whose frames are released as for any other
// (releaseSRTConnection) unless cancelSRTReleases cancels that.
func (a *App) stopSRTSources() {
	a.srtSourcesMu.Lock()
	sources := make(map[string]*srtSourceState, len(a.srtSources))
	for k, v := range a.srtSources {
		sources[k] = v
	}
	a.srtSources = make(map[string]*srtSourceState)
	a.srtSourcesMu.Unlock()

	for key, state := range sources {
		slog.Info("stopping SRT source", "key", key)
		state.source.Stop() // triggers OnStopped → closes channels → goroutines clean up encoders
	}
}

// cancelSRTReleases stops the pending releases of gone sources' frames,
// for shutdown. A GPU rebuild stops the sources but keeps these, those
// its own stops armed among them: a listener whose caller never comes
// back would otherwise keep its frames until the process exits.
func (a *App) cancelSRTReleases() {
	a.srtSourcesMu.Lock()
	defer a.srtSourcesMu.Unlock()
	for key, t := range a.srtGone {
		t.Stop()
		delete(a.srtGone, key)
	}
}

// srtManagerAdapter implements control.SRTManager for the API layer.
// It wraps the SRT caller, store, and stats manager to provide the
// interface expected by the control API handlers.
type srtManagerAdapter struct {
	app    *App
	caller *srt.Caller
	stats  *srt.StatsManager
	store  *srt.Store
}

var _ control.SRTManager = (*srtManagerAdapter)(nil)

// CreatePull starts an outbound SRT pull connection and returns the source key.
// The source is pre-registered with the switcher/mixer so it appears immediately
// in the UI with "offline" status while the connection is being established.
func (m *srtManagerAdapter) CreatePull(ctx context.Context, address, streamID, label string, latencyMs int, inputBW int64, overheadBW int) (string, error) {
	key := srt.KeyPrefix + srt.ExtractStreamKey(streamID)

	// Pre-register source so it appears immediately in the UI.
	m.app.sw.RegisterSRTSource(key)
	m.app.mixer.AddChannel(key)
	_ = m.app.mixer.SetAFV(key, true)
	if label != "" {
		_ = m.app.sw.SetLabel(ctx, key, label)
	}

	// Pre-create stats entry so enrichState populates SRT metadata
	// (mode, streamID, latencyMs) in the state broadcast.
	m.stats.Create(key, srt.ModeCaller, streamID, latencyMs)

	// Use the app-level SRT context, NOT the HTTP request context.
	// r.Context() is cancelled when the HTTP response is sent, which
	// would immediately kill the connect loop goroutine before it can dial.
	err := m.caller.Pull(m.app.srtCtx, srt.SourceConfig{
		Key:        key,
		Mode:       srt.ModeCaller,
		Address:    address,
		StreamID:   streamID,
		Label:      label,
		LatencyMs:  latencyMs,
		InputBW:    inputBW,
		OverheadBW: overheadBW,
	})
	if err != nil {
		// Rollback pre-registration on failure.
		m.app.sw.UnregisterSource(key)
		m.app.mixer.RemoveChannel(key)
		m.stats.Remove(key)
		return "", err
	}
	return key, nil
}

// StopPull cancels an active pull and removes it from the store.
// Also unregisters the source from the switcher/mixer so it disappears from the UI.
func (m *srtManagerAdapter) StopPull(key string) error {
	if !strings.HasPrefix(key, srt.KeyPrefix) {
		return control.ErrNotSRTSource
	}
	_, ok := m.store.Get(key)
	if !ok {
		return control.ErrNotSRTSource
	}
	m.caller.Stop(key)
	m.stats.Remove(key)

	// Unregister from switcher/mixer so source disappears from UI.
	m.app.sw.UnregisterSource(key)
	m.app.mixer.RemoveChannel(key)
	return nil
}

// GetStats returns SRT connection stats for the given source key.
func (m *srtManagerAdapter) GetStats(key string) (interface{}, bool) {
	if !strings.HasPrefix(key, srt.KeyPrefix) {
		return nil, false
	}
	// Use read-only Get to avoid creating phantom entries for
	// sources that were never connected.
	cs, ok := m.stats.Get(key)
	if !ok {
		return nil, false
	}
	return cs.Snapshot(), true
}

// UpdateLatency changes the SRT latency for an active source.
func (m *srtManagerAdapter) UpdateLatency(key string, latencyMs int) error {
	if !strings.HasPrefix(key, srt.KeyPrefix) {
		return control.ErrNotSRTSource
	}
	cfg, ok := m.store.Get(key)
	if !ok {
		return control.ErrNotSRTSource
	}
	cfg.LatencyMs = latencyMs
	return m.store.Save(cfg)
}

// relayDropProvider exposes per-source relay path drop counters via debug snapshots.
type relayDropProvider struct {
	app *App
}

func (p *relayDropProvider) DebugSnapshot() map[string]any {
	p.app.srtSourcesMu.Lock()
	defer p.app.srtSourcesMu.Unlock()

	sources := make(map[string]any, len(p.app.srtSources))
	for key, state := range p.app.srtSources {
		entry := map[string]int64{}
		if state.relayVideoDrops != nil {
			entry["videoDrops"] = state.relayVideoDrops.Load()
		}
		if state.relayAudioDrops != nil {
			entry["audioDrops"] = state.relayAudioDrops.Load()
		}
		sources[key] = entry
	}
	return map[string]any{
		"sources": sources,
	}
}

// srtAnchorStats returns the placement statistics of each connected SRT
// source's TrackingAnchor. They belong to the connection: a reconnect
// starts them afresh.
func (a *App) srtAnchorStats() map[string]clock.TrackingStats {
	a.srtSourcesMu.Lock()
	defer a.srtSourcesMu.Unlock()
	out := make(map[string]clock.TrackingStats, len(a.srtSources))
	for key, state := range a.srtSources {
		if state.anchor != nil {
			out[key] = state.anchor.Stats()
		}
	}
	return out
}

// srtAnchorProvider exposes each connected SRT source's anchor offset
// change since its connection was placed, in ms, and the times it was
// raised, via debug snapshots.
type srtAnchorProvider struct {
	app *App
}

func (p srtAnchorProvider) DebugSnapshot() map[string]any {
	stats := p.app.srtAnchorStats()
	sources := make(map[string]any, len(stats))
	for key, s := range stats {
		sources[key] = map[string]any{
			"offset_ms": float64(s.Offset) / 90,
			"raises":    s.Raises,
			"moves":     s.Moves,
		}
	}
	return map[string]any{"sources": sources}
}

var (
	descSRTAnchorOffset = prometheus.NewDesc("switchframe_srt_anchor_offset_seconds",
		"How far a connected SRT source's TrackingAnchor offset moved since the connection was placed: up when its timestamps arrived later than before, down when a window forgot a lateness. Starts at 0 on every connection.",
		[]string{"source"}, nil)
	descSRTAnchorRaises = prometheus.NewDesc("switchframe_srt_anchor_offset_raises_total",
		"Timestamps of a connected SRT source that raised its TrackingAnchor offset, each moving its later frames that much later. Starts at 0 on every connection.",
		[]string{"source"}, nil)
)

// srtAnchorCollector exposes the SRT anchors' statistics at scrape time.
type srtAnchorCollector struct {
	app *App
}

// Describe implements prometheus.Collector.
func (c srtAnchorCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descSRTAnchorOffset
	ch <- descSRTAnchorRaises
}

// Collect implements prometheus.Collector.
func (c srtAnchorCollector) Collect(ch chan<- prometheus.Metric) {
	for key, s := range c.app.srtAnchorStats() {
		source := metrics.SourceLabel(key)
		ch <- prometheus.MustNewConstMetric(descSRTAnchorOffset, prometheus.GaugeValue, float64(s.Offset)/90000, source)
		ch <- prometheus.MustNewConstMetric(descSRTAnchorRaises, prometheus.CounterValue, float64(s.Raises), source)
	}
}
