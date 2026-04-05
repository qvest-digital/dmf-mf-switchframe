package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
)

// SnapshotProvider captures and applies full engine state snapshots for
// multi-region active-active redundancy. A browser detecting sustained state
// divergence pulls a snapshot from the authoritative engine and pushes it
// to the lagging one via these endpoints.
type SnapshotProvider interface {
	// CaptureFullSnapshot returns the complete restorable engine state.
	CaptureFullSnapshot() internal.FullStateSnapshot

	// ApplyTarget returns the target that can apply a snapshot to restore state.
	ApplyTarget() internal.SnapshotApplyTarget
}

// WithSnapshotProvider attaches a snapshot provider to the API.
func WithSnapshotProvider(sp SnapshotProvider) APIOption {
	return func(a *API) { a.snapshotProvider = sp }
}

// SetSnapshotProvider sets the snapshot provider after API construction.
// This is a setter rather than an option because the provider depends on
// the App which is initialized after the API is created.
func (a *API) SetSnapshotProvider(sp SnapshotProvider) {
	a.snapshotProvider = sp
	// Routes were already registered conditionally — since the provider is
	// set after construction, we register the routes now on the internal mux.
	a.registerSyncRoutes(a.mux)
}

// registerSyncRoutes registers snapshot-related routes on the given mux.
// Called from registerAPIRoutes only when a snapshot provider is configured.
func (a *API) registerSyncRoutes(mux *http.ServeMux) {
	if a.snapshotProvider == nil {
		return
	}
	mux.HandleFunc("GET /api/sync/state-snapshot", a.handleGetStateSnapshot)
	mux.HandleFunc("POST /api/sync/state-snapshot", a.handleApplyStateSnapshot)
}

// handleGetStateSnapshot returns the full restorable engine state as JSON.
// Used by browsers to pull a snapshot from the authoritative engine during
// redundancy reconciliation.
func (a *API) handleGetStateSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := a.snapshotProvider.CaptureFullSnapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

// handleApplyStateSnapshot accepts a FullStateSnapshot and applies it to
// restore the engine to the given state. Returns whether the apply succeeded
// and any warnings for subsystems that could not be fully restored.
func (a *API) handleApplyStateSnapshot(w http.ResponseWriter, r *http.Request) {
	var snap internal.FullStateSnapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid snapshot JSON")
		return
	}

	warnings := internal.ApplyFullSnapshot(r.Context(), snap, a.snapshotProvider.ApplyTarget())

	resp := struct {
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings,omitempty"`
	}{
		Applied:  true,
		Warnings: warnings,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
