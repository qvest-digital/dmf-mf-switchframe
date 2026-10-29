package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/peer"
)

// peerRoles are the values switchframe_peer_role is one-hot over.
var peerRoles = map[peer.Role]string{
	peer.RoleStandalone:          "standalone",
	peer.RoleLeader:              "leader",
	peer.RoleFollowerHealthy:     "follower",
	peer.RoleFollowerPartitioned: "follower_partitioned",
}

var (
	descPeerRole = prometheus.NewDesc("switchframe_peer_role",
		"1 for this engine's role in its pair, 0 for the others: leader, follower, follower_partitioned (the peer is unreachable and holds authority), or standalone without a peer.",
		[]string{"role"}, nil)
	descPeerReachable = prometheus.NewDesc("switchframe_peer_reachable",
		"1 while the peer engine answers its health polls.", nil, nil)
	descPeerFailures = prometheus.NewDesc("switchframe_peer_consecutive_failures",
		"Health polls of the peer that failed in a row.", nil, nil)
	descPeerLastSeen = prometheus.NewDesc("switchframe_peer_last_seen_age_seconds",
		"Time since the peer last answered a health poll.", nil, nil)
	descEpoch = prometheus.NewDesc("switchframe_peer_epoch",
		"Leadership epoch, of this engine (side local) and as the peer last reported it (side peer). A forced leader change raises it.",
		[]string{"side"}, nil)
	descProgramAgrees = prometheus.NewDesc("switchframe_peer_program_agrees",
		"1 while the peer last reported the same program source as this engine, 0 when the two disagree. Absent without a reachable peer.",
		nil, nil)

	descNMOSRegistered = prometheus.NewDesc("switchframe_nmos_registered",
		"1 while this engine holds the node's registration with the NMOS registry.", nil, nil)
	descNMOSRegistrations = prometheus.NewDesc("switchframe_nmos_registrations_total",
		"Registrations of the node with the NMOS registry, by result.", []string{"result"}, nil)
	descNMOSHeartbeats = prometheus.NewDesc("switchframe_nmos_heartbeats_total",
		"Heartbeats to the NMOS registry, by result.", []string{"result"}, nil)
	descNMOSHeartbeatAge = prometheus.NewDesc("switchframe_nmos_last_heartbeat_age_seconds",
		"Time since the registry last accepted a registration or heartbeat from this engine.", nil, nil)
	descNMOSActivations = prometheus.NewDesc("switchframe_nmos_activations_total",
		"IS-05 activations of an MXL receiver, by result.", []string{"result"}, nil)
)

// redundancyCollector exposes the engine pair's health and the NMOS node's
// registration as Prometheus metrics read at scrape time.
type redundancyCollector struct {
	a *App
}

// Describe implements prometheus.Collector.
func (c *redundancyCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descPeerRole, descPeerReachable, descPeerFailures, descPeerLastSeen, descEpoch, descProgramAgrees,
		descNMOSRegistered, descNMOSRegistrations, descNMOSHeartbeats, descNMOSHeartbeatAge, descNMOSActivations,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *redundancyCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v), labels...)
	}
	flag := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}

	// Role is nil-safe and reads standalone without a poller.
	p := c.a.peerPoller
	role := p.Role()
	for r, name := range peerRoles {
		gauge(descPeerRole, flag(r == role), name)
	}
	if p != nil {
		st := p.Status()
		gauge(descPeerReachable, flag(st.Reachable))
		gauge(descPeerFailures, float64(st.ConsecFailures))
		if !st.LastSeen.IsZero() {
			gauge(descPeerLastSeen, time.Since(st.LastSeen).Seconds())
		}
		gauge(descEpoch, float64(p.LocalEpoch()), "local")
		gauge(descEpoch, float64(st.PeerEpoch), "peer")
		if st.Reachable && st.ProgramSource != "" && c.a.sw != nil {
			gauge(descProgramAgrees, flag(st.ProgramSource == c.a.sw.State().ProgramSource))
		}
	}

	if n := c.a.mxlNMOS; n != nil && n.reg != nil {
		n.mu.Lock()
		registered := n.registered
		n.mu.Unlock()
		gauge(descNMOSRegistered, flag(registered))
		counter(descNMOSRegistrations, n.registrations.Load(), "ok")
		counter(descNMOSRegistrations, n.registrationErrors.Load(), "error")
		counter(descNMOSHeartbeats, n.heartbeats.Load(), "ok")
		counter(descNMOSHeartbeats, n.heartbeatErrors.Load(), "error")
		if last := n.lastHeartbeat.Load(); last != 0 {
			gauge(descNMOSHeartbeatAge, time.Since(time.Unix(0, last)).Seconds())
		}
	}
	if n := c.a.mxlNMOS; n != nil {
		counter(descNMOSActivations, n.activations.Load(), "ok")
		counter(descNMOSActivations, n.activationErrors.Load(), "error")
	}
}
