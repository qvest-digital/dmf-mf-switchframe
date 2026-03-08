package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/control"
)

// startHTTPAPIServer launches a plain HTTP/1.1 server on TCP :8081 that mirrors
// the REST API routes. Prism's distribution server only listens on QUIC/UDP, so
// the Vite dev proxy (and curl) can't reach it. This TCP listener provides the
// same API endpoints over standard HTTP.
//
// Returns a stop function that gracefully shuts down the server, or an error if
// the TCP listener cannot bind.
func (a *App) startHTTPAPIServer(ctx context.Context) (stop func(), err error) {
	apiMux := http.NewServeMux()
	a.api.RegisterOnMux(apiMux)

	var apiHandler http.Handler = apiMux
	apiHandler = a.operatorMW(apiHandler)
	apiHandler = control.MetricsMiddleware(apiHandler)
	apiHandler = control.LoggerMiddleware(slog.Default())(apiHandler)
	apiHandler = a.authMW(apiHandler)

	httpSrv := &http.Server{
		Handler: apiHandler,
	}
	httpLn, err := net.Listen("tcp", ":8081")
	if err != nil {
		return nil, fmt.Errorf("listen TCP :8081: %w", err)
	}

	go func() {
		slog.Info("HTTP API server listening", "addr", ":8081")
		if err := httpSrv.Serve(httpLn); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP API server error", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP API server shutdown error", "err", err)
		}
	}()

	return func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}, nil
}
