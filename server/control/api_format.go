package control

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/switcher"
)

// formatInfo describes the current pipeline format.
type formatInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPSNum int    `json:"fpsNum"`
	FPSDen int    `json:"fpsDen"`
	Name   string `json:"name"`
}

// formatResponse is the JSON response for GET /api/format.
type formatResponse struct {
	Format  formatInfo `json:"format"`
	Presets []string   `json:"presets"`
}

// formatRequest is the JSON body for PUT /api/format.
type formatRequest struct {
	Format string `json:"format,omitempty"` // preset name like "1080p25"
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	FPSNum int    `json:"fpsNum,omitempty"`
	FPSDen int    `json:"fpsDen,omitempty"`
}

// handleGetFormat returns the current pipeline format and available presets.
func (a *API) handleGetFormat(w http.ResponseWriter, r *http.Request) {
	f := a.switcher.PipelineFormat()

	presets := make([]string, 0, len(switcher.FormatPresets))
	for name := range switcher.FormatPresets {
		presets = append(presets, name)
	}
	sort.Strings(presets)

	resp := formatResponse{
		Format: formatInfo{
			Width:  f.Width,
			Height: f.Height,
			FPSNum: f.FPSNum,
			FPSDen: f.FPSDen,
			Name:   f.Name,
		},
		Presets: presets,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSetFormat changes the pipeline format using a preset name or custom dimensions.
func (a *API) handleSetFormat(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req formatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	var f switcher.PipelineFormat

	switch {
	case req.Format != "":
		// Preset name provided.
		preset, ok := switcher.FormatPresets[req.Format]
		if !ok {
			httperr.Write(w, http.StatusBadRequest, "unknown format preset: "+req.Format)
			return
		}
		f = preset

	case req.Width > 0 || req.Height > 0 || req.FPSNum > 0 || req.FPSDen > 0:
		// Custom dimensions provided.
		if req.Width < 320 || req.Width > 7680 {
			httperr.Write(w, http.StatusBadRequest, "width must be 320-7680")
			return
		}
		if req.Height < 180 || req.Height > 4320 {
			httperr.Write(w, http.StatusBadRequest, "height must be 180-4320")
			return
		}
		if req.Width%2 != 0 {
			httperr.Write(w, http.StatusBadRequest, "width must be even")
			return
		}
		if req.Height%2 != 0 {
			httperr.Write(w, http.StatusBadRequest, "height must be even")
			return
		}
		if req.FPSNum <= 0 {
			httperr.Write(w, http.StatusBadRequest, "fpsNum must be positive")
			return
		}
		if req.FPSDen <= 0 {
			httperr.Write(w, http.StatusBadRequest, "fpsDen must be positive")
			return
		}
		f = switcher.PipelineFormat{
			Width:  req.Width,
			Height: req.Height,
			FPSNum: req.FPSNum,
			FPSDen: req.FPSDen,
		}

	default:
		httperr.Write(w, http.StatusBadRequest, "provide format preset or width/height/fpsNum/fpsDen")
		return
	}

	if err := a.switcher.SetPipelineFormat(f); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
