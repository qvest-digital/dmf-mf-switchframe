// Package control implements the switcher control plane, including
// REST command handling and MoQ state broadcasting.
package control

import (
	"encoding/json"
	"log/slog"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/internal"
)

// PublishFunc is a callback invoked with serialized state data.
// In production this will write to a MoQ "control" track;
// for now it is a simple function callback.
type PublishFunc func(data []byte)

// StatePublisher serializes ControlRoomState to JSON and invokes
// a publish callback to broadcast it to connected browsers.
type StatePublisher struct {
	publishFn PublishFunc
}

// NewStatePublisher creates a StatePublisher that calls fn
// each time state is published.
func NewStatePublisher(fn PublishFunc) *StatePublisher {
	return &StatePublisher{publishFn: fn}
}

// Publish serializes the given state to JSON and invokes the
// publish callback. If marshalling fails, it logs the error
// and returns without calling the callback.
func (sp *StatePublisher) Publish(state internal.ControlRoomState) {
	data, err := json.Marshal(state)
	if err != nil {
		slog.Error("failed to marshal state", "error", err)
		return
	}
	sp.publishFn(data)
}

// ChannelPublisher sends serialized JSON state to a channel for MoQ broadcast.
type ChannelPublisher struct {
	ch      chan []byte
	dropped atomic.Int64
}

// NewChannelPublisher creates a publisher with a buffered channel.
func NewChannelPublisher(bufSize int) *ChannelPublisher {
	return &ChannelPublisher{ch: make(chan []byte, bufSize)}
}

// Publish serializes state and sends to the channel.
// If the channel is full, the oldest message is dropped.
func (cp *ChannelPublisher) Publish(state internal.ControlRoomState) {
	data, err := json.Marshal(state)
	if err != nil {
		slog.Error("failed to marshal state", "error", err)
		return
	}
	for {
		select {
		case cp.ch <- data:
			return
		default:
			// Drop oldest to make room
			select {
			case <-cp.ch:
				cp.dropped.Add(1)
			default:
			}
		}
	}
}

// DroppedCount returns the number of state messages dropped due to a full buffer.
func (cp *ChannelPublisher) DroppedCount() int64 {
	return cp.dropped.Load()
}

// Ch returns the read-only channel for MoQ consumption.
func (cp *ChannelPublisher) Ch() <-chan []byte {
	return cp.ch
}
