package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	srtgo "github.com/zsiec/srtgo"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
)

// initSRT initializes the SRT listener, caller, store, and stats manager.
// Called after initSubsystems, before initAPI. No-op if --srt-listen is empty.
func (a *App) initSRT() error {
	if a.cfg.SRTListen == "" {
		return nil // SRT not configured
	}

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
// by the listener. Fire-and-forget — the Source manages its own lifecycle.
func (a *App) onSRTListenerSource(cfg srt.SourceConfig, conn *srtgo.Conn) {
	src := a.wireSRTSource(cfg, conn)
	if src == nil {
		return
	}
	if err := src.Start(context.Background()); err != nil {
		slog.Error("srt: failed to start source", "key", cfg.Key, "error", err)
	}
}

// onSRTCallerSource is the callback for a successful outbound SRT pull
// connection. Returns a done channel that closes when the source stops,
// triggering reconnection in the Caller's connect loop.
func (a *App) onSRTCallerSource(cfg srt.SourceConfig, conn *srtgo.Conn) <-chan struct{} {
	doneCh := make(chan struct{})
	src := a.wireSRTSource(cfg, conn)
	if src == nil {
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
		close(doneCh)
	}

	if err := src.Start(context.Background()); err != nil {
		slog.Error("srt: failed to start source", "key", cfg.Key, "error", err)
		close(doneCh)
		return doneCh
	}

	return doneCh
}

// wireSRTSource creates an SRT Source and wires it into the switcher/mixer/relay.
// Returns the Source with callbacks configured but NOT started. The caller is
// responsible for calling src.Start() after any additional callback wrapping.
//
// Follows the same fan-out pattern as initMXL (app.go) and startMXLDemo (app_mxl_demo.go):
//   - Register source with switcher (IngestRawVideo path, no relay viewer)
//   - Register audio channel with mixer (IngestPCM path)
//   - Create browser relay for MoQ subscribers
//   - Wire OnRawVideo → ProcessingFrame → IngestRawVideo
//   - Wire OnRawAudio → IngestPCM
//   - Wire OnStopped → stats update
func (a *App) wireSRTSource(cfg srt.SourceConfig, conn *srtgo.Conn) *srt.Source {
	key := cfg.Key

	// Register with switcher and mixer.
	a.sw.RegisterSRTSource(key)
	a.mixer.AddChannel(key)
	_ = a.mixer.SetAFV(key, true)

	// Create browser relay for MoQ viewing.
	_ = a.server.RegisterStream(key)

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

	// Wire video: decoded YUV → ProcessingFrame → IngestRawVideo.
	// Same pattern as MXL OnRawVideo in app.go and app_mxl_demo.go.
	src.OnRawVideo = func(sourceKey string, yuv []byte, w, h int, pts int64) {
		pf := &switcher.ProcessingFrame{
			YUV:    yuv,
			Width:  w,
			Height: h,
			PTS:    pts,
			DTS:    pts,
			Codec:  "h264",
		}
		a.sw.IngestRawVideo(sourceKey, pf)
	}

	// Wire audio: decoded PCM → IngestPCM → mixer.
	// Same pattern as MXL OnRawAudio in app.go and app_mxl_demo.go.
	src.OnRawAudio = func(sourceKey string, pcm []float32, pts int64, sampleRate, channels int) {
		a.mixer.IngestPCM(sourceKey, pcm, pts, channels)
	}

	// Wire stopped callback: update stats.
	src.OnStopped = func(sourceKey string) {
		cs.SetDisconnected()
		slog.Info("SRT source disconnected", "key", sourceKey)
	}

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
// Called from Run() after all subsystems are initialized.
func (a *App) startSRT(ctx context.Context) {
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
