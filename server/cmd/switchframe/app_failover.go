package main

import (
	"context"
	"time"

	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/peer"
)

// peerProbeTimeout bounds the fresh read of the peer an engine makes before
// it starts a destination on its own.
const peerProbeTimeout = time.Second

// peerOutputView is what the output manager needs of the peer poller to move
// destinations between the engines of a pair.
func peerOutputView(p *peer.HealthPoller) output.PeerView {
	st := p.Status()
	return output.PeerView{
		Known:        st.Reachable || st.Down,
		Down:         st.Down,
		Label:        st.PeerLabel,
		Sending:      st.PeerSending,
		Armed:        st.PeerArmed,
		Destinations: st.PeerDestinations,
		SeenPolledAt: st.SeenPolledAt,
		Uptime:       time.Duration(st.UptimeMs) * time.Millisecond,
		LocalLeads:   p.Role() == peer.RoleLeader,
		Grace:        p.DownAfter(),
	}
}

// peerProbe reads the peer's health once, for the output manager to check
// right before it starts a destination on its own.
func peerProbe(ctx context.Context, p *peer.HealthPoller) func() (output.PeerView, bool) {
	return func() (output.PeerView, bool) {
		ctx, cancel := context.WithTimeout(ctx, peerProbeTimeout)
		defer cancel()
		sent := time.Now()
		h, err := p.Fetch(ctx)
		if err != nil {
			return output.PeerView{}, false
		}
		return output.PeerView{
			Known:        true,
			Label:        h.EngineLabel,
			Sending:      h.Sending,
			Armed:        h.Armed,
			Destinations: h.Destinations,
			SeenPolledAt: sent,
			Uptime:       time.Duration(h.UptimeMs) * time.Millisecond,
		}, true
	}
}

// runDestinationFailover hands the output manager a fresh view of the peer
// every tick until ctx ends.
func runDestinationFailover(ctx context.Context, p *peer.HealthPoller, m *output.Manager, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.ReconcileDestinations(peerOutputView(p))
		}
	}
}
