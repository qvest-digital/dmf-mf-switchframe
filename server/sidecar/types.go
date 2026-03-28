package sidecar

import (
	"net"
	"time"
)

// Config configures a sidecar host.
type Config struct {
	// Binary is the path to the sidecar executable.
	Binary string

	// Args are additional command-line arguments passed to the sidecar.
	Args []string

	// ReadyTimeout is how long to wait for the "READY" signal on stdout.
	// Default: 30s.
	ReadyTimeout time.Duration

	// ShutdownTimeout is how long to wait for graceful exit before SIGKILL.
	// Default: 2s.
	ShutdownTimeout time.Duration

	// InitialBackoff is the starting restart delay. Default: 1s.
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential restart delay. Default: 30s.
	MaxBackoff time.Duration

	// SocketDir overrides the temp directory for Unix sockets.
	// If empty, os.MkdirTemp is used.
	SocketDir string

	// BuildArgs constructs the command arguments from the socket directory.
	// If nil, the control socket path is passed as the first argument
	// followed by Args.
	BuildArgs func(socketDir string) []string

	// Connect is called after the sidecar prints READY to establish the
	// control connection. If nil, the Host creates a Unix listener and
	// waits for the sidecar to connect.
	//
	// When set, the Host does NOT create a listener or run its built-in
	// readLoop. The caller is responsible for reading from the connection
	// and calling TriggerRestart on errors. This is needed for sidecars
	// like CEF that create their own listeners and use non-standard
	// protocols (e.g. newline-delimited JSON instead of length-prefixed).
	Connect func(socketDir string) (net.Conn, error)

	// DisableReadLoop prevents the Host from starting its built-in readLoop
	// after spawn. Use this when the sidecar uses a custom wire protocol
	// (e.g. asripc) that is incompatible with the SDK's ReadMessage format
	// but otherwise follows the standard connect pattern (sidecar connects
	// to host listener). The caller should read from Conn() directly and
	// call TriggerRestart on errors.
	DisableReadLoop bool

	// DisableShutdownMessage prevents the Host from sending its own
	// shutdown message in Close(). Use this when the caller sends the
	// shutdown command using a different protocol format (e.g. CEF uses
	// newline-delimited JSON, not the SDK's length-prefixed format).
	DisableShutdownMessage bool

	// OnStatus is called when the sidecar reports status changes.
	OnStatus func(Status)

	// OnRestart is called after a successful restart (backoff reset).
	OnRestart func(generation int64)
}

// Status represents sidecar health.
type Status struct {
	Alive      bool
	Generation int64
	Restarts   int64
	LastError  string
}

// Message is the control channel wire format (length-prefixed JSON).
type Message struct {
	// Host -> Worker
	Cmd    string `json:"cmd,omitempty"`
	Config any    `json:"config,omitempty"`

	// Worker -> Host
	Type  string `json:"type,omitempty"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

func (c Config) withDefaults() Config {
	if c.ReadyTimeout == 0 {
		c.ReadyTimeout = 30 * time.Second
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 2 * time.Second
	}
	if c.InitialBackoff == 0 {
		c.InitialBackoff = 1 * time.Second
	}
	if c.MaxBackoff == 0 {
		c.MaxBackoff = 30 * time.Second
	}
	return c
}
