package fastctrl

import (
	"encoding/binary"
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

	// Detect PTP timestamp prefix: 8-byte big-endian µs timestamp in a
	// plausible range (2020-01-01 to 2040-01-01). Old-format datagrams whose
	// first byte is a message type (0x01-0x09) will never be misinterpreted
	// because those values produce uint64s far below the 2020 threshold.
	if len(data) >= 10 {
		ts := binary.BigEndian.Uint64(data[0:8])
		if ts > 1_577_836_800_000_000 && ts < 2_208_988_800_000_000 {
			data = data[8:]
		}
	}

	h := d.handlers[data[0]]
	if h == nil {
		d.log.Debug("unknown fast-control message type", "type", data[0])
		return ErrUnknownType
	}
	return h(data[1:])
}
