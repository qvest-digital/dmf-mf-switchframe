package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/operator"
)

// registerOperatorRoutes adds operator management routes to the mux.
func (a *API) registerOperatorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/operator/register", a.handleOperatorRegister)
	mux.HandleFunc("POST /api/operator/reconnect", a.handleOperatorReconnect)
	mux.HandleFunc("POST /api/operator/heartbeat", a.handleOperatorHeartbeat)
	mux.HandleFunc("GET /api/operator/list", a.handleOperatorList)
	mux.HandleFunc("POST /api/operator/lock", a.handleOperatorLock)
	mux.HandleFunc("POST /api/operator/unlock", a.handleOperatorUnlock)
	mux.HandleFunc("POST /api/operator/force-unlock", a.handleOperatorForceUnlock)
	mux.HandleFunc("DELETE /api/operator/{id}", a.handleOperatorDelete)
}

func (a *API) handleOperatorRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string        `json:"name"`
		Role operator.Role `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	op, err := a.operatorStore.Register(req.Name, req.Role)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	// Auto-connect the newly registered operator.
	a.sessionMgr.Connect(op.ID, op.Name, op.Role)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":    op.ID,
		"name":  op.Name,
		"role":  op.Role,
		"token": op.Token,
	})
}

func (a *API) handleOperatorReconnect(w http.ResponseWriter, r *http.Request) {
	token := operator.ExtractBearerToken(r)
	if token == "" {
		httperr.Write(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	op, err := a.operatorStore.GetByToken(token)
	if err != nil {
		httperr.Write(w, http.StatusUnauthorized, "invalid token")
		return
	}

	// Re-establish session.
	a.sessionMgr.Connect(op.ID, op.Name, op.Role)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":   op.ID,
		"name": op.Name,
		"role": op.Role,
	})
}

func (a *API) handleOperatorHeartbeat(w http.ResponseWriter, r *http.Request) {
	token := operator.ExtractBearerToken(r)
	op, err := a.operatorStore.GetByToken(token)
	if err != nil {
		httperr.Write(w, http.StatusUnauthorized, "invalid token")
		return
	}

	a.sessionMgr.Heartbeat(op.ID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (a *API) handleOperatorList(w http.ResponseWriter, _ *http.Request) {
	operators := a.operatorStore.List()
	sessions := a.sessionMgr.ActiveSessions()

	// Build a map of connected operator IDs for fast lookup.
	connectedSet := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		connectedSet[s.OperatorID] = true
	}

	result := make([]operator.OperatorInfo, len(operators))
	for i, op := range operators {
		result[i] = operator.OperatorInfo{
			ID:        op.ID,
			Name:      op.Name,
			Role:      op.Role,
			Connected: connectedSet[op.ID],
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (a *API) handleOperatorLock(w http.ResponseWriter, r *http.Request) {
	token := operator.ExtractBearerToken(r)
	op, err := a.operatorStore.GetByToken(token)
	if err != nil {
		httperr.Write(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var req struct {
		Subsystem operator.Subsystem `json:"subsystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.sessionMgr.AcquireLock(op.ID, req.Subsystem); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (a *API) handleOperatorUnlock(w http.ResponseWriter, r *http.Request) {
	token := operator.ExtractBearerToken(r)
	op, err := a.operatorStore.GetByToken(token)
	if err != nil {
		httperr.Write(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var req struct {
		Subsystem operator.Subsystem `json:"subsystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.sessionMgr.ReleaseLock(op.ID, req.Subsystem); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (a *API) handleOperatorForceUnlock(w http.ResponseWriter, r *http.Request) {
	token := operator.ExtractBearerToken(r)
	op, err := a.operatorStore.GetByToken(token)
	if err != nil {
		httperr.Write(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var req struct {
		Subsystem operator.Subsystem `json:"subsystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.sessionMgr.ForceReleaseLock(op.ID, req.Subsystem); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (a *API) handleOperatorDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "operator id required")
		return
	}

	token := operator.ExtractBearerToken(r)
	requester, err := a.operatorStore.GetByToken(token)
	if err != nil {
		// No token or invalid — allow if no operators registered (backward compat).
		if a.operatorStore.Count() > 0 {
			httperr.Write(w, http.StatusUnauthorized, "invalid token")
			return
		}
	} else if requester.ID != id && requester.Role != operator.RoleDirector {
		httperr.Write(w, http.StatusForbidden, "only self or director can delete operators")
		return
	}

	// Delete from persistent store first — if this fails, session stays intact.
	if err := a.operatorStore.Delete(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	// Store succeeded — clean up in-memory session (cannot fail).
	a.sessionMgr.Disconnect(id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
