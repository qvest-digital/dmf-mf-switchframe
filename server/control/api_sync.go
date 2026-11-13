package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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

// isHeldBy reports whether lockID is the currently-held, unexpired lock.
// Used by the snapshot GET handler to verify the caller already acquired
// the lock — prevents unauthenticated browsers from racing a reconcile in
// the opposite direction on the peer engine.
func (l *reconcileLockState) isHeldBy(lockID string) bool {
	if lockID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lockID != "" && l.lockID == lockID && time.Now().Before(l.deadline)
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

// SnapshotReadiness is optionally implemented by a SnapshotProvider. An
// engine that has just started holds none of the production until it has
// taken its peer's state; a browser copying its state to the peer
// meanwhile would wipe the peer's.
type SnapshotReadiness interface {
	SnapshotReady() bool
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
	mux.HandleFunc("POST /api/sync/pull-from-peer", a.handlePullFromPeer)
	if a.sequenceEngine != nil {
		mux.HandleFunc("GET /api/sync/graphics-sequence", a.handleGetGraphicsSequence)
	}
	mux.HandleFunc("POST /api/sync/state-snapshot", a.handleApplyStateSnapshot)
	mux.HandleFunc("POST /api/sync/reconcile-lock", a.handleAcquireReconcileLock)
	mux.HandleFunc("PUT /api/sync/reconcile-lock/{lockId}", a.handleRefreshReconcileLock)
	mux.HandleFunc("DELETE /api/sync/reconcile-lock/{lockId}", a.handleReleaseReconcileLock)
}

// maxSnapshotBodySize bounds a pushed snapshot. It carries every graphics
// layer's image and rendered frame, so it takes what a graphics import
// takes.
const maxSnapshotBodySize = 64 << 20

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
//
// Callers must supply a ?lockId= query matching a currently-held reconcile
// lock on this engine, or ?admin=true for break-glass access. Without a
// matching lock the handler returns 409. Gating the capture side (not just
// apply) prevents two browsers from pulling opposing snapshots and
// reconciling each other's engines in contradictory directions. While the
// engine is starting a locked read returns 503; the peer engine's own
// startup read is an admin one.
func (a *API) handleGetStateSnapshot(w http.ResponseWriter, r *http.Request) {
	admin := r.URL.Query().Get("admin") == "true"
	if admin && r.URL.Query().Get("ready") == "true" {
		// An admin read that copies the state on, as a pull for a
		// browser does, refuses a starting engine as a locked read does.
		if sr, ok := a.snapshotProvider.(SnapshotReadiness); ok && !sr.SnapshotReady() {
			httperr.Write(w, http.StatusServiceUnavailable, "engine still starting: its state is not the one to copy")
			return
		}
	}
	if !admin {
		lockID := r.URL.Query().Get("lockId")
		if !a.reconcileLock.isHeldBy(lockID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "no active reconcile lock",
			})
			return
		}
		if sr, ok := a.snapshotProvider.(SnapshotReadiness); ok && !sr.SnapshotReady() {
			httperr.Write(w, http.StatusServiceUnavailable, "engine still starting: its state is not the one to copy")
			return
		}
	}

	// Capture seq FIRST, before the snapshot. A command could execute between
	// these two calls — by reading enrichedState first, the envelope seq is
	// guaranteed to be <= the snapshot's actual state. The receiving engine
	// will see a seq that is at-or-behind the snapshot, never ahead of it.
	state := a.enrichedState()
	snap := a.snapshotProvider.CaptureFullSnapshot()
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "marshal failed")
		return
	}
	slog.Info("state snapshot captured", "bytes", len(snapJSON), "graphicsLayers", len(snap.Graphics))
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
//
// Staleness: when an envelope is provided and its Seq is strictly behind the
// engine's current LastCommandSeq, the request is rejected with HTTP 409 and
// a JSON body {error, snapshot_seq, current_seq}. Applying a stale snapshot
// would silently regress state and drop commands. The `?force=true` query
// parameter overrides the check for admin-driven recovery.
func (a *API) handleApplyStateSnapshot(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Bound the read so a missing/incorrect outer body cap cannot translate
	// into an unbounded allocation when restoring a snapshot.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSnapshotBodySize))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httperr.Write(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("snapshot larger than %d MiB", maxSnapshotBodySize>>20))
		} else {
			httperr.Write(w, http.StatusBadRequest, "could not read body")
		}
		if a.appMetrics != nil {
			a.appMetrics.SnapshotApplyTotal.WithLabelValues("error").Inc()
		}
		return
	}

	// Record snapshot size.
	if a.appMetrics != nil {
		a.appMetrics.SnapshotSizeBytes.Observe(float64(len(raw)))
	}

	var snap internal.FullStateSnapshot
	var snapSeq uint64
	var warnings []string

	force := r.URL.Query().Get("force") == "true"

	// Detect envelope vs raw snapshot by probing for the "snapshot" key.
	var probe struct {
		Snapshot json.RawMessage `json:"snapshot"`
		Seq      uint64          `json:"seq"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.Snapshot != nil {
		// Envelope format.
		snapSeq = probe.Seq
		if err := json.Unmarshal(probe.Snapshot, &snap); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid snapshot JSON")
			if a.appMetrics != nil {
				a.appMetrics.SnapshotApplyTotal.WithLabelValues("error").Inc()
			}
			return
		}
		// Staleness check: reject with 409 if the snapshot's seq is behind the
		// engine's current seq. Applying a stale snapshot would regress state
		// and silently drop commands executed between snapshot capture and
		// apply. `?force=true` is an admin escape hatch (e.g. crash recovery).
		if probe.Seq > 0 {
			currentSeq := a.enrichedState().LastCommandSeq
			if currentSeq > probe.Seq {
				slog.Warn("snapshot apply: stale seq",
					"snapshot_seq", probe.Seq,
					"current_seq", currentSeq,
					"force", force)
				if !force {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":        "snapshot stale",
						"snapshot_seq": probe.Seq,
						"current_seq":  currentSeq,
					})
					if a.appMetrics != nil {
						a.appMetrics.SnapshotApplyTotal.WithLabelValues("stale").Inc()
					}
					return
				}
				slog.Warn("snapshot apply: forcing stale snapshot override",
					"snapshot_seq", probe.Seq,
					"current_seq", currentSeq)
				warnings = append(warnings, fmt.Sprintf("forced apply: snapshot seq %d is behind engine seq %d", probe.Seq, currentSeq))
			}
		}
	} else {
		// Raw FullStateSnapshot (backward compat or direct push).
		if err := json.Unmarshal(raw, &snap); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid snapshot JSON")
			if a.appMetrics != nil {
				a.appMetrics.SnapshotApplyTotal.WithLabelValues("error").Inc()
			}
			return
		}
	}

	// A snapshot of an engine that holds no graphics, or one that names no
	// command sequence, may come from an engine that came up empty and went
	// live alone. Taking it would remove every layer here, so it is refused
	// as stale, and a browser pushes this engine's state the other way.
	if removed := a.graphicsLayersRemovedBy(snap); removed > 0 && (snapSeq == 0 || len(snap.Graphics) == 0) && !force {
		slog.Warn("snapshot apply: refused, it would remove graphics layers",
			"snapshot_seq", snapSeq, "snapshot_layers", len(snap.Graphics), "removed", removed)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":        "snapshot would remove graphics layers",
			"snapshot_seq": snapSeq,
			"current_seq":  a.enrichedState().LastCommandSeq,
		})
		if a.appMetrics != nil {
			a.appMetrics.SnapshotApplyTotal.WithLabelValues("stale").Inc()
		}
		return
	}

	warnings = append(warnings, internal.ApplyFullSnapshot(r.Context(), snap, a.snapshotProvider.ApplyTarget())...)

	// The engine now holds the source's state as of snapSeq. Without taking
	// the seq along, a gap between the engines' seqs outlives the push, and
	// a browser keeps reading the engines as diverged and pushing again.
	// The tracker keeps the higher of the two, so a forced stale apply
	// leaves this engine's seq alone.
	if snapSeq > 0 && a.seqTracker != nil {
		a.seqTracker(snapSeq)
	}

	// Record apply duration and result.
	if a.appMetrics != nil {
		a.appMetrics.SnapshotApplyDuration.Observe(time.Since(start).Seconds())
		if len(warnings) > 0 {
			a.appMetrics.SnapshotApplyTotal.WithLabelValues("partial").Inc()
		} else {
			a.appMetrics.SnapshotApplyTotal.WithLabelValues("success").Inc()
		}
	}

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

// graphicsLayersRemovedBy counts the layers here that applying snap would
// remove.
func (a *API) graphicsLayersRemovedBy(snap internal.FullStateSnapshot) int {
	if !snap.GraphicsContent || a.compositor == nil {
		return 0
	}
	keep := make(map[int]bool, len(snap.Graphics))
	for _, l := range snap.Graphics {
		keep[l.ID] = true
	}
	n := 0
	for _, l := range a.compositor.Status().Layers {
		if !keep[l.ID] {
			n++
		}
	}
	return n
}

// peerSnapshotReadTimeout bounds reading the peer's snapshot for a pull.
const peerSnapshotReadTimeout = 60 * time.Second

// handlePullFromPeer reads the peer engine's state snapshot and applies it
// here, as a browser reconciling the two would by reading it and pushing it:
// the snapshot travels between the engines rather than through the browser.
// ?lockId= is the reconcile lock the caller holds on the peer, without
// which the peer's state is read as an admin read that a starting peer
// refuses too; ?force=true is passed on
// to the apply. The caller's credentials are passed on to the peer. Answers
// as POST /api/sync/state-snapshot does, 503 while the peer is starting
// and 502 when the peer cannot be read.
func (a *API) handlePullFromPeer(w http.ResponseWriter, r *http.Request) {
	if a.peerPoller.PeerURL() == "" {
		httperr.Write(w, http.StatusNotImplemented, "no peer engine configured")
		return
	}
	q := url.Values{}
	if lockID := r.URL.Query().Get("lockId"); lockID != "" {
		q.Set("lockId", lockID)
	} else {
		q.Set("admin", "true")
		q.Set("ready", "true")
	}
	ctx, cancel := context.WithTimeout(r.Context(), peerSnapshotReadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.peerPoller.PeerURL()+"/api/sync/state-snapshot?"+q.Encode(), nil)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := a.peerPoller.HTTPClient().Do(req)
	if err != nil {
		httperr.Write(w, http.StatusBadGateway, fmt.Sprintf("peer snapshot: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		httperr.Write(w, http.StatusServiceUnavailable, "peer engine still starting: its state is not the one to copy")
		return
	default:
		httperr.Write(w, http.StatusBadGateway, fmt.Sprintf("peer snapshot: status %d", resp.StatusCode))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshotBodySize+1))
	if err != nil {
		httperr.Write(w, http.StatusBadGateway, fmt.Sprintf("peer snapshot: %v", err))
		return
	}
	apply := r.Clone(r.Context())
	apply.Body = io.NopCloser(bytes.NewReader(body))
	apply.ContentLength = int64(len(body))
	a.handleApplyStateSnapshot(w, apply)
}

// handleGetGraphicsSequence serves the ZIP the sequence on a layer was
// uploaded as, for the peer engine to load the same one. With ?hash= it is
// served only while the layer holds that sequence.
func (a *API) handleGetGraphicsSequence(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("layer"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	rc, size, err := a.sequenceEngine.OpenSource(id, r.URL.Query().Get("hash"))
	if err != nil {
		httperr.Write(w, http.StatusNotFound, "no such sequence on the layer")
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(w, rc)
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
