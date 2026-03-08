package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers on http.DefaultServeMux.
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/metrics"
)

// readyFlag is set to true once all components are initialized and the server
// is ready to accept traffic. The /ready endpoint returns 503 until this is set.
var readyFlag atomic.Bool

// StartAdminServer launches an HTTP server on addr that exposes operational
// endpoints: Prometheus metrics, health/readiness probes, Go pprof, and the
// cert-hash endpoint for dev bootstrapping (Vite proxy can reach TCP :9090).
// It returns a stop function that gracefully shuts down the server.
func StartAdminServer(ctx context.Context, adminAddr, quicAddr, certHash string) (stop func()) {
	mux := http.NewServeMux()

	// Prometheus metrics scrape endpoint.
	mux.Handle("GET /metrics", metrics.Handler())

	// Liveness probe — always 200 if the process is running.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Readiness probe — 503 until readyFlag is set, then 200.
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
	// Mount it under /debug/ so all /debug/pprof/* paths work.
	mux.Handle("/debug/", http.DefaultServeMux)

	// Cert-hash endpoint for dev bootstrapping (Vite proxy can reach TCP :9090).
	mux.HandleFunc("GET /api/cert-hash", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"hash": certHash,
			"addr": quicAddr,
		})
	})

	srv := &http.Server{
		Handler: mux,
	}

	ln, err := net.Listen("tcp", adminAddr)
	if err != nil {
		slog.Error("admin server listen failed", "addr", adminAddr, "err", err)
		return func() {}
	}

	go func() {
		slog.Info("admin server listening", "addr", adminAddr)
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
	}
}
