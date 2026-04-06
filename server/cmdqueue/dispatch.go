package cmdqueue

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownAction is returned by Dispatch when no handler is registered for
// the command's action name.
var ErrUnknownAction = errors.New("unknown command action")

// HandlerFunc is the signature for command handler functions. The payload is
// the raw JSON bytes from the Command and may be nil for actions that carry no
// additional data.
type HandlerFunc func(payload json.RawMessage) error

// Dispatcher routes Commands to registered HandlerFunc values by action name.
// It sits between the queue's Drain() output and the actual subsystem methods.
type Dispatcher struct {
	handlers map[string]HandlerFunc
}

// NewDispatcher returns a ready-to-use Dispatcher with an empty handler map.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		handlers: make(map[string]HandlerFunc),
	}
}

// Register associates action with fn. Subsequent calls with the same action
// name overwrite the previous registration.
func (d *Dispatcher) Register(action string, fn HandlerFunc) {
	d.handlers[action] = fn
}

// Dispatch looks up cmd.Action in the registered handlers and calls the
// matching handler with cmd.Payload. If no handler is registered for the
// action, it returns a wrapped ErrUnknownAction that includes the action name.
func (d *Dispatcher) Dispatch(cmd Command) error {
	fn, ok := d.handlers[cmd.Action]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAction, cmd.Action)
	}
	return fn(cmd.Payload)
}
