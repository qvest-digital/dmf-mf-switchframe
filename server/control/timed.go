package control

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// timedRequest extracts PTP timing fields from a command body.
type timedRequest struct {
	ExecuteAt  int64  `json:"execute_at"`
	CommandSeq uint64 `json:"command_seq"`
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
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "failed to read body")
			return
		}

		var timed timedRequest
		_ = json.Unmarshal(body, &timed) // ignore error — fields are optional

		if timed.ExecuteAt > 0 && q != nil {
			cmd := cmdqueue.Command{
				ExecuteAt: timed.ExecuteAt,
				Seq:       timed.CommandSeq,
				Action:    action,
				Payload:   json.RawMessage(body),
			}
			if err := q.Enqueue(cmd); err != nil {
				httperr.Write(w, http.StatusBadRequest, err.Error())
				return
			}
			if seqTracker != nil && timed.CommandSeq > 0 {
				seqTracker(timed.CommandSeq)
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		handler(w, r, body)
	}
}

// timedHandlerWithPath wraps a command handler, injecting URL path values into
// the JSON payload before enqueueing. This ensures that when the dispatcher
// replays the command (without an HTTP request), path-derived values like
// {source} or {id} are available in the payload as "_path_<key>".
func timedHandlerWithPath(q *cmdqueue.Queue, action string, pathKeys []string, handler immediateHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
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
				Seq:       timed.CommandSeq,
				Action:    action,
				Payload:   json.RawMessage(enriched),
			}
			if err := q.Enqueue(cmd); err != nil {
				httperr.Write(w, http.StatusBadRequest, err.Error())
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		handler(w, r, body)
	}
}
