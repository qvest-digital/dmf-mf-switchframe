package main

import (
	"errors"
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
	if dir := a.cfg.ClipLibraryDir; dir != "" {
		if err := store.AttachLibrary(dir, a.cfg.EngineLabel); err != nil {
			return err
		}
		res, err := store.ScanLibrary()
		if err != nil {
			return err
		}
		slog.Info("clip library attached", "path", dir,
			"added", res.Added, "removed", res.Removed, "skipped", res.Skipped)
		// Clips a restart caught before they reached the library, copied
		// in the background: they play from local storage meanwhile. The
		// store may still be completing the upload the restart cut short,
		// and refuses to replace it until then, so a few more tries follow.
		go func() {
			for try := 0; try < 6; try++ {
				if try > 0 {
					time.Sleep(10 * time.Second)
				}
				n, left := store.PublishLocal()
				if n > 0 {
					slog.Info("clips: copied into the clip library after a restart", "clips", n)
				}
				if left == 0 {
					return
				}
			}
		}()
	}

	// Use native 10-bit decoders for clips when the pipeline is professional mode,
	// preserving 10-bit quality through the clip decode path.
	pf := a.sw.PipelineFormat()
	is10bitPipeline := pf.Color.ColorFormat == switcher.ColorYUV422_10bit

	previewW, previewH := parsePreviewResolution(a.cfg.PreviewResolution)
	mgr := clip.NewManager(store, clip.ManagerConfig{
		DecoderFactoryForCodec: clipDecoderFactory(is10bitPipeline),
		OutputRate: func() (int, int) {
			pf := a.sw.PipelineFormat()
			return pf.FPSNum, pf.FPSDen
		},
		EncoderFactory: clipRelayEncoderFactory(previewW, previewH, a.cfg.PreviewBitrate),
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
			if gs := a.gpu(); idx >= 0 && idx < clip.MaxPlayers && a.cfg.PreviewProxy && gs != nil && gs.sourceManager != nil {
				pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
				pf := a.sw.PipelineFormat()
				relay := a.clipRelays[idx]
				gs.sourceManager.RegisterSource(key, pf.Width, pf.Height, &gpu.PreviewConfig{
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
			if gs := a.gpu(); gs != nil && gs.sourceManager != nil {
				gs.sourceManager.RemoveSource(key)
			}
		},
	)

	// Raw video output: clip player sends decoded YUV to switcher pipeline.
	// Clips may be any resolution; scale to pipeline format before ingest
	// (same pattern as MXL/SRT sources which scale before IngestRawVideo).
	var clipRawFrameCount [clip.MaxPlayers]int64
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

		// Copy (and scale, if the clip resolution differs from the pipeline
		// format) into a frame buffer acquired per frame. The ingested frame
		// is retained by the frame-sync ring — and as the freeze-repeat
		// lastRawVideo — across ticks, so it must not alias any buffer the
		// decode goroutine reuses (the decoder's scratch/hold buffers, or the
		// per-player persistent scale buffer this path previously used, which
		// produced torn frames whenever the next decode overwrote pixels
		// under the sync loop).
		pf := a.sw.PipelineFormat()
		pool := a.sw.GetFramePool()
		outW, outH := w, h
		var outYUV []byte
		var pooled bool
		if w != pf.Width || h != pf.Height {
			outYUV, pooled = scaleClipFrame(pool, yuv, w, h, pf.Width, pf.Height)
			outW, outH = pf.Width, pf.Height
		} else {
			outYUV, pooled = acquireClipFrameBuf(pool, len(yuv))
			copy(outYUV, yuv)
		}

		frame := &switcher.ProcessingFrame{
			Data:       outYUV,
			Width:      outW,
			Height:     outH,
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
		}
		if pooled {
			frame.SetPool(pool)
		}
		// Managed lifecycle: the ring owns this reference and sync-loop
		// consumers borrow their own (see ProcessingFrame.Ref invariant).
		frame.SetRefs(1)
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
		a.replayMgr.SetOnClipExported(func(source, tempFile, clipID string) {
			a.handleReplayClipExported(source, tempFile, clipID, store)
		})
		slog.Info("clips: replay export callback wired")
	}

	return nil
}

// scaleClipFrame scales a decoded clip frame to the target dimensions into a
// freshly acquired buffer and reports whether that buffer is pool-owned.
//
// Every call returns an independent buffer (no persistent scratch reuse):
// the resulting ProcessingFrame is retained by the frame-sync ring and as the
// freeze-repeat lastRawVideo across ticks, so scaling into a reused
// per-player buffer would let the decode goroutine mutate pixels under the
// sync loop, tearing frames whenever the clip resolution differs from the
// pipeline format. This matches the SRT path, which pool-acquires per frame.
func scaleClipFrame(pool *switcher.FramePool, yuv []byte, w, h, targetW, targetH int) ([]byte, bool) {
	targetSize := switcher.ColorYUV420_8bit.FrameSize(targetW, targetH)
	buf, pooled := acquireClipFrameBuf(pool, targetSize)
	transition.ScaleYUV420(yuv, w, h, buf, targetW, targetH)
	return buf, pooled
}

// acquireClipFrameBuf returns a buffer of exactly size bytes — from the frame
// pool when it fits, otherwise heap-allocated — and reports whether the buffer
// came from the pool (so the caller knows whether to SetPool on the frame).
func acquireClipFrameBuf(pool *switcher.FramePool, size int) ([]byte, bool) {
	if pool != nil && size <= pool.BufSize() {
		return pool.Acquire()[:size], true
	}
	return make([]byte, size), false
}

// clipPlayerIDFromKey extracts the player ID from a "clip:N" key.
func clipPlayerIDFromKey(key string) int {
	s := strings.TrimPrefix(key, "clip:")
	n, _ := strconv.Atoi(s)
	return n
}

// addReplayClip adds an exported replay to store under clipID, the ID the
// replay request named on every engine it was sent to, or under a fresh ID
// when clipID is "".
func addReplayClip(store *clip.Store, c *clip.Clip, clipID string) error {
	if clipID == "" {
		return store.Add(c)
	}
	return store.AddWithID(clipID, c)
}

// replayClipFilename names a replay's file in the clip store. A replay
// with a requested clip ID is named after it, as an upload is, so that the
// engines storing one replay under one ID hold it at one path: playlists
// name a clip by its path, and the UI sends one playlist to every engine.
func replayClipFilename(source, clipID string, now time.Time) string {
	if clipID != "" {
		return clipID + ".ts"
	}
	return fmt.Sprintf("replay_%s_%s.ts", source, now.Format("20060102_150405.000"))
}

// handleReplayClipExported processes a temp TS file exported from replay,
// validates it, creates an ephemeral clip entry under clipID ("" for a
// fresh ID), and moves it to the store.
func (a *App) handleReplayClipExported(source, tempFile, clipID string, store *clip.Store) {
	// Validate the temp TS file to extract metadata.
	result, err := clip.Validate(tempFile)
	if err != nil {
		slog.Warn("clips: replay export validation failed", "source", source, "error", err)
		_ = os.Remove(tempFile)
		return
	}

	filename := replayClipFilename(source, clipID, time.Now())
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

	if err := addReplayClip(store, c, clipID); err != nil {
		slog.Warn("clips: failed to add replay clip", "source", source, "clipID", clipID, "error", err)
		_ = os.Remove(destPath)
		return
	}

	slog.Info("clips: replay clip exported",
		"source", source,
		"clipID", c.ID,
		"duration_ms", c.DurationMs,
		"size", c.ByteSize,
	)

	if err := store.PublishSoon(c.ID); errors.Is(err, clip.ErrPublishPending) {
		slog.Info("clips: replay clip copied into the clip library once the other engine's copy ends", "clipID", c.ID)
	} else if err != nil {
		slog.Warn("clips: replay clip not copied into the clip library", "clipID", c.ID, "error", err)
	}

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

// clipDecoderFactory returns the decoder factory for clip players. The
// decoders are frame-threaded: one thread decodes a high-bitrate 1080p H.264
// clip below real time on a server core, and a player decoding slower than
// its frame rate plays the clip slowed down, audio included. The player
// gives the frames a threaded decoder holds back their slots when they come
// out.
func clipDecoderFactory(is10bitPipeline bool) func(codecName string) (clip.VideoDecoder, error) {
	return func(codecName string) (clip.VideoDecoder, error) {
		if is10bitPipeline {
			return codec.NewVideoDecoderForCodecNative10bit(codecName)
		}
		return codec.NewVideoDecoderForCodec(codecName)
	}
}

// clipRelayEncoderFactory returns the factory for the encoders that feed a
// clip player's browser relay. The relay only feeds the UI's tiles, so it
// is encoded at the preview size and bitrate, the settings the GPU preview
// proxy uses for the same relays; a clip smaller than that is not scaled
// up. Encoding every clip picture at full 1080p took a large share of a
// CPU engine with two players running.
func clipRelayEncoderFactory(pw, ph, bitrate int) func(w, h, fpsNum, fpsDen int) (clip.VideoEncoder, error) {
	return func(w, h, fpsNum, fpsDen int) (clip.VideoEncoder, error) {
		ew, eh := pw, ph
		if w <= pw || h <= ph {
			ew, eh = w, h
		}
		enc, err := codec.NewPreviewEncoder(ew, eh, bitrate, fpsNum, fpsDen)
		if err != nil {
			return nil, err
		}
		if ew == w && eh == h {
			return enc, nil
		}
		return &scaledEncoder{enc: enc, srcW: w, srcH: h, w: ew, h: eh, buf: make([]byte, ew*eh*3/2)}, nil
	}
}

// scaledEncoder scales each 8-bit 4:2:0 frame to its encoder's size first.
type scaledEncoder struct {
	enc        clip.VideoEncoder
	srcW, srcH int
	w, h       int
	buf        []byte
}

func (s *scaledEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	if len(yuv) != s.srcW*s.srcH*3/2 {
		return nil, false, fmt.Errorf("clip relay encoder: %d bytes is not a %dx%d 8-bit 4:2:0 frame", len(yuv), s.srcW, s.srcH)
	}
	transition.ScaleYUV420(yuv, s.srcW, s.srcH, s.buf, s.w, s.h)
	return s.enc.Encode(s.buf, pts, forceIDR)
}

func (s *scaledEncoder) Close() { s.enc.Close() }
