package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// relayedStateKeys are the commands an engine of a pair passes on to its
// peer, each with the states it sets: the program, the preview and the
// squeezeback. Each sets its state whole, so of two the later holds. A
// cut, and a transition as it starts, also put the old program on
// preview. A toggle, as FTB is, is left to the caller and to convergence.
var relayedStateKeys = map[string][]string{
	"cut":             {"program", "preview"},
	"transition":      {"program", "preview"},
	"preview":         {"preview"},
	"squeezeback_set": {"squeezeback_config"},
	"squeezeback_on":  {"squeezeback"},
	"squeezeback_off": {"squeezeback"},
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

// WithPeerRelay passes the program, preview and squeezeback commands a
// caller sends to this engine alone on to the peer engine of
// WithPeerPoller, authorised as the caller was, or with token when the
// caller sent no credentials.
func WithPeerRelay(token string) APIOption {
	return func(a *API) {
		a.relayEnabled = true
		a.relayToken = token
	}
}

// relayedHandler is timedHandler for a command whose state both engines of
// a pair hold. A command naming a command_seq is one its caller sends to
// both engines, or one the peer relayed, and is taken as timedHandler
// takes it. One naming none was sent to this engine alone: the engine
// gives it a seq, runs it, on arrival or at its execute_at, and sends the
// peer the same command with the same seq and an execute_at of when it ran
// here, so the two place it alike among their commands on that state (see
// cmdqueue.WithStateKeys). The caller's answer is this engine's: a peer
// down or refusing the command takes this engine's state later, at its
// start or by convergence.
func (a *API) relayedHandler(action string, handler immediateHandlerFunc) http.HandlerFunc {
	timed := timedHandler(a.cmdQueue, action, a.seqTracker, handler)
	if !a.relayEnabled || a.cmdQueue == nil || a.peerPoller == nil || a.peerPoller.PeerURL() == "" {
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

		seq := a.nextRelaySeq()
		at := req.ExecuteAt
		runNow := at <= 0
		if runNow {
			at = a.cmdQueue.Now()
		}
		stamped, err := stampCommand(a.resolveRelayed(action, body), at, seq)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid json")
			return
		}
		if runNow {
			cmd := cmdqueue.Command{OrderAt: at, Seq: seq, Action: action, Payload: stamped}
			if !runKeyed(a.cmdQueue, cmd, w, r, stamped, handler) {
				return
			}
		} else {
			cmd := cmdqueue.Command{ExecuteAt: at, Seq: seq, Action: action, Payload: stamped}
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

// runKeyed runs a command on a state the queue keys on arrival, as one of
// a pair relays or a caller sends to both: refused with 409 when a later
// command on its state has run here, and recorded as run when the handler
// succeeds. The handler's answer is buffered and written once the queue
// is released, so a slow caller never holds up the frame loop. It reports
// whether the command ran.
func runKeyed(q *cmdqueue.Queue, cmd cmdqueue.Command, w http.ResponseWriter, r *http.Request, body []byte, handler immediateHandlerFunc) bool {
	rec := newBufferedResponse()
	err := q.RunLocal(cmd, func() bool {
		handler(rec, r, body)
		return rec.status < http.StatusMultipleChoices
	})
	if err != nil {
		writeEnqueueError(w, err)
		return false
	}
	rec.writeTo(w)
	return rec.status < http.StatusMultipleChoices
}

// bufferedResponse holds a handler's answer until it is written out.
type bufferedResponse struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
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
}

// seqOrderAt returns where a command naming seq and no execute_at falls in
// the order: at seq, when it reads as wall-clock microseconds near now as
// the browser's and the engines' own seqs do, so every engine places it
// alike whenever it arrives; else 0, when it arrives.
func seqOrderAt(q *cmdqueue.Queue, seq uint64) int64 {
	const near = int64(10 * time.Second / time.Microsecond)
	if seq > math.MaxInt64 {
		return 0
	}
	if d := int64(seq) - q.Now(); d > -near && d < near {
		return int64(seq)
	}
	return 0
}

// stampCommand returns body, a JSON object or empty, with execute_at and
// command_seq set.
func stampCommand(body []byte, at int64, seq uint64) ([]byte, error) {
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
	return json.Marshal(fields)
}

// nextRelaySeq returns a seq for a command this engine relays: above every
// seq it has taken, wall-clock microseconds as the browser's are, even on
// engine a and odd on engine b so the two never give out the same one.
func (a *API) nextRelaySeq() uint64 {
	parity := uint64(0)
	if a.peerPoller.LocalLabel() == "b" {
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

// relayToPeer sends a command to the peer in the background. A peer
// counted down is not tried.
func (a *API) relayToPeer(method, path, auth string, body []byte) {
	if a.peerPoller.PeerDown() {
		slog.Warn("peer relay: peer down, command not passed on; it takes this engine's state once back",
			"path", path)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), relayTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, a.peerPoller.PeerURL()+path, bytes.NewReader(body))
		if err != nil {
			slog.Warn("peer relay: request not built", "path", path, "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := a.peerPoller.HTTPClient().Do(req)
		if err != nil {
			slog.Warn("peer relay: command not passed on", "path", path, "error", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		switch {
		case resp.StatusCode < http.StatusMultipleChoices:
		case resp.StatusCode == http.StatusConflict && strings.Contains(string(msg), cmdqueue.ErrSuperseded.Error()):
			// The peer ran a later command on the same state.
			slog.Info("peer relay: peer holds a later command", "path", path, "response", string(msg))
		default:
			slog.Warn("peer relay: peer refused the command", "path", path,
				"status", resp.StatusCode, "response", string(msg))
		}
	}()
}
