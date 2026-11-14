package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/dve"
)

// registerSqueezeRoutes registers the squeeze-back routes on the given mux.
func (a *API) registerSqueezeRoutes(mux *http.ServeMux) {
	if a.squeeze == nil {
		return
	}
	mux.HandleFunc("GET /api/squeezeback", a.handleGetSqueeze)
	mux.HandleFunc("PUT /api/squeezeback", timedHandler(a.cmdQueue, "squeezeback_set", a.seqTracker, a.handleSetSqueezeInner))
	mux.HandleFunc("POST /api/squeezeback/on", timedHandler(a.cmdQueue, "squeezeback_on", a.seqTracker, a.handleSqueezeOnInner))
	mux.HandleFunc("POST /api/squeezeback/off", timedHandler(a.cmdQueue, "squeezeback_off", a.seqTracker, a.handleSqueezeOffInner))
}

func (a *API) handleGetSqueeze(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.squeeze.Snapshot())
}

func (a *API) handleSetSqueezeInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	var cfg dve.SqueezeConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if cfg.Background != "" && !a.switcher.HasSource(cfg.Background) {
		httperr.Write(w, http.StatusBadRequest, "unknown background source: "+cfg.Background)
		return
	}
	if err := a.squeeze.Set(cfg); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.squeeze.Snapshot())
}

func (a *API) handleSqueezeOnInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.handleSqueezeMove(w, r, body, a.squeeze.On)
}

func (a *API) handleSqueezeOffInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.handleSqueezeMove(w, r, body, a.squeeze.Off)
}

// handleSqueezeMove runs On or Off; an empty body takes the configured
// duration and easing.
func (a *API) handleSqueezeMove(w http.ResponseWriter, r *http.Request, body []byte, move func(dve.SqueezeMove) error) {
	a.setLastOperator(r)
	var m dve.SqueezeMove
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	if err := move(m); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}
