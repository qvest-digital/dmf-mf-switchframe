package fastctrl

import (
	"errors"
	"log/slog"
)

var (
	ErrEmptyDatagram = errors.New("empty datagram")
	ErrUnknownType   = errors.New("unknown message type")
)

type Handler func(data []byte) error

type Dispatcher struct {
	handlers [256]Handler
	log      *slog.Logger
}

// NewDispatcher creates a new fast-control datagram dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{log: slog.Default()}
}

// Deprecated: New is an alias for NewDispatcher. Use NewDispatcher instead.
func New() *Dispatcher {
	return NewDispatcher()
}

func (d *Dispatcher) Register(msgType byte, h Handler) {
	d.handlers[msgType] = h
}

func (d *Dispatcher) Dispatch(data []byte) error {
	if len(data) == 0 {
		return ErrEmptyDatagram
	}
	h := d.handlers[data[0]]
	if h == nil {
		d.log.Debug("unknown fast-control message type", "type", data[0])
		return ErrUnknownType
	}
	return h(data[1:])
}
