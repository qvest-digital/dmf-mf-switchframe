package control

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/peer"
)

// registerPeerRoutes registers the peer health endpoint on the given mux.
// The /api/peer/health endpoint is unauthenticated so peer engines can
// call it without API tokens.
func (a *API) registerPeerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/peer/health", a.handlePeerHealth)
}

// handlePeerHealth returns lightweight health information for peer engine
// polling. This is an unauthenticated endpoint — peer engines call it every
// 5 seconds to detect failures before the control plane notices.
func (a *API) handlePeerHealth(w http.ResponseWriter, r *http.Request) {
	state := a.switcher.State()
	resp := peer.HealthResponse{
		Status:         "ok",
		UptimeMs:       time.Since(a.startTime).Milliseconds(),
		ProgramSource:  state.ProgramSource,
		LastCommandSeq: state.LastCommandSeq,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
