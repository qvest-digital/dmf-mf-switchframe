package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/replay"
)

// registerReplayRoutes registers replay-related API routes on the given mux.
func (a *API) registerReplayRoutes(mux *http.ServeMux) {
	if a.replayMgr == nil {
		return
	}
	mux.HandleFunc("POST /api/replay/mark-in", a.handleReplayMarkIn)
	mux.HandleFunc("POST /api/replay/mark-out", a.handleReplayMarkOut)
	mux.HandleFunc("POST /api/replay/play", a.handleReplayPlay)
	mux.HandleFunc("POST /api/replay/stop", a.handleReplayStop)
	mux.HandleFunc("GET /api/replay/status", a.handleReplayStatus)
	mux.HandleFunc("GET /api/replay/sources", a.handleReplaySources)
	mux.HandleFunc("POST /api/replay/quick", a.handleReplayQuick)
	mux.HandleFunc("POST /api/replay/pause", a.handleReplayPause)
	mux.HandleFunc("POST /api/replay/resume", a.handleReplayResume)
	mux.HandleFunc("PATCH /api/replay/seek", a.handleReplaySeek)
	mux.HandleFunc("PATCH /api/replay/speed", a.handleReplaySpeed)
	mux.HandleFunc("PATCH /api/replay/marks", a.handleReplayMarks)
	mux.HandleFunc("GET /api/replay/peek", a.handleReplayPeek)
}

func (a *API) handleReplayMarkIn(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.MarkInRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.replayMgr.MarkIn(req.Source); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayMarkOut(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.MarkOutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.replayMgr.MarkOut(req.Source); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayPlay(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.PlayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if req.Speed == 0 {
		req.Speed = 1.0
	}
	if err := a.replayMgr.Play(req.Source, req.Speed, req.Loop); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayStop(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	if err := a.replayMgr.Stop(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayStatus(w http.ResponseWriter, _ *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	rs := a.replayMgr.Status()
	resp := struct {
		State      string                    `json:"state"`
		Source     string                    `json:"source,omitempty"`
		Speed      float64                   `json:"speed,omitempty"`
		Loop       bool                      `json:"loop,omitempty"`
		Position   float64                   `json:"position,omitempty"`
		MarkIn     *int64                    `json:"markIn,omitempty"`
		MarkOut    *int64                    `json:"markOut,omitempty"`
		MarkSource string                    `json:"markSource,omitempty"`
		Buffers    []replay.SourceBufferInfo `json:"buffers,omitempty"`
	}{
		State:      string(rs.State),
		Source:     rs.Source,
		Speed:      rs.Speed,
		Loop:       rs.Loop,
		Position:   rs.Position,
		MarkIn:     rs.MarkInUnixMs(),
		MarkOut:    rs.MarkOutUnixMs(),
		MarkSource: rs.MarkSource,
		Buffers:    rs.Buffers,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (a *API) handleReplaySources(w http.ResponseWriter, _ *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	status := a.replayMgr.Status()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status.Buffers)
}

func (a *API) handleReplayQuick(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.QuickReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Seconds <= 0 {
		httperr.Write(w, http.StatusBadRequest, "seconds must be > 0")
		return
	}
	source := req.Source
	if source == "" {
		source = a.enrichedState().ProgramSource
	}
	if err := a.replayMgr.QuickReplay(source, req.Seconds, req.Speed); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayPause(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	if err := a.replayMgr.Pause(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayResume(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	if err := a.replayMgr.Resume(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplaySeek(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.SeekRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.replayMgr.Seek(req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplaySpeed(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.SpeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.replayMgr.SetSpeed(req.Speed); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayMarks(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	a.setLastOperator(r)
	var req replay.AdjustMarksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.replayMgr.SetMarks(req.MarkIn, req.MarkOut); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

func (a *API) handleReplayPeek(w http.ResponseWriter, r *http.Request) {
	if a.replayMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "replay not enabled")
		return
	}
	source := r.URL.Query().Get("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	data, err := a.replayMgr.PeekFrame(source)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	if data == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
