package output

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Sentinel errors for destination operations.
var (
	ErrDestinationNotFound = errors.New("output: destination not found")
	ErrDestinationActive   = errors.New("output: destination already active")
	ErrDestinationStopped  = errors.New("output: destination not active")
)

// DestinationConfig configures a single output destination.
type DestinationConfig struct {
	Type       string `json:"type"` // "srt-caller" or "srt-listener"
	Address    string `json:"address,omitempty"`
	Port       int    `json:"port"`
	Latency    int    `json:"latency,omitempty"` // ms, default 120
	StreamID   string `json:"streamID,omitempty"`
	Encryption string `json:"encryption,omitempty"` // "", "aes-128", "aes-256"
	Passphrase string `json:"passphrase,omitempty"`
	MaxBW      int64  `json:"maxBandwidth,omitempty"`
	MaxConns   int    `json:"maxConns,omitempty"` // listener only
	Name       string `json:"name,omitempty"`     // user-friendly label
}

// DestinationStatus is the runtime status of a destination.
type DestinationStatus struct {
	ID             string            `json:"id"`
	Config         DestinationConfig `json:"config"`
	State          string            `json:"state"` // "stopped", "starting", "connected", "error"
	BytesWritten   int64             `json:"bytesWritten"`
	DroppedPackets int64             `json:"droppedPackets"`
	OverflowCount  int64             `json:"overflowCount,omitempty"`
	Connections    int               `json:"connections,omitempty"`
	Error          string            `json:"error,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	StartedAt      *time.Time        `json:"startedAt,omitempty"`
}

// OutputDestination represents a single configured output destination.
type OutputDestination struct {
	mu        sync.Mutex
	id        string
	config    DestinationConfig
	adapter   OutputAdapter
	async     *AsyncAdapter
	active    bool
	createdAt time.Time
	startedAt *time.Time
}

// generateDestinationID creates a random 8-character hex ID.
func generateDestinationID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// status returns the current DestinationStatus snapshot.
func (d *OutputDestination) status() DestinationStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := DestinationStatus{
		ID:        d.id,
		Config:    d.config,
		State:     "stopped",
		CreatedAt: d.createdAt,
		StartedAt: d.startedAt,
	}

	if d.adapter != nil {
		adapterStatus := d.adapter.Status()
		st.State = string(adapterStatus.State)
		st.BytesWritten = adapterStatus.BytesWritten
		st.Error = adapterStatus.Error
	}

	if d.async != nil {
		st.DroppedPackets = d.async.Dropped()
	}

	// Type-specific fields.
	if d.adapter != nil {
		switch a := d.adapter.(type) {
		case *SRTCaller:
			st.OverflowCount = a.overflowCount.Load()
		case *SRTListener:
			st.Connections = a.ConnectionCount()
		}
	}

	return st
}
