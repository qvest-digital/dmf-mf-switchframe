package control

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// maxCommandBodySize bounds command-handler request bodies as defense-in-depth,
// independent of MaxBytesMiddleware. It matches the global JSON cap so the
// legitimate path is unaffected, but ensures io.ReadAll cannot allocate without
// bound even if the outer middleware is misconfigured or removed.
const maxCommandBodySize = maxJSONBodySize

// timedRequest extracts PTP timing fields from a command body.
type timedRequest struct {
	ExecuteAt  int64  `json:"execute_at"`
	CommandSeq uint64 `json:"command_seq"`
	// OrderAt places a relayed command among those on its state; see
	// cmdqueue.Command.OrderAt.
	OrderAt int64 `json:"order_at"`
}

// immediateHandlerFunc is a handler that receives the pre-read request body.
type immediateHandlerFunc func(w http.ResponseWriter, r *http.Request, body []byte)

// timedHandler wraps a command handler with execute_at/command_seq support.
// If execute_at is present and a command queue is configured, the command is
// enqueued for timed execution (202 Accepted).
// If absent, zero, or no queue is configured, the inner handler executes immediately.
// seqTracker, when non-nil, is called after successful enqueue to update the
// highest-enqueued sequence number (for staleness detection in state broadcasts).
func timedHandler(q *cmdqueue.Queue, action string, seqTracker func(uint64), handler immediateHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Bound the read so a missing/incorrect outer body cap cannot translate
		// into an unbounded allocation. Command bodies are small JSON.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommandBodySize))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "failed to read body")
			return
		}

		var timed timedRequest
		_ = json.Unmarshal(body, &timed) // ignore error — fields are optional

		if timed.ExecuteAt > 0 && q != nil {
			cmd := cmdqueue.Command{
				ExecuteAt: timed.ExecuteAt,
				OrderAt:   placeAt(q, timed.OrderAt),
				Seq:       timed.CommandSeq,
				Action:    action,
				Payload:   json.RawMessage(body),
			}
			if err := q.Enqueue(cmd); err != nil {
				writeEnqueueError(w, err)
				// One overtaken by a later command on its state was
				// taken all the same: both engines of a pair count it.
				if errors.Is(err, cmdqueue.ErrSuperseded) && seqTracker != nil && timed.CommandSeq > 0 {
					seqTracker(timed.CommandSeq)
				}
				return
			}
			if seqTracker != nil && timed.CommandSeq > 0 {
				seqTracker(timed.CommandSeq)
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		if q != nil && timed.CommandSeq > 0 && q.Keyed(action) {
			// A command both engines of a pair take, run on arrival: it
			// falls among the commands on its state at the order_at its
			// caller stamped it with alike for both, else as it arrives.
			cmd := cmdqueue.Command{Seq: timed.CommandSeq, OrderAt: placeAt(q, timed.OrderAt), Action: action, Payload: json.RawMessage(body)}
			switch runKeyed(q, cmd, w, r, body, handler) {
			case keyedFailed:
				return
			case keyedSuperseded:
				// The peer took it too; the seq is taken alike on both.
				if seqTracker != nil {
					seqTracker(timed.CommandSeq)
				}
				return
			}
		} else {
			handler(w, r, body)
		}

		// Track command_seq for immediate execution too, so the sync health
		// endpoint detects all state changes, not just queued ones.
		if seqTracker != nil && timed.CommandSeq > 0 {
			seqTracker(timed.CommandSeq)
		}
	}
}

// timedHandlerWithPath wraps a command handler, injecting URL path values into
// the JSON payload before enqueueing. This ensures that when the dispatcher
// replays the command (without an HTTP request), path-derived values like
// {source} or {id} are available in the payload as "_path_<key>".
func timedHandlerWithPath(q *cmdqueue.Queue, action string, seqTracker func(uint64), pathKeys []string, handler immediateHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Bound the read so a missing/incorrect outer body cap cannot translate
		// into an unbounded allocation. Command bodies are small JSON.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommandBodySize))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "failed to read body")
			return
		}

		var timed timedRequest
		_ = json.Unmarshal(body, &timed) // ignore error — fields are optional

		if timed.ExecuteAt > 0 && q != nil {
			// Inject path values into payload for deferred execution.
			var payload map[string]interface{}
			if len(body) > 0 {
				_ = json.Unmarshal(body, &payload)
			}
			if payload == nil {
				payload = make(map[string]interface{})
			}
			for _, key := range pathKeys {
				payload["_path_"+key] = r.PathValue(key)
			}
			enriched, _ := json.Marshal(payload)

			cmd := cmdqueue.Command{
				ExecuteAt: timed.ExecuteAt,
				OrderAt:   placeAt(q, timed.OrderAt),
				Seq:       timed.CommandSeq,
				Action:    action,
				Payload:   json.RawMessage(enriched),
			}
			if err := q.Enqueue(cmd); err != nil {
				writeEnqueueError(w, err)
				// One overtaken by a later command on its state was
				// taken all the same: both engines of a pair count it.
				if errors.Is(err, cmdqueue.ErrSuperseded) && seqTracker != nil && timed.CommandSeq > 0 {
					seqTracker(timed.CommandSeq)
				}
				return
			}
			if seqTracker != nil && timed.CommandSeq > 0 {
				seqTracker(timed.CommandSeq)
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		if q != nil && timed.CommandSeq > 0 && q.Keyed(action) {
			// A command both engines of a pair take, run on arrival: it
			// falls among the commands on its state at the order_at its
			// caller stamped it with alike for both, else as it arrives.
			cmd := cmdqueue.Command{Seq: timed.CommandSeq, OrderAt: placeAt(q, timed.OrderAt), Action: action, Payload: json.RawMessage(body)}
			switch runKeyed(q, cmd, w, r, body, handler) {
			case keyedFailed:
				return
			case keyedSuperseded:
				// The peer took it too; the seq is taken alike on both.
				if seqTracker != nil {
					seqTracker(timed.CommandSeq)
				}
				return
			}
		} else {
			handler(w, r, body)
		}

		// Track command_seq for immediate execution too, so the sync health
		// endpoint detects all state changes, not just queued ones.
		if seqTracker != nil && timed.CommandSeq > 0 {
			seqTracker(timed.CommandSeq)
		}
	}
}

// writeEnqueueError answers a command the queue refused: 409 for one a
// later command on its state has overtaken, else 400.
func writeEnqueueError(w http.ResponseWriter, err error) {
	if errors.Is(err, cmdqueue.ErrSuperseded) {
		httperr.Write(w, http.StatusConflict, err.Error())
		return
	}
	httperr.Write(w, http.StatusBadRequest, err.Error())
}

// placeAt returns orderAt when it lies within orderWindow of the queue's
// clock, else 0, which places a command when it arrives or by its
// execute_at: a caller whose clock is off does not have its commands
// placed before ones it made earlier.
func placeAt(q *cmdqueue.Queue, orderAt int64) int64 {
	if orderAt <= 0 {
		return 0
	}
	if d := time.Duration(orderAt-q.Now()) * time.Microsecond; d > orderWindow || d < -orderWindow {
		return 0
	}
	return orderAt
}
