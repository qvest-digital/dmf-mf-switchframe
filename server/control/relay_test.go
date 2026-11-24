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
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
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
	mu         sync.Mutex
	hold       chan struct{}
	drained    []string // actions the queue dispatched
	slow       string   // path delayed, see slowPath
	positions  int      // T-bar positions relayed here
	healthDown bool     // see failHealth
	slowFor    time.Duration
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
			if r.URL.Path == "/api/peer/health" {
				e.mu.Lock()
				down := e.healthDown
				e.mu.Unlock()
				if down {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				e.api.Mux().ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-Test-Caller") == "" && r.URL.Path == "/api/switch/transition/position" {
				e.mu.Lock()
				e.positions++
				e.mu.Unlock()
			}
			if r.Header.Get("X-Test-Caller") == "" {
				e.mu.Lock()
				hold, slow, slowFor := e.hold, e.slow, e.slowFor
				e.mu.Unlock()
				if hold != nil {
					<-hold
				}
				if r.URL.Path == slow {
					time.Sleep(slowFor)
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
		e.sw.SetTransitionConfig(switcher.TransitionConfig{
			DecoderFactory: func() (transition.VideoDecoder, error) {
				return transition.NewMockDecoder(320, 240), nil
			},
		})
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
			_ = e.q.Exec(c, func() error { return e.dispatch(ctx, c) })
			e.sw.TrackCommandSeq(c.Seq)
		}
		release()
	}
}

// dispatch runs a drained command as the engine's dispatcher does.
func (e *relayEngine) dispatch(ctx context.Context, c cmdqueue.Command) error {
	e.mu.Lock()
	e.drained = append(e.drained, c.Action)
	e.mu.Unlock()
	switch c.Action {
	case "cut", "preview":
		var req switchRequest
		_ = json.Unmarshal(c.Payload, &req)
		if c.Action == "cut" {
			return e.sw.Cut(ctx, req.Source)
		}
		return e.sw.SetPreview(ctx, req.Source)
	case "transition":
		var req transitionRequest
		_ = json.Unmarshal(c.Payload, &req)
		return e.sw.StartTransition(ctx, req.Source, req.Type, req.DurationMs, req.WipeDirection)
	case "transition_position":
		var req transitionPositionRequest
		_ = json.Unmarshal(c.Payload, &req)
		return e.sw.SetTransitionPosition(ctx, req.Position)
	case "squeezeback_set":
		var cfg dve.SqueezeConfig
		if err := json.Unmarshal(c.Payload, &cfg); err != nil {
			return err
		}
		return e.sq.Set(cfg)
	case "squeezeback_on", "squeezeback_off":
		return e.api.MoveQueuedSqueeze(c.Action == "squeezeback_on", c.Payload)
	}
	return nil
}

// drainedActions returns the dispatched commands of action.
func (e *relayEngine) drainedActions(action string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, a := range e.drained {
		if a == action {
			out = append(out, a)
		}
	}
	return out
}

// positionsReceived counts the T-bar positions the peer passed on here.
func (e *relayEngine) positionsReceived() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.positions
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

// A browser command run on arrival falls where it arrives, whatever its
// seq says: a browser whose clock runs behind has its cut taken, not
// refused as older than one relayed before it.
func TestPeerRelay_SkewedBrowserSeqIsTaken(t *testing.T) {
	a, _ := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	behind := uint64(time.Now().Add(-5 * time.Second).UnixMicro())
	browser := fmt.Sprintf(`{"source":"cam3","command_seq":%d}`, behind)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", browser))
	assert.Equal(t, "cam3", a.program())
}

// A transition a caller pulls back on one engine is pulled back on both:
// the T-bar positions are relayed with the start.
func TestPeerRelay_TbarPullBackReachesThePeer(t *testing.T) {
	a, b := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Eventually(t, func() bool { return b.program() == "cam1" }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition",
		`{"source":"cam2","type":"mix","durationMs":5000}`))
	require.Eventually(t, func() bool { return b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition/position", `{"position":0.7}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition/position", `{"position":0.05}`))
	require.False(t, a.sw.State().InTransition, "pulled back here")

	require.Eventually(t, func() bool { return !b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond,
		"the peer kept the transition running")
	assert.Equal(t, "cam1", b.program())
}

// A squeezeback configured and turned on reaches the peer in that order,
// even when the peer is slow to take the configuration.
func TestPeerRelay_InOrder(t *testing.T) {
	a, b := newRelayPair(t)
	b.slowPath("/api/squeezeback", 150*time.Millisecond)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPut, "/api/squeezeback",
		`{"rect":{"x":80,"y":0,"w":240,"h":180},"durationMs":0}`))
	require.Equal(t, http.StatusNoContent, a.send(t, http.MethodPost, "/api/squeezeback/on", ""))
	require.Eventually(t, func() bool { return b.sq.Snapshot().On }, 5*time.Second, 5*time.Millisecond)
}

// A move taken from the peer moves this engine although it cannot send the
// move's graphics, as it lacks the HTML5 graphic.
func TestPeerRelay_MoveWithoutGraphicsHereStillMoves(t *testing.T) {
	a, b := newRelayPair(t)
	g := &recordingGfx{mockHTMLGfxManager: newMockHTMLGfxManager(2)}
	_, err := g.AllocateBrowser(3)
	require.NoError(t, err)
	a.api.SetHTMLGfxManager(g)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPut, "/api/squeezeback",
		`{"rect":{"x":80,"y":0,"w":240,"h":180},"durationMs":0}`))
	require.Equal(t, http.StatusNoContent, a.send(t, http.MethodPost, "/api/squeezeback/on",
		`{"graphics":[{"layerId":3,"data":{"layout":"lframe"}}]}`))
	require.Eventually(t, func() bool { return b.sq.Snapshot().On }, 5*time.Second, 5*time.Millisecond)
}

// A relayed command the peer drops as overtaken still counts on its seq,
// so the two engines report the same last command. It can carry the
// higher seq: an engine mints its seqs above the browser's, whose clock
// may run ahead.
func TestPeerRelay_OvertakenCommandCountsItsSeq(t *testing.T) {
	a, b := newRelayPair(t)
	ahead := fmt.Sprintf(`{"source":"cam2","command_seq":%d}`, time.Now().Add(5*time.Second).UnixMicro())
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", ahead))
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPost, "/api/switch/cut", ahead))
	b.holdRelays()
	// two commands, so a's last seq lies above the one b mints next
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/preview", `{"source":"cam1"}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	time.Sleep(2 * time.Millisecond)
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam3"}`))
	require.Greater(t, a.sw.LastCommandSeq(), b.sw.LastCommandSeq())
	b.releaseRelays()
	require.Eventually(t, func() bool {
		return a.program() == "cam3" && a.sw.LastCommandSeq() == b.sw.LastCommandSeq()
	}, 5*time.Second, 5*time.Millisecond, "seqs %d and %d", a.sw.LastCommandSeq(), b.sw.LastCommandSeq())
	assert.Equal(t, "cam3", b.program())
}

// An engine without a label does not relay: two unlabelled engines would
// give out the same seqs.
func TestPeerRelay_OffWithoutLabel(t *testing.T) {
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	sw.RegisterSource("cam1", distribution.NewRelay())
	peerHit := make(chan struct{}, 1)
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/peer/health" {
			peerHit <- struct{}{}
		}
	}))
	t.Cleanup(peerSrv.Close)
	q := cmdqueue.New(func() int64 { return time.Now().UnixMicro() }, cmdqueue.WithStateKeys(RelayedStateKeys()))
	api := NewAPI(sw, WithCommandQueue(q), WithPeerPoller(peer.NewHealthPoller(peerSrv.URL)), WithPeerRelay(""))
	require.Equal(t, http.StatusOK, serve(t, api, "POST", "/api/switch/cut", `{"source":"cam1"}`).Code)
	select {
	case <-peerHit:
		t.Fatal("an unlabelled engine relayed")
	case <-time.After(200 * time.Millisecond):
	}
}

// The state a keyed command answers with is encoded once the queue is
// released, so the frame loop's dispatch does not wait for it.
func TestPeerRelay_AnswerEncodedAfterTheQueueIsReleased(t *testing.T) {
	a, _ := newRelayPair(t)
	held := make(chan bool, 1)
	a.api.SetEnrichFunc(func(s internal.ControlRoomState) internal.ControlRoomState {
		got := make(chan struct{})
		go func() { release := a.q.Hold(); release(); close(got) }()
		select {
		case <-got:
			held <- false
		case <-time.After(200 * time.Millisecond):
			held <- true
		}
		return s
	})
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam2"}`))
	assert.False(t, <-held, "the queue was held while the answer was encoded")
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

// slowPath delays requests for path the test did not send.
func (e *relayEngine) slowPath(path string, d time.Duration) {
	e.mu.Lock()
	e.slow, e.slowFor = path, d
	e.mu.Unlock()
}

// A cut to the source already on program leaves the preview alone, so a
// preview chosen before it on the other engine still lands.
func TestPeerRelay_CutToProgramKeepsAnEarlierPreview(t *testing.T) {
	a, b := newRelayPair(t)
	// on program alike, without a relayed cut the one below would repeat
	for _, e := range []*relayEngine{a, b} {
		require.NoError(t, e.sw.Cut(context.Background(), "cam1"))
	}
	b.holdRelays()
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/preview", `{"source":"cam3"}`))
	time.Sleep(2 * time.Millisecond)
	require.Equal(t, http.StatusOK, b.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	b.releaseRelays()
	require.Eventually(t, func() bool { return b.sw.State().PreviewSource == "cam3" }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, "cam3", a.sw.State().PreviewSource)
}

// T-bar positions sent over fast control reach the peer as the REST
// route's do: a pull-back on one engine aborts the transition on both.
func TestPeerRelay_FastControlPullBackReachesThePeer(t *testing.T) {
	a, b := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Eventually(t, func() bool { return b.program() == "cam1" }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition",
		`{"source":"cam2","type":"mix","durationMs":5000}`))
	require.Eventually(t, func() bool { return b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond)
	for _, p := range []float64{0.3, 0.7, 0.4, 0.05} {
		require.NoError(t, a.api.SetTransitionPosition(context.Background(), p))
	}
	require.False(t, a.sw.State().InTransition, "pulled back here")
	require.Eventually(t, func() bool { return !b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond,
		"the peer kept the transition running")
	assert.Equal(t, "cam1", b.program())
}

// Positions queued behind a slow peer are coalesced: the peer gets the
// furthest forward and the last, not every one, and a cut queued after
// them is not held up behind them.
func TestPeerRelay_PositionsCoalesce(t *testing.T) {
	a, b := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Eventually(t, func() bool { return b.program() == "cam1" }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition",
		`{"source":"cam2","type":"mix","durationMs":5000}`))
	require.Eventually(t, func() bool { return b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond)
	b.holdRelays()
	// the first is in flight, held; the rest queue behind it: forward to
	// 0.8, then back below the abort threshold
	var moves []float64
	for i := 1; i <= 40; i++ {
		moves = append(moves, float64(i)*0.02)
	}
	moves = append(moves, 0.7, 0.6, 0.5, 0.4, 0.25)
	for _, p := range moves {
		require.NoError(t, a.api.SetTransitionPosition(context.Background(), p))
	}
	require.False(t, a.sw.State().InTransition, "pulled back here")
	b.releaseRelays()
	require.Eventually(t, func() bool { return !b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond,
		"the pull-back did not reach the peer")
	assert.LessOrEqual(t, b.positionsReceived(), 4, "positions were not coalesced")
}

// A command that waited past the relay's max age, behind a peer that does
// not answer, is dropped rather than sent: the peer would refuse it.
func TestPeerRelay_StaleJobDropped(t *testing.T) {
	a, b := newRelayPair(t)
	a.api.relay.maxAge = 100 * time.Millisecond
	b.holdRelays()
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam3"}`))
	time.Sleep(300 * time.Millisecond)
	b.releaseRelays()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, "cam1", b.program(), "the command that waited too long was sent")
}

// T-bar positions taken out of order keep the later one: an older position
// arriving after a newer one is refused, not applied, as a stale one could
// abort the transition.
func TestPeerRelay_PositionsKeepTheirOrder(t *testing.T) {
	a, _ := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition",
		`{"source":"cam2","type":"mix","durationMs":5000}`))
	now := time.Now().UnixMicro()
	// as the peer relays them: run at now, placed at order_at
	pos := func(p float64, seq uint64, at int64) string {
		return fmt.Sprintf(`{"position":%v,"command_seq":%d,"execute_at":%d,"order_at":%d}`, p, seq, now, at)
	}
	require.Equal(t, http.StatusAccepted, a.send(t, http.MethodPost, "/api/switch/transition/position", pos(0.8, uint64(now)+2, now+2)))
	require.Eventually(t, func() bool { return len(a.drainedActions("transition_position")) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, http.StatusConflict, a.send(t, http.MethodPost, "/api/switch/transition/position", pos(0.1, uint64(now)+1, now+1)))
	assert.True(t, a.sw.State().InTransition, "the older position aborted the transition")
}

func TestSeqParity(t *testing.T) {
	for _, c := range []struct {
		local, peer string
		want        uint64
	}{
		{"a", "", 0}, {"a", "b", 0}, {"b", "", 1}, {"b", "a", 1},
		{"x", "", 1}, {"y", "", 0},
		// same parity: the order of the labels
		{"a", "c", 0}, {"c", "a", 1},
	} {
		assert.Equal(t, c.want, seqParity(c.local, c.peer), "%s with peer %q", c.local, c.peer)
	}
	for _, pair := range [][2]string{{"a", "b"}, {"a", "c"}, {"x", "y"}, {"engine-1", "engine-3"}} {
		assert.NotEqual(t, seqParity(pair[0], pair[1]), seqParity(pair[1], pair[0]), "%v", pair)
	}
	// a and b differ before either knows the other's label, and keep their
	// parity once it does
	assert.NotEqual(t, seqParity("a", ""), seqParity("b", ""))
	assert.Equal(t, seqParity("a", ""), seqParity("a", "b"))
}

// X turns the squeezeback on and off on a; Y turns it on on b after X's
// off ran on a but before it reached b. Y's on is the latest command on the
// squeezeback, so both engines end on.
func TestPeerRelay_OperatorsInterleavedWithTheRelays(t *testing.T) {
	a, b := newRelayPair(t)
	for _, e := range []*relayEngine{a, b} {
		require.NoError(t, e.sq.Set(dve.SqueezeConfig{Rect: dve.Rect{X: 80, W: 240, H: 180}}))
	}
	b.holdRelays()
	require.Equal(t, http.StatusNoContent, a.send(t, http.MethodPost, "/api/squeezeback/on", `{"durationMs":0}`))
	require.Equal(t, http.StatusNoContent, a.send(t, http.MethodPost, "/api/squeezeback/off", `{"durationMs":0}`))
	require.False(t, a.sq.Snapshot().On)
	time.Sleep(2 * time.Millisecond)
	require.Equal(t, http.StatusNoContent, b.send(t, http.MethodPost, "/api/squeezeback/on", `{"durationMs":0}`))
	b.releaseRelays()
	require.Eventually(t, func() bool { return a.sq.Snapshot().On }, 5*time.Second, 5*time.Millisecond, "Y's on did not reach a")
	time.Sleep(200 * time.Millisecond)
	assert.True(t, a.sq.Snapshot().On)
	assert.True(t, b.sq.Snapshot().On, "X's earlier on/off overrode Y's later on on b")
}

// A forward-only T-bar throw on one engine completes on both, however late
// the positions reach the peer: the peer takes them in order and never
// sees the T-bar step back.
func TestPeerRelay_ForwardThrowCompletesOnBoth(t *testing.T) {
	for _, gap := range []time.Duration{0, 50 * time.Millisecond, 150 * time.Millisecond} {
		t.Run(gap.String(), func(t *testing.T) {
			a, b := newRelayPair(t)
			require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
			require.Eventually(t, func() bool { return b.program() == "cam1" }, 5*time.Second, 5*time.Millisecond)
			require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition",
				`{"source":"cam2","type":"mix","durationMs":5000}`))
			require.Eventually(t, func() bool { return b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond)
			b.slowPath("/api/switch/transition/position", gap)
			for i := 1; i <= 50; i++ {
				require.NoError(t, a.api.SetTransitionPosition(context.Background(), float64(i)/50))
				time.Sleep(time.Second / 60)
			}
			require.Equal(t, "cam2", a.program())
			require.Eventually(t, func() bool { return !b.sw.State().InTransition }, 5*time.Second, 5*time.Millisecond)
			assert.Equal(t, "cam2", b.program(), "the throw aborted on the peer")
		})
	}
}

// A command queued for a peer that goes down before it is sent is dropped,
// not sent to a peer that may be back with a state of its own.
func TestPeerRelay_PeerDownDropsQueuedCommand(t *testing.T) {
	a, b := newRelayPair(t)
	a.poll.Start()
	t.Cleanup(a.poll.Stop)
	require.Eventually(t, a.poll.PeerReachable, 5*time.Second, 5*time.Millisecond)
	b.holdRelays()
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam3"}`))
	b.failHealth(true)
	require.Eventually(t, a.poll.PeerDown, 5*time.Second, 5*time.Millisecond)
	b.releaseRelays()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, "cam1", b.program(), "the command queued while the peer went down was sent")
}

// A full backlog drops its oldest command, so the latest state still
// reaches the peer.
func TestPeerRelay_FullBacklogDropsTheOldest(t *testing.T) {
	a, b := newRelayPair(t)
	a.api.relay.limit = 2
	b.holdRelays()
	for _, src := range []string{"cam1", "cam2", "cam3", "cam1"} {
		require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", fmt.Sprintf(`{"source":%q}`, src)))
	}
	b.releaseRelays()
	require.Eventually(t, func() bool { return len(b.drainedActions("cut")) == 3 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, "cam1", b.program())
	assert.Len(t, b.drainedActions("cut"), 3, "the first, in flight, and the last two")
}

// failHealth makes the engine's peer health answer 503.
func (e *relayEngine) failHealth(fail bool) {
	e.mu.Lock()
	e.healthDown = fail
	e.mu.Unlock()
}

// A queued command whose order_at lies far from its execute_at is placed
// at its execute_at: an order_at an hour ahead does not refuse the
// commands after it.
func TestPeerRelay_FarOrderAtIgnored(t *testing.T) {
	a, _ := newRelayPair(t)
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/cut", `{"source":"cam1"}`))
	require.Equal(t, http.StatusOK, a.send(t, http.MethodPost, "/api/switch/transition",
		`{"source":"cam2","type":"mix","durationMs":5000}`))
	now := time.Now().UnixMicro()
	far := fmt.Sprintf(`{"position":0.5,"command_seq":%d,"execute_at":%d,"order_at":%d}`, now, now, now+3_600_000_000)
	require.Equal(t, http.StatusAccepted, a.send(t, http.MethodPost, "/api/switch/transition/position", far))
	require.Eventually(t, func() bool { return len(a.drainedActions("transition_position")) == 1 }, 5*time.Second, time.Millisecond)
	next := time.Now().UnixMicro()
	later := fmt.Sprintf(`{"position":0.6,"command_seq":%d,"execute_at":%d,"order_at":%d}`, next, next, next)
	assert.Equal(t, http.StatusAccepted, a.send(t, http.MethodPost, "/api/switch/transition/position", later))
}
