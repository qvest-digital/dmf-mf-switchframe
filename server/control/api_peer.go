package control

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/peer"
)

// registerPeerRoutes registers the peer health endpoint on the given mux.
// The /api/peer/health endpoint is unauthenticated so peer engines can
// call it without API tokens. /api/peer/force-leader is authenticated
// via a separate control-plane shared secret.
func (a *API) registerPeerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/peer/health", a.handlePeerHealth)
	mux.HandleFunc("POST /api/peer/force-leader", a.handleForceLeader)
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
	// Defensive nil guard: handler is unauthenticated, so a latent
	// nil-deref on an accessor refactor would be a remote DoS.
	if a.peerPoller != nil {
		resp.EngineLabel = a.peerPoller.LocalLabel()
		resp.Epoch = a.peerPoller.LocalEpoch()
	}
	if a.outputMgr != nil {
		resp.Sending = a.outputMgr.SendingDestinationIDs()
		resp.Destinations, resp.Armed = a.outputMgr.PairDestinationIDs()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
