package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	videoEncoder transition.VideoEncoder
	audioEncoder audio.Encoder
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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("srt: get home directory: %w", err)
	}
	storePath := filepath.Join(homeDir, ".switchframe", "srt_sources.json")
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
		conn.Close()
		return
	}
	if err := src.Start(a.srtCtx); err != nil {
		slog.Error("srt: failed to start source", "key", cfg.Key, "error", err)
		conn.Close()
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
		conn.Close()
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
		conn.Close()
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
	a.srtSourcesMu.Lock()
	if oldState, ok := a.srtSources[key]; ok {
		slog.Info("SRT source reconnecting, cleaning up old source", "key", key)
		oldState.source.Stop()
		if oldState.videoEncoder != nil {
			oldState.videoEncoder.Close()
		}
		if oldState.audioEncoder != nil {
			_ = oldState.audioEncoder.Close()
		}
		delete(a.srtSources, key)
	}
	a.srtSourcesMu.Unlock()

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

	// Per-source encoder state, accessed only from callbacks on the decode goroutine.
	// Using closures to capture mutable state -- safe because srt.Source callbacks
	// are called from a single goroutine (the decode loop).
	var (
		videoEncoder  transition.VideoEncoder
		audioEncoder  audio.Encoder
		groupID       atomic.Uint32
		videoInfoSent bool
		encoderYUV    []byte // reusable buffer for encoder input (avoids aliasing)
	)

	pf := a.sw.PipelineFormat()

	// Wire video: decoded YUV -> ProcessingFrame -> IngestRawVideo + encode -> relay.
	// Same pattern as MXL encodeAndBroadcastVideo in mxl/source.go.
	src.OnRawVideo = func(sourceKey string, yuv []byte, w, h int, pts int64) {
		// 1. Deliver raw YUV to switcher pipeline.
		pfr := &switcher.ProcessingFrame{
			YUV:    yuv,
			Width:  w,
			Height: h,
			PTS:    pts,
			DTS:    pts,
			Codec:  "h264",
		}
		a.sw.IngestRawVideo(sourceKey, pfr)

		// 2. Encode YUV -> H.264 and broadcast to relay for browser viewing.
		if relay == nil {
			return
		}

		// Lazy encoder creation on first frame.
		if videoEncoder == nil {
			bitrate := 6_000_000
			enc, err := codec.NewVideoEncoder(w, h, bitrate, pf.FPSNum, pf.FPSDen)
			if err != nil {
				slog.Error("srt: failed to create video encoder", "key", sourceKey, "error", err)
				return
			}
			videoEncoder = enc
			state.videoEncoder = enc
		}

		// Copy YUV to avoid aliasing with the decoder's buffer.
		needed := len(yuv)
		if cap(encoderYUV) < needed {
			encoderYUV = make([]byte, needed)
		}
		encoderYUV = encoderYUV[:needed]
		copy(encoderYUV, yuv)

		encoded, isKeyframe, err := videoEncoder.Encode(encoderYUV, pts, false)
		if err != nil {
			slog.Error("srt: video encode failed", "key", sourceKey, "error", err)
			return
		}
		if len(encoded) == 0 {
			return // encoder warming up
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		if isKeyframe {
			groupID.Add(1)
		}

		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
			GroupID:    groupID.Load(),
		}

		// Extract SPS/PPS from keyframes for VideoInfo.
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

			// Set VideoInfo on first keyframe so browsers can init their decoder.
			if !videoInfoSent && frame.SPS != nil && frame.PPS != nil {
				videoInfoSent = true
				avcC := moq.BuildAVCDecoderConfig(frame.SPS, frame.PPS)
				if avcC != nil {
					relay.SetVideoInfo(distribution.VideoInfo{
						Codec:         codec.ParseSPSCodecString(frame.SPS),
						Width:         w,
						Height:        h,
						DecoderConfig: avcC,
					})
					slog.Info("SRT source: relay VideoInfo set", "key", sourceKey, "w", w, "h", h)
				}
			}
		}

		relay.BroadcastVideo(frame)
	}

	// Wire audio: decoded PCM -> IngestPCM -> mixer + encode -> relay.BroadcastAudio.
	// Same pattern as MXL encodeAndBroadcastAudio in mxl/source.go.
	src.OnRawAudio = func(sourceKey string, pcm []float32, pts int64, sampleRate, channels int) {
		// 1. Deliver raw PCM to mixer.
		a.mixer.IngestPCM(sourceKey, pcm, pts, channels)

		// 2. Encode PCM -> AAC and broadcast to relay for browser viewing.
		if relay == nil {
			return
		}

		// Lazy encoder creation on first audio frame.
		if audioEncoder == nil {
			enc, err := audio.NewFDKEncoder(sampleRate, channels)
			if err != nil {
				slog.Error("srt: failed to create audio encoder", "key", sourceKey, "error", err)
				return
			}
			audioEncoder = enc
			state.audioEncoder = enc
		}

		encoded, err := audioEncoder.Encode(pcm)
		if err != nil {
			slog.Error("srt: audio encode failed", "key", sourceKey, "error", err)
			return
		}
		if len(encoded) == 0 {
			return // encoder warming up
		}

		audioFrame := &media.AudioFrame{
			PTS:        pts,
			Data:       encoded,
			SampleRate: sampleRate,
			Channels:   channels,
		}
		relay.BroadcastAudio(audioFrame)
	}

	// Wire stopped callback: clean up encoders and remove from active sources.
	src.OnStopped = func(sourceKey string) {
		cs.SetDisconnected()
		slog.Info("SRT source disconnected", "key", sourceKey)

		// Clean up encoders.
		if videoEncoder != nil {
			videoEncoder.Close()
			videoEncoder = nil
		}
		if audioEncoder != nil {
			_ = audioEncoder.Close()
			audioEncoder = nil
		}

		// Remove from active sources map.
		a.srtSourcesMu.Lock()
		delete(a.srtSources, sourceKey)
		a.srtSourcesMu.Unlock()

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
		state.source.Stop()
		// Encoders are cleaned up by OnStopped callback,
		// but guard against cases where Stop() doesn't trigger it.
		if state.videoEncoder != nil {
			state.videoEncoder.Close()
		}
		if state.audioEncoder != nil {
			_ = state.audioEncoder.Close()
		}
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
	// Check store first (covers both listener and caller sources).
	if _, ok := m.store.Get(key); !ok {
		return nil, false
	}
	cs := m.stats.GetOrCreate(key)
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
