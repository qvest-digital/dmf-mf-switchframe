package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/peer"
)

// The output manager's read of the peer before a start of its own reaches
// the peer itself, not the last poll.
func TestPeerProbe_ReadsPeerNow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(peer.HealthResponse{Status: "ok", EngineLabel: "b", Sending: []string{"0a1b2c3d"}, UptimeMs: 2000})
	}))
	defer srv.Close()
	probe := peerProbe(context.Background(), peer.NewHealthPoller(srv.URL))

	v, ok := probe()
	require.True(t, ok)
	require.Equal(t, "b", v.Label)
	require.Equal(t, []string{"0a1b2c3d"}, v.Sending)
	require.False(t, v.SeenPolledAt.IsZero())

	srv.Close()
	_, ok = probe()
	require.False(t, ok, "an unreachable peer gives no answer")
}
