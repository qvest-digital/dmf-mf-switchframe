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

// startHTTPAPIServer launches a plain HTTP/1.1 server on TCP :8081 for
// curl/scripts that can't speak HTTP/3. Enabled via --http-fallback flag.
//
// Returns a stop function that gracefully shuts down the server, or an error if
// the TCP listener cannot bind.
func (a *App) startHTTPAPIServer(ctx context.Context) (stop func(), err error) {
	apiMux := http.NewServeMux()
	a.api.RegisterOnMux(apiMux)

	// Cert-hash outside auth chain (browsers need it before they have tokens).
	apiMux.HandleFunc("GET /api/cert-hash", a.handleCertHash)

	// Middleware chain matches ExtraRoutes: CORS -> logger -> metrics -> auth -> operator -> maxbytes
	var apiHandler http.Handler = apiMux
	apiHandler = control.MaxBytesMiddleware(apiHandler)
	apiHandler = a.operatorMW(apiHandler)
	apiHandler = a.authMW(apiHandler)
	apiHandler = control.MetricsMiddleware(apiHandler)
	apiHandler = control.LoggerMiddleware(slog.Default())(apiHandler)
	apiHandler = control.CORSMiddleware(nil)(apiHandler)

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

	return func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP API server shutdown error", "err", err)
		}
	}, nil
}
