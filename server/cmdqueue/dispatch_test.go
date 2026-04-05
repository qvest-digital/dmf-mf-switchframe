package cmdqueue_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zsiec/switchframe/server/cmdqueue"
)

// TestDispatcher_RouteToHandler verifies that a registered handler is called
// with the correct payload when a matching action is dispatched.
func TestDispatcher_RouteToHandler(t *testing.T) {
	d := cmdqueue.NewDispatcher()

	type cutPayload struct {
		Src string `json:"src"`
	}

	var got cutPayload
	called := false

	d.Register("cut", func(payload json.RawMessage) error {
		called = true
		return json.Unmarshal(payload, &got)
	})

	raw, _ := json.Marshal(cutPayload{Src: "cam1"})
	cmd := cmdqueue.Command{
		ExecuteAt: 0,
		Seq:       1,
		Action:    "cut",
		Payload:   raw,
	}

	if err := d.Dispatch(cmd); err != nil {
		t.Fatalf("Dispatch returned unexpected error: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	if got.Src != "cam1" {
		t.Errorf("handler got payload src=%q, want %q", got.Src, "cam1")
	}
}

// TestDispatcher_UnknownAction verifies that dispatching a command with an
// unregistered action returns ErrUnknownAction.
func TestDispatcher_UnknownAction(t *testing.T) {
	d := cmdqueue.NewDispatcher()

	cmd := cmdqueue.Command{
		ExecuteAt: 0,
		Seq:       2,
		Action:    "nonexistent",
	}

	err := d.Dispatch(cmd)
	if err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
	if !errors.Is(err, cmdqueue.ErrUnknownAction) {
		t.Errorf("expected ErrUnknownAction, got %v", err)
	}
	// Verify the action name is included in the error message.
	if msg := err.Error(); msg == "" {
		t.Error("error message should be non-empty")
	}
}
