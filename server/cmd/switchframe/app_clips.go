package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// initClips creates the clip store and manager, wires lifecycle callbacks,
// registers per-player MoQ relays for browser streaming, and performs
// ephemeral cleanup. Follows the pattern of initCaptions/replay.
func (a *App) initClips() error {
	clipDir := a.statePath("clips")
	store, err := clip.NewStore(clipDir, a.cfg.ClipStorageMax)
	if err != nil {
		return fmt.Errorf("create clip store: %w", err)
	}
	slog.Info("clip store initialized", "path", clipDir, "maxBytes", a.cfg.ClipStorageMax)

	// Use native 10-bit decoders for clips when the pipeline is professional mode,
	// preserving 10-bit quality through the clip decode path.
	pf := a.sw.PipelineFormat()
	is10bitPipeline := pf.Color.ColorFormat == switcher.ColorYUV422_10bit

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DecoderFactoryForCodec: func(codecName string) (clip.VideoDecoder, error) {
			// Single-threaded decoder eliminates frame-level multithreading
			// buffering delay — each Decode() produces output immediately
			// (only B-frame reordering delay remains, typically 1-2 frames).
			if is10bitPipeline {
				return codec.NewVideoDecoderForCodecNative10bitSingleThread(codecName)
			}
			return codec.NewVideoDecoderForCodecSingleThread(codecName)
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (clip.VideoEncoder, error) {
			bitrate := switcher.DefaultBitrateForResolution(w, h)
			return codec.NewVideoEncoder(w, h, bitrate, fpsNum, fpsDen)
		},
	})

	// PTS anchoring (same pattern as replay).
	mgr.SetPTSProvider(a.sw.LastBroadcastVideoPTS)

	// Register per-player MoQ relays so browsers can subscribe to clip sources.
	for i := 0; i < clip.MaxPlayers; i++ {
		key := fmt.Sprintf("clip:%d", i+1)
		a.clipRelays[i] = a.server.RegisterStream(key)
		slog.Info("clip relay registered", "key", key)
	}

	// Per-slot flag: true when GPU preview encoding handles relay, so the
	// full-quality SetVideoOutput callback should skip relay broadcast.
	var clipGPUPreview [clip.MaxPlayers]bool
	// Per-slot MoQ group ID counter for GPU preview frames.
	var clipPreviewGroupID [clip.MaxPlayers]atomic.Uint32

	// Player lifecycle: register/unregister virtual sources.
	mgr.OnPlayerLifecycle(
		// onStart: register clip player as a raw YUV switcher source + mixer channel.
		func(playerID int, key string) {
			a.sw.RegisterReplaySource(key)
			a.mixer.AddChannel(key)
			_ = a.mixer.SetAFV(key, true)
			// If program is already this key, re-trigger AFV activation
			// so the new channel becomes active.
			a.mixer.OnProgramChange(a.sw.ProgramSource())

			// Register clip source with GPU preview encoder when available.
			// This replaces the full-quality relay encode with 480p preview,
			// matching the SRT source preview proxy path.
			idx := playerID - 1
			if idx >= 0 && idx < clip.MaxPlayers && a.cfg.PreviewProxy && a.gpuState != nil && a.gpuState.sourceManager != nil {
				pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
				pf := a.sw.PipelineFormat()
				relay := a.clipRelays[idx]
				a.gpuState.sourceManager.RegisterSource(key, pf.Width, pf.Height, &gpu.PreviewConfig{
					Width:   pw,
					Height:  ph,
					Bitrate: a.cfg.PreviewBitrate,
					FPSNum:  pf.FPSNum,
					FPSDen:  pf.FPSDen,
					OnPreview: func(data []byte, isIDR bool, pts int64) {
						avc1 := codec.AnnexBToAVC1(data)
						if isIDR {
							clipPreviewGroupID[idx].Add(1)
						}
						frame := &media.VideoFrame{
							PTS: pts, DTS: pts, IsKeyframe: isIDR,
							WireData: avc1, Codec: "h264",
							GroupID: clipPreviewGroupID[idx].Load(),
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
				clipGPUPreview[idx] = true
				slog.Info("clip: GPU preview encoder registered", "key", key)
			}
		},
		// onStop: unregister clip player source.
		func(playerID int, key string) {
			a.sw.UnregisterSource(key)
			a.mixer.RemoveChannel(key)
			idx := playerID - 1
			if idx >= 0 && idx < clip.MaxPlayers {
				clipGPUPreview[idx] = false
				clipPreviewGroupID[idx].Store(0)
			}
			// Release GPU preview encoder and cached frames.
			if a.gpuState != nil && a.gpuState.sourceManager != nil {
				a.gpuState.sourceManager.RemoveSource(key)
			}
		},
	)

	// Raw video output: clip player sends decoded YUV to switcher pipeline.
	// Clips may be any resolution; scale to pipeline format before ingest
	// (same pattern as MXL/SRT sources which scale before IngestRawVideo).
	var clipRawFrameCount [clip.MaxPlayers]int64
	var clipScaleBuf []byte // persistent scale buffer, reused across frames
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		idx := clipPlayerIDFromKey(key) - 1
		if idx >= 0 && idx < clip.MaxPlayers {
			clipRawFrameCount[idx]++
			count := clipRawFrameCount[idx]
			if count <= 3 || count%100 == 0 {
				slog.Info("clip: RawVideoOutput (decoded YUV)",
					"key", key,
					"frame", count,
					"width", w,
					"height", h,
					"yuvLen", len(yuv),
					"pts", pts,
					"isKeyframe", isKeyframe,
				)
			}
		}

		// Scale to pipeline format if clip resolution differs.
		pf := a.sw.PipelineFormat()
		outYUV, outW, outH := yuv, w, h
		if w != pf.Width || h != pf.Height {
			targetSize := switcher.ColorYUV420_8bit.FrameSize(pf.Width, pf.Height)
			if cap(clipScaleBuf) < targetSize {
				clipScaleBuf = make([]byte, targetSize)
			}
			clipScaleBuf = clipScaleBuf[:targetSize]
			transition.ScaleYUV420(yuv, w, h, clipScaleBuf, pf.Width, pf.Height)
			outYUV, outW, outH = clipScaleBuf, pf.Width, pf.Height
		}

		frame := &switcher.ProcessingFrame{
			Data:       outYUV,
			Width:      outW,
			Height:     outH,
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
		}
		a.sw.IngestReplayVideo(key, frame)
	})

	// Video output: forward H.264 wire data from clip players to per-player
	// MoQ relays for browser playback. The player may re-encode (8-bit) or
	// pass through original wire data depending on EncoderFactory config.
	var clipVideoFrameCount [clip.MaxPlayers]int64
	var clipGroupID [clip.MaxPlayers]uint32 // MoQ group IDs (1-based; 0 is Prism sentinel)
	mgr.SetVideoOutput(func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
		idx := clipPlayerIDFromKey(key) - 1
		if idx < 0 || idx >= clip.MaxPlayers {
			return
		}
		// When GPU preview encoding is active for this slot, skip the
		// full-quality relay broadcast — the GPU source manager handles
		// preview encode via the IngestYUV → preview encoder path.
		if clipGPUPreview[idx] {
			return
		}
		relay := a.clipRelays[idx]
		if relay == nil {
			return
		}

		clipVideoFrameCount[idx]++
		count := clipVideoFrameCount[idx]

		// Track MoQ group boundaries: increment on each keyframe.
		// GroupID must start at 1 (Prism uses 0 as "not damaged" sentinel).
		if isKeyframe {
			clipGroupID[idx]++
		}
		groupID := clipGroupID[idx]
		if groupID == 0 {
			// First frame is not a keyframe — start at 1 anyway.
			clipGroupID[idx] = 1
			groupID = 1
		}

		// Set VideoInfo on relay on each keyframe with SPS/PPS so the
		// MoQ catalog stays current (clip resolution/codec may vary).
		if isKeyframe && sps != nil && pps != nil {
			avcC := a.buildAVCConfig(sps, pps)
			if avcC != nil {
				w, h := clip.ParseSPSDimensions(sps)
				slog.Info("clip: SetVideoInfo",
					"key", key,
					"width", w,
					"height", h,
					"codec", codec.ParseSPSCodecString(sps),
					"spsLen", len(sps),
					"ppsLen", len(pps),
					"avcCLen", len(avcC),
				)
				relay.SetVideoInfo(a.buildVideoInfo(sps, avcC, w, h))
			}
		}

		// Log first few frames and then every 100th for diagnostics.
		if count <= 3 || count%100 == 0 {
			slog.Info("clip: BroadcastVideo",
				"key", key,
				"frame", count,
				"pts", pts,
				"isKeyframe", isKeyframe,
				"wireDataLen", len(wireData),
				"hasSPS", sps != nil,
				"groupID", groupID,
			)
		}

		var codecStr string
		if sps != nil {
			codecStr = codec.ParseSPSCodecString(sps)
		}

		frame := &media.VideoFrame{
			PTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   wireData,
			Codec:      codecStr,
			SPS:        sps,
			PPS:        pps,
			GroupID:    groupID,
		}
		relay.BroadcastVideo(frame)
	})

	// Audio output: clip player sends audio frames to mixer AND relay.
	mgr.SetAudioOutput(func(key string, data []byte, pts int64, sampleRate, channels int) {
		frame := &media.AudioFrame{
			Data:       data,
			PTS:        pts,
			SampleRate: sampleRate,
			Channels:   channels,
		}
		a.mixer.IngestFrame(key, frame)

		// Broadcast audio to per-player relay for browser playback.
		idx := clipPlayerIDFromKey(key) - 1
		if idx >= 0 && idx < clip.MaxPlayers && a.clipRelays[idx] != nil {
			a.clipRelays[idx].BroadcastAudio(frame)
		}
	})

	// State change broadcasts.
	mgr.SetOnStateChange(func() {
		a.clearLastOperator()
		a.broadcastState(nil)
	})

	// Ephemeral cleanup on startup.
	if a.cfg.ClipEphemeralTTL > 0 {
		loaded := mgr.LoadedClipIDs()
		removed := store.CleanEphemeral(a.cfg.ClipEphemeralTTL, loaded)
		if removed > 0 {
			slog.Info("clips: cleaned ephemeral clips", "removed", removed)
		}
	}

	a.clipMgr = mgr
	a.clipStore = store

	// Wire replay → clip store: when a replay clip is exported, validate
	// and add it as an ephemeral clip entry.
	if a.replayMgr != nil {
		a.replayMgr.SetOnClipExported(func(source string, tempFile string) {
			a.handleReplayClipExported(source, tempFile, store)
		})
		slog.Info("clips: replay export callback wired")
	}

	return nil
}

// clipPlayerIDFromKey extracts the player ID from a "clip:N" key.
func clipPlayerIDFromKey(key string) int {
	s := strings.TrimPrefix(key, "clip:")
	n, _ := strconv.Atoi(s)
	return n
}

// handleReplayClipExported processes a temp TS file exported from replay,
// validates it, creates an ephemeral clip entry, and moves it to the store.
func (a *App) handleReplayClipExported(source string, tempFile string, store *clip.Store) {
	// Validate the temp TS file to extract metadata.
	result, err := clip.Validate(tempFile)
	if err != nil {
		slog.Warn("clips: replay export validation failed", "source", source, "error", err)
		_ = os.Remove(tempFile)
		return
	}

	// Generate a filename for the clip in the store directory.
	filename := fmt.Sprintf("replay_%s_%s.ts", source, time.Now().Format("20060102_150405.000"))
	destPath := filepath.Join(store.Dir(), filename)

	// Move the temp file into the clip store directory.
	if err := os.Rename(tempFile, destPath); err != nil {
		// Rename may fail across filesystem boundaries; fall back to copy.
		if copyErr := copyFile(tempFile, destPath); copyErr != nil {
			slog.Warn("clips: failed to move replay clip", "source", source, "error", copyErr)
			_ = os.Remove(tempFile)
			return
		}
		_ = os.Remove(tempFile)
	}

	// Get the file size.
	info, err := os.Stat(destPath)
	if err != nil {
		slog.Warn("clips: failed to stat replay clip", "path", destPath, "error", err)
		_ = os.Remove(destPath)
		return
	}

	c := &clip.Clip{
		Name:       fmt.Sprintf("%s replay", source),
		Filename:   filename,
		Source:     clip.SourceReplay,
		Codec:      result.Codec,
		AudioCodec: result.AudioCodec,
		Width:      result.Width,
		Height:     result.Height,
		FPSNum:     result.FPSNum,
		FPSDen:     result.FPSDen,
		DurationMs: result.DurationMs,
		SampleRate: result.SampleRate,
		Channels:   result.Channels,
		ByteSize:   info.Size(),
		Ephemeral:  true,
	}

	if err := store.Add(c); err != nil {
		slog.Warn("clips: failed to add replay clip", "source", source, "error", err)
		_ = os.Remove(destPath)
		return
	}

	slog.Info("clips: replay clip exported",
		"source", source,
		"clipID", c.ID,
		"duration_ms", c.DurationMs,
		"size", c.ByteSize,
	)

	// Broadcast state change so UI updates.
	a.clearLastOperator()
	a.broadcastState(nil)
}

// copyFile copies src to dst. Used as fallback when os.Rename fails
// (e.g., across filesystem boundaries).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
