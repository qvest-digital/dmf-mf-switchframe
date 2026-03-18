package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
)

// commsJoinRequest is the JSON body for joining the comms channel.
type commsJoinRequest struct {
	OperatorID string `json:"operatorId"`
	Name       string `json:"name"`
}

// commsLeaveRequest is the JSON body for leaving the comms channel.
type commsLeaveRequest struct {
	OperatorID string `json:"operatorId"`
}

// commsMuteRequest is the JSON body for setting mute state.
type commsMuteRequest struct {
	OperatorID string `json:"operatorId"`
	Muted      bool   `json:"muted"`
}

// registerCommsRoutes registers comms-related API routes on the given mux.
func (a *API) registerCommsRoutes(mux *http.ServeMux) {
	if a.commsMgr == nil {
		return
	}
	mux.HandleFunc("POST /api/comms/join", a.handleCommsJoin)
	mux.HandleFunc("POST /api/comms/leave", a.handleCommsLeave)
	mux.HandleFunc("PUT /api/comms/mute", a.handleCommsMute)
	mux.HandleFunc("GET /api/comms/status", a.handleCommsStatus)
}

func (a *API) handleCommsJoin(w http.ResponseWriter, r *http.Request) {
	if a.commsMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "comms not enabled")
		return
	}
	a.setLastOperator(r)
	var req commsJoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.OperatorID == "" {
		httperr.Write(w, http.StatusBadRequest, "operatorId required")
		return
	}
	if req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}
	if err := a.commsMgr.Join(req.OperatorID, req.Name); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleCommsLeave(w http.ResponseWriter, r *http.Request) {
	if a.commsMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "comms not enabled")
		return
	}
	a.setLastOperator(r)
	var req commsLeaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.OperatorID == "" {
		httperr.Write(w, http.StatusBadRequest, "operatorId required")
		return
	}
	a.commsMgr.Leave(req.OperatorID)
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleCommsMute(w http.ResponseWriter, r *http.Request) {
	if a.commsMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "comms not enabled")
		return
	}
	a.setLastOperator(r)
	var req commsMuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.OperatorID == "" {
		httperr.Write(w, http.StatusBadRequest, "operatorId required")
		return
	}
	if err := a.commsMgr.SetMuted(req.OperatorID, req.Muted); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleCommsStatus(w http.ResponseWriter, _ *http.Request) {
	if a.commsMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "comms not enabled")
		return
	}
	state := a.commsMgr.State()
	if state == nil {
		state = &internal.CommsState{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
