package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/switcher"
)

// initClips creates the clip store and manager, wires lifecycle callbacks,
// and performs ephemeral cleanup. Follows the pattern of initCaptions/replay.
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
		// onStop: unregister clip player source.
		func(playerID int, key string) {
			a.sw.UnregisterSource(key)
			a.mixer.RemoveChannel(key)
		},
	)

	// Raw video output: clip player sends decoded YUV to switcher pipeline.
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		pf := &switcher.ProcessingFrame{
			YUV:        yuv,
			Width:      w,
			Height:     h,
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: true,
		}
		a.sw.IngestReplayVideo(key, pf)
	})

	// Audio output: clip player sends audio frames to mixer.
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
