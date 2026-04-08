package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// transitionRequest is the JSON body for transition commands.
type transitionRequest struct {
	Source        string                   `json:"source"`
	Type          string                   `json:"type"`
	DurationMs    int                      `json:"durationMs"`
	WipeDirection string                   `json:"wipeDirection,omitempty"`
	WipeConfig    *wipemap.WipeConfig      `json:"wipeConfig,omitempty"`
	StingerName   string                   `json:"stingerName,omitempty"`
	Easing        *transition.EasingConfig `json:"easing,omitempty"`
}

// registerTransitionRoutes registers transition-related API routes on the given mux.
func (a *API) registerTransitionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/switch/transition", timedHandler(a.cmdQueue, "transition", a.seqTracker, a.handleTransitionInner))
	mux.HandleFunc("POST /api/switch/transition/position", timedHandler(a.cmdQueue, "transition_position", a.seqTracker, a.handleTransitionPositionInner))
	mux.HandleFunc("POST /api/switch/ftb", timedHandler(a.cmdQueue, "ftb", a.seqTracker, a.handleFTBInner))
}

// handleTransitionInner starts a mix, dip, wipe, or stinger transition to the specified source.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleTransitionInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	var req transitionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	validTypes := map[string]bool{
		"mix": true, "dip": true, "wipe": true, "stinger": true,
	}
	// Accept all DVE transitions (dve-* prefix) dynamically.
	if !validTypes[req.Type] && !dve.IsDVETransition(req.Type) {
		httperr.Write(w, http.StatusBadRequest, "invalid transition type")
		return
	}
	if req.Type == "wipe" {
		if req.WipeConfig != nil {
			// Gradient-map wipe: validate pattern ID exists in the wipe store.
			if a.wipeStore == nil {
				httperr.Write(w, http.StatusNotImplemented, "wipe pattern store not configured")
				return
			}
			if req.WipeConfig.PatternID == "" {
				httperr.Write(w, http.StatusBadRequest, "wipeConfig.pattern is required")
				return
			}
			if _, err := a.wipeStore.GetPattern(req.WipeConfig.PatternID); err != nil {
				httperr.Write(w, http.StatusBadRequest, "unknown wipe pattern: "+req.WipeConfig.PatternID)
				return
			}
		} else {
			// Legacy directional wipe.
			wd := transition.WipeDirection(req.WipeDirection)
			if !transition.ValidWipeDirections[wd] {
				httperr.Write(w, http.StatusBadRequest, "wipeDirection must be one of: h-left, h-right, v-top, v-bottom, box-center-out, box-edges-in")
				return
			}
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
	if req.Easing != nil {
		et := transition.EasingType(req.Easing.Type)
		if !transition.ValidEasingTypes[et] {
			httperr.Write(w, http.StatusBadRequest,
				"easing type must be one of: linear, ease, ease-in, ease-out, ease-in-out, smoothstep, custom")
			return
		}
		var ec *transition.EasingCurve
		if et == transition.EasingCustom {
			var err error
			ec, err = transition.NewCustomEasingCurve(req.Easing.X1, req.Easing.Y1, req.Easing.X2, req.Easing.Y2)
			if err != nil {
				httperr.Write(w, http.StatusBadRequest, err.Error())
				return
			}
		} else {
			ec = transition.NewEasingCurve(et)
		}
		opts = append(opts, switcher.WithEasing(ec))
	}
	if req.Type == "stinger" {
		clip, ok := a.stingerStore.Get(req.StingerName)
		if !ok {
			httperr.Write(w, http.StatusNotFound, "stinger clip not found")
			return
		}
		sd := clipToStingerData(clip)
		opts = append(opts, switcher.WithStingerData(sd))
	}
	if req.WipeConfig != nil {
		opts = append(opts, switcher.WithWipeConfig(req.WipeConfig))
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

// handleTransitionPositionInner sets the T-bar position during an active transition.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleTransitionPositionInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	var req transitionPositionRequest
	if err := json.Unmarshal(body, &req); err != nil {
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

// handleFTBInner starts or toggles a Fade to Black transition.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleFTBInner(w http.ResponseWriter, r *http.Request, _ []byte) {
	a.setLastOperator(r)
	if err := a.switcher.FadeToBlack(r.Context()); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
