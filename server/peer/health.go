// Package peer provides inter-engine health monitoring for active-active
// redundancy. Each engine polls its peer's /api/peer/health endpoint to
// detect failures before the control plane's 30s health cycle notices.
package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// HealthResponse is the JSON payload returned by /api/peer/health.
//
// EngineLabel and Epoch carry the responding engine's active-active identity.
// Empty EngineLabel means the peer is running in single-engine (standalone)
// mode; Role computation treats it as the conservative follower case.
type HealthResponse struct {
	Status         string `json:"status"`
	UptimeMs       int64  `json:"uptimeMs"`
	ProgramSource  string `json:"programSource"`
	LastCommandSeq uint64 `json:"lastCommandSeq"`
	EngineLabel    string `json:"engineLabel,omitempty"`
	Epoch          uint64 `json:"epoch,omitempty"`
}

// PeerStatus holds the current peer health state, safe to read from any
// goroutine via HealthPoller.Status().
type PeerStatus struct {
	Reachable      bool
	LastSeen       time.Time
	ConsecFailures int
	ProgramSource  string
	LastCommandSeq uint64
	UptimeMs       int64
	PeerLabel      string
	PeerEpoch      uint64
}

// Option configures a HealthPoller.
type Option func(*HealthPoller)

// WithPollInterval sets the polling interval (default 5s).
func WithPollInterval(d time.Duration) Option {
	return func(p *HealthPoller) { p.pollInterval = d }
}

// WithFailureThreshold sets the number of consecutive failures before the
// peer is considered unreachable (default 3).
func WithFailureThreshold(n int) Option {
	return func(p *HealthPoller) { p.failureThreshold = n }
}

// WithHTTPClient sets a custom HTTP client for peer health requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *HealthPoller) { p.client = c }
}

// WithLocalIdentity sets the local engine label used by Role() to decide
// leader/follower status. Pass "" for standalone (Role always returns
// RoleStandalone and Gate() is a no-op).
func WithLocalIdentity(label string) Option {
	return func(p *HealthPoller) { p.localLabel = label }
}

// WithInitialEpoch seeds the local epoch at construction. Callers restoring
// persisted epoch after restart should pass the prior value so force-
// takeover state survives process death.
func WithInitialEpoch(e uint64) Option {
	return func(p *HealthPoller) { p.localEpoch.Store(e) }
}

// HealthPoller periodically polls a peer engine's health endpoint and
// tracks whether the peer is reachable.
type HealthPoller struct {
	peerURL          string
	pollInterval     time.Duration
	failureThreshold int
	client           *http.Client

	// localLabel is immutable after construction. Empty means standalone.
	localLabel string

	// localEpoch is bumped by BumpEpoch and read by Role. Lock-free.
	localEpoch atomic.Uint64

	// coldStartWarnOnce fires a one-time log when the poller has failed
	// enough times to declare the peer unreachable AND has never heard
	// from the peer. Intended to surface misconfigured --peer-engine-url.
	coldStartWarnOnce sync.Once

	mu             sync.RWMutex
	reachable      bool
	lastSeen       time.Time
	consecFailures int
	programSource  string
	lastCommandSeq uint64
	uptimeMs       int64
	peerLabel      string
	peerEpoch      uint64

	cancel  context.CancelFunc
	stopped atomic.Bool
	done    chan struct{}
}

// NewHealthPoller creates a new poller for the given peer URL. If peerURL
// is empty, the poller does nothing (no peer configured).
func NewHealthPoller(peerURL string, opts ...Option) *HealthPoller {
	p := &HealthPoller{
		peerURL:          peerURL,
		pollInterval:     5 * time.Second,
		failureThreshold: 3,
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		done: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start begins polling the peer. No-op if no peer URL is configured.
func (p *HealthPoller) Start() {
	if p.peerURL == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.loop(ctx)
}

// Stop halts the polling loop. Safe to call multiple times.
func (p *HealthPoller) Stop() {
	if !p.stopped.CompareAndSwap(false, true) {
		return // Already stopped or never started.
	}
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
}

// PeerReachable returns true if the peer engine is responding to health checks.
func (p *HealthPoller) PeerReachable() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.reachable
}

// PeerLastSeen returns the last time the peer responded successfully.
func (p *HealthPoller) PeerLastSeen() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastSeen
}

// Status returns a snapshot of the current peer status.
func (p *HealthPoller) Status() PeerStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return PeerStatus{
		Reachable:      p.reachable,
		LastSeen:       p.lastSeen,
		ConsecFailures: p.consecFailures,
		ProgramSource:  p.programSource,
		LastCommandSeq: p.lastCommandSeq,
		UptimeMs:       p.uptimeMs,
		PeerLabel:      p.peerLabel,
		PeerEpoch:      p.peerEpoch,
	}
}

// LocalLabel returns this engine's configured label ("", "a", or "b").
func (p *HealthPoller) LocalLabel() string {
	if p == nil {
		return ""
	}
	return p.localLabel
}

// LocalEpoch returns the current local epoch, incremented by each BumpEpoch.
func (p *HealthPoller) LocalEpoch() uint64 {
	if p == nil {
		return 0
	}
	return p.localEpoch.Load()
}

// BumpEpoch atomically increments the local epoch and returns the new value.
// Used by the control-plane-authenticated force-leader endpoint (Phase 3).
// A nil receiver returns 0 — consistent with LocalEpoch's nil-safety so the
// force-leader handler can nil-check via the return path.
func (p *HealthPoller) BumpEpoch() uint64 {
	if p == nil {
		return 0
	}
	return p.localEpoch.Add(1)
}

// Role reports this engine's current authority class for non-idempotent
// commands. See the Role type comment for the tiebreak rules.
//
// A nil receiver returns RoleStandalone — callers can therefore skip a nil
// check on code paths where the poller is optional.
func (p *HealthPoller) Role() Role {
	if p == nil || p.localLabel == "" {
		return RoleStandalone
	}

	p.mu.RLock()
	peerReachable := p.reachable
	peerLabel := p.peerLabel
	peerEpoch := p.peerEpoch
	// Load localEpoch inside the RLock region so the Role snapshot is
	// linearizable relative to recordSuccess's write of peerEpoch —
	// otherwise a concurrent BumpEpoch can cause transient role flicker.
	localEpoch := p.localEpoch.Load()
	p.mu.RUnlock()

	// Epoch precedes label in the tiebreak — a forced-leader bump dominates
	// the default lexical ordering whether we're reachable or partitioned.
	if localEpoch > peerEpoch {
		return RoleLeader
	}
	if peerEpoch > localEpoch {
		if peerReachable {
			return RoleFollowerHealthy
		}
		return RoleFollowerPartitioned
	}

	// Equal epochs: lexical label wins. "a" < "b".
	if peerReachable {
		if p.localLabel < peerLabel {
			return RoleLeader
		}
		return RoleFollowerHealthy
	}

	// Partition with no peer label yet → we've never heard from peer.
	// Default to follower so we don't double-fire non-idempotent commands
	// against an assumed-dead peer that might actually be healthy and we're
	// the one who's partitioned.
	if peerLabel == "" {
		return RoleFollowerPartitioned
	}
	if p.localLabel < peerLabel {
		return RoleLeader
	}
	return RoleFollowerPartitioned
}

// SetPeerStateForTest lets tests inject peer-poll results without running
// the HTTP loop. Exported (with Test in the name) so tests in other
// packages — control/ in particular — can exercise the role-dependent
// handlers without standing up an httptest peer.
func (p *HealthPoller) SetPeerStateForTest(reachable bool, label string, epoch uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reachable = reachable
	p.peerLabel = label
	p.peerEpoch = epoch
}

func (p *HealthPoller) loop(ctx context.Context) {
	defer close(p.done)

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Poll immediately on start.
	p.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *HealthPoller) poll(ctx context.Context) {
	url := p.peerURL + "/api/peer/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		p.recordFailure(fmt.Sprintf("create request: %v", err))
		return
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.recordFailure(fmt.Sprintf("request failed: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		p.recordFailure(fmt.Sprintf("status %d", resp.StatusCode))
		return
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		p.recordFailure(fmt.Sprintf("decode: %v", err))
		return
	}

	p.recordSuccess(health)
}

func (p *HealthPoller) recordSuccess(health HealthResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()

	wasUnreachable := !p.reachable && p.consecFailures > 0
	p.reachable = true
	p.lastSeen = time.Now()
	p.consecFailures = 0
	p.programSource = health.ProgramSource
	p.lastCommandSeq = health.LastCommandSeq
	p.uptimeMs = health.UptimeMs
	p.peerLabel = health.EngineLabel
	p.peerEpoch = health.Epoch

	if wasUnreachable {
		slog.Info("peer engine recovered",
			"url", p.peerURL,
			"peerLabel", health.EngineLabel,
			"peerEpoch", health.Epoch,
			"localEpoch", p.localEpoch.Load())
	}

	// Misconfiguration guard: both engines reporting the same label
	// collapses the lexical tiebreak (both become followers under
	// partition, both become leaders with swapped labels). Surface
	// loudly so ops can fix before Phase 2 gating strands commands.
	if p.localLabel != "" && health.EngineLabel == p.localLabel {
		slog.Error("peer reports same engine label as local — active-active tiebreak will collapse; fix --engine-label on one engine",
			"localLabel", p.localLabel,
			"peerURL", p.peerURL)
	}
}

func (p *HealthPoller) recordFailure(reason string) {
	p.mu.Lock()
	p.consecFailures++
	justWentDown := p.consecFailures >= p.failureThreshold && p.reachable
	if justWentDown {
		p.reachable = false
		slog.Warn("peer engine unreachable",
			"url", p.peerURL,
			"consecutiveFailures", p.consecFailures,
			"reason", reason,
		)
	}
	// peerLabel / peerEpoch are intentionally NOT reset on transition
	// to unreachable — Role() uses their last-known values to preserve
	// the forced-takeover outcome through a partition.
	neverSeenPeer := p.peerLabel == "" && p.consecFailures >= p.failureThreshold && p.localLabel != ""
	p.mu.Unlock()

	// Cold-start misconfiguration warn. Fires once per process lifetime
	// when we're configured with a label + peer URL but have never
	// successfully polled — usually a bad --peer-engine-url.
	if neverSeenPeer {
		p.coldStartWarnOnce.Do(func() {
			slog.Warn("peer never reached on startup — non-idempotent REST endpoints (recording.start, output.srt.start) will return 409 on this engine until the peer responds; check --peer-engine-url or force-promote via POST /api/peer/force-leader",
				"peerURL", p.peerURL,
				"localLabel", p.localLabel)
		})
	}
}
