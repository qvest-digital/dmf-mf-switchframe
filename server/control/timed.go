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
func timedHandler(q *cmdqueue.Queue, action string, handler immediateHandlerFunc) http.HandlerFunc {
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
			w.WriteHeader(http.StatusAccepted)
			return
		}

		handler(w, r, body)
	}
}
