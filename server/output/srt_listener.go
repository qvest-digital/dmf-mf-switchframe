package output

// NOTE: This is a stub file created to allow the package to compile while
// the SRT listener implementation (Task 8) is in progress. The test file
// srt_listener_test.go was already committed and references these types.
// Replace this stub with the real implementation.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxConns    = 4
	defaultListenerBuf = 64 // channel buffer per connection
)

// SRTListenerConfig holds configuration for SRT listener (pull mode).
type SRTListenerConfig struct {
	Port     int
	Latency  int // ms, default 120
	MaxConns int // max simultaneous connections
}

// listenerClient represents a single connected SRT client.
type listenerClient struct {
	id   string
	conn srtConn
	ch   chan []byte
}

// SRTListener accepts SRT connections and fans out MPEG-TS data.
type SRTListener struct {
	config SRTListenerConfig

	mu       sync.Mutex
	clients  map[string]*listenerClient
	ctx      context.Context
	cancel   context.CancelFunc
	state    atomic.Value // AdapterState
	started  time.Time
	bytes    atomic.Int64
	lastErr  string
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
		config:  config,
		clients: make(map[string]*listenerClient),
	}
	l.state.Store(StateStopped)
	return l
}

func (l *SRTListener) ID() string { return "srt-listener" }

func (l *SRTListener) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ctx, l.cancel = context.WithCancel(ctx)
	l.started = time.Now()
	l.state.Store(StateActive)
	return nil
}

func (l *SRTListener) Write(tsData []byte) (int, error) {
	state := l.state.Load().(AdapterState)
	if state != StateActive {
		return 0, fmt.Errorf("listener not active")
	}

	l.bytes.Add(int64(len(tsData)))

	l.mu.Lock()
	clients := make([]*listenerClient, 0, len(l.clients))
	for _, c := range l.clients {
		clients = append(clients, c)
	}
	l.mu.Unlock()

	for _, c := range clients {
		cp := make([]byte, len(tsData))
		copy(cp, tsData)
		select {
		case c.ch <- cp:
		default:
			// Drop frame for slow client
		}
	}

	return len(tsData), nil
}

func (l *SRTListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cancel != nil {
		l.cancel()
	}

	for id, c := range l.clients {
		c.conn.Close()
		close(c.ch)
		delete(l.clients, id)
	}

	l.state.Store(StateStopped)
	return nil
}

func (l *SRTListener) Status() AdapterStatus {
	state := l.state.Load().(AdapterState)
	return AdapterStatus{
		State:        state,
		BytesWritten: l.bytes.Load(),
		StartedAt:    l.started,
		Error:        l.lastErr,
	}
}

func (l *SRTListener) AddConnection(id string, conn srtConn) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.clients) >= l.config.MaxConns {
		return fmt.Errorf("max connections (%d) reached", l.config.MaxConns)
	}

	c := &listenerClient{
		id:   id,
		conn: conn,
		ch:   make(chan []byte, defaultListenerBuf),
	}
	l.clients[id] = c

	go l.clientWriter(c)
	return nil
}

func (l *SRTListener) RemoveConnection(id string) {
	l.mu.Lock()
	c, ok := l.clients[id]
	if ok {
		delete(l.clients, id)
	}
	l.mu.Unlock()

	if ok {
		c.conn.Close()
	}
}

func (l *SRTListener) ConnectionCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.clients)
}

func (l *SRTListener) SRTStatusSnapshot() SRTOutputStatus {
	status := l.Status()
	return SRTOutputStatus{
		Active:       status.State == StateActive,
		Mode:         "listener",
		Port:         l.config.Port,
		State:        string(status.State),
		Connections:  l.ConnectionCount(),
		BytesWritten: status.BytesWritten,
		Error:        status.Error,
	}
}

func (l *SRTListener) clientWriter(c *listenerClient) {
	for data := range c.ch {
		if _, err := c.conn.Write(data); err != nil {
			l.RemoveConnection(c.id)
			return
		}
	}
}

var _ OutputAdapter = (*SRTListener)(nil)
