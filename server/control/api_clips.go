package control

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// registerClipRoutes registers clip management and player routes on the given mux.
func (a *API) registerClipRoutes(mux *http.ServeMux) {
	if a.clipMgr == nil || a.clipStore == nil {
		return
	}
	// Register specific named routes before wildcard {id} routes to ensure
	// Go's ServeMux picks them correctly.
	mux.HandleFunc("GET /api/clips/players", a.handleClipPlayersList)
	mux.HandleFunc("GET /api/clips/recordings", a.handleClipRecordings)
	mux.HandleFunc("POST /api/clips/upload", a.handleClipUpload)
	mux.HandleFunc("POST /api/clips/from-recording", a.handleClipFromRecording)
	mux.HandleFunc("GET /api/clips", a.handleClipsList)
	mux.HandleFunc("GET /api/clips/{id}", a.handleClipGet)
	mux.HandleFunc("PUT /api/clips/{id}", a.handleClipUpdate)
	mux.HandleFunc("DELETE /api/clips/{id}", a.handleClipDelete)
	mux.HandleFunc("POST /api/clips/{id}/pin", a.handleClipPin)
	mux.HandleFunc("POST /api/clips/players/{n}/load", a.handleClipPlayerLoad)
	mux.HandleFunc("POST /api/clips/players/{n}/eject", a.handleClipPlayerEject)
	mux.HandleFunc("POST /api/clips/players/{n}/play", a.handleClipPlayerPlay)
	mux.HandleFunc("POST /api/clips/players/{n}/pause", a.handleClipPlayerPause)
	mux.HandleFunc("POST /api/clips/players/{n}/stop", a.handleClipPlayerStop)
	mux.HandleFunc("POST /api/clips/players/{n}/seek", a.handleClipPlayerSeek)
	mux.HandleFunc("POST /api/clips/players/{n}/speed", a.handleClipPlayerSpeed)
	mux.HandleFunc("POST /api/clips/players/{n}/loop", a.handleClipPlayerLoop)
}

// handleClipsList returns all clips from the store sorted by creation time.
func (a *API) handleClipsList(w http.ResponseWriter, _ *http.Request) {
	clips := a.clipStore.List()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(clips)
}

// handleClipGet returns metadata for a single clip by ID.
func (a *API) handleClipGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := a.clipStore.Get(id)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

// handleClipUpdate modifies mutable fields (name, loop) on a clip.
func (a *API) handleClipUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name *string `json:"name"`
		Loop *bool   `json:"loop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	err := a.clipStore.Update(id, func(c *clip.Clip) {
		if req.Name != nil {
			c.Name = *req.Name
		}
		if req.Loop != nil {
			c.Loop = *req.Loop
		}
	})
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	c, _ := a.clipStore.Get(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

// handleClipDelete removes a clip from the store and deletes its media file.
func (a *API) handleClipDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.clipStore.Delete(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleClipUpload accepts a multipart file upload of a media clip.
// The file is validated, metadata extracted, and stored in the clip library.
func (a *API) handleClipUpload(w http.ResponseWriter, r *http.Request) {
	// Limit to 2GB.
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)

	// Parse multipart form (32MB in memory, rest to disk).
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid multipart form or file too large")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "file field required")
		return
	}
	defer func() { _ = file.Close() }()

	// Validate extension.
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !clip.IsAcceptedExtension(ext) {
		httperr.Write(w, http.StatusBadRequest, "unsupported file type")
		return
	}

	// Write to temp file in clip dir.
	dir := a.clipStore.Dir()
	tmp, err := os.CreateTemp(dir, "upload-*"+ext)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "failed to create temp file")
		return
	}
	tmpPath := tmp.Name()

	n, err := io.Copy(tmp, file)
	_ = tmp.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		httperr.Write(w, http.StatusInternalServerError, "failed to write upload")
		return
	}

	// Transcode non-H.264 files to H.264+AAC MPEG-TS.
	alreadyTranscoded := false
	if clip.NeedsTranscode(tmpPath) {
		tsPath := tmpPath + ".transcoded.ts"
		encoderName, _ := codec.ProbeEncoders()
		slog.Info("clip: transcoding upload", "input", header.Filename, "encoder", encoderName)

		_, tcErr := codec.TranscodeFile(tmpPath, tsPath, encoderName, 0)
		_ = os.Remove(tmpPath) // clean up original
		if tcErr != nil {
			_ = os.Remove(tsPath)
			wrappedErr := fmt.Errorf("%w: %v", clip.ErrTranscodeFailed, tcErr)
			httperr.WriteErr(w, errorStatus(wrappedErr), wrappedErr)
			return
		}
		tmpPath = tsPath
		ext = ".ts"
		alreadyTranscoded = true
		if info, statErr := os.Stat(tmpPath); statErr == nil {
			n = info.Size()
		}
	}

	// Validate the (potentially transcoded) file.
	probe, err := clip.Validate(tmpPath)
	if err != nil && !alreadyTranscoded && clip.CanTranscodeFallback(ext) {
		// Validate failed on a native-extension file that ProbeFile thought
		// was H.264. The container may be a variant the Go demuxer can't
		// parse (fragmented MP4, unusual box layout, etc.). Fall back to
		// FFmpeg transcode which handles all MP4 variants.
		slog.Info("clip: validate failed, falling back to transcode",
			"input", header.Filename, "error", err)
		tsPath := tmpPath + ".transcoded.ts"
		encoderName, _ := codec.ProbeEncoders()
		_, tcErr := codec.TranscodeFile(tmpPath, tsPath, encoderName, 0)
		_ = os.Remove(tmpPath)
		if tcErr != nil {
			_ = os.Remove(tsPath)
			wrappedErr := fmt.Errorf("%w: %v", clip.ErrTranscodeFailed, tcErr)
			httperr.WriteErr(w, errorStatus(wrappedErr), wrappedErr)
			return
		}
		tmpPath = tsPath
		ext = ".ts"
		if info, statErr := os.Stat(tmpPath); statErr == nil {
			n = info.Size()
		}
		// Re-validate the transcoded file.
		probe, err = clip.Validate(tmpPath)
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	// Derive clip name from original filename (without extension).
	name := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	if name == "" {
		name = "untitled"
	}

	// Build clip metadata.
	c := &clip.Clip{
		Name:       name,
		Filename:   filepath.Base(tmpPath),
		Source:     clip.SourceUpload,
		Codec:      probe.Codec,
		AudioCodec: probe.AudioCodec,
		Width:      probe.Width,
		Height:     probe.Height,
		FPSNum:     probe.FPSNum,
		FPSDen:     probe.FPSDen,
		DurationMs: probe.DurationMs,
		SampleRate: probe.SampleRate,
		Channels:   probe.Channels,
		ByteSize:   n,
	}

	if err := a.clipStore.Add(c); err != nil {
		_ = os.Remove(tmpPath)
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	// Rename temp file to use the clip ID for stable reference.
	finalName := c.ID + ext
	finalPath := filepath.Join(dir, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		// Metadata is already in store; just log and keep temp name.
		_ = a.clipStore.Update(c.ID, func(cl *clip.Clip) {})
	} else {
		_ = a.clipStore.Update(c.ID, func(cl *clip.Clip) {
			cl.Filename = finalName
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	// Re-fetch to get the latest state (post-rename).
	updated, _ := a.clipStore.Get(c.ID)
	if updated != nil {
		_ = json.NewEncoder(w).Encode(updated)
	} else {
		_ = json.NewEncoder(w).Encode(c)
	}
}

// handleClipPin marks an ephemeral clip as permanent.
func (a *API) handleClipPin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.clipStore.Pin(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	c, _ := a.clipStore.Get(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

// RecordingFileInfo describes a recording file available for import.
type RecordingFileInfo struct {
	Filename string    `json:"filename"`
	Path     string    `json:"path"`
	ByteSize int64     `json:"byteSize"`
	ModTime  time.Time `json:"modTime"`
}

// handleClipRecordings lists available recording files for clip import.
// Returns .ts files in the recording directory, excluding the currently
// active recording file (if any).
func (a *API) handleClipRecordings(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	recDirPtr := a.recordingDir.Load()
	if recDirPtr == nil || *recDirPtr == "" {
		_ = json.NewEncoder(w).Encode([]RecordingFileInfo{})
		return
	}
	recDir := *recDirPtr

	entries, err := os.ReadDir(recDir)
	if err != nil {
		_ = json.NewEncoder(w).Encode([]RecordingFileInfo{})
		return
	}

	// Determine the active recording filename to exclude.
	var activeFilename string
	if a.outputMgr != nil {
		status := a.outputMgr.RecordingStatus()
		if status.Active {
			activeFilename = status.Filename
		}
	}

	var result []RecordingFileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".ts") {
			continue
		}
		if name == activeFilename {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, RecordingFileInfo{
			Filename: name,
			Path:     filepath.Join(recDir, name),
			ByteSize: info.Size(),
			ModTime:  info.ModTime(),
		})
	}

	if result == nil {
		result = []RecordingFileInfo{}
	}
	_ = json.NewEncoder(w).Encode(result)
}

// handleClipFromRecording imports a recording file as a clip.
// The recording file is copied (not moved) to the clip store directory,
// validated, and added to the clip store.
func (a *API) handleClipFromRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.Path == "" {
		httperr.Write(w, http.StatusBadRequest, "path required")
		return
	}

	recDirPtr := a.recordingDir.Load()
	if recDirPtr == nil || *recDirPtr == "" {
		httperr.Write(w, http.StatusBadRequest, "recording directory not configured")
		return
	}

	// Validate path is within the recording dir (prevent path traversal).
	cleanPath := filepath.Clean(req.Path)
	cleanRecDir := filepath.Clean(*recDirPtr)
	if !filepath.IsAbs(cleanPath) || !strings.HasPrefix(cleanPath, cleanRecDir+string(os.PathSeparator)) {
		httperr.Write(w, http.StatusBadRequest, "path must be within the recording directory")
		return
	}

	// Copy the file to the clips directory (don't move — recordings should stay).
	dir := a.clipStore.Dir()
	ext := filepath.Ext(cleanPath)
	tmp, err := os.CreateTemp(dir, "rec-import-*"+ext)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "failed to create temp file")
		return
	}
	tmpPath := tmp.Name()

	src, err := os.Open(cleanPath)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		httperr.Write(w, http.StatusInternalServerError, "failed to open recording file")
		return
	}

	n, err := io.Copy(tmp, src)
	_ = src.Close()
	_ = tmp.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		httperr.Write(w, http.StatusInternalServerError, "failed to copy recording file")
		return
	}

	// Validate the copied file.
	probe, err := clip.Validate(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	// Derive clip name from original filename (without extension).
	name := strings.TrimSuffix(filepath.Base(cleanPath), filepath.Ext(cleanPath))
	if name == "" {
		name = "recording"
	}

	// Build clip metadata.
	c := &clip.Clip{
		Name:       name,
		Filename:   filepath.Base(tmpPath),
		Source:     clip.SourceRecording,
		Codec:      probe.Codec,
		AudioCodec: probe.AudioCodec,
		Width:      probe.Width,
		Height:     probe.Height,
		FPSNum:     probe.FPSNum,
		FPSDen:     probe.FPSDen,
		DurationMs: probe.DurationMs,
		SampleRate: probe.SampleRate,
		Channels:   probe.Channels,
		ByteSize:   n,
		Ephemeral:  false,
	}

	if err := a.clipStore.Add(c); err != nil {
		_ = os.Remove(tmpPath)
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	// Rename temp file to use the clip ID for stable reference.
	finalName := c.ID + ext
	finalPath := filepath.Join(dir, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = a.clipStore.Update(c.ID, func(cl *clip.Clip) {})
	} else {
		_ = a.clipStore.Update(c.ID, func(cl *clip.Clip) {
			cl.Filename = finalName
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	// Re-fetch to get the latest state (post-rename).
	updated, _ := a.clipStore.Get(c.ID)
	if updated != nil {
		_ = json.NewEncoder(w).Encode(updated)
	} else {
		_ = json.NewEncoder(w).Encode(c)
	}
}

// handleClipPlayersList returns the state of all 4 clip player slots.
func (a *API) handleClipPlayersList(w http.ResponseWriter, _ *http.Request) {
	states := a.clipMgr.PlayerStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states)
}

// handleClipPlayerLoad loads a clip into a player slot.
func (a *API) handleClipPlayerLoad(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	var req struct {
		ClipID string `json:"clipId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.ClipID == "" {
		httperr.Write(w, http.StatusBadRequest, "clipId required")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.Load(n, req.ClipID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerEject ejects the clip from a player slot.
func (a *API) handleClipPlayerEject(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.Eject(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerPlay starts or resumes clip playback.
func (a *API) handleClipPlayerPlay(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	var req struct {
		Speed float64 `json:"speed"`
		Loop  bool    `json:"loop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Speed == 0 {
		req.Speed = 1.0
	}
	a.setLastOperator(r)
	if err := a.clipMgr.Play(n, req.Speed, req.Loop); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerPause pauses clip playback.
func (a *API) handleClipPlayerPause(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.Pause(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerStop stops clip playback but keeps the clip loaded.
func (a *API) handleClipPlayerStop(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.Stop(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerSpeed changes the playback speed mid-playback.
func (a *API) handleClipPlayerSpeed(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	var req struct {
		Speed float64 `json:"speed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.SetSpeed(n, req.Speed); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerLoop changes the loop setting mid-playback.
func (a *API) handleClipPlayerLoop(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	var req struct {
		Loop bool `json:"loop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.SetLoop(n, req.Loop); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleClipPlayerSeek seeks to a position (0.0 to 1.0) in the clip.
func (a *API) handleClipPlayerSeek(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid player id")
		return
	}
	var req struct {
		Position float64 `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	a.setLastOperator(r)
	if err := a.clipMgr.Seek(n, req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
