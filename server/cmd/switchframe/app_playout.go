package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/gpu"
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
		MaxBytes: int64(a.cfg.PlayoutCacheMaxGB) * 1024 * 1024 * 1024,
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

	// Create asset provider backed by cache. demuxFn uses FileFrameIterator
	// which demuxes and decodes cached media files to raw YUV+audio frames.
	provider := playout.NewCacheAssetProvider(cache, func(path string) (playout.FrameIterator, error) {
		return playout.NewFileFrameIterator(path)
	})

	mgr := playout.NewManager(playout.ManagerConfig{
		AssetProvider: provider,
	})

	// Set pipeline format on manager so channels get correct dimensions/FPS.
	mgr.SetPipelineFormat(pf.Width, pf.Height, pf.FPS())

	// PTS anchoring.
	mgr.SetPTSProvider(a.sw.LastBroadcastVideoPTS)

	// Register MoQ relays for playout channels (same pattern as clips).
	for i := 0; i < playout.MaxChannels; i++ {
		key := fmt.Sprintf("playout:%d", i+1)
		a.playoutRelays[i] = a.server.RegisterStream(key)
		slog.Info("playout relay registered", "key", key)
	}

	// Per-slot MoQ group ID counter for GPU preview frames.
	var playoutPreviewGroupID [playout.MaxChannels]atomic.Uint32

	// Channel lifecycle: register/unregister as switcher sources.
	mgr.OnChannelLifecycle(
		// onStart: register playout channel as a raw YUV switcher source + mixer channel.
		func(channelID int, key string) {
			a.sw.RegisterReplaySource(key)
			a.mixer.AddChannel(key)
			_ = a.mixer.SetAFV(key, true)
			a.mixer.OnProgramChange(a.sw.ProgramSource())

			// Register playout source with GPU preview encoder when available.
			// This replaces the full-quality relay encode with low-bitrate preview,
			// matching the clip/SRT source preview proxy path.
			idx := channelID - 1
			if idx >= 0 && idx < playout.MaxChannels && a.cfg.PreviewProxy && a.gpuState != nil && a.gpuState.sourceManager != nil {
				pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
				curPF := a.sw.PipelineFormat()
				relay := a.playoutRelays[idx]
				a.gpuState.sourceManager.RegisterSource(key, curPF.Width, curPF.Height, &gpu.PreviewConfig{
					Width:   pw,
					Height:  ph,
					Bitrate: a.cfg.PreviewBitrate,
					FPSNum:  curPF.FPSNum,
					FPSDen:  curPF.FPSDen,
					OnPreview: func(data []byte, isIDR bool, pts int64) {
						avc1 := codec.AnnexBToAVC1(data)
						if isIDR {
							playoutPreviewGroupID[idx].Add(1)
						}
						frame := &media.VideoFrame{
							PTS: pts, DTS: pts, IsKeyframe: isIDR,
							WireData: avc1, Codec: "h264",
							GroupID: playoutPreviewGroupID[idx].Load(),
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
							if frame.SPS != nil && frame.PPS != nil {
								avcC := a.buildAVCConfig(frame.SPS, frame.PPS)
								if avcC != nil {
									relay.SetVideoInfo(a.buildVideoInfo(frame.SPS, avcC, pw, ph))
								}
							}
						}
						relay.BroadcastVideo(frame)
					},
				})
				slog.Info("playout: GPU preview encoder registered", "key", key)
			}
		},
		// onStop: unregister playout channel source.
		func(channelID int, key string) {
			idx := channelID - 1
			if idx >= 0 && idx < playout.MaxChannels {
				playoutPreviewGroupID[idx].Store(0)
			}
			// Release GPU preview encoder and cached frames.
			if a.gpuState != nil && a.gpuState.sourceManager != nil {
				a.gpuState.sourceManager.RemoveSource(key)
			}
			a.sw.UnregisterSource(key)
			a.mixer.RemoveChannel(key)
		},
	)

	// GPU-native live source aliasing: zero-copy frame routing in VRAM.
	// When a playout channel encounters a "live" item, this aliases the
	// channel's GPU frame to the live source's GPU frame — no CPU download
	// or re-upload needed (saves ~3MB/frame round-trip at 30fps).
	mgr.SetGPUAlias(func(channelKey, liveSourceKey string) bool {
		if a.gpuState == nil || a.gpuState.sourceManager == nil {
			return false
		}
		return a.gpuState.sourceManager.AliasSource(channelKey, liveSourceKey)
	})

	// CPU fallback tap for live playlist items. Used when GPU aliasing is
	// unavailable (non-GPU builds or when the source has no GPU frame yet).
	mgr.SetGPUSourceTap(func(channelKey, sourceKey string) *playout.DecodedFrame {
		if a.gpuState == nil || a.gpuState.sourceManager == nil {
			return nil
		}
		yuv, w, h, pts := a.gpuState.sourceManager.ReadCachedFrame(sourceKey)
		if yuv == nil {
			return nil
		}
		return &playout.DecodedFrame{
			YUV: yuv, Width: w, Height: h, PTS: pts, IsKeyframe: true,
		}
	})

	// Raw video output: playout sends decoded YUV to switcher pipeline.
	// nil YUV = GPU-aliased signal frame (triggers RunFromCache, no CPU upload).
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		curPF := a.sw.PipelineFormat()
		outYUV, outW, outH := yuv, w, h
		if len(yuv) == 0 {
			// GPU-aliased frame: pass through with nil YUV. The switcher's
			// handleRawVideoFrame → IngestYUV no-ops on nil, then the GPU
			// pipeline uses RunFromCache to read the aliased VRAM frame.
			outW, outH = curPF.Width, curPF.Height
		} else if w != curPF.Width || h != curPF.Height {
			targetSize := curPF.Width * curPF.Height * 3 / 2
			scaled := make([]byte, targetSize)
			transition.ScaleYUV420(yuv, w, h, scaled, curPF.Width, curPF.Height)
			outYUV, outW, outH = scaled, curPF.Width, curPF.Height
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

	// Audio output: playout sends decoded audio to the mixer (same pattern as clips).
	mgr.SetAudioOutput(func(key string, data []byte, pts int64, sampleRate, channels int) {
		a.mixer.IngestFrame(key, &media.AudioFrame{
			Data:       data,
			PTS:        pts,
			SampleRate: sampleRate,
			Channels:   channels,
		})
	})

	// State change broadcasts.
	mgr.SetOnStateChange(func() {
		a.clearLastOperator()
		a.broadcastState(nil)
	})

	// Wire secondary event handler. The splice coordinator and PTS provider
	// are set below after the coordinator is created.
	secHandler := &playoutSecondaryHandler{
		compositor:     a.compositor,
		mixer:          a.mixer,
		captionMgr:     a.captionMgr,
		scte35Injector: a.scte35Injector,
	}
	mgr.SetSecondaryHandler(secHandler)

	// As-run logger: persistent event log for traffic reconciliation.
	// Must be wired before SetSpliceCoordinator, which propagates the
	// logger to the splice coordinator.
	asRunDir := filepath.Join(a.cfg.StateDir, "playout-asrun")
	asRunLogger, err := playout.NewAsRunLogger(playout.AsRunLoggerConfig{
		Dir:           asRunDir,
		RetentionDays: a.cfg.PlayoutAsRunRetDays,
	})
	if err != nil {
		return fmt.Errorf("playout as-run logger: %w", err)
	}
	mgr.SetAsRunLogger(asRunLogger)

	// Watchdog: detect stalled channels and auto-recover.
	watchdog := playout.NewWatchdog(playout.WatchdogConfig{
		OnStall: func(channelID int) {
			slog.Warn("playout: watchdog detected stall, attempting recovery",
				"channel", channelID)
			if err := mgr.RecoverChannel(channelID); err != nil {
				slog.Error("playout: watchdog recovery failed",
					"channel", channelID, "error", err)
			}
		},
	})
	watchdog.Start()
	mgr.SetWatchdog(watchdog)

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

	// Wire splice coordinator into the secondary handler so playlist
	// scte35_cue events route through PTS-based frame-accurate insertion
	// instead of wall-clock-based direct injection.
	secHandler.spliceCoordinator = spliceCoord
	secHandler.ptsProvider = a.sw.LastBroadcastVideoPTS

	a.playoutMgr = mgr
	a.playoutCache = cache
	a.playoutPrefetcher = prefetcher

	// Create playlist save directory for persistence.
	playlistDir := filepath.Join(a.cfg.StateDir, "playout-playlists")
	if err := os.MkdirAll(playlistDir, 0o755); err != nil {
		return fmt.Errorf("playout playlist dir: %w", err)
	}
	a.playoutPlaylistDir = playlistDir

	// Generate sample clips for template playlists.
	sampleDir := filepath.Join(cacheDir, "samples")
	a.playoutSamples = playout.GenerateSampleClips(sampleDir)

	slog.Info("playout manager initialized", "cacheDir", cacheDir, "asRunDir", asRunDir,
		"playlistDir", playlistDir, "sampleClips", len(a.playoutSamples))
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
	compositor        graphicsController
	mixer             audioController
	captionMgr        captionController
	scte35Injector    scte35Cuer
	spliceCoordinator *playout.SpliceCoordinator
	ptsProvider       func() int64
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
	if data == nil {
		return nil
	}

	var durationMs int64
	if data.BreakDurationMs != nil {
		durationMs = *data.BreakDurationMs
	}

	// Determine isOut from the data. If not explicitly set, infer from preset
	// and segmentation descriptors (break_start / segmentation_type 34 = cue-out).
	isOut := data.IsOut
	if !isOut {
		if data.Preset == "break_start" || data.Preset == "cue_out" {
			isOut = true
		}
		for _, d := range data.Descriptors {
			// Segmentation types 34 (break start), 48 (provider ad start),
			// 50 (distributor ad start) are all cue-out signals.
			if d.SegmentationType == 34 || d.SegmentationType == 48 || d.SegmentationType == 50 {
				isOut = true
				break
			}
		}
	}

	// Use the SpliceCoordinator for frame-accurate PTS-based insertion.
	// The coordinator collects candidate frames, runs black detection,
	// and schedules the cue at the optimal splice point with IDR alignment.
	if h.spliceCoordinator != nil && h.ptsProvider != nil {
		return h.spliceCoordinator.BeginSplice(
			"",                  // no pod ID for secondary events
			isOut,               //
			data.AutoReturn,     //
			durationMs,
			h.ptsProvider(),     // current program PTS
		)
	}

	// Fallback: direct injection (no splice coordinator available).
	if h.scte35Injector == nil {
		return nil
	}
	msg := &scte35.CueMessage{
		CommandType:     data.CommandType,
		IsOut:           isOut,
		AutoReturn:      data.AutoReturn,
		Source:          "playout",
		UniqueProgramID: data.UniqueProgramID,
		AvailNum:        data.AvailNum,
		AvailsExpected:  data.AvailsExpected,
		Tier:            data.Tier,
		PTSAdjustment:   uint64(data.PTSAdjustment),
	}
	if msg.CommandType == 0 {
		msg.CommandType = 0x05 // default to splice_insert
	}
	if data.EventID != nil {
		msg.EventID = *data.EventID
	}
	if durationMs > 0 {
		d := time.Duration(durationMs) * time.Millisecond
		msg.BreakDuration = &d
	}
	if data.SpliceTimePTS != nil {
		pts := *data.SpliceTimePTS
		msg.SpliceTimePTS = &pts
		msg.Timing = "scheduled"
	}
	// Map segmentation descriptors for time_signal commands.
	for _, sd := range data.Descriptors {
		desc := scte35.SegmentationDescriptor{
			SegmentationType:                 sd.SegmentationType,
			SegEventID:                       sd.SegEventID,
			SegmentationEventCancelIndicator: sd.Cancel,
			UPIDType:                         sd.UPIDType,
			UPID:                             []byte(sd.UPID),
		}
		if sd.DurationMs != nil {
			ticks := uint64(*sd.DurationMs) * 90 // ms to 90kHz
			desc.DurationTicks = &ticks
		}
		desc.DeliveryRestrictions = &scte35.DeliveryRestrictions{
			WebDeliveryAllowed: sd.WebDeliveryAllowed,
			NoRegionalBlackout: sd.NoRegionalBlackout,
			ArchiveAllowed:     sd.ArchiveAllowed,
			DeviceRestrictions: sd.DeviceRestrictions,
		}
		msg.Descriptors = append(msg.Descriptors, desc)
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
