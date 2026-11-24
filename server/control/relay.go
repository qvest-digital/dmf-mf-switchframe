package control

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// relayedStateKeys are the commands an engine of a pair passes on to its
// peer, each with the states it sets: the program, the preview and the
// squeezeback, and the T-bar position of a transition, so a transition a
// caller holds or pulls back on one engine is held or pulled back on both.
// Each sets its state whole, so of two the later holds. A cut, and a
// transition as it starts, also put the old program on preview. A toggle,
// as FTB is, is left to the caller and to convergence.
var relayedStateKeys = map[string][]string{
	"cut":                 {"program", "preview"},
	"transition":          {"program", "preview"},
	"transition_position": {"tbar"},
	"preview":             {"preview"},
	"squeezeback_set":     {"squeezeback_config"},
	"squeezeback_on":      {"squeezeback"},
	"squeezeback_off":     {"squeezeback"},
}

// RelayedStateKeys returns the states each command an engine relays to
// its peer sets, as cmdqueue.WithStateKeys takes them.
func RelayedStateKeys() map[string][]string {
	keys := maps.Clone(relayedStateKeys)
	for k, v := range keys {
		keys[k] = slices.Clone(v)
	}
	return keys
}

// relayTimeout bounds one command sent to the peer.
const relayTimeout = 2 * time.Second

// relayBacklog bounds the commands waiting to be passed on; past it the
// oldest is dropped.
const relayBacklog = 256

// defaultRelayMaxAge is how old a command waiting to be passed on may grow
// before it is dropped: twice the queue's stale age, past which the peer
// refuses it anyway.
const defaultRelayMaxAge = 2 * time.Duration(cmdqueue.DefaultMaxAge) * time.Microsecond

// WithPeerRelay passes the program, preview, T-bar and squeezeback commands
// a caller sends to this engine alone on to the peer engine of
// WithPeerPoller, in the order they ran here. A command carries the
// caller's Authorization header, so the peer takes it as the caller's; one
// sent without credentials, or over fast control, carries token, "" when
// this engine runs without authentication and so takes anonymous commands
// the peer would not. Relaying needs an engine label (--engine-label),
// which keeps the two engines' command seqs apart.
func WithPeerRelay(token string) APIOption {
	return func(a *API) {
		a.relayEnabled = true
		a.relayToken = token
	}
}

// relayJob is one command waiting to be passed on.
type relayJob struct {
	method, path, auth string
	body               []byte
	queued             time.Time
	position           *float64 // a T-bar position, which later ones replace
}

// relayState is the relay's worker, its backlog and what it has seen.
type relayState struct {
	once   sync.Once
	wake   chan struct{}
	maxAge time.Duration // see defaultRelayMaxAge; tests shorten it
	limit  int           // see relayBacklog; tests shorten it

	mu      sync.Mutex
	backlog []relayJob
}

// relayOn reports whether commands sent to this engine alone are passed on.
func (a *API) relayOn() bool {
	return a.relayEnabled && a.cmdQueue != nil && a.peerPoller != nil && a.peerPoller.PeerURL() != "" &&
		a.peerPoller.LocalLabel() != ""
}

// relayedHandler is timedHandler for a command whose state both engines of
// a pair hold. A command naming a command_seq is one its caller sends to
// both engines, or one the peer relayed, and is taken as timedHandler
// takes it. One naming none was sent to this engine alone: the engine
// gives it a seq, runs it, on arrival or at its execute_at, and sends the
// peer the same command with the same seq, an execute_at of when it ran
// here and an order_at placing it after every command run here, so the
// two place it alike among their commands on that state (see
// cmdqueue.WithStateKeys). The caller's answer is this engine's: a peer
// down or refusing the command takes this engine's state later, at its
// start or by convergence. A caller sending the same command to both
// engines without a seq has it run twice on each.
func (a *API) relayedHandler(action string, handler immediateHandlerFunc) http.HandlerFunc {
	timed := timedHandler(a.cmdQueue, action, a.seqTracker, handler)
	if !a.relayEnabled || a.cmdQueue == nil || a.peerPoller == nil || a.peerPoller.PeerURL() == "" {
		return timed
	}
	if !a.relayOn() {
		slog.Warn("peer relay: off, the engine has no --engine-label", "action", action)
		return timed
	}
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommandBodySize))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "failed to read body")
			return
		}
		var req timedRequest
		_ = json.Unmarshal(body, &req)
		if req.CommandSeq != 0 {
			r.Body = io.NopCloser(bytes.NewReader(body))
			timed(w, r)
			return
		}

		seq, at, order, stamped, err := a.stampAlone(action, body, req.ExecuteAt)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.ExecuteAt <= 0 {
			cmd := cmdqueue.Command{OrderAt: order, Seq: seq, Action: action, Payload: stamped}
			switch runKeyed(a.cmdQueue, cmd, w, r, stamped, handler) {
			case keyedRan:
			case keyedUnchanged:
				// It set nothing here, so it is not passed on: the peer,
				// taking it later, might find its state moved on and set
				// it back.
				if a.seqTracker != nil {
					a.seqTracker(seq)
				}
				return
			default:
				return
			}
		} else {
			cmd := cmdqueue.Command{ExecuteAt: at, OrderAt: order, Seq: seq, Action: action, Payload: stamped}
			if err := a.cmdQueue.Enqueue(cmd); err != nil {
				writeEnqueueError(w, err)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" && a.relayToken != "" {
			auth = "Bearer " + a.relayToken
		}
		a.passOn(action, r.Method, r.URL.Path, auth, body, stamped, seq)
	}
}

// stampAlone gives a command sent to this engine alone its seq, the time it
// runs, at executeAt or now, and its place in the order, and returns its
// body stamped with them.
func (a *API) stampAlone(action string, body []byte, executeAt int64) (seq uint64, at, order int64, stamped []byte, err error) {
	seq = a.nextRelaySeq()
	at, order = executeAt, executeAt
	if executeAt <= 0 {
		at, order = a.cmdQueue.Now(), a.cmdQueue.NextOrder()
	}
	stamped, err = stampCommand(a.resolveRelayed(action, body), at, seq, order)
	return seq, at, order, stamped, err
}

// passOn counts the seq of a command sent to this engine alone, once it ran
// or was queued here, and passes the command on to the peer.
func (a *API) passOn(action, method, path, auth string, body, stamped []byte, seq uint64) {
	if a.seqTracker != nil {
		a.seqTracker(seq)
	}
	job := relayJob{method: method, path: path, auth: auth, body: stamped}
	if action == "transition_position" {
		var req transitionPositionRequest
		if json.Unmarshal(body, &req) == nil {
			job.position = &req.Position
		}
	}
	a.relayToPeer(job)
}

// SetTransitionPosition moves the T-bar as POST /api/switch/transition/position
// does, for fast control, and passes the position on to the peer as that
// route does: a transition held or pulled back on one engine is held or
// pulled back on both.
func (a *API) SetTransitionPosition(ctx context.Context, position float64) error {
	if !a.relayOn() {
		return a.switcher.SetTransitionPosition(ctx, position)
	}
	const action = "transition_position"
	body, _ := json.Marshal(transitionPositionRequest{Position: position})
	seq, _, order, stamped, err := a.stampAlone(action, body, 0)
	if err != nil {
		return err
	}
	var runErr error
	cmd := cmdqueue.Command{OrderAt: order, Seq: seq, Action: action, Payload: stamped}
	if err := a.cmdQueue.RunLocal(cmd, func() bool {
		runErr = a.switcher.SetTransitionPosition(ctx, position)
		return runErr == nil
	}); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	auth := ""
	if a.relayToken != "" {
		auth = "Bearer " + a.relayToken
	}
	a.passOn(action, http.MethodPost, "/api/switch/transition/position", auth, body, stamped, seq)
	return nil
}

// changesState tells the command queue whether a keyed command sets its
// state: a cut to the source already on program sets neither program nor
// preview, also while a transition takes that source away, which it does
// not stop. Recorded, it would drop a transition made before it on the
// other engine of a pair and reaching this one after it, while that
// engine completes the transition: the two would end apart.
func (a *API) changesState(cmd cmdqueue.Command) bool {
	if cmd.Action != "cut" && cmd.Action != "transition" {
		return true
	}
	var req switchRequest
	if json.Unmarshal(cmd.Payload, &req) != nil || req.Source == "" {
		return true
	}
	return req.Source != a.switcher.ProgramSource()
}

// resolveRelayed returns body with what the engine fills in from its own
// state made explicit, so the peer, which may hold another, does as this
// engine did: a squeezeback configuration naming no graphics offset keeps
// this engine's.
func (a *API) resolveRelayed(action string, body []byte) []byte {
	if action != "squeezeback_set" || a.squeeze == nil {
		return body
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body
	}
	if _, ok := fields["graphicsOffsetMs"]; ok {
		return body
	}
	fields["graphicsOffsetMs"], _ = json.Marshal(a.squeeze.Snapshot().GraphicsOffset().Milliseconds())
	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return out
}

// keyedResult is what became of a command runKeyed was given.
type keyedResult int

const (
	keyedRan        keyedResult = iota
	keyedUnchanged              // it ran and set nothing (see changesState)
	keyedSuperseded             // a later command on its state ran here
	keyedFailed                 // its handler refused it
)

// runKeyed runs a command on a state the queue keys on arrival, as one of
// a pair relays or a caller sends to both: refused with 409 when a later
// command on its state has run here, and recorded as run when the handler
// succeeds. The handler's answer is buffered, and what it defers to after
// the run (writeState, broadcastAfter) done, once the queue is released,
// so neither a slow caller nor the state's encoding holds up the frame
// loop.
func runKeyed(q *cmdqueue.Queue, cmd cmdqueue.Command, w http.ResponseWriter, r *http.Request, body []byte, handler immediateHandlerFunc) keyedResult {
	rec := newBufferedResponse()
	err := q.RunLocal(cmd, func() bool {
		handler(rec, r, body)
		return rec.status < http.StatusMultipleChoices
	})
	if errors.Is(err, cmdqueue.ErrUnchanged) {
		rec.writeTo(w)
		return keyedUnchanged
	}
	if err != nil {
		writeEnqueueError(w, err)
		if errors.Is(err, cmdqueue.ErrSuperseded) {
			return keyedSuperseded
		}
		return keyedFailed
	}
	rec.writeTo(w)
	if rec.status >= http.StatusMultipleChoices {
		return keyedFailed
	}
	return keyedRan
}

// bufferedResponse holds a handler's answer until it is written out, with
// work the handler deferred until then.
type bufferedResponse struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
	after  []func(w http.ResponseWriter)
}

// later runs fn with the response writer once the answer is written: at
// once, unless w is a bufferedResponse.
func later(w http.ResponseWriter, fn func(w http.ResponseWriter)) {
	if b, ok := w.(*bufferedResponse); ok {
		b.after = append(b.after, fn)
		return
	}
	fn(w)
}

// writeState answers with the enriched state, encoded after a keyed run.
func (a *API) writeState(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	later(w, func(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(a.enrichedState()) })
}

// broadcastAfter broadcasts the state, after a keyed run.
func (a *API) broadcastAfter(w http.ResponseWriter) {
	later(w, func(http.ResponseWriter) { a.broadcast() })
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: http.Header{}, status: http.StatusOK}
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.wrote = true
	return b.body.Write(p)
}

// WriteHeader keeps the first status, as net/http does.
func (b *bufferedResponse) WriteHeader(code int) {
	if !b.wrote {
		b.status, b.wrote = code, true
	}
}

func (b *bufferedResponse) writeTo(w http.ResponseWriter) {
	maps.Copy(w.Header(), b.header)
	w.WriteHeader(b.status)
	_, _ = w.Write(b.body.Bytes())
	for _, fn := range b.after {
		fn(w)
	}
}

// stampCommand returns body, a JSON object or empty, with execute_at,
// command_seq and order_at set.
func stampCommand(body []byte, at int64, seq uint64, order int64) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, err
		}
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
	}
	fields["execute_at"], _ = json.Marshal(at)
	fields["command_seq"], _ = json.Marshal(seq)
	fields["order_at"], _ = json.Marshal(order)
	return json.Marshal(fields)
}

// nextRelaySeq returns a seq for a command this engine relays: above every
// seq it has taken, wall-clock microseconds as the browser's are, of the
// parity seqParity gives this engine, so the two never give out the same
// one.
func (a *API) nextRelaySeq() uint64 {
	parity := seqParity(a.peerPoller.LocalLabel(), a.peerPoller.Status().PeerLabel)
	for {
		last := a.relaySeq.Load()
		seq := max(last+1, a.switcher.LastCommandSeq()+1, uint64(time.Now().UnixMicro()))
		if seq%2 != parity {
			seq++
		}
		if a.relaySeq.CompareAndSwap(last, seq) {
			return seq
		}
	}
}

// seqParity returns the parity of the seqs an engine labelled local mints:
// the other of its label's last byte's, even on engine a and odd on engine
// b, whether or not each knows the other's label yet. Two labels of one
// parity, once the peer's is known, take the order of the labels instead.
func seqParity(local, peer string) uint64 {
	own := func(label string) uint64 { return 1 - uint64(label[len(label)-1])%2 }
	p := own(local)
	if peer != "" && peer != local && own(peer) == p {
		if local > peer {
			return 1
		}
		return 0
	}
	return p
}

// relayToPeer queues a command for the peer, sent in the order the
// commands ran here: a squeezeback configured and then turned on reaches
// the peer configured first. A T-bar position replaces the positions
// queued right before it but the furthest forward of them, which the peer
// needs to tell a pull-back. A peer counted down is not tried, and a full
// backlog drops its oldest command; the peer takes this engine's state
// later.
func (a *API) relayToPeer(job relayJob) {
	if a.peerPoller.PeerDown() {
		slog.Warn("peer relay: peer down, command not passed on; it takes this engine's state once back",
			"path", job.path)
		return
	}
	a.relay.once.Do(func() {
		a.relay.wake = make(chan struct{}, 1)
		if a.relay.maxAge == 0 {
			a.relay.maxAge = defaultRelayMaxAge
		}
		go a.relayWorker()
	})
	job.queued = time.Now()
	r := &a.relay
	r.mu.Lock()
	if job.position != nil {
		i := len(r.backlog)
		for i > 0 && r.backlog[i-1].position != nil {
			i--
		}
		furthest := -1
		for k := i; k < len(r.backlog); k++ {
			if *r.backlog[k].position > *job.position && (furthest < 0 || *r.backlog[k].position > *r.backlog[furthest].position) {
				furthest = k
			}
		}
		keep := r.backlog[:i]
		if furthest >= 0 {
			keep = append(keep, r.backlog[furthest])
		}
		r.backlog = keep
	}
	r.backlog = append(r.backlog, job)
	if n := len(r.backlog) - cmp.Or(r.limit, relayBacklog); n > 0 {
		slog.Warn("peer relay: backlog full, oldest commands not passed on", "dropped", n)
		r.backlog = r.backlog[n:]
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// relayWorker sends queued commands to the peer one after another. One
// older than the relay's max age, or due while the peer is down, is
// dropped: the peer would refuse it as stale, or takes this engine's state
// once back.
func (a *API) relayWorker() {
	r := &a.relay
	for range r.wake {
		for {
			r.mu.Lock()
			if len(r.backlog) == 0 {
				r.mu.Unlock()
				break
			}
			job := r.backlog[0]
			r.backlog = r.backlog[1:]
			r.mu.Unlock()
			switch {
			case time.Since(job.queued) > r.maxAge:
				slog.Warn("peer relay: command waited too long, not passed on", "path", job.path,
					"waited", time.Since(job.queued))
			case a.peerPoller.PeerDown():
				slog.Warn("peer relay: peer down, command not passed on", "path", job.path)
			default:
				a.sendToPeer(job)
			}
		}
	}
}

func (a *API) sendToPeer(job relayJob) {
	ctx, cancel := context.WithTimeout(context.Background(), relayTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, job.method, a.peerPoller.PeerURL()+job.path, bytes.NewReader(job.body))
	if err != nil {
		slog.Warn("peer relay: request not built", "path", job.path, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if job.auth != "" {
		req.Header.Set("Authorization", job.auth)
	}
	resp, err := a.peerPoller.HTTPClient().Do(req)
	if err != nil {
		slog.Warn("peer relay: command not passed on", "path", job.path, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode < http.StatusMultipleChoices:
	case resp.StatusCode == http.StatusConflict && strings.Contains(string(msg), cmdqueue.ErrSuperseded.Error()):
		// The peer ran a later command on the same state.
		slog.Info("peer relay: peer holds a later command", "path", job.path, "response", string(msg))
	default:
		slog.Warn("peer relay: peer refused the command", "path", job.path,
			"status", resp.StatusCode, "response", string(msg))
	}
}
