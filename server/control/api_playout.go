package control

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/playout/bxf"
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
	mux.HandleFunc("GET /api/playout/channels/{n}/playlist", a.handlePlayoutPlaylist)

	// As-run endpoints.
	mux.HandleFunc("GET /api/playout/channels/{n}/asrun", a.handlePlayoutAsRunQuery)
	mux.HandleFunc("GET /api/playout/channels/{n}/asrun/export", a.handlePlayoutAsRunExport)

	// BXF import endpoint.
	mux.HandleFunc("POST /api/playout/import/bxf", a.handlePlayoutBXFImport)

	// Health and recovery endpoints.
	mux.HandleFunc("GET /api/playout/health", a.handlePlayoutHealth)
	mux.HandleFunc("POST /api/playout/channels/{n}/recover", a.handlePlayoutRecover)

	// Cache endpoints (available even without playout manager, guarded by nil checks).
	mux.HandleFunc("GET /api/playout/cache/status", a.handlePlayoutCacheStatus)
	mux.HandleFunc("POST /api/playout/cache/lookup", a.handlePlayoutCacheLookup)
	mux.HandleFunc("POST /api/playout/cache/evict", a.handlePlayoutCacheEvict)

	// Sample clips, clip library, and template playlist endpoints.
	mux.HandleFunc("GET /api/playout/samples", a.handlePlayoutSamples)
	mux.HandleFunc("GET /api/playout/clips", a.handlePlayoutClips)
	mux.HandleFunc("GET /api/playout/templates", a.handlePlayoutTemplates)
	mux.HandleFunc("GET /api/playout/templates/{name}", a.handlePlayoutTemplate)
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

	// Trigger prefetch so clip assets are cached before playback.
	if a.playoutPrefetcher != nil {
		a.playoutPrefetcher.UpdatePlaylist(&pl, 0)
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

// handlePlayoutPlaylist returns the playlist items for a playout channel.
func (a *API) handlePlayoutPlaylist(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	items, err := a.playoutMgr.PlaylistItems(n)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	if items == nil {
		items = []playout.PlaylistItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
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

// handlePlayoutAsRunExport exports as-run events in JSON or BXF format.
func (a *API) handlePlayoutAsRunExport(w http.ResponseWriter, r *http.Request) {
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
	events := logger.Query(q)

	format := r.URL.Query().Get("format")
	if format == "bxf" {
		msg := bxf.ExportAsRun(events)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(xml.Header))
		_ = xml.NewEncoder(w).Encode(msg)
		return
	}

	// Default: JSON export.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

// handlePlayoutBXFImport imports a BXF schedule as a playlist.
func (a *API) handlePlayoutBXFImport(w http.ResponseWriter, r *http.Request) {
	channelStr := r.URL.Query().Get("channel")
	if channelStr == "" {
		channelStr = "1"
	}
	channelID, err := strconv.Atoi(channelStr)
	if err != nil || channelID < 1 || channelID > playout.MaxChannels {
		httperr.Write(w, http.StatusBadRequest, "invalid channel")
		return
	}

	var msg bxf.BxfMessage
	if err := xml.NewDecoder(r.Body).Decode(&msg); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid BXF XML")
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "bxf-import"
	}

	pl, err := bxf.ImportPlaylist(msg, name)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	pl.Channel = fmt.Sprintf("playout:%d", channelID)

	if err := a.playoutMgr.LoadPlaylist(channelID, pl); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[channelID-1])
}

// handlePlayoutHealth returns the watchdog health summary.
func (a *API) handlePlayoutHealth(w http.ResponseWriter, _ *http.Request) {
	health := a.playoutMgr.HealthSummary()
	if health == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "no watchdog configured"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

// handlePlayoutRecover triggers manual recovery for a playout channel.
func (a *API) handlePlayoutRecover(w http.ResponseWriter, r *http.Request) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	if err := a.playoutMgr.RecoverChannel(n); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	states := a.playoutMgr.ChannelStates()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(states[n-1])
}

// handlePlayoutSamples returns the list of available sample clips.
func (a *API) handlePlayoutSamples(w http.ResponseWriter, _ *http.Request) {
	samples := a.playoutSamples
	if samples == nil {
		samples = []playout.SampleClip{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(samples)
}

// handlePlayoutClips returns clips from both the clip store and playout samples,
// formatted as a unified list for the playout clip browser.
func (a *API) handlePlayoutClips(w http.ResponseWriter, _ *http.Request) {
	type playoutClip struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Duration int    `json:"duration"` // seconds
		Source   string `json:"source"`   // "clip-store", "sample", "recording"
	}

	var clips []playoutClip

	// Add clips from clip store.
	if a.clipStore != nil {
		for _, c := range a.clipStore.List() {
			source := "clip-store"
			if c.Source == "recording" {
				source = "recording"
			}
			clips = append(clips, playoutClip{
				Name:     c.Name,
				Path:     filepath.Join(a.clipStore.Dir(), c.Filename),
				Duration: int(c.DurationMs / 1000),
				Source:   source,
			})
		}
	}

	// Add playout sample clips.
	for _, s := range a.playoutSamples {
		clips = append(clips, playoutClip{
			Name:     s.Title,
			Path:     s.Path,
			Duration: s.Duration,
			Source:   "sample",
		})
	}

	if clips == nil {
		clips = []playoutClip{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(clips)
}

// handlePlayoutTemplates returns the names of available template playlists.
func (a *API) handlePlayoutTemplates(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(playout.AvailableTemplates())
}

// handlePlayoutTemplate returns a template playlist by name, populated with sample clip paths.
func (a *API) handlePlayoutTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pl := playout.TemplatePlaylist(name, a.playoutSamples)
	if pl == nil {
		httperr.Write(w, http.StatusNotFound, fmt.Sprintf("unknown template: %s", name))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pl)
}
