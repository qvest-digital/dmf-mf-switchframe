package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// maxSnapshotBytes bounds the peer's state snapshot read at startup.
const maxSnapshotBytes = 16 << 20

// StartupEngine is what RunStartup needs of the local engine.
type StartupEngine interface {
	// ProgramSource returns the source on air, "" while none is.
	ProgramSource() string

	// ApplyPeerSnapshot takes over the peer's state, captured at command
	// sequence seq.
	ApplyPeerSnapshot(ctx context.Context, snap internal.FullStateSnapshot, seq uint64)

	// Cut puts source on air.
	Cut(ctx context.Context, source string) error

	// GoLive puts the engine's own initial program source on air. It
	// returns false while no source is available yet, and is called again.
	GoLive(ctx context.Context) bool
}

type startupStep int

const (
	stepWait startupStep = iota
	stepFollow
	stepGoLive
	stepDone
)

// nextStartupStep decides what an engine that has just started does next.
//
// A peer on air is followed whatever the labels say: an engine that comes
// back takes over the production the other one kept running, so a returning
// engine a follows b too. Following is done once a peer poll after the last
// apply shows both engines on the same source; a peer that cut meanwhile,
// or finished a transition the snapshot caught half way, is followed again.
//
// With no peer on air, an engine already on air (an operator cut first) is
// done. Otherwise the engine that leads goes live with a source of its own,
// and the other waits for it and then follows. An engine whose peer is down
// goes live alone, so a production never stays dark for want of a peer.
//
// synced is when the peer's state was last applied, zero before.
func nextStartupStep(local string, st PeerStatus, peerDown, localLeads bool, synced time.Time) startupStep {
	if st.Reachable && st.ProgramSource != "" {
		switch {
		case synced.IsZero():
			return stepFollow
		case !st.LastSeen.After(synced):
			return stepWait
		case st.ProgramSource == local:
			return stepDone
		default:
			return stepFollow
		}
	}
	if local != "" {
		return stepDone
	}
	if peerDown || (st.Reachable && localLeads) {
		return stepGoLive
	}
	return stepWait
}

// RunStartup brings the engine on air once after it starts, without an
// operator cut: on the program of a peer already on air, else on a source of
// its own. p nil is a single engine, which goes live at once. token is the
// bearer token the peer's API takes; both engines of a pair share one. It
// returns when the engine is on air or ctx ends.
func RunStartup(ctx context.Context, p *HealthPoller, eng StartupEngine, token string, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	var synced time.Time
	for {
		var st PeerStatus
		peerDown, localLeads := true, false
		if p != nil {
			st = p.Status()
			peerDown = p.PeerDown()
			localLeads = p.Role() == RoleLeader
		}

		switch nextStartupStep(eng.ProgramSource(), st, peerDown, localLeads, synced) {
		case stepDone:
			slog.Info("startup: on air", "programSource", eng.ProgramSource())
			return
		case stepFollow:
			synced = time.Now()
			snap, seq, err := p.FetchStateSnapshot(ctx, token)
			if err != nil {
				// The program still follows, from the peer's health, when
				// the rest of its state cannot be read.
				slog.Warn("startup: peer state snapshot failed; following the peer's program source alone",
					"peerURL", p.peerURL, "programSource", st.ProgramSource, "error", err)
				if err := eng.Cut(ctx, st.ProgramSource); err != nil {
					slog.Warn("startup: cut to the peer's program failed", "programSource", st.ProgramSource, "error", err)
				}
				break
			}
			slog.Info("startup: following the peer's program",
				"programSource", snap.ProgramSource, "seq", seq)
			eng.ApplyPeerSnapshot(ctx, snap, seq)
		case stepGoLive:
			if eng.GoLive(ctx) {
				slog.Info("startup: on air with no peer to follow", "programSource", eng.ProgramSource())
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PeerDown reports whether the peer has failed enough consecutive polls to
// count as unreachable, including one never reached since start. False
// while the first polls are still out.
func (p *HealthPoller) PeerDown() bool {
	if p == nil {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.reachable && p.consecFailures >= p.failureThreshold
}

// FetchStateSnapshot reads the peer's full state snapshot and the command
// sequence it was captured at. It reads past the peer's reconcile lock
// (admin=true): the lock serializes browsers pushing state between engines,
// and this only reads the peer to apply its state here.
func (p *HealthPoller) FetchStateSnapshot(ctx context.Context, token string) (internal.FullStateSnapshot, uint64, error) {
	var env struct {
		Snapshot internal.FullStateSnapshot `json:"snapshot"`
		Seq      uint64                     `json:"seq"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.peerURL+"/api/sync/state-snapshot?admin=true", nil)
	if err != nil {
		return env.Snapshot, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return env.Snapshot, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return env.Snapshot, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSnapshotBytes)).Decode(&env); err != nil {
		return env.Snapshot, 0, fmt.Errorf("decode: %w", err)
	}
	return env.Snapshot, env.Seq, nil
}
