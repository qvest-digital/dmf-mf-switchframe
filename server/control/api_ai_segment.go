package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
)

// reHexColor matches a 6-character hex color string (e.g. "FF0080").
var reHexColor = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// registerAISegmentRoutes registers AI segmentation API routes on the given mux.
// Routes are only registered when an AISegmentManager is configured.
func (a *API) registerAISegmentRoutes(mux *http.ServeMux) {
	if a.aiSegmentMgr == nil {
		return
	}
	mux.HandleFunc("PUT /api/sources/{source}/ai-segment", a.handleEnableAISegment)
	mux.HandleFunc("GET /api/sources/{source}/ai-segment", a.handleGetAISegment)
	mux.HandleFunc("DELETE /api/sources/{source}/ai-segment", a.handleDisableAISegment)
	mux.HandleFunc("GET /api/ai-segment/status", a.handleAISegmentStatus)
}

// aiSegmentRequest is the JSON body for PUT /api/sources/{source}/ai-segment.
type aiSegmentRequest struct {
	Sensitivity float32 `json:"sensitivity"` // 0.0-1.0
	EdgeSmooth  float32 `json:"edgeSmooth"`  // 0.0-1.0
	Background  string  `json:"background"`  // ""/"transparent"/"blur:N"/"color:RRGGBB"
}

// handleEnableAISegment configures (or reconfigures) AI segmentation for a source.
//
// PUT /api/sources/{source}/ai-segment
//
// Defaults: sensitivity=0.7, edgeSmooth=0.5, background="" (transparent).
func (a *API) handleEnableAISegment(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")

	var req aiSegmentRequest
	// Apply defaults before decoding so a partial body still has valid values.
	req.Sensitivity = 0.7
	req.EdgeSmooth = 0.5

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.Sensitivity < 0 || req.Sensitivity > 1 {
		httperr.Write(w, http.StatusBadRequest, "sensitivity must be 0.0-1.0")
		return
	}
	if req.EdgeSmooth < 0 || req.EdgeSmooth > 1 {
		httperr.Write(w, http.StatusBadRequest, "edgeSmooth must be 0.0-1.0")
		return
	}
	if err := validateAIBackground(req.Background); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.aiSegmentMgr.EnableAISegment(source, req.Sensitivity, req.EdgeSmooth, req.Background); err != nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, err)
		return
	}

	cfg := internal.AISegmentConfig{
		Enabled:     true,
		Sensitivity: req.Sensitivity,
		EdgeSmooth:  req.EdgeSmooth,
		Background:  req.Background,
	}

	// Trigger state broadcast so browsers see the updated AI segment config.
	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// handleGetAISegment returns the current AI segmentation config for a source.
//
// GET /api/sources/{source}/ai-segment
func (a *API) handleGetAISegment(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	cfg, ok := a.aiSegmentMgr.GetAISegmentConfig(source)
	if !ok {
		httperr.Write(w, http.StatusNotFound, "no ai-segment config for source")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// handleDisableAISegment stops AI segmentation for a source.
//
// DELETE /api/sources/{source}/ai-segment
func (a *API) handleDisableAISegment(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	a.aiSegmentMgr.DisableAISegment(source)

	// Trigger state broadcast so browsers see the updated AI segment config.
	a.broadcast()

	w.WriteHeader(http.StatusNoContent)
}

// aiSegmentStatusResponse is the JSON response for GET /api/ai-segment/status.
type aiSegmentStatusResponse struct {
	Available bool `json:"available"`
}

// handleAISegmentStatus returns whether AI segmentation is available on this server.
//
// GET /api/ai-segment/status
func (a *API) handleAISegmentStatus(w http.ResponseWriter, r *http.Request) {
	resp := aiSegmentStatusResponse{
		Available: a.aiSegmentMgr.IsAISegmentAvailable(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// validateAIBackground validates the background field value.
// Valid values: "" or "transparent", "color:RRGGBB".
// "blur:N" is recognized but not yet implemented (returns error).
func validateAIBackground(bg string) error {
	if bg == "" || bg == "transparent" {
		return nil
	}
	if len(bg) > 5 && bg[:5] == "blur:" {
		// Validate the format for forward compatibility, but reject until
		// the GPU Gaussian blur kernel is implemented.
		var n int
		if _, err := fmt.Sscanf(bg[5:], "%d", &n); err != nil || n < 1 || n > 50 {
			return fmt.Errorf("blur radius must be 1-50 (e.g. 'blur:10')")
		}
		return fmt.Errorf("blur mode not yet implemented; use 'transparent' or 'color:RRGGBB'")
	}
	if len(bg) == 12 && bg[:6] == "color:" {
		hex := bg[6:]
		if !reHexColor.MatchString(hex) {
			return fmt.Errorf("color must be hex RRGGBB (e.g. 'color:00FF00')")
		}
		return nil
	}
	return fmt.Errorf("background must be '', 'transparent', or 'color:RRGGBB'")
}
