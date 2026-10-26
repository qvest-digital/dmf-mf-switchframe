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

// maxStartupFollows bounds how often an engine applies its peer's state
// without ending on the peer's program, as when that source does not exist
// here: each apply repeats its side effects (FTB, playout seeks, outputs).
const maxStartupFollows = 5

// StartupConfig configures RunStartup.
type StartupConfig struct {
	// Token is the bearer token the peer's API takes; both engines of a
	// pair share one.
	Token string

	// Tick is how often the engine's and the peer's state are looked at.
	Tick time.Duration

	// LeaderWait bounds how long an engine that does not lead waits for a
	// reachable leader to go on air before it goes live on its own.
	LeaderWait time.Duration
}

// startupState is what nextStartupStep decides on.
type startupState struct {
	local      string // the local program source
	peer       PeerStatus
	peerDown   bool
	localLeads bool
	waitedOut  bool      // LeaderWait is over
	synced     time.Time // when the peer's state was last applied, zero before
	live       time.Time // when the engine went live on its own source, zero before
}

// nextStartupStep decides what an engine that has just started does next.
//
// A peer on air is followed whatever the labels say: an engine that comes
// back takes over the production the other one kept running, so a returning
// engine a follows b too. Following is done once a peer poll sent after the
// last apply shows both engines on the same source; a peer that cut
// meanwhile, or finished a transition the snapshot caught half way, is
// followed again.
//
// With no peer on air, an engine already on air (an operator cut first) is
// done. Otherwise the engine that leads goes live with a source of its own,
// and the other waits for it and then follows. An engine whose peer is down,
// or whose leader stays dark past LeaderWait, goes live alone, so a
// production never stays dark for want of a peer.
//
// A peer counted down or seen dark when the engine went live may have gone
// live alongside it, on another source. A poll sent after going live settles
// it: an engine that leads, or whose peer does not answer, is done, and one
// that does not lead follows its leader once that is on air, waiting while
// it is still dark.
func nextStartupStep(s startupState) startupStep {
	peerOnAir := s.peer.Reachable && s.peer.ProgramSource != ""
	if !s.live.IsZero() {
		answered := s.peer.SeenPolledAt.Equal(s.peer.PolledAt)
		switch {
		case !s.peer.PolledAt.After(s.live):
			return stepWait
		case !answered || s.localLeads:
			return stepDone
		case s.peer.ProgramSource == "":
			return stepWait
		case s.peer.ProgramSource != s.local:
			return stepFollow
		default:
			return stepDone
		}
	}
	if peerOnAir {
		switch {
		case s.synced.IsZero():
			return stepFollow
		case !s.peer.SeenPolledAt.After(s.synced):
			return stepWait
		case s.peer.ProgramSource == s.local:
			return stepDone
		default:
			return stepFollow
		}
	}
	if s.local != "" {
		return stepDone
	}
	if s.peerDown || (s.peer.Reachable && (s.localLeads || s.waitedOut)) {
		return stepGoLive
	}
	return stepWait
}

// RunStartup brings the engine on air once after it starts, without an
// operator cut: on the program of a peer already on air, else on a source of
// its own. p nil is a single engine, which goes live at once. It returns when
// the engine is on air or ctx ends.
func RunStartup(ctx context.Context, p *HealthPoller, eng StartupEngine, cfg StartupConfig) {
	ticker := time.NewTicker(cfg.Tick)
	defer ticker.Stop()

	start := time.Now()
	var synced, live time.Time
	follows := 0
	gaveUp := false
	for {
		step := stepGoLive
		if p != nil && !gaveUp {
			step = nextStartupStep(startupState{
				local:      eng.ProgramSource(),
				peer:       p.Status(),
				peerDown:   p.PeerDown(),
				localLeads: p.Role() == RoleLeader,
				waitedOut:  time.Since(start) >= cfg.LeaderWait,
				synced:     synced,
				live:       live,
			})
		}
		if step == stepFollow && follows >= maxStartupFollows {
			slog.Error("startup: the peer's program is not on air here after following it; going on without the peer",
				"follows", follows, "programSource", eng.ProgramSource())
			gaveUp = true
			continue
		}

		switch step {
		case stepDone:
			slog.Info("startup: on air", "programSource", eng.ProgramSource())
			return
		case stepFollow:
			follows++
			synced, live = time.Now(), time.Time{}
			peerProgram := p.Status().ProgramSource
			snap, seq, err := p.FetchStateSnapshot(ctx, cfg.Token)
			if err != nil {
				// The program still follows, from the peer's health, when
				// the rest of its state cannot be read.
				slog.Warn("startup: peer state snapshot failed; following the peer's program source alone",
					"peerURL", p.peerURL, "programSource", peerProgram, "error", err)
				if err := eng.Cut(ctx, peerProgram); err != nil {
					slog.Warn("startup: cut to the peer's program failed", "programSource", peerProgram, "error", err)
				}
				break
			}
			slog.Info("startup: following the peer's program",
				"programSource", snap.ProgramSource, "seq", seq)
			eng.ApplyPeerSnapshot(ctx, snap, seq)
		case stepGoLive:
			if gaveUp && eng.ProgramSource() != "" {
				return
			}
			if eng.GoLive(ctx) {
				slog.Info("startup: on air on its own source", "programSource", eng.ProgramSource())
				if p == nil || gaveUp {
					return
				}
				live = time.Now()
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
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return env.Snapshot, 0, fmt.Errorf("status %d: the peer does not take this engine's API token, and both engines need the same one", resp.StatusCode)
	default:
		return env.Snapshot, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSnapshotBytes)).Decode(&env); err != nil {
		return env.Snapshot, 0, fmt.Errorf("decode: %w", err)
	}
	return env.Snapshot, env.Seq, nil
}
