package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// initPlayout creates the playout Manager and wires lifecycle callbacks
// so playout channels register as switcher sources. Follows the initClips pattern.
func (a *App) initPlayout() error {
	// Create asset cache.
	cacheDir := filepath.Join(a.cfg.StateDir, "playout-cache")
	cache, err := playout.NewCache(playout.CacheConfig{
		Dir:      cacheDir,
		MaxBytes: 200 * 1024 * 1024 * 1024, // 200 GB default
	})
	if err != nil {
		return fmt.Errorf("playout cache: %w", err)
	}

	// Create asset resolver (no path restrictions for now).
	resolver := playout.NewAssetResolver(nil)

	// Create transcoder using pipeline format for target dimensions.
	pf := a.sw.PipelineFormat()
	transcoder := playout.NewTranscoder(playout.TranscoderConfig{
		TargetWidth:  pf.Width,
		TargetHeight: pf.Height,
		TargetFPS:    int(pf.FPS()),
	})

	// Create prefetcher.
	prefetcher := playout.NewPrefetcher(playout.PrefetcherConfig{
		Cache:          cache,
		Resolver:       resolver,
		Transcoder:     transcoder,
		LookaheadItems: 5,
	})
	prefetcher.Start(context.Background())

	// Create asset provider backed by cache.
	// demuxFn is nil until a real clip.DemuxFile + codec.NewVideoDecoder pipeline
	// is wired in a follow-up task. Channels with cached assets will use the
	// cache provider once a real demux function is configured.
	provider := playout.NewCacheAssetProvider(cache, nil)

	mgr := playout.NewManager(playout.ManagerConfig{
		AssetProvider: provider,
	})

	// PTS anchoring.
	mgr.SetPTSProvider(a.sw.LastBroadcastVideoPTS)

	// Channel lifecycle: register/unregister as switcher sources.
	mgr.OnChannelLifecycle(
		func(channelID int, key string) {
			a.sw.RegisterReplaySource(key)
			a.mixer.AddChannel(key)
			_ = a.mixer.SetAFV(key, true)
			a.mixer.OnProgramChange(a.sw.ProgramSource())
		},
		func(channelID int, key string) {
			a.sw.UnregisterSource(key)
			a.mixer.RemoveChannel(key)
		},
	)

	// Raw video output: playout sends decoded YUV to switcher pipeline.
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		pf := a.sw.PipelineFormat()
		outYUV, outW, outH := yuv, w, h
		if w != pf.Width || h != pf.Height {
			targetSize := pf.Width * pf.Height * 3 / 2
			scaled := make([]byte, targetSize)
			transition.ScaleYUV420(yuv, w, h, scaled, pf.Width, pf.Height)
			outYUV, outW, outH = scaled, pf.Width, pf.Height
		}

		frame := &switcher.ProcessingFrame{
			YUV:        outYUV,
			Width:      outW,
			Height:     outH,
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
		}
		a.sw.IngestReplayVideo(key, frame)
	})

	// State change broadcasts.
	mgr.SetOnStateChange(func() {
		a.clearLastOperator()
		a.broadcastState(nil)
	})

	// Wire secondary event handler.
	mgr.SetSecondaryHandler(&playoutSecondaryHandler{
		compositor:     a.compositor,
		mixer:          a.mixer,
		captionMgr:     a.captionMgr,
		scte35Injector: a.scte35Injector,
	})

	// Wire SCTE-35 injector to pod boards (spliceSCTE35Adapter satisfies both
	// podSCTE35Injector and spliceScheduleInjector).
	spliceAdapter := &spliceSCTE35Adapter{injector: a.scte35Injector}
	mgr.SetSCTE35Injector(spliceAdapter)

	// Create black detector and splice coordinator for frame-accurate break entry.
	blackDetector := playout.NewBlackDetector(playout.BlackDetectorConfig{})
	spliceCoord := playout.NewSpliceCoordinator(playout.SpliceCoordinatorConfig{
		Injector:      spliceAdapter,
		BlackDetector: blackDetector,
		PreRollMs:     5000,
		WindowMs:      500,
	})
	mgr.SetSpliceCoordinator(spliceCoord)

	a.playoutMgr = mgr
	a.playoutCache = cache
	a.playoutPrefetcher = prefetcher
	slog.Info("playout manager initialized", "cacheDir", cacheDir)
	return nil
}

// playoutChannelIDFromKey extracts the channel ID from a "playout:N" key.
func playoutChannelIDFromKey(key string) int {
	s := strings.TrimPrefix(key, "playout:")
	n, _ := strconv.Atoi(s)
	return n
}

// playoutSecondaryHandler implements playout.SecondaryEventHandler by
// delegating to real subsystems (graphics, audio, captions, SCTE-35).
// Nil subsystem pointers are safe — each method checks before calling.
type playoutSecondaryHandler struct {
	compositor     graphicsController
	mixer          audioController
	captionMgr     captionController
	scte35Injector scte35Cuer
}

// Narrow interfaces — depend only on methods we actually call.
type graphicsController interface {
	DeleteImage(id int) error
}

type audioController interface {
	SetLevel(sourceKey string, levelDB float64) error
	SetMuted(sourceKey string, muted bool) error
	SetAFV(sourceKey string, afv bool) error
}

type captionController interface {
	SetMode(mode caption.Mode)
	IngestText(text string)
}

type scte35Cuer interface {
	InjectCue(msg *scte35.CueMessage) (uint32, error)
}

// scte35Scheduler is the narrow interface for scheduled SCTE-35 injection.
// *scte35.Injector satisfies this via InjectCue + ScheduleCue.
type scte35Scheduler interface {
	InjectCue(msg *scte35.CueMessage) (uint32, error)
	ScheduleCue(msg *scte35.CueMessage, preRollMs int64) (uint32, error)
}

func (h *playoutSecondaryHandler) OnGraphicsOn(layer int, template string) error {
	// Graphics on with template: Phase 3 logs the event. Full template-based
	// image loading will be added when the graphics template registry exists.
	slog.Info("playout: secondary graphics_on", "layer", layer, "template", template)
	return nil
}

func (h *playoutSecondaryHandler) OnGraphicsOff(layer int) error {
	if h.compositor == nil {
		return nil
	}
	return h.compositor.DeleteImage(layer)
}

func (h *playoutSecondaryHandler) OnGraphicsUpdate(layer int, template string) error {
	slog.Info("playout: secondary graphics_update", "layer", layer, "template", template)
	return nil
}

func (h *playoutSecondaryHandler) OnAudioLevel(sourceKey string, levelDB float64) error {
	if h.mixer == nil {
		return nil
	}
	return h.mixer.SetLevel(sourceKey, levelDB)
}

func (h *playoutSecondaryHandler) OnAudioMute(sourceKey string, muted bool) error {
	if h.mixer == nil {
		return nil
	}
	return h.mixer.SetMuted(sourceKey, muted)
}

func (h *playoutSecondaryHandler) OnAudioAFV(sourceKey string, afv bool) error {
	if h.mixer == nil {
		return nil
	}
	return h.mixer.SetAFV(sourceKey, afv)
}

func (h *playoutSecondaryHandler) OnCaptionMode(mode string) error {
	if h.captionMgr == nil {
		return nil
	}
	modeMap := map[string]caption.Mode{
		"off":         caption.ModeOff,
		"passthrough": caption.ModePassThrough,
		"author":      caption.ModeAuthor,
		"auto":        caption.ModeAuto,
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("playout: unknown caption mode %q", mode)
	}
	h.captionMgr.SetMode(m)
	return nil
}

func (h *playoutSecondaryHandler) OnCaptionText(text string) error {
	if h.captionMgr == nil {
		return nil
	}
	h.captionMgr.IngestText(text)
	return nil
}

func (h *playoutSecondaryHandler) OnSCTE35Cue(data *playout.SCTE35EventData) error {
	if h.scte35Injector == nil || data == nil {
		return nil
	}
	msg := &scte35.CueMessage{
		CommandType: 0x05, // splice_insert
		IsOut:       true,
		AutoReturn:  data.AutoReturn,
		Source:      "playout",
	}
	if data.BreakDurationMs > 0 {
		d := time.Duration(data.BreakDurationMs) * time.Millisecond
		msg.BreakDuration = &d
	}
	_, err := h.scte35Injector.InjectCue(msg)
	return err
}

// spliceSCTE35Adapter wraps scte35.Injector for splice-aligned pod cue injection.
// It implements both playout.podSCTE35Injector (InjectPodCue) and
// playout.spliceScheduleInjector (InjectPodCue + SchedulePodCue).
type spliceSCTE35Adapter struct {
	injector scte35Scheduler
}

func (a *spliceSCTE35Adapter) InjectPodCue(isOut, autoReturn bool, durationMs int64) (uint32, error) {
	if a.injector == nil {
		return 0, nil
	}
	msg := buildPodCueMessage(isOut, autoReturn, durationMs)
	return a.injector.InjectCue(msg)
}

func (a *spliceSCTE35Adapter) SchedulePodCue(isOut, autoReturn bool, durationMs, preRollMs int64) (uint32, error) {
	if a.injector == nil {
		return 0, nil
	}
	msg := buildPodCueMessage(isOut, autoReturn, durationMs)
	return a.injector.ScheduleCue(msg, preRollMs)
}

// buildPodCueMessage is a shared helper for constructing the CueMessage.
func buildPodCueMessage(isOut, autoReturn bool, durationMs int64) *scte35.CueMessage {
	msg := &scte35.CueMessage{
		CommandType: 0x05, // splice_insert
		IsOut:       isOut,
		AutoReturn:  autoReturn,
		Source:      "playout-pod",
	}
	if durationMs > 0 {
		d := time.Duration(durationMs) * time.Millisecond
		msg.BreakDuration = &d
	}
	return msg
}
