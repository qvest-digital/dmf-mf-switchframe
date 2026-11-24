package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// A T-bar position taken over fast control moves the T-bar here and is
// passed on to the peer as one taken over REST is.
func TestFastTransitionUpdateRelaysToThePeer(t *testing.T) {
	got := make(chan float64, 8)
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/switch/transition/position" {
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Position   float64 `json:"position"`
			CommandSeq uint64  `json:"command_seq"`
		}
		_ = json.Unmarshal(body, &req)
		if req.CommandSeq > 0 {
			got <- req.Position
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(peerSrv.Close)

	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	for _, key := range []string{"cam1", "cam2"} {
		sw.RegisterSource(key, distribution.NewRelay())
	}
	sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(320, 240), nil },
	})
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))

	q := cmdqueue.New(func() int64 { return time.Now().UnixMicro() }, cmdqueue.WithStateKeys(control.RelayedStateKeys()))
	poller := peer.NewHealthPoller(peerSrv.URL, peer.WithLocalIdentity("a"))
	a := &App{sw: sw, api: control.NewAPI(sw, control.WithCommandQueue(q), control.WithPeerPoller(poller),
		control.WithPeerRelay("tok"))}

	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, math.Float32bits(0.25))
	require.NoError(t, a.handleFastTransitionUpdate(data))

	require.InDelta(t, 0.25, sw.State().TransitionPosition, 1e-6)
	select {
	case p := <-got:
		require.InDelta(t, 0.25, p, 1e-6)
	case <-time.After(2 * time.Second):
		t.Fatal("the position was not passed on to the peer")
	}
}
