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
	// LocalLeads is set when this engine holds the pair's leader role.
	LocalLeads bool
	// Grace is how long an armed destination may go unsent by a reachable
	// peer that owns it before this engine takes it over.
	Grace time.Duration
}

func (v PeerView) sends(id string) bool {
	return v.Known && !v.Down && slices.Contains(v.Sending, id)
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

// ReconcileDestinations moves destinations between the engines of a pair
// from the latest view of the peer, and starts or stops adapters to match:
//
//   - a destination this engine owns but the peer sends goes to the peer: the
//     peer took it over while this engine was away. When both send it, as
//     after a partition, the leader keeps it.
//   - an armed destination the peer owns is taken over once the peer is
//     down, or reachable but not sending it for v.Grace, as when it lost
//     its state in a restart.
func (m *Manager) ReconcileDestinations(v PeerView) {
	now := time.Now()
	m.mu.Lock()
	m.peer = v
	changed := false
	ids := make([]string, 0, len(m.destinations))
	for id, d := range m.destinations {
		ids = append(ids, id)
		d.mu.Lock()
		if !m.ownedLocked(d) || !v.Known {
			d.unsentSince = time.Time{}
			d.mu.Unlock()
			continue
		}
		local := d.config.Engine == m.engineLabel
		switch {
		case local && v.Label != "" && v.sends(id) && (!d.active || !v.LocalLeads):
			d.config.Engine = v.Label
			changed = true
			m.log.Info("destination sent by peer, standing by", "id", id, "peer", v.Label)
		case !local && d.armed && !d.active && m.peerAbsentLocked(d, v, now):
			d.config.Engine = m.engineLabel
			changed = true
			m.log.Warn("destination taken over from peer", "id", id, "peer", v.Label, "peerDown", v.Down)
		}
		if local || !d.armed || v.Down || v.sends(id) {
			d.unsentSince = time.Time{}
		}
		d.mu.Unlock()
	}
	fn := m.onState
	m.mu.Unlock()

	sort.Strings(ids)
	for _, id := range ids {
		m.settle(id)
	}
	if changed && fn != nil {
		fn()
	}
}

// peerAbsentLocked reports whether the peer, which owns the armed d, fails
// to send it: down, or up without sending it for v.Grace.
func (m *Manager) peerAbsentLocked(d *Destination, v PeerView, now time.Time) bool {
	if v.Down {
		return true
	}
	if v.sends(d.id) {
		return false
	}
	if d.unsentSince.IsZero() {
		d.unsentSince = now
		return false
	}
	return now.Sub(d.unsentSince) >= v.Grace
}

// settle starts or stops the destination's adapter to match whether it is
// armed and sent from here.
func (m *Manager) settle(id string) {
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
	dest.mu.Unlock()
	m.mu.Unlock()

	switch {
	case armed && sends && !active && !wait:
		if err := m.startAdapter(id); err != nil && !errors.Is(err, ErrDestinationActive) {
			m.log.Warn("destination start failed", "id", id, "err", err)
		}
	case active && (!armed || !sends):
		if err := m.stopDestination(id, false); err != nil && !errors.Is(err, ErrDestinationStopped) {
			m.log.Warn("destination stop failed", "id", id, "err", err)
		}
	}
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
	dest.armed = true
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
	changed := dest.config.Engine != engine || dest.armed != armed
	dest.config.Engine = engine
	dest.armed = armed
	dest.mu.Unlock()
	fn := m.onState
	m.mu.Unlock()

	m.settle(id)
	if changed && fn != nil {
		fn()
	}
	return nil
}
