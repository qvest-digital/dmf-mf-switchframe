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

	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/metrics"
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
		slog.Error("admin server listen failed", "addr", adminAddr, "err", err)
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
