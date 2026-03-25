package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

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

	// As-run endpoints.
	mux.HandleFunc("GET /api/playout/channels/{n}/asrun", a.handlePlayoutAsRunQuery)

	// Cache endpoints (available even without playout manager, guarded by nil checks).
	mux.HandleFunc("GET /api/playout/cache/status", a.handlePlayoutCacheStatus)
	mux.HandleFunc("POST /api/playout/cache/lookup", a.handlePlayoutCacheLookup)
	mux.HandleFunc("POST /api/playout/cache/evict", a.handlePlayoutCacheEvict)
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

// handlePlayoutCacheStatus returns the aggregate cache status as JSON.
func (a *API) handlePlayoutCacheStatus(w http.ResponseWriter, _ *http.Request) {
	if a.playoutCache == nil {
		httperr.Write(w, http.StatusNotImplemented, "cache not configured")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.playoutCache.Status())
}

// handlePlayoutCacheLookup looks up cache status for an asset by URI.
func (a *API) handlePlayoutCacheLookup(w http.ResponseWriter, r *http.Request) {
	if a.playoutCache == nil {
		httperr.Write(w, http.StatusNotImplemented, "cache not configured")
		return
	}

	var req struct {
		AssetURI string `json:"asset_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AssetURI == "" {
		httperr.Write(w, http.StatusBadRequest, "asset_uri required")
		return
	}

	entry, err := a.playoutCache.Peek(req.AssetURI)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entry)
}

// handlePlayoutCacheEvict removes an asset from the cache by URI.
func (a *API) handlePlayoutCacheEvict(w http.ResponseWriter, r *http.Request) {
	if a.playoutCache == nil {
		httperr.Write(w, http.StatusNotImplemented, "cache not configured")
		return
	}

	var req struct {
		AssetURI string `json:"asset_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AssetURI == "" {
		httperr.Write(w, http.StatusBadRequest, "asset_uri required")
		return
	}

	if err := a.playoutCache.Evict(req.AssetURI); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "evicted"})
}

// handlePlayoutAsRunQuery returns as-run events for a channel, with optional filters.
func (a *API) handlePlayoutAsRunQuery(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	logger := a.playoutMgr.AsRunLogger()
	if logger == nil {
		httperr.Write(w, http.StatusNotImplemented, "as-run logging not configured")
		return
	}

	q := playout.AsRunQuery{ChannelID: &n}

	if typ := r.URL.Query().Get("type"); typ != "" {
		q.Type = playout.AsRunEventType(typ)
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			q.Since = &t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			q.Until = &t
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			q.Limit = limit
		}
	}

	events := logger.Query(q)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}
