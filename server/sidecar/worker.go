package sidecar

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Worker is the sidecar-side of the SDK. Sidecar binaries use it to connect
// back to the host process over a Unix socket.
type Worker struct {
	conn       net.Conn
	socketPath string
	writeMu    sync.Mutex // serializes Send calls to prevent interleaved writes
}

// NewWorker connects to the host's Unix socket at socketPath.
func NewWorker(socketPath string) (*Worker, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("sidecar worker: connect: %w", err)
	}
	return &Worker{
		conn:       conn,
		socketPath: socketPath,
	}, nil
}

// Ready signals the host that initialization is complete.
// Prints "READY" to stdout. Must be called exactly once.
func (w *Worker) Ready() {
	fmt.Println("READY")
}

// Conn returns the control socket for direct protocol access.
func (w *Worker) Conn() net.Conn {
	return w.conn
}

// Send sends a message to the host. Safe for concurrent use — writes
// are serialized by writeMu to prevent interleaved header+body.
func (w *Worker) Send(msg *Message) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return WriteMessage(w.conn, msg)
}

// Recv reads the next message from the host (blocking).
func (w *Worker) Recv() (*Message, error) {
	return ReadMessage(w.conn)
}

// Run reads messages and dispatches to handler until context is cancelled or
// a shutdown command is received. If handler returns a non-nil Message, it is
// sent back to the host.
//
// On every exit path Run unblocks its internal recv goroutine by setting a
// past read deadline on the connection, so the goroutine never leaks even when
// Run returns on a shutdown command (where the conn is otherwise left open).
// Callers that keep the process alive after Run returns must still Close() the
// Worker to release the socket; Run only sets the deadline, it does not close.
func (w *Worker) Run(ctx context.Context, handler func(*Message) *Message) error {
	// Unblock the recv goroutine on any return: a past read deadline forces a
	// parked w.Recv() (blocking conn read) to fail immediately, letting the
	// goroutine observe the error and exit instead of leaking until Close().
	defer func() {
		if c, ok := w.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = c.SetReadDeadline(time.Now())
		}
	}()

	// Use a goroutine to make Recv cancellable via context.
	type recvResult struct {
		msg *Message
		err error
	}
	recvCh := make(chan recvResult, 1)

	go func() {
		for {
			msg, err := w.Recv()
			select {
			case recvCh <- recvResult{msg, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-recvCh:
			if r.err != nil {
				return fmt.Errorf("sidecar worker: recv: %w", r.err)
			}

			// Handle shutdown command.
			if r.msg.Cmd == "shutdown" {
				return nil
			}

			// Dispatch to handler.
			if reply := handler(r.msg); reply != nil {
				if err := w.Send(reply); err != nil {
					return fmt.Errorf("sidecar worker: send reply: %w", err)
				}
			}
		}
	}
}

// Close closes the control connection. Callers that keep the sidecar process
// alive after Run returns should call Close() to release the socket; Run unwinds
// its recv goroutine via a read deadline but intentionally does not close the
// conn, so the caller retains ownership.
func (w *Worker) Close() error {
	return w.conn.Close()
}
