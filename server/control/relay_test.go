package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// relayEngine is one engine of a pair: its switcher, squeezeback, command
// queue and API, served over HTTP, with a loop running what the queue
// drains as the engine's frame loop does.
type relayEngine struct {
	sw   *switcher.Switcher
	sq   *dve.Squeeze
	q    *cmdqueue.Queue
	api  *API
	srv  *httptest.Server
	poll *peer.HealthPoller

	// hold, while set, keeps requests the test did not send, the peer's
	// relays and health polls, from reaching the API until it is closed.
	mu   sync.Mutex
	hold chan struct{}
}

func (e *relayEngine) holdRelays() {
	e.mu.Lock()
	e.hold = make(chan struct{})
	e.mu.Unlock()
}

func (e *relayEngine) releaseRelays() {
	e.mu.Lock()
	close(e.hold)
	e.hold = nil
	e.mu.Unlock()
}

// newRelayPair returns engines a and b, each relaying to the other.
func newRelayPair(t *testing.T) (a, b *relayEngine) {
	t.Helper()
	a, b = &relayEngine{}, &relayEngine{}
	for _, e := range []*relayEngine{a, b} {
		e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Caller") == "" {
				e.mu.Lock()
				hold := e.hold
				e.mu.Unlock()
				if hold != nil {
					<-hold
				}
			}
			e.api.Mux().ServeHTTP(w, r)
		}))
		t.Cleanup(e.srv.Close)
	}
	for label, e := range map[string]*relayEngine{"a": a, "b": b} {
		peerSrv := b.srv
		if label == "b" {
			peerSrv = a.srv
		}
		e.sw = switcher.NewTestSwitcher(t, distribution.NewRelay())
		for _, key := range []string{"cam1", "cam2", "cam3"} {
			e.sw.RegisterSource(key, distribution.NewRelay())
		}
		e.sq = dve.NewSqueeze(320, 240)
		e.q = cmdqueue.New(func() int64 { return time.Now().UnixMicro() },
			cmdqueue.WithStateKeys(RelayedStateKeys()))
		e.poll = peer.NewHealthPoller(peerSrv.URL,
			peer.WithLocalIdentity(label),
			peer.WithPollInterval(10*time.Millisecond),
			peer.WithFailureThreshold(2),
			peer.WithHTTPClient(&http.Client{Timeout: time.Second}))
		e.api = NewAPI(e.sw,
			WithCommandQueue(e.q),
			WithSeqTracker(e.sw.TrackCommandSeq),
			WithSqueeze(e.sq),
			WithPeerPoller(e.poll),
			WithPeerRelay("relayed"),
		)
		go e.drain(t)
	}
	return a, b
}

// drain runs what the queue drains until the test ends.
func (e *relayEngine) drain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		release := e.q.Hold()
		for _, c := range e.q.Drain() {
			switch c.Action {
			case "cut", "preview":
				var req switchRequest
				_ = json.Unmarshal(c.Payload, &req)
				if c.Action == "cut" {
					_ = e.sw.Cut(ctx, req.Source)
				} else {
					_ = e.sw.SetPreview(ctx, req.Source)
				}
			case "squeezeback_set":
				var cfg dve.SqueezeConfig
				_ = json.Unmarshal(c.Payload, &cfg)
				_ = e.sq.Set(cfg)
			case "squeezeback_on", "squeezeback_off":
				var m dve.SqueezeMove
				_ = json.Unmarshal(c.Payload, &m)
				if c.Action == "squeezeback_on" {
					_ = e.sq.On(m)
				} else {
					_ = e.sq.Off(m)
				}
			}
			e.sw.TrackCommandSeq(c.Seq)
		}
		release()
	}
}

func (e *relayEngine) send(t *testing.T, method, path, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer console")
	req.Header.Set("X-Test-Caller", "console")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (e *relayEngine) program() string { return e.sw.State().ProgramSource }

// A cut and a preview sent to one engine alone reach the other, which
// takes them with the same command seq, so the two compare equal.
func TestPeerRelay_SwitchingReachesThePeer(t *testing.T) {
	a, b := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam2"}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/preview", `{"source":"cam3"}`))
	assert.Equal(t, "cam2", a.program())

	require.Eventually(t, func() bool {
		st := b.sw.State()
		return st.ProgramSource == "cam2" && st.PreviewSource == "cam3"
	}, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		return a.sw.LastCommandSeq() == b.sw.LastCommandSeq()
	}, 5*time.Second, 5*time.Millisecond)
	assert.Zero(t, a.sw.LastCommandSeq()%2, "engine a gives out even seqs")
}

// The squeezeback configured and moved on engine b alone reaches engine a.
func TestPeerRelay_SqueezebackReachesThePeer(t *testing.T) {
	a, b := newRelayPair(t)
	cfg := `{"rect":{"x":80,"y":0,"w":240,"h":180},"durationMs":0}`
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPut, "/api/squeezeback", cfg))
	require.Equal(t, http.StatusNoContent, b.send(t, http.MethodPost, "/api/squeezeback/on", ""))

	require.Eventually(t, func() bool {
		st := a.sq.Snapshot()
		return st.Configured && st.On && st.Rect == dve.Rect{X: 80, W: 240, H: 180}
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, uint64(1), b.sw.LastCommandSeq()%2, "engine b gives out odd seqs")

	require.Equal(t, http.StatusNoContent, a.send(t, http.MethodPost, "/api/squeezeback/off", ""))
	require.Eventually(t, func() bool { return !b.sq.Snapshot().On }, 5*time.Second, 5*time.Millisecond)
}

// Two engines cut to different sources at nearly the same moment, each
// relay reaching the other after its own cut: both end on the cut made
// later, and stay there.
func TestPeerRelay_ConcurrentCutsEndOnTheLater(t *testing.T) {
	for _, later := range []string{"a", "b"} {
		t.Run(later+" later", func(t *testing.T) {
			a, b := newRelayPair(t)
			a.holdRelays()
			b.holdRelays()
			first, second := a, b
			if later == "a" {
				first, second = b, a
			}
			require.Equal(t, http.StatusOK, first.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
			time.Sleep(2 * time.Millisecond)
			require.Equal(t, http.StatusOK, second.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam3"}`))
			a.releaseRelays()
			b.releaseRelays()

			require.Eventually(t, func() bool {
				return a.program() == "cam3" && b.program() == "cam3"
			}, 5*time.Second, 5*time.Millisecond)
			time.Sleep(100 * time.Millisecond)
			assert.Equal(t, "cam3", a.program())
			assert.Equal(t, "cam3", b.program())
		})
	}
}

// A command naming a command_seq was sent to both engines by its caller,
// or relayed by the peer, and is not passed on again.
func TestPeerRelay_CommandWithSeqIsNotRelayed(t *testing.T) {
	a, b := newRelayPair(t)
	body := fmt.Sprintf(`{"source":"cam2","command_seq":%d}`, time.Now().UnixMicro())
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", body))
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, "cam2", a.program())
	assert.Empty(t, b.program())
}

// A command this engine refuses is not passed on.
func TestPeerRelay_RefusedCommandIsNotRelayed(t *testing.T) {
	a, b := newRelayPair(t)
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Eventually(t, func() bool { return a.program() == "cam1" }, 5*time.Second, 5*time.Millisecond)
	require.GreaterOrEqual(t, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"nope"}`), 400)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, "cam1", b.program())
}

// With the peer down, a cut still takes here at once.
func TestPeerRelay_PeerDownStillCutsHere(t *testing.T) {
	a, b := newRelayPair(t)
	a.poll.Start()
	t.Cleanup(a.poll.Stop)
	b.srv.Close()
	require.Eventually(t, a.poll.PeerDown, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam2"}`))
	assert.Equal(t, "cam2", a.program())
}

// A caller naming an execute_at has the command run at that time on both
// engines.
func TestPeerRelay_TimedCommandRunsAtItsTimeOnBoth(t *testing.T) {
	a, b := newRelayPair(t)
	at := time.Now().Add(300 * time.Millisecond).UnixMicro()
	require.Equal(t, http.StatusAccepted,
		a.send(t, http.MethodPost, "/api/switch/cut", fmt.Sprintf(`{"source":"cam3","execute_at":%d}`, at)))
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, a.program(), "not before its time")
	assert.Empty(t, b.program(), "not before its time")
	require.Eventually(t, func() bool {
		return a.program() == "cam3" && b.program() == "cam3"
	}, 5*time.Second, 5*time.Millisecond)
}

// A browser's command run on arrival on both engines falls among the
// commands relayed between them: a relayed cut made before it, reaching
// an engine after it, is dropped there.
func TestPeerRelay_BrowserCommandRunOnArrivalHolds(t *testing.T) {
	a, b := newRelayPair(t)
	b.holdRelays()
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	time.Sleep(2 * time.Millisecond)
	browser := fmt.Sprintf(`{"source":"cam3","execute_at":0,"command_seq":%d}`, time.Now().UnixMicro()*10)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", browser))
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPost, "/api/switch/cut", browser))
	b.releaseRelays()

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, "cam3", a.program())
	assert.Equal(t, "cam3", b.program(), "the earlier relayed cut landed after the browser's")
}

// A squeezeback configuration naming no graphics offset reaches the peer
// with the offset it kept here, not whatever the peer held.
func TestPeerRelay_SqueezebackSetCarriesResolvedOffset(t *testing.T) {
	a, b := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPut, "/api/squeezeback",
		`{"rect":{"x":80,"y":0,"w":240,"h":180},"graphicsOffsetMs":400}`))
	require.Eventually(t, func() bool { return b.sq.Snapshot().GraphicsOffset() == 400*time.Millisecond },
		5*time.Second, 5*time.Millisecond)
	zero := 0
	require.NoError(t, b.sq.Set(dve.SqueezeConfig{Rect: dve.Rect{X: 80, W: 240, H: 180}, GraphicsOffsetMs: &zero}))

	require.Equal(t, http.StatusOK, a.send(t, http.MethodPut, "/api/squeezeback", `{"rect":{"x":0,"y":0,"w":240,"h":180}}`))
	require.Eventually(t, func() bool { return b.sq.Snapshot().Rect.X == 0 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 400*time.Millisecond, b.sq.Snapshot().GraphicsOffset())
}

// Every relayed command that changes the program also puts the old one on
// preview, so each is keyed on the preview too: a preview crossing one
// would otherwise end different on the two engines.
func TestRelayedStateKeys_ProgramChangesKeyPreview(t *testing.T) {
	for action, keys := range RelayedStateKeys() {
		if keys[0] == "program" {
			assert.Contains(t, keys, "preview", action)
		}
	}
}

// A browser command run on arrival falls at its seq, the browser's clock,
// on both engines, so a relayed cut made between its arrivals at the two
// lands alike on both.
func TestPeerRelay_BrowserCommandFallsAtItsSeq(t *testing.T) {
	a, b := newRelayPair(t)
	b.holdRelays()
	browserSeq := uint64(time.Now().UnixMicro())
	browser := fmt.Sprintf(`{"source":"cam3","command_seq":%d}`, browserSeq)
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPost, "/api/switch/cut", browser))
	time.Sleep(5 * time.Millisecond)
	// a's own cut, made after the browser's, before the browser's reaches a.
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, http.StatusConflict, a.send(t, http.MethodPost, "/api/switch/cut", browser),
		"the browser's cut falls before a's own")
	b.releaseRelays()

	require.Eventually(t, func() bool { return b.program() == "cam1" }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, "cam1", a.program())
}

func TestBufferedResponseKeepsFirstStatus(t *testing.T) {
	b := newBufferedResponse()
	b.WriteHeader(http.StatusAccepted)
	b.WriteHeader(http.StatusInternalServerError)
	assert.Equal(t, http.StatusAccepted, b.status)
	b = newBufferedResponse()
	_, _ = b.Write([]byte("x"))
	b.WriteHeader(http.StatusInternalServerError)
	assert.Equal(t, http.StatusOK, b.status)
}
