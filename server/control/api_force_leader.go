package control

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// handleForceLeader bumps the local peer epoch, promoting this engine to
// leader in the tiebreak regardless of label. Authenticated via the
// X-Control-Plane-Secret header (NOT the regular API key) so an operator
// pressing a normal UI button cannot accidentally fire it; only the
// control plane, which holds the secret, can invoke it.
//
// Response: {"newEpoch": <uint64>, "role": <string>, "label": <string>}.
// 501 if no secret is configured OR no peer poller exists (standalone engine).
// 403 if the secret header is missing or mismatched (constant-time compare).
func (a *API) handleForceLeader(w http.ResponseWriter, r *http.Request) {
	if a.controlPlaneSecret == "" || a.peerPoller == nil {
		httperr.Write(w, http.StatusNotImplemented,
			"force-leader not configured (missing --control-plane-secret or --peer-engine-url)")
		return
	}

	provided := r.Header.Get("X-Control-Plane-Secret")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(a.controlPlaneSecret)) != 1 {
		httperr.Write(w, http.StatusForbidden, "invalid control-plane secret")
		return
	}

	// Body is optional; decode reason for audit logging, ignore parse errors.
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
	}

	newEpoch := a.peerPoller.BumpEpoch()
	slog.Warn("engine forced to leader role via /api/peer/force-leader",
		"newEpoch", newEpoch,
		"label", a.peerPoller.LocalLabel(),
		"reason", body.Reason)

	resp := struct {
		NewEpoch uint64 `json:"newEpoch"`
		Role     string `json:"role"`
		Label    string `json:"label"`
	}{
		NewEpoch: newEpoch,
		Role:     a.peerPoller.Role().String(),
		Label:    a.peerPoller.LocalLabel(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
