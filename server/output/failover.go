package output

import (
	"errors"
	"slices"
	"sort"
	"time"
)

// PeerView is what an engine of a pair knows of the other one, from its
// peer health polls.
type PeerView struct {
	// Known is set once the peer answered a poll, or stayed silent for the
	// failure threshold.
	Known bool
	// Down is set while the peer has stayed silent for the failure
	// threshold.
	Down bool
	// Label is the peer's engine label.
	Label string
	// Sending lists the destinations whose adapter runs on the peer.
	Sending []string
	// Armed lists the destinations an operator started on the peer, and
	// Destinations every destination the peer holds, of those one engine
	// of a pair sends.
	Armed        []string
	Destinations []string
	// SeenPolledAt is when the poll that reported the peer's lists was
	// sent: they are no older than that.
	SeenPolledAt time.Time
	// Uptime is how long the peer process had run when it answered; a
	// smaller value than before means it restarted.
	Uptime time.Duration
	// LocalLeads is set when this engine holds the pair's leader role.
	LocalLeads bool
	// Grace is how long an armed destination may go unsent by a reachable
	// peer that owns it before this engine takes it over.
	Grace time.Duration
	// Skew is how far apart the two legs of an operator's change sent to
	// both engines may land, PairChangeSkew outside tests. A poll sent
	// within it after an operator's change here may show the peer before
	// its own leg: it is not read as the peer keeping a destination
	// sending, or having stopped it.
	Skew time.Duration
}

// PairChangeSkew is how far apart the UI's two legs of an operator's
// change may land. fanOutMutation in ui/src/lib/api/switch-api.ts waits
// for both legs, each aborted after ENGINE_FETCH_TIMEOUT_MS (3 s), and
// retryFailedEngines sends a failed leg again 500 ms later with another
// 3 s: a retried leg lands up to 6.5 s after the first. A leg landing
// later than PairChangeSkew, such as an aborted request the server still
// handles afterwards, is not covered.
const PairChangeSkew = 7 * time.Second

func (v PeerView) sends(id string) bool {
	return v.Known && !v.Down && slices.Contains(v.Sending, id)
}

// freshFor reports whether the peer's lists were taken after this engine
// last changed d, so they answer the change rather than predate it. Holds
// d.mu.
func (v PeerView) freshFor(d *Destination) bool {
	return v.Known && !v.Down && v.SeenPolledAt.After(d.changedAt)
}

// settledFor reports whether the peer's lists answer this engine's last
// change of d and were taken more than v.Skew after an operator last
// changed it here, so the peer's own leg of that change has landed too.
// A change this engine made on its own has no second leg to wait for.
// Holds d.mu.
func (v PeerView) settledFor(d *Destination) bool {
	return v.freshFor(d) && v.SeenPolledAt.After(d.operatorChangedAt.Add(v.Skew))
}

// SetPeerProbe installs a fresh read of the peer that the manager makes
// right before it starts a destination on its own, such as on a takeover
// or from a peer's snapshot: a poll may be seconds old, and the peer may
// have started sending it since. ok is false when the peer did not answer.
func (m *Manager) SetPeerProbe(fn func() (v PeerView, ok bool)) {
	m.mu.Lock()
	m.peerProbe = fn
	m.mu.Unlock()
}

// ownedLocked reports whether dest has one sender among the engines of a
// pair. A listener does not: each engine listens on its own address and a
// receiver picks one.
func (m *Manager) ownedLocked(d *Destination) bool {
	return m.engineLabel != "" && d.config.Engine != "" && d.config.Type != "srt-listener"
}

// sendsLocked reports whether this engine runs dest's adapter while it is
// armed. Holds m.mu and d.mu.
func (m *Manager) sendsLocked(d *Destination) bool {
	return !m.ownedLocked(d) || d.config.Engine == m.engineLabel
}

// EnablePeerFailover makes the manager wait for a peer view before it starts
// a destination it was not started for by an operator, such as one
// --outputs-config declares: the peer may have taken it over while this
// engine was away. Call it before ApplyStartup when a peer is configured.
func (m *Manager) EnablePeerFailover() {
	m.mu.Lock()
	m.peerFailover = true
	m.mu.Unlock()
}

// SendingDestinationIDs lists the destinations whose adapter runs here and
// that one engine of a pair sends, for the peer to read.
func (m *Manager) SendingDestinationIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, d := range m.destinations {
		d.mu.Lock()
		if d.active && m.ownedLocked(d) {
			ids = append(ids, id)
		}
		d.mu.Unlock()
	}
	sort.Strings(ids)
	return ids
}

// PairDestinationIDs lists the destinations one engine of a pair sends, and
// of those the ones an operator started, for the peer to read.
func (m *Manager) PairDestinationIDs() (all, armed []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, d := range m.destinations {
		d.mu.Lock()
		if m.ownedLocked(d) {
			all = append(all, id)
			if d.armed {
				armed = append(armed, id)
			}
		}
		d.mu.Unlock()
	}
	sort.Strings(all)
	sort.Strings(armed)
	return all, armed
}

// ReconcileDestinations moves destinations between the engines of a pair
// from the latest view of the peer, and starts or stops adapters to match:
//
//   - a destination this engine owns but the peer sends goes to the peer: the
//     peer took it over while this engine was away. When both send it, as
//     after a partition, the leader keeps it.
//   - a destination on standby here that the peer holds stopped is stopped
//     here too, and one the peer process held and no longer does is
//     deleted: the operator's stop or delete reached the peer only.
//   - an armed destination the peer owns is taken over once the peer is
//     down, or reachable but neither sending nor holding it for v.Grace,
//     as when it lost its state in a restart.
//
// A view taken before this engine's last change of a destination is not
// acted on, other than a peer that is down: it shows the peer as it was
// before the change, as when a switch-over moved the destination here.
// Nor is one taken within v.Skew of an operator's change read as the peer
// keeping the destination or having stopped it, unless an earlier view
// showed the peer arm it: the peer's leg of the change may not have landed
// yet.
func (m *Manager) ReconcileDestinations(v PeerView) {
	now := time.Now()
	m.mu.Lock()
	restarted := m.peerRestartedLocked(v)
	m.peer = v
	changed := false
	var deleted []string
	ids := make([]string, 0, len(m.destinations))
	for id, d := range m.destinations {
		ids = append(ids, id)
		d.mu.Lock()
		if !m.ownedLocked(d) || !v.Known {
			d.unsentSince = time.Time{}
			d.mu.Unlock()
			continue
		}
		if restarted || v.Down {
			d.peerListed, d.peerArmed = false, false
		}
		fresh := v.freshFor(d)
		listed := fresh && slices.Contains(v.Destinations, id)
		armedThere := fresh && slices.Contains(v.Armed, id)
		local := d.config.Engine == m.engineLabel
		switch {
		case local && v.settledFor(d) && v.Label != "" && v.sends(id) && (!d.active || !v.LocalLeads):
			d.setEngineLocked(v.Label, now)
			changed = true
			m.log.Info("destination sent by peer, standing by", "id", id, "peer", v.Label)
		case !local && fresh && d.peerListed && !listed:
			deleted = append(deleted, id)
			m.log.Warn("destination deleted on peer, deleting here", "id", id, "peer", v.Label)
		case !local && d.armed && listed && !armedThere && (d.peerArmed || v.settledFor(d)):
			d.setArmedLocked(false, now)
			changed = true
			m.log.Warn("destination stopped on peer, disarming here", "id", id, "peer", v.Label)
		case !local && d.armed && !d.active && m.peerAbsentLocked(d, v, now, fresh, listed && !armedThere):
			d.setEngineLocked(m.engineLabel, now)
			changed = true
			m.log.Warn("destination taken over from peer", "id", id, "peer", v.Label, "peerDown", v.Down)
		}
		if listed {
			d.peerListed = true
		}
		if armedThere && d.armed {
			d.peerArmed = true
		}
		if d.config.Engine == m.engineLabel || !d.armed || v.Down || v.sends(id) {
			d.unsentSince = time.Time{}
		}
		d.mu.Unlock()
	}
	fn := m.onState
	m.mu.Unlock()

	for _, id := range deleted {
		if err := m.RemoveDestination(id); err != nil && !errors.Is(err, ErrDestinationNotFound) {
			m.log.Warn("destination delete failed", "id", id, "err", err)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		m.settle(id, true)
	}
	if changed && fn != nil {
		fn()
	}
}

// peerRestartedLocked reports whether v comes from a new peer process: its
// uptime went down since the last poll. Holds m.mu.
func (m *Manager) peerRestartedLocked(v PeerView) bool {
	prev := m.peer
	if !v.Known || v.Down || !prev.Known || prev.Down || !v.SeenPolledAt.After(prev.SeenPolledAt) {
		return false
	}
	return v.Uptime < prev.Uptime
}

// peerAbsentLocked reports whether the peer, which owns the armed d, fails
// to send it: down, or up without sending it for v.Grace by polls taken
// after this engine last changed d. A peer that holds d stopped did not
// lose it: an operator stopped it there.
func (m *Manager) peerAbsentLocked(d *Destination, v PeerView, now time.Time, fresh, stoppedThere bool) bool {
	if v.Down {
		return true
	}
	if stoppedThere {
		d.unsentSince = time.Time{}
		return false
	}
	if !fresh || v.sends(d.id) {
		return false
	}
	if d.unsentSince.IsZero() {
		d.unsentSince = now
		return false
	}
	return now.Sub(d.unsentSince) >= v.Grace
}

// settle starts or stops the destination's adapter to match whether it is
// armed and sent from here. With auto, the start is this engine's own
// decision rather than an operator's, and waits for a fresh read of the
// peer: one that sends the destination already keeps it.
func (m *Manager) settle(id string, auto bool) {
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	dest.mu.Lock()
	armed, active, sends := dest.armed, dest.active, m.sendsLocked(dest)
	// Without a view of the peer this engine cannot tell whether the peer
	// sends it already.
	wait := m.peerFailover && m.ownedLocked(dest) && !m.peer.Known
	probe := m.peerProbe
	if !auto || !m.ownedLocked(dest) {
		probe = nil
	}
	dest.mu.Unlock()
	m.mu.Unlock()

	switch {
	case armed && sends && !active && !wait:
		if probe != nil && m.peerKeeps(id, probe) {
			return
		}
		if err := m.startAdapter(id); err != nil && !errors.Is(err, ErrDestinationActive) {
			m.log.Warn("destination start failed", "id", id, "err", err)
		}
	case active && (!armed || !sends):
		if err := m.stopDestination(id, false); err != nil && !errors.Is(err, ErrDestinationStopped) {
			m.log.Warn("destination stop failed", "id", id, "err", err)
		}
	}
}

// peerKeeps reads the peer with probe and, when it sends the destination
// already, hands the destination to it instead of starting it here.
func (m *Manager) peerKeeps(id string, probe func() (PeerView, bool)) bool {
	v, ok := probe()
	if !ok || v.Label == "" || !v.sends(id) {
		return false
	}
	m.mu.Lock()
	dest, found := m.destinations[id]
	if !found {
		m.mu.Unlock()
		return true
	}
	dest.mu.Lock()
	dest.setEngineLocked(v.Label, time.Now())
	dest.mu.Unlock()
	fn := m.onState
	m.mu.Unlock()
	m.log.Info("destination sent by peer, standing by", "id", id, "peer", v.Label)
	if fn != nil {
		fn()
	}
	return true
}

// armDestination arms a destination without starting it, leaving the start
// to the next ReconcileDestinations.
func (m *Manager) armDestination(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dest, ok := m.destinations[id]
	if !ok {
		return ErrDestinationNotFound
	}
	dest.mu.Lock()
	dest.setArmedLocked(true, time.Now())
	dest.mu.Unlock()
	return nil
}

// ApplyDestinationState sets the engine that sends a destination and whether
// it is armed, as the peer holds them, and starts or stops its adapter here
// to match.
func (m *Manager) ApplyDestinationState(id, engine string, armed bool) error {
	if engine != "a" && engine != "b" && engine != "" {
		return errors.New("output: engine must be 'a', 'b', or empty")
	}
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return ErrDestinationNotFound
	}
	dest.mu.Lock()
	now := time.Now()
	changed := dest.setEngineLocked(engine, now)
	changed = dest.setArmedLocked(armed, now) || changed
	dest.mu.Unlock()
	fn := m.onState
	m.mu.Unlock()

	m.settle(id, true)
	if changed && fn != nil {
		fn()
	}
	return nil
}
