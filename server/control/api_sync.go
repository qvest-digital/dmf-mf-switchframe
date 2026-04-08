package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
)

// reconcileLockState holds an in-memory lock that prevents two browser tabs
// from reconciling engine state simultaneously on the same engine.
type reconcileLockState struct {
	mu       sync.Mutex
	holder   string
	lockID   string
	expiry   time.Duration
	deadline time.Time
}

// newReconcileLockState returns an initialized lock with a 15s default expiry.
func newReconcileLockState() reconcileLockState {
	return reconcileLockState{expiry: 15 * time.Second}
}

// tryAcquire attempts to acquire the lock for the given holder.
// Returns (lockID, true) on success, or (holder, false) if already held.
func (l *reconcileLockState) tryAcquire(holder string) (string, string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	// If a lock is held and not yet expired, reject.
	if l.lockID != "" && now.Before(l.deadline) {
		return "", l.holder, false
	}

	// Grant the lock.
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	l.lockID = hex.EncodeToString(b)
	l.holder = holder
	l.deadline = now.Add(l.expiry)
	return l.lockID, "", true
}

// release clears the lock if the lockID matches. Returns true if released.
func (l *reconcileLockState) release(lockID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lockID == "" {
		// No lock held — idempotent success.
		return true
	}
	if l.lockID != lockID {
		return false
	}
	l.lockID = ""
	l.holder = ""
	l.deadline = time.Time{}
	return true
}

// refresh extends the deadline of a held lock. Returns true if the lockID matches
// and the lock has not expired.
func (l *reconcileLockState) refresh(lockID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lockID == "" || l.lockID != lockID || time.Now().After(l.deadline) {
		return false
	}
	l.deadline = time.Now().Add(l.expiry)
	return true
}

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
	if a.snapshotProvider != nil {
		return // Already configured
	}
	a.snapshotProvider = sp
	// Routes were already registered conditionally — since the provider is
	// set after construction, we register the routes now on the internal mux.
	a.registerSyncRoutes(a.mux)
}

// registerSyncRoutes registers snapshot-related routes on the given mux.
// Called from registerAPIRoutes only when a snapshot provider is configured.
//
// Protected by auth middleware applied at the server level (see app.go) —
// all /api/ routes go through the CORS -> logger -> metrics -> auth ->
// operator -> maxbytes middleware chain before reaching these handlers.
func (a *API) registerSyncRoutes(mux *http.ServeMux) {
	if a.snapshotProvider == nil {
		return
	}
	mux.HandleFunc("GET /api/sync/state-snapshot", a.handleGetStateSnapshot)
	mux.HandleFunc("POST /api/sync/state-snapshot", a.handleApplyStateSnapshot)
	mux.HandleFunc("POST /api/sync/reconcile-lock", a.handleAcquireReconcileLock)
	mux.HandleFunc("PUT /api/sync/reconcile-lock/{lockId}", a.handleRefreshReconcileLock)
	mux.HandleFunc("DELETE /api/sync/reconcile-lock/{lockId}", a.handleReleaseReconcileLock)
}

// snapshotEnvelope wraps a FullStateSnapshot with staleness metadata.
// Seq is the engine's LastCommandSeq at capture time, used by the receiving
// engine to warn when a stale snapshot is being applied (seq is behind
// current engine state). Timestamp is Unix microseconds for wall-clock age.
type snapshotEnvelope struct {
	Snapshot  json.RawMessage `json:"snapshot"`
	Seq       uint64          `json:"seq"`
	Timestamp int64           `json:"timestamp"` // Unix microseconds
}

// handleGetStateSnapshot returns the full restorable engine state wrapped
// in a snapshotEnvelope with staleness metadata. Used by browsers to pull
// a snapshot from the authoritative engine during redundancy reconciliation.
func (a *API) handleGetStateSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := a.snapshotProvider.CaptureFullSnapshot()
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "marshal failed")
		return
	}
	state := a.enrichedState()
	env := snapshotEnvelope{
		Snapshot:  snapJSON,
		Seq:       state.LastCommandSeq,
		Timestamp: time.Now().UnixMicro(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}

// handleApplyStateSnapshot accepts a FullStateSnapshot (or a snapshotEnvelope
// wrapping one) and applies it to restore the engine to the given state.
// Returns whether the apply succeeded and any warnings for subsystems that
// could not be fully restored.
//
// Backward compatibility: if the body has a "snapshot" key it is decoded as a
// snapshotEnvelope; otherwise it is decoded directly as a FullStateSnapshot.
// When an envelope is provided and its Seq is behind the current engine's
// LastCommandSeq, a staleness warning is added.
func (a *API) handleApplyStateSnapshot(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "could not read body")
		return
	}

	var snap internal.FullStateSnapshot
	var warnings []string

	// Detect envelope vs raw snapshot by probing for the "snapshot" key.
	var probe struct {
		Snapshot json.RawMessage `json:"snapshot"`
		Seq      uint64          `json:"seq"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.Snapshot != nil {
		// Envelope format.
		if err := json.Unmarshal(probe.Snapshot, &snap); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid snapshot JSON")
			return
		}
		// Staleness check: warn if the snapshot's seq is behind the engine's.
		if probe.Seq > 0 {
			currentSeq := a.enrichedState().LastCommandSeq
			if currentSeq > probe.Seq {
				warnings = append(warnings, fmt.Sprintf("snapshot seq %d is behind engine seq %d", probe.Seq, currentSeq))
			}
		}
	} else {
		// Raw FullStateSnapshot (backward compat or direct push).
		if err := json.Unmarshal(raw, &snap); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid snapshot JSON")
			return
		}
	}

	warnings = append(warnings, internal.ApplyFullSnapshot(r.Context(), snap, a.snapshotProvider.ApplyTarget())...)

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

// handleAcquireReconcileLock attempts to acquire the per-engine reconcile lock.
// This prevents multiple browser tabs from reconciling engine state simultaneously.
// Body: {"holder": "browser-tab-id"}
// Returns: {"acquired": true, "lockId": "uuid"} or {"acquired": false, "holder": "existing-holder"}
func (a *API) handleAcquireReconcileLock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Holder string `json:"holder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid request body")
		return
	}

	lockID, holder, acquired := a.reconcileLock.tryAcquire(body.Holder)

	resp := struct {
		Acquired bool   `json:"acquired"`
		LockID   string `json:"lockId,omitempty"`
		Holder   string `json:"holder,omitempty"`
	}{
		Acquired: acquired,
	}
	if acquired {
		resp.LockID = lockID
	} else {
		resp.Holder = holder
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleReleaseReconcileLock releases a previously acquired reconcile lock.
// The lockId in the URL path must match the one returned from acquire.
func (a *API) handleReleaseReconcileLock(w http.ResponseWriter, r *http.Request) {
	lockID := r.PathValue("lockId")
	if lockID == "" {
		// Fallback: extract from URL path for Go <1.22 mux compatibility.
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) > 0 {
			lockID = parts[len(parts)-1]
		}
	}

	if !a.reconcileLock.release(lockID) {
		httperr.Write(w, http.StatusConflict, "lock ID does not match")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"released":true}`))
}

// handleRefreshReconcileLock extends the deadline of a held reconcile lock.
// The lockId in the URL path must match the one returned from acquire.
func (a *API) handleRefreshReconcileLock(w http.ResponseWriter, r *http.Request) {
	lockID := r.PathValue("lockId")
	if lockID == "" {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) > 0 {
			lockID = parts[len(parts)-1]
		}
	}
	if !a.reconcileLock.refresh(lockID) {
		httperr.Write(w, http.StatusConflict, "lock ID does not match or lock expired")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"refreshed":true}`))
}
