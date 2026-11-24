package control

import (
	"bytes"
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

// relayHeader marks a command one engine of a pair passes on to the other.
const relayHeader = "X-Switchframe-Relay"

// relayDedupWindow is how long a command sent to this engine alone and the
// same command relayed by the peer count as one caller sending it to both.
const relayDedupWindow = 2 * time.Second

// relayBacklog bounds the commands waiting to be passed on.
const relayBacklog = 256

// WithPeerRelay passes the program, preview and squeezeback commands a
// caller sends to this engine alone on to the peer engine of
// WithPeerPoller, in the order they ran here. A command carries the
// caller's Authorization header, so the peer takes it as the caller's; one
// sent without credentials carries token, "" when this engine runs without
// authentication and so takes anonymous commands the peer would not.
// Relaying needs an engine label (--engine-label), which keeps the two
// engines' command seqs apart.
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
}

// relayState is the relay's worker and what it has seen.
type relayState struct {
	once sync.Once
	jobs chan relayJob

	mu      sync.Mutex
	direct  map[string]time.Time // commands sent to this engine alone, by dedupKey
	relayed map[string]time.Time // commands the peer passed on, by dedupKey
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
// start or by convergence. A caller that sends the same command to both
// engines without a seq has it run once on each (see relayDedupWindow).
func (a *API) relayedHandler(action string, handler immediateHandlerFunc) http.HandlerFunc {
	timed := timedHandler(a.cmdQueue, action, a.seqTracker, handler)
	if !a.relayEnabled || a.cmdQueue == nil || a.peerPoller == nil || a.peerPoller.PeerURL() == "" {
		return timed
	}
	if a.peerPoller.LocalLabel() == "" {
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
			if r.Header.Get(relayHeader) != "" && a.seenAlone(action, body) {
				// The caller sent it here too, and it ran here then.
				if a.seqTracker != nil {
					a.seqTracker(req.CommandSeq)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			timed(w, r)
			return
		}
		if a.seenRelayed(action, body) {
			// The caller sent it to the peer too, which ran it and passed
			// it on here.
			a.writeDuplicate(action, w)
			return
		}

		a.noteAlone(action, body)
		seq := a.nextRelaySeq()
		at, order := req.ExecuteAt, req.ExecuteAt
		runNow := at <= 0
		if runNow {
			at, order = a.cmdQueue.Now(), a.cmdQueue.NextOrder()
		}
		stamped, err := stampCommand(a.resolveRelayed(action, body), at, seq, order)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid json")
			return
		}
		if runNow {
			cmd := cmdqueue.Command{OrderAt: order, Seq: seq, Action: action, Payload: stamped}
			if runKeyed(a.cmdQueue, cmd, w, r, stamped, handler) != keyedRan {
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
		if a.seqTracker != nil {
			a.seqTracker(seq)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" && a.relayToken != "" {
			auth = "Bearer " + a.relayToken
		}
		a.relayToPeer(r.Method, r.URL.Path, auth, stamped)
	}
}

// changesState tells the command queue whether a keyed command sets its
// further keys: a cut or transition to the source already on program
// leaves the preview where it is.
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

// dedupKey names a command by its action and body, without the fields an
// engine stamps or fills in.
func dedupKey(action string, body []byte) string {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(body)) > 0 && json.Unmarshal(body, &fields) != nil {
		return action + "\x00" + string(body)
	}
	for _, k := range []string{"execute_at", "command_seq", "order_at", "graphicsOffsetMs"} {
		delete(fields, k)
	}
	out, _ := json.Marshal(fields)
	return action + "\x00" + string(out)
}

// noteAlone records a command sent to this engine alone.
func (a *API) noteAlone(action string, body []byte) {
	a.relay.note(&a.relay.direct, dedupKey(action, body))
}

// seenAlone reports, and forgets, the same command sent to this engine
// alone within relayDedupWindow; else records it as relayed.
func (a *API) seenAlone(action string, body []byte) bool {
	key := dedupKey(action, body)
	if a.relay.take(a.relay.direct, key) {
		return true
	}
	a.relay.note(&a.relay.relayed, key)
	return false
}

// seenRelayed reports, and forgets, the same command relayed by the peer
// within relayDedupWindow.
func (a *API) seenRelayed(action string, body []byte) bool {
	return a.relay.take(a.relay.relayed, dedupKey(action, body))
}

func (r *relayState) note(m *map[string]time.Time, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if *m == nil {
		*m = map[string]time.Time{}
	}
	for k, t := range *m {
		if now.Sub(t) > relayDedupWindow {
			delete(*m, k)
		}
	}
	(*m)[key] = now
}

func (r *relayState) take(m map[string]time.Time, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := m[key]
	if ok {
		delete(m, key)
	}
	return ok && time.Since(t) <= relayDedupWindow
}

// writeDuplicate answers a command the engine took already, as the peer
// relayed it, the way its handler answers success.
func (a *API) writeDuplicate(action string, w http.ResponseWriter) {
	switch action {
	case "squeezeback_on", "squeezeback_off":
		w.WriteHeader(http.StatusNoContent)
	case "squeezeback_set":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.squeeze.Snapshot())
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.enrichedState())
	}
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
// seq it has taken, wall-clock microseconds as the browser's are, even on
// the engine whose label sorts first and odd on the other, so the two never
// give out the same one.
func (a *API) nextRelaySeq() uint64 {
	parity := uint64(0)
	local, peer := a.peerPoller.LocalLabel(), a.peerPoller.Status().PeerLabel
	if (peer != "" && local > peer) || (peer == "" && local == "b") {
		parity = 1
	}
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

// relayToPeer queues a command for the peer, sent in the order the
// commands ran here: a squeezeback configured and then turned on reaches
// the peer configured first. A peer counted down is not tried, and a full
// backlog drops the command; the peer takes this engine's state later.
func (a *API) relayToPeer(method, path, auth string, body []byte) {
	if a.peerPoller.PeerDown() {
		slog.Warn("peer relay: peer down, command not passed on; it takes this engine's state once back",
			"path", path)
		return
	}
	a.relay.once.Do(func() {
		a.relay.jobs = make(chan relayJob, relayBacklog)
		go a.relayWorker()
	})
	select {
	case a.relay.jobs <- relayJob{method, path, auth, body}:
	default:
		slog.Warn("peer relay: backlog full, command not passed on", "path", path)
	}
}

// relayWorker sends queued commands to the peer one after another.
func (a *API) relayWorker() {
	for job := range a.relay.jobs {
		a.sendToPeer(job)
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
	req.Header.Set(relayHeader, a.peerPoller.LocalLabel())
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
