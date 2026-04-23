package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type serverConfig struct {
	Token string
	Host  string
	Port  int
}

type server struct {
	d   db
	cfg serverConfig
	bus *eventBus
}

func newServer(d db, cfg serverConfig) *server {
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
