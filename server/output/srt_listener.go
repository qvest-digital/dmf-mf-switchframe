package output

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxConns    = 8
	listenerChanBuffer = 512 // Must exceed AsyncAdapter's 256-slot buffer to avoid silent drops
)

// SRTListenerConfig holds configuration for SRT listener (pull mode).
type SRTListenerConfig struct {
	Port       int
	Latency    int   // ms, default 120
	MaxConns   int   // max simultaneous connections, default 8
	InputBW    int64 // bytes/sec, 0 = auto-estimate
	OverheadBW int   // percent, 0 = use srtgo default (25%)
}

// listenerConn wraps an SRT connection with a buffered channel for non-blocking writes.
type listenerConn struct {
	conn   srtConn
	dataCh chan []byte
	id     string
	cancel context.CancelFunc
}

// SRTListener accepts SRT pull connections and fans out TS data to all clients.
// Each connection has a buffered channel and a dedicated writer goroutine.
// Write() sends data to all channels non-blocking: slow clients get dropped
// data rather than stalling the pipeline.
type SRTListener struct {
	config SRTListenerConfig

	mu    sync.RWMutex
	conns map[string]*listenerConn

	ctx    context.Context
	cancel context.CancelFunc

	bytesWritten atomic.Int64
	clientDrops  atomic.Int64 // TS chunks dropped due to slow client dataCh
	state        atomic.Pointer[AdapterState]
	lastError    atomic.Pointer[string]
	startedAt    time.Time

	// acceptFn is overridden in tests to avoid real network I/O.
	// When set, Start() launches it in a background goroutine.
	acceptFn func(ctx context.Context, config SRTListenerConfig) error

	// onConnect is called when a new SRT client connects.
	// Used to force IDR keyframes so the client can start decoding immediately.
	onConnect func()
}

// NewSRTListener creates an SRT listener adapter.
func NewSRTListener(config SRTListenerConfig) *SRTListener {
	if config.Latency == 0 {
		config.Latency = defaultSRTLatency
	}
	if config.MaxConns == 0 {
		config.MaxConns = defaultMaxConns
	}

	l := &SRTListener{
		config: config,
		conns:  make(map[string]*listenerConn),
	}
	l.state.Store(ptrTo(StateStopped))
	l.lastError.Store(ptrTo(""))
	return l
}

// OnConnect registers a callback invoked when a new SRT client connects.
// Used to force IDR keyframes so clients can start decoding immediately
// without waiting for the next natural keyframe (up to 2s GOP interval).
func (l *SRTListener) OnConnect(fn func()) {
	l.onConnect = fn
}

// ID returns the adapter identifier.
func (l *SRTListener) ID() string { return "srt-listener" }

// Start begins listening for SRT connections.
func (l *SRTListener) Start(ctx context.Context) error {
	l.mu.Lock()
	l.ctx, l.cancel = context.WithCancel(ctx)
	l.startedAt = time.Now()
	l.bytesWritten.Store(0)
	l.state.Store(ptrTo(StateActive))
	l.lastError.Store(ptrTo(""))
	l.mu.Unlock()

	if l.acceptFn != nil {
		go func() {
			if err := l.acceptFn(l.ctx, l.config); err != nil {
				slog.Warn("SRT listener accept error", "error", err)
			}
		}()
	}

	return nil
}

// AddConnection registers a new SRT pull connection.
// Returns error if max connections reached.
func (l *SRTListener) AddConnection(id string, conn srtConn) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.conns) >= l.config.MaxConns {
		return fmt.Errorf("max connections reached (%d)", l.config.MaxConns)
	}

	ctx, cancel := context.WithCancel(l.ctx)
	lc := &listenerConn{
		conn:   conn,
		dataCh: make(chan []byte, listenerChanBuffer),
		id:     id,
		cancel: cancel,
	}
	l.conns[id] = lc

	// Start writer goroutine for this connection
	go l.connWriter(ctx, lc)

	slog.Info("SRT listener client connected", "id", id, "total", len(l.conns))

	// Request keyframe so the new client can start decoding immediately.
	if l.onConnect != nil {
		l.onConnect()
	}

	return nil
}

// RemoveConnection disconnects and removes a client.
func (l *SRTListener) RemoveConnection(id string) {
	l.mu.Lock()
	lc, ok := l.conns[id]
	if ok {
		delete(l.conns, id)
	}
	l.mu.Unlock()

	if ok {
		lc.cancel()
		lc.conn.Close()
		slog.Info("SRT listener client disconnected", "id", id)
	}
}

// ConnectionCount returns the number of active connections.
func (l *SRTListener) ConnectionCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.conns)
}

// Write fans out TS data to all connected clients non-blocking.
func (l *SRTListener) Write(tsData []byte) (int, error) {
	if state := *l.state.Load(); state != StateActive {
		return 0, fmt.Errorf("srt listener not active (state: %s)", state)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.conns) == 0 {
		l.bytesWritten.Add(int64(len(tsData)))
		return len(tsData), nil
	}

	// Single copy shared across all connections. connWriter goroutines
	// only read the data (passing to conn.Write), so sharing is safe.
	cp := make([]byte, len(tsData))
	copy(cp, tsData)

	for _, lc := range l.conns {
		// Non-blocking send: drop data for slow clients
		select {
		case lc.dataCh <- cp:
		default:
			// Slow client — drop this chunk. This is the most likely
			// cause of low FPS on SRT output clients (e.g., VLC).
			l.clientDrops.Add(1)
		}
	}

	l.bytesWritten.Add(int64(len(tsData)))
	return len(tsData), nil
}

// Close stops the listener and disconnects all clients.
func (l *SRTListener) Close() error {
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
	}
	for id, lc := range l.conns {
		lc.cancel()
		lc.conn.Close()
		delete(l.conns, id)
	}
	l.mu.Unlock()

	l.state.Store(ptrTo(StateStopped))
	return nil
}

// Status returns the current listener status.
func (l *SRTListener) Status() AdapterStatus {
	state := *l.state.Load()
	errStr := *l.lastError.Load()

	return AdapterStatus{
		State:        state,
		BytesWritten: l.bytesWritten.Load(),
		StartedAt:    l.startedAt,
		Error:        errStr,
	}
}

// SRTStatusSnapshot returns an SRTStatus for ControlRoomState.
func (l *SRTListener) SRTStatusSnapshot() SRTStatus {
	status := l.Status()
	return SRTStatus{
		Active:       status.State == StateActive,
		Mode:         "listener",
		Port:         l.config.Port,
		State:        string(status.State),
		Connections:  l.ConnectionCount(),
		BytesWritten: status.BytesWritten,
		Error:        status.Error,
	}
}

// connWriter drains the data channel and writes to the SRT connection.
// On write error, the client is removed. Exits when the connection's
// context is cancelled.
func (l *SRTListener) connWriter(ctx context.Context, lc *listenerConn) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-lc.dataCh:
			if !ok {
				return
			}
			if _, err := lc.conn.Write(data); err != nil {
				slog.Warn("SRT listener write failed, removing client",
					"id", lc.id, "error", err)
				l.RemoveConnection(lc.id)
				return
			}
		}
	}
}

// Compile-time check that SRTListener satisfies the Adapter interface.
var _ Adapter = (*SRTListener)(nil)
