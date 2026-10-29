package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers on http.DefaultServeMux.
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/replay"
)

func init() {
	// Enable mutex and block profiling for pprof analysis.
	// Only enabled when SWITCHFRAME_PROFILING=1 to avoid overhead in production.
	if os.Getenv("SWITCHFRAME_PROFILING") == "1" {
		// Fraction=5 means ~20% of mutex contention events are sampled.
		runtime.SetMutexProfileFraction(5)
		// Rate=1000 means block events >= 1µs are recorded.
		runtime.SetBlockProfileRate(1000)
	}
}

// registerCollectors registers the collectors that read a subsystem's state
// at scrape time. A second registration of the same metrics, as when a test
// builds a second App in one process, is logged and the first one kept.
func registerCollectors(reg prometheus.Registerer, cs ...prometheus.Collector) {
	for _, c := range cs {
		if err := reg.Register(c); err != nil {
			slog.Warn("metrics collector not registered", "err", err)
		}
	}
}

// registerSubsystemCollectors registers the scrape-time collectors of the
// playout, clip, replay, graphics and HTML5 graphics subsystems, each only
// when it runs.
func (a *App) registerSubsystemCollectors() {
	if a.appMetrics == nil {
		return
	}
	var cs []prometheus.Collector
	if a.playoutMgr != nil {
		cs = append(cs, playout.NewMetricsCollector(a.playoutMgr, a.playoutCache))
	}
	if a.clipMgr != nil {
		cs = append(cs, clip.NewMetricsCollector(a.clipMgr))
	}
	if a.replayMgr != nil {
		cs = append(cs, replay.NewMetricsCollector(a.replayMgr))
	}
	if a.compositor != nil {
		cs = append(cs, graphics.NewMetricsCollector(a.compositor))
	}
	if a.htmlGfxMgr != nil {
		cs = append(cs, htmlgfx.NewMetricsCollector(a.htmlGfxMgr))
	}
	// Reads the peer poller, which initAPI creates after this, at scrape
	// time.
	cs = append(cs, &redundancyCollector{a: a})
	// initMXL has run: the configured inputs do not change after start.
	sources := a.mxlSources
	cs = append(cs, &mxlReaderCollector{sources: func() []*mxl.Source { return sources }})
	registerCollectors(metrics.GetRegistry(), cs...)
}

// readyFlag is set to true once all components are initialized and the server
// is ready to accept traffic. The /ready endpoint returns 503 until this is set.
var readyFlag atomic.Bool

// StartAdminServer launches an HTTP server on addr that exposes operational
// endpoints: Prometheus metrics, health/readiness probes, Go pprof, and the
// cert-hash endpoint for dev bootstrapping (Vite proxy can reach TCP :9090).
// If adminToken is non-empty, /metrics and /debug/* require a matching
// Bearer token; /health, /ready, and /api/cert-hash remain unauthenticated
// so that k8s probes and browser bootstrapping always work.
// It returns a stop function and the actual listen address (useful when
// adminAddr uses port 0 for auto-assignment).
func StartAdminServer(ctx context.Context, adminAddr, quicAddr, certHash string, trusted bool, adminToken string) (stop func(), listenAddr string) {
	mux := http.NewServeMux()

	// Admin auth middleware: protects sensitive endpoints when a token is configured.
	adminAuth := func(next http.Handler) http.Handler {
		if adminToken == "" {
			return next // No auth configured — pass through.
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			if !strings.HasPrefix(token, "Bearer ") ||
				subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(token, "Bearer ")), []byte(adminToken)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	// Prometheus metrics scrape endpoint (protected by admin token).
	mux.Handle("GET /metrics", adminAuth(metrics.Handler()))

	// Liveness probe — always 200 if the process is running. No auth.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Readiness probe — 503 until readyFlag is set, then 200. No auth.
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !readyFlag.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_ready"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})

	// Go pprof — net/http/pprof registers on http.DefaultServeMux via init().
	// Mount it under /debug/ so all /debug/pprof/* paths work. Protected by admin token.
	mux.Handle("/debug/", adminAuth(http.DefaultServeMux))

	// Cert-hash endpoint for dev bootstrapping (Vite proxy can reach TCP :9090).
	// Wrapped with CORSMiddleware to handle OPTIONS preflight from cross-origin browsers.
	// No auth — browsers need this to establish WebTransport connections.
	certHashHandler := control.CORSMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"hash":    certHash,
			"addr":    quicAddr,
			"trusted": trusted,
		})
	}))
	mux.Handle("/api/cert-hash", certHashHandler)

	srv := &http.Server{
		Handler: mux,
	}

	ln, err := net.Listen("tcp", adminAddr)
	if err != nil {
		// Best-effort: the process keeps running, but with NO /metrics scrape
		// endpoint, NO /health and /ready probes, and NO pprof. Log loudly and
		// name the disabled capabilities so operators can alert on it (e.g. a
		// k8s deployment would otherwise see opaque probe failures).
		slog.Error("admin server unavailable: metrics, health/readiness probes, and pprof are DISABLED",
			"addr", adminAddr, "err", err)
		return func() {}, ""
	}

	actualAddr := ln.Addr().String()

	go func() {
		slog.Info("admin server listening", "addr", actualAddr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("admin server error", "err", err)
		}
	}()

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("admin server shutdown error", "err", err)
		}
	}, actualAddr
}
