package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// ErrNotSRTSource is returned when an operation that requires an SRT source
// is attempted on a non-SRT source (demo, MXL, etc.).
var ErrNotSRTSource = errors.New("not an SRT source")

// maxSRTLatencyMs is the maximum allowed SRT latency in milliseconds.
const maxSRTLatencyMs = 10000

// labelRequest is the JSON body for the set-label command.
type labelRequest struct {
	Label string `json:"label"`
}

// createSourceRequest is the JSON body for creating an SRT pull source.
type createSourceRequest struct {
	Type      string `json:"type"`
	Mode      string `json:"mode"`
	Address   string `json:"address"`
	StreamID  string `json:"streamID"`
	Label     string `json:"label"`
	LatencyMs int    `json:"latencyMs"`
}

// updateSourceSRTRequest is the JSON body for updating SRT config.
type updateSourceSRTRequest struct {
	LatencyMs int `json:"latencyMs"`
}

// registerSourceRoutes registers source-related API routes on the given mux.
func (a *API) registerSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", a.handleSources)
	mux.HandleFunc("GET /api/sources/{key}", a.handleGetSource)
	mux.HandleFunc("POST /api/sources", a.handleCreateSource)
	mux.HandleFunc("POST /api/sources/{key}/label", a.handleSetLabel)
	mux.HandleFunc("POST /api/sources/{key}/delay", a.handleSetDelay)
	mux.HandleFunc("PUT /api/sources/{key}/position", a.handleSetPosition)
	mux.HandleFunc("DELETE /api/sources/{key}", a.handleDeleteSource)
	mux.HandleFunc("GET /api/sources/{key}/srt/stats", a.handleGetSourceSRTStats)
	mux.HandleFunc("PUT /api/sources/{key}/srt", a.handleUpdateSourceSRT)
}

// handleSetLabel sets a human-readable label on a source.
func (a *API) handleSetLabel(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	key := r.PathValue("key")
	var req labelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.switcher.SetLabel(r.Context(), key, req.Label); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// delayRequest is the JSON body for the set-delay command.
type delayRequest struct {
	DelayMs int `json:"delayMs"`
}

// handleSetDelay sets the input delay for a source (0-500ms).
func (a *API) handleSetDelay(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	key := r.PathValue("key")
	var req delayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.switcher.SetSourceDelay(key, req.DelayMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// positionRequest is the JSON body for the set-position command.
type positionRequest struct {
	Position int `json:"position"`
}

// handleSetPosition sets the display position for a source.
func (a *API) handleSetPosition(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	key := r.PathValue("key")
	var req positionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.switcher.SetSourcePosition(key, req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSources returns the map of registered sources and their info.
func (a *API) handleSources(w http.ResponseWriter, r *http.Request) {
	state := a.enrichedState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state.Sources)
}

// handleGetSource returns a single source's info including SRT stats if applicable.
// GET /api/sources/{key}
func (a *API) handleGetSource(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	state := a.enrichedState()
	info, ok := state.Sources[key]
	if !ok {
		httperr.Write(w, http.StatusNotFound, "source not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// handleCreateSource creates a new SRT pull source.
// POST /api/sources
// Body: {"type":"srt","mode":"caller","address":"...","streamID":"...","label":"...","latencyMs":200}
func (a *API) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	if a.srtMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "SRT not configured")
		return
	}

	a.setLastOperator(r)

	var req createSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	// Only SRT sources can be created via API.
	if !strings.EqualFold(req.Type, "srt") {
		httperr.Write(w, http.StatusBadRequest, "only type \"srt\" is supported")
		return
	}

	// Only caller (pull) mode is supported for API-created sources.
	if !strings.EqualFold(req.Mode, "caller") {
		httperr.Write(w, http.StatusBadRequest, "only mode \"caller\" is supported for source creation")
		return
	}

	if req.Address == "" {
		httperr.Write(w, http.StatusBadRequest, "address is required")
		return
	}

	if req.StreamID == "" {
		httperr.Write(w, http.StatusBadRequest, "streamID is required")
		return
	}

	key, err := a.srtMgr.CreatePull(r.Context(), req.Address, req.StreamID, req.Label, req.LatencyMs)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"key": key})
}

// handleDeleteSource removes an SRT pull source.
// DELETE /api/sources/{key}
func (a *API) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	if a.srtMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "SRT not configured")
		return
	}

	a.setLastOperator(r)

	key := r.PathValue("key")
	if err := a.srtMgr.StopPull(key); err != nil {
		if errors.Is(err, ErrNotSRTSource) {
			httperr.Write(w, http.StatusMethodNotAllowed, "only SRT pull sources can be deleted via API")
			return
		}
		httperr.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetSourceSRTStats returns SRT connection stats for a source.
// GET /api/sources/{key}/srt/stats
func (a *API) handleGetSourceSRTStats(w http.ResponseWriter, r *http.Request) {
	if a.srtMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "SRT not configured")
		return
	}

	key := r.PathValue("key")
	stats, ok := a.srtMgr.GetStats(key)
	if !ok {
		httperr.Write(w, http.StatusNotFound, "SRT source not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

// handleUpdateSourceSRT updates SRT configuration (latency) for a source.
// PUT /api/sources/{key}/srt
// Body: {"latencyMs": 200}
func (a *API) handleUpdateSourceSRT(w http.ResponseWriter, r *http.Request) {
	if a.srtMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "SRT not configured")
		return
	}

	a.setLastOperator(r)

	key := r.PathValue("key")

	var req updateSourceSRTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.LatencyMs < 0 || req.LatencyMs > maxSRTLatencyMs {
		httperr.Write(w, http.StatusBadRequest, "latencyMs must be 0-10000")
		return
	}

	if err := a.srtMgr.UpdateLatency(key, req.LatencyMs); err != nil {
		if errors.Is(err, ErrNotSRTSource) {
			httperr.Write(w, http.StatusNotFound, "SRT source not found")
			return
		}
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
