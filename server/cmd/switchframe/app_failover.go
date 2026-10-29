package main

import (
	"context"
	"time"

	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/peer"
)

// peerOutputView is what the output manager needs of the peer poller to move
// destinations between the engines of a pair.
func peerOutputView(p *peer.HealthPoller) output.PeerView {
	st := p.Status()
	return output.PeerView{
		Known:      st.Reachable || st.Down,
		Down:       st.Down,
		Label:      st.PeerLabel,
		Sending:    st.PeerSending,
		LocalLeads: p.Role() == peer.RoleLeader,
		Grace:      p.DownAfter(),
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
