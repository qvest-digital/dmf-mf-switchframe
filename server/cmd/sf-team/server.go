package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

type serverConfig struct {
	Token    string
	Host     string
	Port     int
	ItemsDir string
}

type server struct {
	d           db
	cfg         serverConfig
	bus         *eventBus
	callerAgent string
}

func newServer(d db, cfg serverConfig) *server {
	if cfg.ItemsDir == "" {
		cfg.ItemsDir = "docs/backlog/items"
	}
	bus := newEventBus()
	d.publisher = bus
	if d.pubRef == nil {
		d.pubRef = &pubRef{}
	}
	d.pubRef.p = bus
	agent, _ := agentID(detectWorktree())
	return &server{d: d, cfg: cfg, bus: bus, callerAgent: agent}
}

func (s *server) actingAgent(r *http.Request) string {
	if h := r.Header.Get("X-SF-Agent"); h != "" {
		return h
	}
	return s.callerAgent
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("GET /api/agents", s.handleAgents)
	mux.HandleFunc("GET /api/claims", s.handleClaims)
	mux.HandleFunc("GET /api/touches", s.handleTouches)
	mux.HandleFunc("GET /api/messages", s.handleMessages)
	mux.HandleFunc("GET /api/items", s.handleItemsList)
	mux.HandleFunc("GET /api/items/{id}", s.handleItemDetail)
	mux.HandleFunc("GET /api/items/{id}/activity", s.handleItemActivity)
	mux.HandleFunc("GET /api/progress/{id}", s.handleProgress)
	mux.HandleFunc("POST /api/say", s.handleSay)
	mux.HandleFunc("POST /api/knock", s.handleKnock)
	mux.HandleFunc("POST /api/claim", s.handleClaim)
	mux.HandleFunc("POST /api/release", s.handleRelease)
	mux.HandleFunc("POST /api/progress", s.handleProgressPost)
	mux.HandleFunc("POST /api/reassign", s.handleReassign)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	webSub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(webSub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Allow", "GET")
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	return s.authMiddleware(mux)
}

func (s *server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" && r.URL.Path == "/api/events" {
			got = r.URL.Query().Get("token")
		}
		if got != s.cfg.Token {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.d.whoList(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (s *server) handleClaims(w http.ResponseWriter, r *http.Request) {
	rows, err := s.d.sqldb.QueryContext(r.Context(), `
		SELECT item_id, agent_id, COALESCE(intent, ''), claimed_at, last_touch FROM claims
		ORDER BY claimed_at
	`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type claimRow struct {
		ItemID    string `json:"item_id"`
		AgentID   string `json:"agent_id"`
		Intent    string `json:"intent"`
		ClaimedAt int64  `json:"claimed_at"`
		LastTouch int64  `json:"last_touch"`
	}
	var out []claimRow
	for rows.Next() {
		var c claimRow
		if err := rows.Scan(&c.ItemID, &c.AgentID, &c.Intent, &c.ClaimedAt, &c.LastTouch); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	thread := q.Get("thread")
	limit := 100
	if lv := q.Get("limit"); lv != "" {
		if n, err := strconv.Atoi(lv); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	var beforeID int64
	if bv := q.Get("before"); bv != "" {
		if n, err := strconv.ParseInt(bv, 10, 64); err == nil {
			beforeID = n
		}
	}

	sqlq := `SELECT id, ts, agent_id, thread, kind, COALESCE(body, ''), COALESCE(mentions, '[]'), priority FROM messages`
	var conds []string
	var args []any
	if thread != "" {
		conds = append(conds, "thread = ?")
		args = append(args, thread)
	}
	if beforeID > 0 {
		conds = append(conds, "id < ?")
		args = append(args, beforeID)
	}
	if len(conds) > 0 {
		sqlq += " WHERE " + strings.Join(conds, " AND ")
	}
	sqlq += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.d.sqldb.QueryContext(r.Context(), sqlq, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type msgRow struct {
		ID       int64           `json:"id"`
		TS       int64           `json:"ts"`
		AgentID  string          `json:"agent_id"`
		Thread   string          `json:"thread"`
		Kind     string          `json:"kind"`
		Body     string          `json:"body"`
		Mentions json.RawMessage `json:"mentions"`
		Priority string          `json:"priority"`
	}
	var out []msgRow
	for rows.Next() {
		var m msgRow
		var ment string
		if err := rows.Scan(&m.ID, &m.TS, &m.AgentID, &m.Thread, &m.Kind, &m.Body, &ment, &m.Priority); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		m.Mentions = json.RawMessage(ment)
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleItemsList(w http.ResponseWriter, r *http.Request) {
	items, err := walkItems(s.cfg.ItemsDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type itemOut struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Type      string `json:"type"`
		Priority  string `json:"priority"`
		Area      string `json:"area"`
		Status    string `json:"status"`
		Estimate  string `json:"estimate"`
		Risk      string `json:"risk"`
		ACTotal   int    `json:"ac_total"`
		ACChecked int    `json:"ac_checked"`
		Progress  int    `json:"progress_pct"`
	}
	out := make([]itemOut, 0, len(items))
	for _, it := range items {
		out = append(out, itemOut{
			ID: it.ID, Title: it.Title, Type: it.Type, Priority: it.Priority,
			Area: it.Area, Status: it.Status, Estimate: it.Estimate, Risk: it.Risk,
			ACTotal: it.ACTotal, ACChecked: it.ACChecked, Progress: it.ProgressPct(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleItemDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	items, err := walkItems(s.cfg.ItemsDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var it *item
	for i := range items {
		if items[i].ID == id {
			it = &items[i]
			break
		}
	}
	if it == nil {
		writeErr(w, http.StatusNotFound, "item not found: "+id)
		return
	}

	var currentClaim any
	row := s.d.sqldb.QueryRowContext(r.Context(),
		`SELECT agent_id, COALESCE(intent, ''), claimed_at FROM claims WHERE item_id=?`, id)
	var cc struct {
		AgentID   string `json:"agent_id"`
		Intent    string `json:"intent"`
		ClaimedAt int64  `json:"claimed_at"`
	}
	switch err := row.Scan(&cc.AgentID, &cc.Intent, &cc.ClaimedAt); {
	case err == nil:
		currentClaim = cc
	case errors.Is(err, sql.ErrNoRows):
		// leave nil — no active claim
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if it.ACItems == nil {
		it.ACItems = []ACItem{}
	}
	if it.BlockedBy == nil {
		it.BlockedBy = []string{}
	}
	if it.RelatesTo == nil {
		it.RelatesTo = []string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": it.ID, "title": it.Title, "type": it.Type, "priority": it.Priority,
		"area": it.Area, "status": it.Status, "estimate": it.Estimate, "risk": it.Risk,
		"ac_total": it.ACTotal, "ac_checked": it.ACChecked, "progress_pct": it.ProgressPct(),
		"body_markdown": it.Body,
		"ac":            it.ACItems,
		"blocked_by":    it.BlockedBy,
		"relates_to":    it.RelatesTo,
		"current_claim": currentClaim,
	})
}

func (s *server) handleItemActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit := 100
	if lv := r.URL.Query().Get("limit"); lv != "" {
		if n, err := strconv.Atoi(lv); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	var before int64 = math.MaxInt64
	if bv := r.URL.Query().Get("before"); bv != "" {
		if n, err := strconv.ParseInt(bv, 10, 64); err == nil && n > 0 {
			before = n
		}
	}

	const q = `
		SELECT 'claim'    AS kind, claimed_at AS ts, agent_id, COALESCE(intent,'') AS detail, 0 AS pct, ''  AS path, ''  AS outcome, '' AS body
		  FROM claims         WHERE item_id = ?  AND claimed_at < ?
		UNION ALL
		SELECT 'claim',        claimed_at,     agent_id, '',                 0,            '',         '',              ''
		  FROM claim_history  WHERE item_id = ?  AND claimed_at < ?
		UNION ALL
		SELECT 'release',      released_at,    agent_id, '',                 0,            '',         COALESCE(outcome,''), ''
		  FROM claim_history  WHERE item_id = ?  AND released_at < ?
		UNION ALL
		SELECT 'progress',     reported_at,    reported_by, COALESCE(note,''), pct,         '',         '',              ''
		  FROM progress       WHERE item_id = ?  AND reported_at < ?
		UNION ALL
		SELECT 'touch',        started_at,     agent_id, '',                 0,            path,       '',              ''
		  FROM touches        WHERE item_id = ?  AND started_at < ?
		UNION ALL
		SELECT 'untouch',      released_at,    agent_id, '',                 0,            path,       '',              ''
		  FROM touches        WHERE item_id = ?  AND released_at IS NOT NULL AND released_at < ?
		UNION ALL
		SELECT kind,           ts,             agent_id, '',                 0,            '',         '',              COALESCE(body,'')
		  FROM messages
		  WHERE thread = ? AND ts < ?
		    AND kind IN ('knock','ask','answer','heads_up','say','done','blocked','review_req')
		ORDER BY ts DESC
		LIMIT ?
	`
	rows, err := s.d.sqldb.QueryContext(r.Context(), q,
		id, before,
		id, before,
		id, before,
		id, before,
		id, before,
		id, before,
		id, before,
		limit,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type activityEvent struct {
		Kind    string `json:"kind"`
		TS      int64  `json:"ts"`
		AgentID string `json:"agent_id"`
		Detail  string `json:"detail,omitempty"`
		Pct     int    `json:"pct,omitempty"`
		Path    string `json:"path,omitempty"`
		Outcome string `json:"outcome,omitempty"`
		Body    string `json:"body,omitempty"`
	}
	out := []activityEvent{}
	for rows.Next() {
		var e activityEvent
		if err := rows.Scan(&e.Kind, &e.TS, &e.AgentID, &e.Detail, &e.Pct, &e.Path, &e.Outcome, &e.Body); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleProgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := s.d.sqldb.QueryContext(r.Context(), `
		SELECT pct, reported_at, reported_by, COALESCE(note, '') FROM progress
		WHERE item_id = ? ORDER BY reported_at DESC
	`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type pRow struct {
		Pct        int    `json:"pct"`
		ReportedAt int64  `json:"reported_at"`
		ReportedBy string `json:"reported_by"`
		Note       string `json:"note"`
	}
	var out []pRow
	for rows.Next() {
		var p pRow
		if err := rows.Scan(&p.Pct, &p.ReportedAt, &p.ReportedBy, &p.Note); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleSay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Thread   string   `json:"thread"`
		Body     string   `json:"body"`
		Mentions []string `json:"mentions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Thread == "" {
		req.Thread = "global"
	}
	if req.Body == "" {
		writeErr(w, http.StatusBadRequest, "body required")
		return
	}
	agent := s.actingAgent(r)
	mentions := req.Mentions
	if mentions == nil {
		mentions = parseMentions(req.Body)
	}
	if err := s.d.say(r.Context(), agent, req.Thread, "say", req.Body, mentions, "normal"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleKnock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToAgent string `json:"to_agent"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ToAgent == "" || req.Body == "" {
		writeErr(w, http.StatusBadRequest, "to_agent and body required")
		return
	}
	if err := s.d.knock(r.Context(), s.actingAgent(r), req.ToAgent, req.Body); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Item    string   `json:"item"`
		Intent  string   `json:"intent"`
		Touches []string `json:"touches"`
		Long    bool     `json:"long"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Item == "" {
		writeErr(w, http.StatusBadRequest, "item required")
		return
	}
	err := s.d.claim(r.Context(), req.Item, s.actingAgent(r), req.Intent, req.Touches, req.Long)
	if err != nil {
		if errors.Is(err, errClaimTaken) {
			writeErr(w, http.StatusConflict, "item already claimed")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Item    string `json:"item"`
		Outcome string `json:"outcome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Item == "" {
		writeErr(w, http.StatusBadRequest, "item required")
		return
	}
	err := s.d.release(r.Context(), req.Item, s.actingAgent(r), req.Outcome)
	if err != nil {
		if errors.Is(err, errNotYours) {
			writeErr(w, http.StatusForbidden, "not your claim")
			return
		}
		if errors.Is(err, errNotClaimed) {
			writeErr(w, http.StatusNotFound, "no active claim")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleProgressPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Item string `json:"item"`
		Pct  int    `json:"pct"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Item == "" {
		writeErr(w, http.StatusBadRequest, "item required")
		return
	}
	if err := s.d.reportProgress(r.Context(), s.actingAgent(r), req.Item, req.Pct, req.Note); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleReassign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Item    string `json:"item"`
		ToAgent string `json:"to_agent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Item == "" || req.ToAgent == "" {
		writeErr(w, http.StatusBadRequest, "item and to_agent required")
		return
	}
	if err := s.d.reassign(r.Context(), req.Item, s.actingAgent(r), req.ToAgent); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			payload, _ := json.Marshal(e)
			_, _ = w.Write([]byte("event: " + e.Kind + "\ndata: "))
			_, _ = w.Write(payload)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

func (s *server) handleTouches(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"
	q := `SELECT agent_id, COALESCE(item_id, ''), path, started_at, COALESCE(released_at, 0) FROM touches`
	if activeOnly {
		q += ` WHERE released_at IS NULL`
	}
	q += ` ORDER BY started_at DESC`
	rows, err := s.d.sqldb.QueryContext(r.Context(), q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type tRow struct {
		AgentID    string `json:"agent_id"`
		ItemID     string `json:"item_id"`
		Path       string `json:"path"`
		StartedAt  int64  `json:"started_at"`
		ReleasedAt int64  `json:"released_at"`
	}
	var out []tRow
	for rows.Next() {
		var t tRow
		if err := rows.Scan(&t.AgentID, &t.ItemID, &t.Path, &t.StartedAt, &t.ReleasedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}
