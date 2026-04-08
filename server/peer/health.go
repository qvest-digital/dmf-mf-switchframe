// Package peer provides inter-engine health monitoring for active-active
// redundancy. Each engine polls its peer's /api/peer/health endpoint to
// detect failures before the control plane's 30s health cycle notices.
package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// HealthResponse is the JSON payload returned by /api/peer/health.
type HealthResponse struct {
	Status         string `json:"status"`
	UptimeMs       int64  `json:"uptimeMs"`
	ProgramSource  string `json:"programSource"`
	LastCommandSeq uint64 `json:"lastCommandSeq"`
}

// PeerStatus holds the current peer health state, safe to read from any
// goroutine via HealthPoller.Status().
type PeerStatus struct {
	Reachable      bool
	LastSeen       time.Time
	ConsecFailures int
	ProgramSource  string
	LastCommandSeq uint64
	UptimeMs       int64
}

// Option configures a HealthPoller.
type Option func(*HealthPoller)

// WithPollInterval sets the polling interval (default 5s).
func WithPollInterval(d time.Duration) Option {
	return func(p *HealthPoller) { p.pollInterval = d }
}

// WithFailureThreshold sets the number of consecutive failures before the
// peer is considered unreachable (default 3).
func WithFailureThreshold(n int) Option {
	return func(p *HealthPoller) { p.failureThreshold = n }
}

// WithHTTPClient sets a custom HTTP client for peer health requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *HealthPoller) { p.client = c }
}

// HealthPoller periodically polls a peer engine's health endpoint and
// tracks whether the peer is reachable.
type HealthPoller struct {
	peerURL          string
	pollInterval     time.Duration
	failureThreshold int
	client           *http.Client

	mu             sync.RWMutex
	reachable      bool
	lastSeen       time.Time
	consecFailures int
	programSource  string
	lastCommandSeq uint64
	uptimeMs       int64

	cancel  context.CancelFunc
	stopped atomic.Bool
	done    chan struct{}
}

// NewHealthPoller creates a new poller for the given peer URL. If peerURL
// is empty, the poller does nothing (no peer configured).
func NewHealthPoller(peerURL string, opts ...Option) *HealthPoller {
	p := &HealthPoller{
		peerURL:          peerURL,
		pollInterval:     5 * time.Second,
		failureThreshold: 3,
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		done: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start begins polling the peer. No-op if no peer URL is configured.
func (p *HealthPoller) Start() {
	if p.peerURL == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.loop(ctx)
}

// Stop halts the polling loop. Safe to call multiple times.
func (p *HealthPoller) Stop() {
	if !p.stopped.CompareAndSwap(false, true) {
		return // Already stopped or never started.
	}
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
}

// PeerReachable returns true if the peer engine is responding to health checks.
func (p *HealthPoller) PeerReachable() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.reachable
}

// PeerLastSeen returns the last time the peer responded successfully.
func (p *HealthPoller) PeerLastSeen() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastSeen
}

// Status returns a snapshot of the current peer status.
func (p *HealthPoller) Status() PeerStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return PeerStatus{
		Reachable:      p.reachable,
		LastSeen:       p.lastSeen,
		ConsecFailures: p.consecFailures,
		ProgramSource:  p.programSource,
		LastCommandSeq: p.lastCommandSeq,
		UptimeMs:       p.uptimeMs,
	}
}

func (p *HealthPoller) loop(ctx context.Context) {
	defer close(p.done)

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Poll immediately on start.
	p.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *HealthPoller) poll(ctx context.Context) {
	url := p.peerURL + "/api/peer/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		p.recordFailure(fmt.Sprintf("create request: %v", err))
		return
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.recordFailure(fmt.Sprintf("request failed: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		p.recordFailure(fmt.Sprintf("status %d", resp.StatusCode))
		return
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		p.recordFailure(fmt.Sprintf("decode: %v", err))
		return
	}

	p.recordSuccess(health)
}

func (p *HealthPoller) recordSuccess(health HealthResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()

	wasUnreachable := !p.reachable && p.consecFailures > 0
	p.reachable = true
	p.lastSeen = time.Now()
	p.consecFailures = 0
	p.programSource = health.ProgramSource
	p.lastCommandSeq = health.LastCommandSeq
	p.uptimeMs = health.UptimeMs

	if wasUnreachable {
		slog.Info("peer engine recovered", "url", p.peerURL)
	}
}

func (p *HealthPoller) recordFailure(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.consecFailures++
	if p.consecFailures >= p.failureThreshold && p.reachable {
		p.reachable = false
		slog.Warn("peer engine unreachable",
			"url", p.peerURL,
			"consecutiveFailures", p.consecFailures,
			"reason", reason,
		)
	}
}
