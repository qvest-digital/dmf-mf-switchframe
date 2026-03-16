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
}

// initSRT initializes the SRT listener, caller, store, and stats manager.
// Called after initSubsystems, before initAPI. No-op if --srt-listen is empty.
func (a *App) initSRT() error {
	if a.cfg.SRTListen == "" {
		return nil // SRT not configured
	}

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

	latency := time.Duration(a.cfg.SRTLatencyMs) * time.Millisecond

	// Create listener.
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

	// Create caller (outbound pulls with auto-reconnect).
	caller := srt.NewCaller(srt.CallerConfig{
		Store:          store,
		DefaultLatency: latency,
		OnSource:       a.onSRTCallerSource,
	})
	a.srtCaller = caller

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

	// Create source orchestrator.
	src := srt.NewSource(cfg, conn, cs, slog.Default())

	// Source state for tracking encoder lifecycle and cleanup.
	state := &srtSourceState{
		source: src,
		relay:  relay,
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

	// PTS linearizers (separate for video/audio — they're interleaved).
	type ptsLinearizer struct {
		lastInput int64
		offset    int64
		frameDur  int64
		inited    bool
	}
	const ptsJumpThreshold = 45000 // 0.5s in 90kHz ticks

	linearize := func(lin *ptsLinearizer, rawPTS int64) int64 {
		if !lin.inited {
			lin.lastInput = rawPTS
			lin.inited = true
			return rawPTS
		}
		delta := rawPTS - lin.lastInput
		if delta < 0 || delta > ptsJumpThreshold {
			if lin.frameDur <= 0 {
				lin.frameDur = 3750
			}
			lin.offset += lin.frameDur - delta
		} else if delta > 0 {
			lin.frameDur = delta
		}
		lin.lastInput = rawPTS
		return (rawPTS + lin.offset) & 0x1FFFFFFFF
	}
	var videoLinear, audioLinear ptsLinearizer

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
			a.sw.IngestRawVideo(key, pfr)
		}
	}()

	// --- Goroutine 2: Relay video encode ---
	var (
		videoEncoder  transition.VideoEncoder
		groupID       atomic.Uint32
		videoInfoSent bool
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

			// Resolution change → recreate encoder.
			if videoEncoder != nil && (w != lastVideoW || h != lastVideoH) {
				videoEncoder.Close()
				videoEncoder = nil
				state.videoEncoder = nil
				videoInfoSent = false
			}

			// Lazy encoder creation.
			if videoEncoder == nil {
				enc, err := codec.NewVideoEncoder(w, h, 6_000_000, pf.FPSNum, pf.FPSDen)
				if err != nil {
					slog.Error("srt: video encoder init failed", "key", key, "error", err)
					continue
				}
				videoEncoder = enc
				state.videoEncoder = enc
				lastVideoW = w
				lastVideoH = h
			}

			// Copy YUV for encoder (job.yuv may be retained by pipeline goroutine).
			needed := len(job.yuv)
			if cap(encoderYUV) < needed {
				encoderYUV = make([]byte, needed)
			}
			encoderYUV = encoderYUV[:needed]
			copy(encoderYUV, job.yuv)

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
							Width: w, Height: h, DecoderConfig: avcC,
						})
						slog.Info("SRT source: relay VideoInfo set", "key", key, "w", w, "h", h)
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
					relay.BroadcastAudio(&media.AudioFrame{
						PTS: audioPTS, Data: encoded,
						SampleRate: job.sampleRate, Channels: job.channels,
					})
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

	// --- Decode callbacks: ZERO blocking work ---
	// Only: linearize PTS + deep copy + non-blocking channel send.

	src.OnRawVideo = func(sourceKey string, yuv []byte, w, h int, pts int64) {
		pts = linearize(&videoLinear, pts)

		// Single deep copy shared by pipeline and relay goroutines.
		yuvCopy := make([]byte, len(yuv))
		copy(yuvCopy, yuv)

		// Pipeline ingest (non-blocking).
		select {
		case pipelineCh <- videoJob{yuv: yuvCopy, w: w, h: h, pts: pts}:
		default:
		}
		// Relay encode (non-blocking, separate copy needed since pipeline
		// goroutine may hold yuvCopy in frame sync ring buffer).
		encCopy := make([]byte, len(yuv))
		copy(encCopy, yuv)
		select {
		case relayVideoCh <- videoJob{yuv: encCopy, w: w, h: h, pts: pts}:
		default:
		}
	}

	src.OnRawAudio = func(sourceKey string, pcm []float32, pts int64, sampleRate, channels int) {
		pts = linearize(&audioLinear, pts)

		pcmCopy := make([]float32, len(pcm))
		copy(pcmCopy, pcm)
		select {
		case audioCh <- audioJob{pcm: pcmCopy, pts: pts, sampleRate: sampleRate, channels: channels}:
		default:
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

		// Remove from active sources map.
		a.srtSourcesMu.Lock()
		delete(a.srtSources, sourceKey)
		a.srtSourcesMu.Unlock()

		// Release listener slot so new connections can be accepted
		// when MaxSources is set.
		if cfg.Mode == srt.ModeListener && a.srtListener != nil {
			a.srtListener.ReleaseSource()
		}

		// Don't unregister from switcher -- leave as "no_signal" for reconnect.
		// The health monitor will mark it stale/no_signal automatically.
	}

	// Track in active sources map.
	a.srtSourcesMu.Lock()
	a.srtSources[key] = state
	a.srtSourcesMu.Unlock()

	// Register replay viewer if replay is active.
	if a.replayMgr != nil {
		if err := a.replayMgr.AddSource(key); err != nil {
			slog.Warn("srt: could not add replay source", "key", key, "err", err)
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
	caller *srt.Caller
	stats  *srt.StatsManager
	store  *srt.Store
}

var _ control.SRTManager = (*srtManagerAdapter)(nil)

// CreatePull starts an outbound SRT pull connection and returns the source key.
func (m *srtManagerAdapter) CreatePull(ctx context.Context, address, streamID, label string, latencyMs int) (string, error) {
	key := srt.KeyPrefix + srt.ExtractStreamKey(streamID)
	err := m.caller.Pull(ctx, srt.SourceConfig{
		Key:       key,
		Mode:      srt.ModeCaller,
		Address:   address,
		StreamID:  streamID,
		Label:     label,
		LatencyMs: latencyMs,
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// StopPull cancels an active pull and removes it from the store.
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
