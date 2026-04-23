package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type serverConfig struct {
	Token    string
	Host     string
	Port     int
	ItemsDir string
}

type server struct {
	d   db
	cfg serverConfig
	bus *eventBus
}

func newServer(d db, cfg serverConfig) *server {
	if cfg.ItemsDir == "" {
		cfg.ItemsDir = "docs/backlog/items"
	}
	bus := newEventBus()
	d.publisher = bus
	return &server{d: d, cfg: cfg, bus: bus}
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
	mux.HandleFunc("GET /api/progress/{id}", s.handleProgress)
	return s.authMiddleware(mux)
}

func (s *server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got != s.cfg.Token {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
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
	for _, it := range items {
		if it.ID == id {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": it.ID, "title": it.Title, "type": it.Type, "priority": it.Priority,
				"area": it.Area, "status": it.Status, "estimate": it.Estimate, "risk": it.Risk,
				"ac_total": it.ACTotal, "ac_checked": it.ACChecked, "progress_pct": it.ProgressPct(),
			})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "item not found: "+id)
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
