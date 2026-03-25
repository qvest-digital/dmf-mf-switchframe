package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/playout"
)

// registerPlayoutRoutes registers playout channel control routes on the given mux.
func (a *API) registerPlayoutRoutes(mux *http.ServeMux) {
	if a.playoutMgr == nil {
		return
	}
	mux.HandleFunc("GET /api/playout/channels", a.handlePlayoutChannelsList)
	mux.HandleFunc("GET /api/playout/channels/{n}", a.handlePlayoutChannelGet)
	mux.HandleFunc("POST /api/playout/channels/{n}/load", a.handlePlayoutLoad)
	mux.HandleFunc("POST /api/playout/channels/{n}/start", a.handlePlayoutStart)
	mux.HandleFunc("POST /api/playout/channels/{n}/stop", a.handlePlayoutStop)
	mux.HandleFunc("POST /api/playout/channels/{n}/next", a.handlePlayoutNext)
	mux.HandleFunc("POST /api/playout/channels/{n}/previous", a.handlePlayoutPrevious)
	mux.HandleFunc("POST /api/playout/channels/{n}/jump", a.handlePlayoutJump)
	mux.HandleFunc("POST /api/playout/channels/{n}/manual", a.handlePlayoutTakeManual)
	mux.HandleFunc("POST /api/playout/channels/{n}/auto", a.handlePlayoutReturnToAuto)
}

// parseChannelID extracts and validates the channel ID from the request path.
func parseChannelID(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		return 0, playout.ErrInvalidChannel
	}
	if n < 1 || n > playout.MaxChannels {
		return 0, playout.ErrInvalidChannel
	}
	return n, nil
}

// handlePlayoutChannelsList returns the status of all playout channels.
func (a *API) handlePlayoutChannelsList(w http.ResponseWriter, _ *http.Request) {
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states)
}

// handlePlayoutChannelGet returns the status of a single playout channel.
func (a *API) handlePlayoutChannelGet(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutLoad loads a playlist into a playout channel.
func (a *API) handlePlayoutLoad(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	var pl playout.Playlist
	if err := json.NewDecoder(r.Body).Decode(&pl); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.playoutMgr.LoadPlaylist(n, &pl); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutStart starts playback on a playout channel.
func (a *API) handlePlayoutStart(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.Start(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutStop stops playback on a playout channel.
func (a *API) handlePlayoutStop(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.Stop(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutNext advances to the next playlist item on a playout channel.
func (a *API) handlePlayoutNext(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.Next(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutPrevious moves to the previous playlist item on a playout channel.
func (a *API) handlePlayoutPrevious(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.Previous(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutJump jumps to a specific playlist item by ID on a playout channel.
func (a *API) handlePlayoutJump(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	var req struct {
		ItemID string `json:"itemId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.ItemID == "" {
		httperr.Write(w, http.StatusBadRequest, "itemId required")
		return
	}

	if err := a.playoutMgr.JumpTo(n, req.ItemID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutTakeManual pauses automated playlist advancement on a playout channel.
func (a *API) handlePlayoutTakeManual(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.TakeManual(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutReturnToAuto resumes automated playlist advancement on a playout channel.
func (a *API) handlePlayoutReturnToAuto(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.ReturnToAuto(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}
