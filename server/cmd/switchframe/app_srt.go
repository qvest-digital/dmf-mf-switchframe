package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
	srtgo "github.com/zsiec/srtgo"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/gpu"
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
	a.debugCollector.Register("relay_drops", &relayDropProvider{app: a})

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
	src := a.wireSRTSource(cfg, conn)
	if src == nil {
		_ = conn.Close()
		return
	}
	if err := src.Start(a.srtCtx); err != nil {
		slog.Error("srt: failed to start source", "key", cfg.Key, "error", err)
		_ = conn.Close()
		src.Stop() // clean up any partially started goroutines
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
		src.Stop()
		closeOnce.Do(func() { close(doneCh) })
		return doneCh
	}

	return doneCh
}

// wireSRTSource creates an SRT Source and wires it into the switcher/mixer/relay.
// Returns the Source with callbacks configured but NOT started. The caller is
// responsible for calling src.Start() after any additional callback wrapping.
//
// Follows the same fan-out pattern as initMXL (app.go) and startMXLDemo (app_mxl_demo.go):
//   - Clean up existing source if reconnecting (same streamID)
//   - Register source with switcher (IngestRawVideo path, no relay viewer)
//   - Register audio channel with mixer (IngestPCM path)
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

	// Unregister old source from switcher (safe even if not registered).
	a.sw.UnregisterSource(key)
	a.mixer.RemoveChannel(key)

	// Register with switcher and mixer.
	a.sw.RegisterSRTSource(key)
	a.mixer.AddChannel(key)
	_ = a.mixer.SetAFV(key, true)

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

	// Pass hardware device context for NVDEC when CUDA GPU is active.
	if a.gpuState != nil && a.gpuState.sourceManager != nil {
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
	}

	// === FULLY ASYNC ARCHITECTURE ===
	//
	// The FFmpeg decode goroutine runs a single-threaded decode loop:
	// av_read_frame → decode → callback → av_read_frame → ...
	// If ANY callback blocks, the entire decode loop stalls — no video
	// OR audio frames are produced. This causes bursty frame delivery
	// with 100-1500ms gaps.
	//
	// Demo cameras don't have this problem because their audio and video
	// arrive on SEPARATE goroutines (sourceViewer via Prism relay).
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

	type videoJob struct {
		yuv []byte
		w   int
		h   int
		pts int64
	}
	type audioJob struct {
		pcm        []float32
		pts        int64
		sampleRate int
		channels   int
	}

	pipelineCh := make(chan videoJob, 4)   // → IngestRawVideo
	relayVideoCh := make(chan videoJob, 4) // → H.264 encode + relay
	audioCh := make(chan audioJob, 8)      // → IngestPCM + AAC encode + relay

	// Replay channel: when preview proxy is active and replay is configured,
	// a separate full-quality encode goroutine feeds the replay viewer directly.
	var replayVideoCh chan videoJob
	var replayViewer interface {
		SendVideo(frame *media.VideoFrame)
		SendAudio(frame *media.AudioFrame)
	}

	// Shared PTS linearizer for video and audio. Uses a single offset so
	// both streams stay aligned after PTS jumps (SRT source loop/reconnect).
	ptsLin := srt.NewPTSLinearizer()

	// --- Goroutine 1: Pipeline ingest (program path) ---
	go func() {
		for job := range pipelineCh {
			pfr := &switcher.ProcessingFrame{
				YUV:    job.yuv,
				Width:  job.w,
				Height: job.h,
				PTS:    job.pts,
				DTS:    job.pts,
				Codec:  "h264",
			}
			if framePool != nil {
				pfr.SetPool(framePool)
			}
			a.sw.IngestRawVideo(key, pfr)
		}
	}()

	// --- Preview proxy encoder (replaces full-quality relay encode) ---
	var previewEnc *preview.Encoder
	var gpuPreviewActive bool
	if a.cfg.PreviewProxy && a.gpuState != nil && a.gpuState.sourceManager != nil {
		// GPU preview encoding — source manager handles it in IngestYUV.
		// Register the source with a PreviewConfig so the GPU source manager
		// creates a per-source GPU preview encoder. The OnPreview callback
		// converts Annex B to AVC1 and broadcasts to the browser relay.
		pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
		var groupID atomic.Uint32
		var videoInfoSent bool
		a.gpuState.sourceManager.RegisterSource(key, pf.Width, pf.Height, &gpu.PreviewConfig{
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
		pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
		var err error
		previewEnc, err = preview.NewEncoder(preview.Config{
			SourceKey:     key,
			Width:         pw,
			Height:        ph,
			Bitrate:       a.cfg.PreviewBitrate,
			FPSNum:        pf.FPSNum,
			FPSDen:        pf.FPSDen,
			Relay:         relay,
			FrameInterval: a.cfg.PreviewFrameInterval,
		})
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
			replayVideoCh = make(chan videoJob, 4)
		}
	}

	// --- Goroutine 2: Relay video encode ---
	// When GPU preview is active, the GPU source manager handles preview
	// encoding via IngestYUV, so we just drain the relay channel to release
	// frame pool buffers. When CPU preview is enabled, the preview.Encoder
	// handles scaling and encoding. Otherwise, use full-quality encode.
	if gpuPreviewActive {
		go func() {
			for job := range relayVideoCh {
				if framePool != nil {
					framePool.Release(job.yuv)
				}
			}
		}()
	} else if previewEnc != nil {
		go func() {
			for job := range relayVideoCh {
				release := func(buf []byte) {
					if framePool != nil {
						framePool.Release(buf)
					}
				}
				previewEnc.SendOwned(job.yuv, job.w, job.h, job.pts, release)
			}
			previewEnc.Stop()
		}()
	}

	// --- Goroutine 2b: Replay full-quality encode (only when preview proxy + replay) ---
	if replayVideoCh != nil {
		go func() {
			var (
				videoEncoder transition.VideoEncoder
				groupID      atomic.Uint32
				encoderYUV   []byte
				lastW, lastH int
			)
			replayEncFactory := encoderFactory()
			for job := range replayVideoCh {
				w, h, pts := job.w, job.h, job.pts

				// Resolution change → recreate encoder.
				if videoEncoder != nil && (w != lastW || h != lastH) {
					videoEncoder.Close()
					videoEncoder = nil
				}

				if videoEncoder == nil {
					enc, err := replayEncFactory(w, h, 6_000_000, pf.FPSNum, pf.FPSDen)
					if err != nil {
						slog.Error("srt: replay encoder init failed", "key", key, "error", err)
						if framePool != nil {
							framePool.Release(job.yuv)
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
				if framePool != nil {
					framePool.Release(job.yuv)
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
				replayViewer.SendVideo(frame)
			}
			if videoEncoder != nil {
				videoEncoder.Close()
			}
		}()
	}

	if previewEnc == nil && !gpuPreviewActive {
		// Relay encode goroutine: ultrafast/baseline at configured resolution.
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
			for job := range relayVideoCh {
				if relay == nil {
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
					state.videoEncoder = nil
					videoInfoSent = false
				}

				// Lazy encoder creation (ultrafast/baseline for low CPU).
				if videoEncoder == nil {
					enc, err := codec.NewPreviewEncoder(encW, encH, a.cfg.RelayBitrate, pf.FPSNum, pf.FPSDen)
					if err != nil {
						slog.Error("srt: relay encoder init failed", "key", key, "error", err)
						continue
					}
					videoEncoder = enc
					state.videoEncoder = enc
					lastVideoW = w
					lastVideoH = h
				}

				// Scale if needed, then copy into encoder buffer.
				var frameYUV []byte
				if w == encW && h == encH {
					frameYUV = job.yuv
				} else {
					targetSize := encW * encH * 3 / 2
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
				if framePool != nil {
					framePool.Release(job.yuv)
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
							relay.SetVideoInfo(distribution.VideoInfo{
								Codec: codec.ParseSPSCodecString(frame.SPS),
								Width: encW, Height: encH, DecoderConfig: avcC,
							})
							slog.Info("SRT source: relay VideoInfo set", "key", key, "w", encW, "h", encH)
							videoInfoSent = true
						}
					}
				}
				relay.BroadcastVideo(frame)
			}
			if videoEncoder != nil {
				videoEncoder.Close()
				videoEncoder = nil
			}
		}()
	}

	// --- Goroutine 3: Audio (mixer + relay encode) ---
	const aacFrameSamples = 1024
	var (
		audioEncoder audio.Encoder
		audioBuf     []float32
		audioPTS     int64
	)
	go func() {
		for job := range audioCh {
			// Deliver to mixer.
			a.mixer.IngestPCM(key, job.pcm, job.pts, job.channels)

			// Encode for relay.
			if relay == nil {
				continue
			}

			if audioEncoder == nil {
				enc, err := audio.NewFDKEncoder(job.sampleRate, job.channels)
				if err != nil {
					slog.Error("srt: audio encoder init failed", "key", key, "error", err)
					continue
				}
				audioEncoder = enc
				state.audioEncoder = enc
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
					relay.BroadcastAudio(af)
					if replayViewer != nil {
						replayViewer.SendAudio(af)
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

	// --- Register preview encoder with debug collector ---
	if previewEnc != nil {
		a.debugCollector.Register("preview:"+key, previewEnc)
	}

	// --- Decode callbacks: ZERO blocking work ---
	// Only: linearize PTS + deep copy + non-blocking channel send.

	src.OnRawVideo = func(sourceKey string, yuv []byte, w, h int, pts int64) {
		pts = ptsLin.Linearize(pts, srt.StreamVideo)

		// Track source video PTS for A/V gap measurement.
		a.sourceLastVideoPTS.Store(pts)
		if a.sourceLastAudioPTS.Load() != 0 {
			a.sourceAVGapInited.Store(true)
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
		case pipelineCh <- videoJob{yuv: pipelineBuf, w: w, h: h, pts: pts}:
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
		case relayVideoCh <- videoJob{yuv: relayBuf, w: w, h: h, pts: pts}:
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
			case replayVideoCh <- videoJob{yuv: replayBuf, w: w, h: h, pts: pts}:
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
	if a.gpuState != nil && a.gpuState.sourceManager != nil && cfg.HWDeviceCtx != nil {
		srcMgr := a.gpuState.sourceManager
		pool := srcMgr.Pool()

		src.OnRawVideoGPU = func(sourceKey string, devPtr uintptr, pitch, w, h int, pts int64) {
			pts = ptsLin.Linearize(pts, srt.StreamVideo)

			// Track source video PTS for A/V gap measurement.
			a.sourceLastVideoPTS.Store(pts)
			if a.sourceLastAudioPTS.Load() != 0 {
				a.sourceAVGapInited.Store(true)
			}

			// Acquire pool frame for device-to-device copy.
			frame, err := pool.Acquire()
			if err != nil {
				return // pool exhausted, drop frame
			}

			// Copy NVDEC NV12 surface to pool frame (handles pitch mismatch).
			if err := gpu.CopyNV12FromDevice(frame, devPtr, pitch, w, h); err != nil {
				frame.Release()
				return
			}

			srcMgr.IngestGPUFrame(sourceKey, frame, pts)

			// Notify the switcher so health monitoring and frame sync see this
			// source as alive. The ProcessingFrame has nil YUV — IngestYUV
			// returns early for nil buffers, and the GPU source manager already
			// has the frame from IngestGPUFrame above.
			a.sw.IngestRawVideo(sourceKey, &switcher.ProcessingFrame{
				Width:  w,
				Height: h,
				PTS:    pts,
				DTS:    pts,
			})
		}

		slog.Info("SRT source: NVDEC zero-copy path enabled", "key", key)
	}

	src.OnRawAudio = func(sourceKey string, pcm []float32, pts int64, sampleRate, channels int) {
		pts = ptsLin.Linearize(pts, srt.StreamAudio)

		// Track source audio PTS for A/V gap measurement.
		a.sourceLastAudioPTS.Store(pts)
		if a.sourceLastVideoPTS.Load() != 0 {
			a.sourceAVGapInited.Store(true)
		}

		pcmCopy := make([]float32, len(pcm))
		copy(pcmCopy, pcm)
		select {
		case audioCh <- audioJob{pcm: pcmCopy, pts: pts, sampleRate: sampleRate, channels: channels}:
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

		// Remove from active sources map.
		a.srtSourcesMu.Lock()
		delete(a.srtSources, sourceKey)
		a.srtSourcesMu.Unlock()

		// Release listener slot so new connections can be accepted
		// when MaxSources is set.
		if cfg.Mode == srt.ModeListener && a.srtListener != nil {
			a.srtListener.ReleaseSource()
		}

		// Remove replay viewer from the source relay.
		if a.replayMgr != nil {
			a.replayMgr.RemoveSource(sourceKey)
		}

		// Remove from GPU source manager (releases GPU frames and preview encoder).
		if a.gpuState != nil && a.gpuState.sourceManager != nil {
			a.gpuState.sourceManager.RemoveSource(sourceKey)
		}

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

// stopSRTSources stops all active SRT sources and cleans up their encoders.
// Called during graceful shutdown.
func (a *App) stopSRTSources() {
	a.srtSourcesMu.Lock()
	sources := make(map[string]*srtSourceState, len(a.srtSources))
	for k, v := range a.srtSources {
		sources[k] = v
	}
	a.srtSources = make(map[string]*srtSourceState)
	a.srtSourcesMu.Unlock()

	for key, state := range sources {
		slog.Info("stopping SRT source on shutdown", "key", key)
		state.source.Stop() // triggers OnStopped → closes channels → goroutines clean up encoders
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
func (m *srtManagerAdapter) CreatePull(ctx context.Context, address, streamID, label string, latencyMs int) (string, error) {
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
		Key:       key,
		Mode:      srt.ModeCaller,
		Address:   address,
		StreamID:  streamID,
		Label:     label,
		LatencyMs: latencyMs,
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
