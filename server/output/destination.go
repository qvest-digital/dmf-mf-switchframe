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
	ErrDestinationNotFound  = errors.New("output: destination not found")
	ErrDestinationActive    = errors.New("output: destination already active")
	ErrDestinationStopped   = errors.New("output: destination not active")
	ErrDestinationExists    = errors.New("output: destination ID already in use")
	ErrInvalidDestinationID = errors.New("output: invalid destination ID")
)

// DestinationConfig configures a single output destination.
type DestinationConfig struct {
	Type          string `json:"type"` // "srt-caller", "srt-listener", or "rtmp"
	Address       string `json:"address,omitempty"`
	Port          int    `json:"port,omitempty"`
	URL           string `json:"url,omitempty"`     // RTMP only: full rtmp:// URL
	Latency       int    `json:"latency,omitempty"` // ms, default 120
	StreamID      string `json:"streamID,omitempty"`
	Encryption    string `json:"encryption,omitempty"` // "", "aes-128", "aes-256"
	Passphrase    string `json:"passphrase,omitempty"`
	MaxBW         int64  `json:"maxBandwidth,omitempty"`
	MaxConns      int    `json:"maxConns,omitempty"`      // listener only
	Name          string `json:"name,omitempty"`          // user-friendly label
	SCTE35Enabled bool   `json:"scte35Enabled,omitempty"` // default true; false strips SCTE-35
	// Engine is the engine of a pair that sends: "a", "b", or "" for
	// every engine. A labeled engine stores "a" when none is given, so
	// both engines of a pair agree on the sender.
	Engine string `json:"engine,omitempty"`

	// AllowUntrustedCert skips verifying an rtmps:// server certificate.
	AllowUntrustedCert bool `json:"allowUntrustedCert,omitempty"`
}

// DestinationStatus is the runtime status of a destination.
type DestinationStatus struct {
	ID             string            `json:"id"`
	Config         DestinationConfig `json:"config"`
	State          string            `json:"state"` // "stopped", "standby", or the adapter's state
	Armed          bool              `json:"armed,omitempty"`
	BytesWritten   int64             `json:"bytesWritten"`
	DroppedPackets int64             `json:"droppedPackets"`
	OverflowCount  int64             `json:"overflowCount,omitempty"`
	Connections    int               `json:"connections,omitempty"`
	Error          string            `json:"error,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	StartedAt      *time.Time        `json:"startedAt,omitempty"`
}

// Destination represents a single configured output destination.
type Destination struct {
	mu           sync.Mutex
	id           string
	config       DestinationConfig
	adapter      Adapter      // TS-based adapters (SRT)
	frameAdapter FrameAdapter // Frame-based adapters (RTMP direct)
	async        *AsyncAdapter
	active       bool      // an adapter runs on this engine
	armed        bool      // started by the operator; the owner engine sends
	unsentSince  time.Time // armed here, owned by a reachable peer not sending it
	createdAt    time.Time
	startedAt    *time.Time

	// changedAt is when this engine last changed the sending engine, the
	// armed state or started the adapter. A peer poll sent before it shows
	// the peer as it was before the change and is not acted on.
	changedAt time.Time
	// operatorChangedAt is when an operator's start, stop or switch-over
	// last changed the destination here. The UI sends those to both engines,
	// so the peer's leg may land up to PeerView.Skew later; this engine's
	// own decisions have no second leg.
	operatorChangedAt time.Time
	// peerListed and peerArmed are set once a poll of the current peer
	// process, sent after changedAt, listed the destination, and listed it
	// armed. Losing either later means an operator deleted or stopped it
	// on the peer.
	peerListed bool
	peerArmed  bool
}

// setEngineLocked sets the sending engine. Holds d.mu.
func (d *Destination) setEngineLocked(engine string, now time.Time) bool {
	if d.config.Engine == engine {
		return false
	}
	d.config.Engine = engine
	d.changedAt = now
	d.unsentSince = time.Time{}
	return true
}

// setArmedLocked sets the armed state. Holds d.mu.
func (d *Destination) setArmedLocked(armed bool, now time.Time) bool {
	if d.armed == armed {
		return false
	}
	d.armed = armed
	d.changedAt = now
	d.unsentSince = time.Time{}
	d.peerArmed = false
	return true
}

// generateDestinationID creates a random 8-character hex ID.
func generateDestinationID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidDestinationID reports whether id has the form of a generated
// destination ID: 8 lowercase hex characters.
func ValidDestinationID(id string) bool {
	if len(id) != 8 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// status returns the current DestinationStatus snapshot.
func (d *Destination) status() DestinationStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := DestinationStatus{
		ID:        d.id,
		Config:    d.config,
		State:     "stopped",
		Armed:     d.armed,
		CreatedAt: d.createdAt,
		StartedAt: d.startedAt,
	}
	if d.armed && !d.active {
		st.State = "standby"
	}

	if d.adapter != nil {
		adapterStatus := d.adapter.Status()
		st.State = string(adapterStatus.State)
		st.BytesWritten = adapterStatus.BytesWritten
		st.Error = adapterStatus.Error
	}

	if d.frameAdapter != nil {
		faStatus := d.frameAdapter.Status()
		st.State = string(faStatus.State)
		st.BytesWritten = faStatus.BytesWritten
		st.Error = faStatus.Error
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
