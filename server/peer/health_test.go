package peer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthPoller_PeerReachable(t *testing.T) {
	// Healthy peer endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Status:        "ok",
			UptimeMs:      12345,
			ProgramSource: "cam1",
			LastCommandSeq: 42,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewHealthPoller(srv.URL, WithPollInterval(50*time.Millisecond), WithFailureThreshold(3))
	p.Start()
	defer p.Stop()

	// Wait for at least one successful poll.
	require.Eventually(t, func() bool {
		return p.PeerReachable()
	}, 2*time.Second, 10*time.Millisecond, "peer should become reachable")

	assert.False(t, p.PeerLastSeen().IsZero(), "last seen should be set")

	status := p.Status()
	assert.True(t, status.Reachable)
	assert.Equal(t, "cam1", status.ProgramSource)
	assert.Equal(t, uint64(42), status.LastCommandSeq)
	assert.True(t, status.UptimeMs > 0)
}

func TestHealthPoller_PeerUnreachable(t *testing.T) {
	// Peer that always fails.
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewHealthPoller(srv.URL,
		WithPollInterval(50*time.Millisecond),
		WithFailureThreshold(3),
	)
	p.Start()
	defer p.Stop()

	// After 3 consecutive failures (150ms minimum), peer should be unreachable.
	require.Eventually(t, func() bool {
		return callCount.Load() >= 3
	}, 2*time.Second, 10*time.Millisecond)

	assert.False(t, p.PeerReachable(), "peer should be unreachable after 3 failures")
}

func TestHealthPoller_RecoveryAfterFailure(t *testing.T) {
	// Start unhealthy, then recover.
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		resp := HealthResponse{
			Status:   "ok",
			UptimeMs: 999,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewHealthPoller(srv.URL,
		WithPollInterval(50*time.Millisecond),
		WithFailureThreshold(2),
	)
	p.Start()
	defer p.Stop()

	// Wait for unreachable.
	require.Eventually(t, func() bool {
		return !p.PeerReachable()
	}, 2*time.Second, 10*time.Millisecond)

	// Make peer healthy again.
	healthy.Store(true)

	// Should recover.
	require.Eventually(t, func() bool {
		return p.PeerReachable()
	}, 2*time.Second, 10*time.Millisecond, "peer should recover after becoming healthy")
}

func TestHealthPoller_NoPeerConfigured(t *testing.T) {
	// Empty URL means no peer — should not be reachable.
	p := NewHealthPoller("")
	assert.False(t, p.PeerReachable())
	assert.True(t, p.PeerLastSeen().IsZero())
}

func TestHealthPoller_ConnectionRefused(t *testing.T) {
	// Use a URL that will refuse connections.
	p := NewHealthPoller("http://127.0.0.1:1",
		WithPollInterval(50*time.Millisecond),
		WithFailureThreshold(2),
	)
	p.Start()
	defer p.Stop()

	// Should never become reachable.
	time.Sleep(300 * time.Millisecond)
	assert.False(t, p.PeerReachable())
}

func TestHealthPoller_StopIdempotent(t *testing.T) {
	p := NewHealthPoller("http://localhost:9999",
		WithPollInterval(50*time.Millisecond),
	)
	p.Start()
	p.Stop()
	p.Stop() // Should not panic.
}

func TestHealthPoller_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	p := NewHealthPoller(srv.URL,
		WithPollInterval(50*time.Millisecond),
		WithFailureThreshold(2),
	)
	p.Start()
	defer p.Stop()

	// Invalid JSON counts as a failure.
	time.Sleep(300 * time.Millisecond)
	assert.False(t, p.PeerReachable())
}

func TestHealthPoller_StatusNil(t *testing.T) {
	// No peer configured — status should reflect that.
	p := NewHealthPoller("")
	status := p.Status()
	assert.False(t, status.Reachable)
	assert.True(t, status.LastSeen.IsZero())
}
