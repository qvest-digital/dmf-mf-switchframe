package control

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/output"
)

// recordingStartRequest is the JSON body for the recording start endpoint.
type recordingStartRequest struct {
	OutputDir       string `json:"outputDir"`
	RotateAfterMins int    `json:"rotateAfterMins,omitempty"`
	MaxFileSizeMB   int    `json:"maxFileSizeMB,omitempty"`
}

// registerOutputRoutes registers output/recording/SRT-related API routes on the given mux.
func (a *API) registerOutputRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/recording/start", a.handleRecordingStart)
	mux.HandleFunc("POST /api/recording/stop", a.handleRecordingStop)
	mux.HandleFunc("GET /api/recording/status", a.handleRecordingStatus)
	mux.HandleFunc("POST /api/output/srt/start", a.handleSRTStart)
	mux.HandleFunc("POST /api/output/srt/stop", a.handleSRTStop)
	mux.HandleFunc("GET /api/output/srt/status", a.handleSRTStatus)
	mux.HandleFunc("GET /api/output/confidence", a.handleConfidence)
	mux.HandleFunc("GET /api/output/cbr", a.handleCBRStatus)
	mux.HandleFunc("POST /api/output/destinations", a.handleAddDestination)
	mux.HandleFunc("GET /api/output/destinations", a.handleListDestinations)
	mux.HandleFunc("GET /api/output/destinations/{id}", a.handleGetDestination)
	mux.HandleFunc("DELETE /api/output/destinations/{id}", a.handleRemoveDestination)
	mux.HandleFunc("POST /api/output/destinations/{id}/start", a.handleStartDestination)
	mux.HandleFunc("POST /api/output/destinations/{id}/stop", a.handleStopDestination)
}

// handleRecordingStart begins recording program output to a file.
func (a *API) handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	var req recordingStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.OutputDir == "" {
		req.OutputDir = filepath.Join(os.TempDir(), "switchframe-recordings")
	}

	// Reject path traversal attempts before cleaning. A raw path containing
	// ".." could resolve to a location outside the intended directory tree
	// (e.g. "/tmp/../../etc/sensitive" -> "/etc/sensitive").
	if strings.Contains(req.OutputDir, "..") {
		httperr.Write(w, http.StatusBadRequest, "outputDir must not contain path traversal (..)")
		return
	}

	outDir := filepath.Clean(req.OutputDir)
	if !filepath.IsAbs(outDir) {
		httperr.Write(w, http.StatusBadRequest, "outputDir must be an absolute path")
		return
	}

	config := output.RecorderConfig{
		Dir:         outDir,
		RotateAfter: time.Hour,
	}
	if req.RotateAfterMins > 0 {
		config.RotateAfter = time.Duration(req.RotateAfterMins) * time.Minute
	}
	if req.MaxFileSizeMB > 0 {
		config.MaxFileSize = int64(req.MaxFileSizeMB) * 1024 * 1024
	}

	if err := a.outputMgr.StartRecording(config); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	// Track the recording directory for clip import.
	a.recordingDir.Store(&outDir)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.outputMgr.RecordingStatus())
}

// handleRecordingStop stops the active recording.
func (a *API) handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	if err := a.outputMgr.StopRecording(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.outputMgr.RecordingStatus())
}

// handleRecordingStatus returns the current recording status.
func (a *API) handleRecordingStatus(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.outputMgr.RecordingStatus())
}

// handleSRTStart begins SRT output with the given configuration.
func (a *API) handleSRTStart(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	var config output.SRTConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if config.Mode != "caller" && config.Mode != "listener" {
		httperr.Write(w, http.StatusBadRequest, "mode must be 'caller' or 'listener'")
		return
	}
	if config.Port <= 0 {
		httperr.Write(w, http.StatusBadRequest, "port is required")
		return
	}
	if config.Mode == "caller" && config.Address == "" {
		httperr.Write(w, http.StatusBadRequest, "address is required for caller mode")
		return
	}
	if err := a.outputMgr.StartSRTOutput(config); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.outputMgr.SRTOutputStatus())
}

// handleSRTStop stops the active SRT output.
func (a *API) handleSRTStop(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	if err := a.outputMgr.StopSRTOutput(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.outputMgr.SRTOutputStatus())
}

// handleSRTStatus returns the current SRT output status.
func (a *API) handleSRTStatus(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.outputMgr.SRTOutputStatus())
}

// handleConfidence returns the latest JPEG confidence thumbnail from the
// program output. Returns 204 No Content if no thumbnail is available.
func (a *API) handleConfidence(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output not configured")
		return
	}
	jpg := a.outputMgr.ConfidenceThumbnail()
	if jpg == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(jpg)
}

// handleAddDestination creates a new output destination.
func (a *API) handleAddDestination(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	var config output.DestinationConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if config.Type != "srt-caller" && config.Type != "srt-listener" {
		httperr.Write(w, http.StatusBadRequest, "type must be 'srt-caller' or 'srt-listener'")
		return
	}
	if config.Port <= 0 {
		httperr.Write(w, http.StatusBadRequest, "port is required")
		return
	}
	if config.Type == "srt-caller" && config.Address == "" {
		httperr.Write(w, http.StatusBadRequest, "address is required for srt-caller")
		return
	}
	id, err := a.outputMgr.AddDestination(config)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	status, _ := a.outputMgr.GetDestination(id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(status)
}

// handleListDestinations returns all configured output destinations.
func (a *API) handleListDestinations(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	dests := a.outputMgr.ListDestinations()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dests)
}

// handleGetDestination returns a single destination by ID.
func (a *API) handleGetDestination(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	id := r.PathValue("id")
	status, err := a.outputMgr.GetDestination(id)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleRemoveDestination deletes a destination by ID.
func (a *API) handleRemoveDestination(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	id := r.PathValue("id")
	if err := a.outputMgr.RemoveDestination(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStartDestination starts a destination's adapter.
func (a *API) handleStartDestination(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	id := r.PathValue("id")
	if err := a.outputMgr.StartDestination(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	status, _ := a.outputMgr.GetDestination(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleCBRStatus returns the current CBR pacer status.
func (a *API) handleCBRStatus(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	status := a.outputMgr.CBRStatus()
	if status == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleStopDestination stops a destination's adapter.
func (a *API) handleStopDestination(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.outputMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "output manager not configured")
		return
	}
	id := r.PathValue("id")
	if err := a.outputMgr.StopDestination(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	status, _ := a.outputMgr.GetDestination(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}
