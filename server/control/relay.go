package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// relayedStateKeys are the commands an engine of a pair passes on to its
// peer, each with the state it sets: the program, the preview and the
// squeezeback. Each sets its state whole, so of two the later holds. A
// toggle, as FTB is, is left to the caller and to convergence.
var relayedStateKeys = map[string]string{
	"cut":             "program",
	"transition":      "program",
	"preview":         "preview",
	"squeezeback_set": "squeezeback_config",
	"squeezeback_on":  "squeezeback",
	"squeezeback_off": "squeezeback",
}

// RelayedStateKeys returns the state each command an engine relays to its
// peer sets, as cmdqueue.WithStateKeys takes it.
func RelayedStateKeys() map[string]string { return maps.Clone(relayedStateKeys) }

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
		runNow := at == 0
		if runNow {
			at = a.cmdQueue.Now()
		}
		stamped, err := stampCommand(body, at, seq)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid json")
			return
		}
		if runNow {
			rec := &statusRecorder{ResponseWriter: w}
			handler(rec, r, stamped)
			if rec.status >= http.StatusMultipleChoices {
				return
			}
			a.cmdQueue.Ran(action, at, seq)
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
		case resp.StatusCode == http.StatusConflict:
			// The peer ran a later command on the same state.
			slog.Info("peer relay: peer holds a later command", "path", path, "response", string(msg))
		default:
			slog.Warn("peer relay: peer refused the command", "path", path,
				"status", resp.StatusCode, "response", string(msg))
		}
	}()
}
