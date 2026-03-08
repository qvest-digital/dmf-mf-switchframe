package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// transitionRequest is the JSON body for transition commands.
type transitionRequest struct {
	Source        string `json:"source"`
	Type          string `json:"type"`
	DurationMs    int    `json:"durationMs"`
	WipeDirection string `json:"wipeDirection,omitempty"`
	StingerName   string `json:"stingerName,omitempty"`
}

// handleTransition starts a mix, dip, wipe, or stinger transition to the specified source.
func (a *API) handleTransition(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req transitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Type != "mix" && req.Type != "dip" && req.Type != "wipe" && req.Type != "stinger" {
		httperr.Write(w, http.StatusBadRequest, "type must be 'mix', 'dip', 'wipe', or 'stinger'")
		return
	}
	if req.Type == "wipe" {
		wd := transition.WipeDirection(req.WipeDirection)
		if !transition.ValidWipeDirections[wd] {
			httperr.Write(w, http.StatusBadRequest, "wipeDirection must be one of: h-left, h-right, v-top, v-bottom, box-center-out, box-edges-in")
			return
		}
	}
	if req.Type == "stinger" {
		if a.stingerStore == nil {
			httperr.Write(w, http.StatusNotImplemented, "stinger store not configured")
			return
		}
		if req.StingerName == "" {
			httperr.Write(w, http.StatusBadRequest, "stingerName required for stinger transition")
			return
		}
	}
	if req.DurationMs < 100 || req.DurationMs > 5000 {
		httperr.Write(w, http.StatusBadRequest, "durationMs must be 100-5000")
		return
	}

	var opts []switcher.TransitionOption
	if req.Type == "stinger" {
		clip, ok := a.stingerStore.Get(req.StingerName)
		if !ok {
			httperr.Write(w, http.StatusNotFound, "stinger clip not found")
			return
		}
		sd := clipToStingerData(clip)
		opts = append(opts, switcher.WithStingerData(sd))
	}

	if err := a.switcher.StartTransition(r.Context(), req.Source, req.Type, req.DurationMs, req.WipeDirection, opts...); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// transitionPositionRequest is the JSON body for the transition position endpoint.
type transitionPositionRequest struct {
	Position float64 `json:"position"`
}

// handleTransitionPosition sets the T-bar position during an active transition.
func (a *API) handleTransitionPosition(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req transitionPositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Position < 0 || req.Position > 1 {
		httperr.Write(w, http.StatusBadRequest, "position must be 0-1")
		return
	}
	if err := a.switcher.SetTransitionPosition(r.Context(), req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleFTB starts or toggles a Fade to Black transition.
func (a *API) handleFTB(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if err := a.switcher.FadeToBlack(r.Context()); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
