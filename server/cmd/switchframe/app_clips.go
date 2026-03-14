package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/switcher"
)

// initClips creates the clip store and manager, wires lifecycle callbacks,
// registers per-player MoQ relays for browser streaming, and performs
// ephemeral cleanup. Follows the pattern of initCaptions/replay.
func (a *App) initClips() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}

	clipDir := filepath.Join(homeDir, ".switchframe", "clips")
	store, err := clip.NewStore(clipDir, a.cfg.ClipStorageMax)
	if err != nil {
		return fmt.Errorf("create clip store: %w", err)
	}
	slog.Info("clip store initialized", "path", clipDir, "maxBytes", a.cfg.ClipStorageMax)

	mgr := clip.NewManager(store, clip.ManagerConfig{
		DecoderFactory: func() (clip.VideoDecoder, error) {
			return codec.NewVideoDecoder()
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
		},
		// onStop: unregister clip player source + close encoder.
		func(playerID int, key string) {
			a.sw.UnregisterSource(key)
			a.mixer.RemoveChannel(key)
			a.closeClipEncoder(playerID)
		},
	)

	// Raw video output: clip player sends decoded YUV to switcher pipeline
	// AND encodes to H.264 for browser relay (lazy encoder init).
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		// Send raw YUV to switcher pipeline.
		pf := &switcher.ProcessingFrame{
			YUV:        yuv,
			Width:      w,
			Height:     h,
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: true,
		}
		a.sw.IngestReplayVideo(key, pf)

		// Encode to H.264 and broadcast to per-player relay.
		a.clipEncodeAndBroadcast(key, yuv, w, h, pts)
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

// clipEncodeAndBroadcast encodes raw YUV to H.264 and broadcasts to the
// per-player MoQ relay. Creates the encoder lazily on the first frame.
// Mirrors the replay player's encode-and-broadcast pattern.
func (a *App) clipEncodeAndBroadcast(key string, yuv []byte, w, h int, pts int64) {
	idx := clipPlayerIDFromKey(key) - 1
	if idx < 0 || idx >= clip.MaxPlayers {
		return
	}

	relay := a.clipRelays[idx]
	if relay == nil {
		return
	}

	a.clipEncMu.Lock()
	enc := a.clipEncoders[idx]

	// Lazy-init encoder on first frame.
	if enc == nil {
		bitrate := clipEstimateBitrate(w, h)
		var err error
		enc, err = codec.NewVideoEncoder(w, h, bitrate, 30, 1)
		if err != nil {
			a.clipEncMu.Unlock()
			slog.Error("clips: encoder creation failed", "key", key, "err", err)
			return
		}
		a.clipEncoders[idx] = enc
		a.clipGroupIDs[idx] = 0
		a.clipInfoSent[idx] = false
		slog.Info("clips: encoder created for browser relay", "key", key, "w", w, "h", h)
	}

	groupID := a.clipGroupIDs[idx]
	infoSent := a.clipInfoSent[idx]
	a.clipEncMu.Unlock()

	// Encode YUV to H.264 (outside lock — encoder is single-writer per player).
	encoded, isKeyframe, err := enc.Encode(yuv, pts, false)
	if err != nil {
		slog.Error("clips: encode failed", "key", key, "err", err)
		return
	}
	if encoded == nil {
		return // Encoder buffering (EAGAIN).
	}

	// Convert Annex B to AVC1 for relay.
	avc1 := codec.AnnexBToAVC1(encoded)
	if len(avc1) == 0 {
		avc1 = encoded
	}

	// Extract SPS/PPS from keyframes for MoQ catalog.
	var spsNALU, ppsNALU []byte
	var codecStr string
	if isKeyframe {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				spsNALU = nalu
				codecStr = codec.ParseSPSCodecString(nalu)
			case 8:
				ppsNALU = nalu
			}
		}

		// Set VideoInfo on relay once so browsers can discover the track.
		if !infoSent && spsNALU != nil && ppsNALU != nil {
			avcC := a.buildAVCConfig(spsNALU, ppsNALU)
			if avcC != nil {
				relay.SetVideoInfo(a.buildVideoInfo(spsNALU, avcC, w, h))
				slog.Info("clips: set relay VideoInfo", "key", key, "w", w, "h", h)
			}
			a.clipEncMu.Lock()
			a.clipInfoSent[idx] = true
			a.clipEncMu.Unlock()
		}

		// Start first MoQ group on first keyframe.
		if groupID == 0 {
			groupID = 1
			a.clipEncMu.Lock()
			a.clipGroupIDs[idx] = groupID
			a.clipEncMu.Unlock()
		}
	}

	frame := &media.VideoFrame{
		PTS:        pts,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      codecStr,
		GroupID:    groupID,
		SPS:        spsNALU,
		PPS:        ppsNALU,
	}
	relay.BroadcastVideo(frame)
}

// closeClipEncoder closes and removes the H.264 encoder for a clip player.
func (a *App) closeClipEncoder(playerID int) {
	idx := playerID - 1
	if idx < 0 || idx >= clip.MaxPlayers {
		return
	}

	a.clipEncMu.Lock()
	enc := a.clipEncoders[idx]
	a.clipEncoders[idx] = nil
	a.clipGroupIDs[idx] = 0
	a.clipInfoSent[idx] = false
	a.clipEncMu.Unlock()

	if enc != nil {
		enc.Close()
		slog.Info("clips: encoder closed", "player", playerID)
	}
}

// clipPlayerIDFromKey extracts the player ID from a "clip:N" key.
func clipPlayerIDFromKey(key string) int {
	s := strings.TrimPrefix(key, "clip:")
	n, _ := strconv.Atoi(s)
	return n
}

// clipEstimateBitrate returns a reasonable bitrate for browser relay encoding.
func clipEstimateBitrate(w, h int) int {
	pixels := w * h
	switch {
	case pixels >= 1920*1080:
		return 8_000_000
	case pixels >= 1280*720:
		return 4_000_000
	default:
		return 2_000_000
	}
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
