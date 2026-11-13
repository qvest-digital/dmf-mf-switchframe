package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// CompareState is what an engine's state is compared on with its peer's,
// served at GET /api/sync/state-compare.
type CompareState struct {
	// Ready is set once the engine has come on air after its start.
	Ready bool `json:"ready"`

	// Seq is the highest command sequence the engine took.
	Seq uint64 `json:"seq"`

	// StartedAtMs is when the engine started, ChangedAtMs when its
	// compared state last changed (its start until it first does), Unix
	// milliseconds. Both engines compare the same two values, so each
	// comes to the same conclusion.
	StartedAtMs int64 `json:"startedAtMs"`
	ChangedAtMs int64 `json:"changedAtMs"`

	// InTransition is set while a transition runs.
	InTransition bool `json:"inTransition,omitempty"`

	// Reconciling is set while a browser copies state between the
	// engines, and shortly after.
	Reconciling bool `json:"reconciling,omitempty"`

	// Snapshot is the engine's state in internal.ComparableSnapshot form.
	Snapshot internal.FullStateSnapshot `json:"snapshot"`
}

// ConvergeConfig configures a Converger.
type ConvergeConfig struct {
	// Token is the bearer token the peer's API takes.
	Token string

	// Tick is how often the two states are compared.
	Tick time.Duration

	// Settle is how long the two states have to differ, unchanged, before
	// the engine takes its peer's. Longer than a browser takes to
	// reconcile them, so with a browser open the browser does.
	Settle time.Duration

	// Backoff is the least time between two applies; it doubles with each
	// apply that leaves the states apart, up to MaxBackoff.
	Backoff, MaxBackoff time.Duration

	// StartTie is how close the engines' start times, and ChangeTie how
	// close the times their states last changed, count as equal.
	StartTie, ChangeTie time.Duration
}

// ConvergeStats is what a Converger observed and did.
type ConvergeStats struct {
	// Diverged is set while the two states differ; Fields names where.
	Diverged bool
	Fields   []string

	// Divergences counts the times the states came to differ, Applies
	// the peer states taken, ApplyBytes their size, Failures the applies
	// that failed.
	Divergences, Applies, ApplyBytes, Failures int64

	// Unresolved counts divergences an apply left in place.
	Unresolved int64
}

// Converger keeps an engine's state in step with its peer's without a
// browser: it compares the two every tick and, once they have differed
// unchanged for the settle time, the engine that does not hold authority
// over the difference takes the other's state. Only one of the two acts on
// any one difference, since both decide authority alike, and an engine
// only ever pulls.
type Converger struct {
	p     *HealthPoller
	cfg   ConvergeConfig
	local func() CompareState
	apply ApplyFunc
	now   func() time.Time

	// Owned by the loop.
	sig            string
	since          time.Time
	appliedSig     string
	nextApply      time.Time
	backoff        time.Duration
	unsupported    bool
	failing        string
	reportedUnheld string

	mu          sync.Mutex
	diverged    bool
	fields      []string
	divergences atomic.Int64
	applies     atomic.Int64
	applyBytes  atomic.Int64
	failures    atomic.Int64
	unresolved  atomic.Int64
}

// ApplyFunc replaces the engine's state with snap, captured at command
// sequence seq, and returns what it could not apply.
type ApplyFunc func(ctx context.Context, snap internal.FullStateSnapshot, seq uint64) []string

// NewConverger returns a Converger for the engine whose state local reads
// and apply replaces with its peer's.
func NewConverger(p *HealthPoller, cfg ConvergeConfig, local func() CompareState, apply ApplyFunc) *Converger {
	return &Converger{p: p, cfg: cfg, local: local, apply: apply, now: time.Now, backoff: cfg.Backoff}
}

// Stats returns what the converger observed and did.
func (c *Converger) Stats() ConvergeStats {
	if c == nil {
		return ConvergeStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConvergeStats{
		Diverged:    c.diverged,
		Fields:      append([]string(nil), c.fields...),
		Divergences: c.divergences.Load(),
		Applies:     c.applies.Load(),
		ApplyBytes:  c.applyBytes.Load(),
		Failures:    c.failures.Load(),
		Unresolved:  c.unresolved.Load(),
	}
}

// Run compares the states every tick until ctx ends.
func (c *Converger) Run(ctx context.Context) {
	t := time.NewTicker(c.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Step(ctx)
		}
	}
}

// peerHoldsAuthority reports whether the peer's state is the one both
// engines keep where they differ. The engine that took the later command
// holds it; at the same command the one started earlier, which kept the
// production while the other was away, an engine that went live alone
// after a restart being the newer one; at about the same start the one
// whose state changed later, as by a write that reached only it (not every
// change carries a command sequence); and else the leader.
func peerHoldsAuthority(local, peer CompareState, localLeads bool, startTie, changeTie time.Duration) bool {
	if peer.Seq != local.Seq {
		return peer.Seq > local.Seq
	}
	if d := time.Duration(local.StartedAtMs-peer.StartedAtMs) * time.Millisecond; d > startTie || d < -startTie {
		return d > 0
	}
	if local.ChangedAtMs != 0 && peer.ChangedAtMs != 0 {
		if d := time.Duration(peer.ChangedAtMs-local.ChangedAtMs) * time.Millisecond; d > changeTie || d < -changeTie {
			return d > 0
		}
	}
	return !localLeads
}

// Step compares the two states once and takes the peer's where it is due.
func (c *Converger) Step(ctx context.Context) {
	local := c.local()
	if !local.Ready || !c.p.PeerReachable() {
		// Starting: RunStartup takes the peer's state. Alone: nothing to
		// converge with.
		c.reset()
		return
	}
	peerState, err := c.p.FetchCompareState(ctx, c.cfg.Token)
	if err != nil {
		if errors.Is(err, errNotFound) {
			if !c.unsupported {
				c.unsupported = true
				slog.Warn("peer converge: the peer does not serve its state for comparison; the engines converge only through a browser",
					"peerURL", c.p.peerURL)
			}
		} else if err.Error() != c.failing {
			c.failing = err.Error()
			slog.Warn("peer converge: peer state not read", "peerURL", c.p.peerURL, "error", err)
		}
		return
	}
	c.unsupported, c.failing = false, ""

	fields := internal.SnapshotDiff(local.Snapshot, peerState.Snapshot)
	if len(fields) == 0 {
		c.mu.Lock()
		was := c.diverged
		c.diverged, c.fields = false, nil
		c.mu.Unlock()
		if was {
			slog.Info("peer converge: state agrees with the peer again")
		}
		c.sig, c.appliedSig, c.reportedUnheld = "", "", ""
		c.backoff = c.cfg.Backoff
		return
	}

	c.mu.Lock()
	was := c.diverged
	c.diverged, c.fields = true, fields
	c.mu.Unlock()
	if !was {
		c.divergences.Add(1)
		slog.Warn("peer converge: state differs from the peer's", "fields", strings.Join(fields, ","),
			"seq", local.Seq, "peerSeq", peerState.Seq)
	}

	now := c.now()
	// The difference itself, so that what both take alike meanwhile
	// neither restarts the settle time nor makes a difference an apply
	// cannot repair look new.
	sig := internal.DiffDigest(local.Snapshot, peerState.Snapshot, fields)
	if sig != c.sig {
		// Still changing: a command reaching the two at different
		// moments, or one applied meanwhile.
		c.sig, c.since = sig, now
		return
	}
	if now.Sub(c.since) < c.cfg.Settle {
		return
	}
	if !peerHoldsAuthority(local, peerState, c.p.Role() == RoleLeader, c.cfg.StartTie, c.cfg.ChangeTie) {
		// The peer takes this engine's state.
		return
	}
	switch {
	case !peerState.Ready || peerState.Snapshot.ProgramSource == "":
		// A starting or dark engine holds no production to copy.
		c.reportUnheld(sig, "the peer is starting or off air")
		return
	case local.InTransition || peerState.InTransition:
		return
	case local.Reconciling || peerState.Reconciling:
		// A browser is copying state between the engines.
		return
	case now.Before(c.nextApply):
		if sig == c.appliedSig {
			c.reportUnheld(sig, "taking the peer's state left this difference in place; taking it again after the backoff")
		}
		return
	}

	// Each apply, and each failed one, waits longer for the next, so a
	// difference an apply cannot repair is tried again ever more rarely
	// rather than given up on.
	c.nextApply = now.Add(c.backoff)
	c.backoff = min(2*c.backoff, c.cfg.MaxBackoff)
	if sig == c.appliedSig {
		c.unresolved.Add(1)
	}
	c.appliedSig = sig
	n, warnings, err := c.pull(ctx, local.Seq)
	if err != nil {
		c.failures.Add(1)
		slog.Warn("peer converge: peer state not taken", "error", err, "retryIn", c.nextApply.Sub(now))
		return
	}
	c.applies.Add(1)
	c.applyBytes.Add(n)
	if len(warnings) > 0 {
		c.failures.Add(1)
		slog.Warn("peer converge: took the peer's state in part", "fields", strings.Join(fields, ","),
			"warnings", warnings, "bytes", n, "retryIn", c.nextApply.Sub(now))
		return
	}
	slog.Info("peer converge: took the peer's state", "fields", strings.Join(fields, ","),
		"seq", local.Seq, "peerSeq", peerState.Seq, "bytes", n)
}

// reportUnheld logs once per difference why the engine leaves it.
func (c *Converger) reportUnheld(sig, why string) {
	if c.reportedUnheld == sig {
		return
	}
	c.reportedUnheld = sig
	c.mu.Lock()
	fields := strings.Join(c.fields, ",")
	c.mu.Unlock()
	slog.Warn("peer converge: state still differs from the peer's", "fields", fields, "reason", why)
}

// pull reads the peer's full state and applies it, unless this engine took
// a command since localSeq that the peer's state predates, or a transition
// or a browser's reconcile started meanwhile. It returns what the apply
// could not take.
func (c *Converger) pull(ctx context.Context, localSeq uint64) (int64, []string, error) {
	snap, seq, n, err := c.p.fetchStateSnapshot(ctx, c.cfg.Token)
	if err != nil {
		return 0, nil, err
	}
	cur := c.local()
	if cur.Seq > seq || !cur.Ready || cur.InTransition || cur.Reconciling {
		return 0, nil, fmt.Errorf("local state moved on (seq %d, peer state at %d)", cur.Seq, seq)
	}
	// As a pushed snapshot is: one with no graphics, or naming no command,
	// may be an engine that came up empty, and taking it would remove
	// every layer here.
	if removed := layersRemovedBy(cur.Snapshot, snap); removed > 0 && (seq == 0 || len(snap.Graphics) == 0) {
		return 0, nil, fmt.Errorf("the peer's state would remove %d graphics layers and names no command or no layer", removed)
	}
	return n, c.apply(ctx, snap, seq), nil
}

// layersRemovedBy counts the graphics layers of local that taking snap
// removes.
func layersRemovedBy(local, snap internal.FullStateSnapshot) int {
	if !snap.GraphicsContent {
		return 0
	}
	keep := make(map[int]bool, len(snap.Graphics))
	for _, l := range snap.Graphics {
		keep[l.ID] = true
	}
	n := 0
	for _, l := range local.Graphics {
		if !keep[l.ID] {
			n++
		}
	}
	return n
}

func (c *Converger) reset() {
	c.mu.Lock()
	c.diverged, c.fields = false, nil
	c.mu.Unlock()
	c.sig, c.appliedSig, c.reportedUnheld = "", "", ""
}

// FetchCompareState reads the peer's state as it is compared.
func (p *HealthPoller) FetchCompareState(ctx context.Context, token string) (CompareState, error) {
	var st CompareState
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.peerURL+"/api/sync/state-compare", nil)
	if err != nil {
		return st, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return st, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return st, errNotFound
	default:
		return st, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSnapshotBytes)).Decode(&st); err != nil {
		return st, fmt.Errorf("decode: %w", err)
	}
	return st, nil
}
