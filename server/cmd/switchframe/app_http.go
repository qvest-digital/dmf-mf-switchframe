package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/oidcauth"
)

// altSvcMiddleware adds the Alt-Svc header to advertise HTTP/3 on the QUIC port.
// Browsers use this to upgrade from TCP HTTPS to QUIC WebTransport.
func altSvcMiddleware(quicPort string, next http.Handler) http.Handler {
	altSvc := fmt.Sprintf(`h3=":%s"; ma=86400`, quicPort)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", altSvc)
		next.ServeHTTP(w, r)
	})
}

// certHashHandler serves /api/cert-hash with the API's CORS headers: the UI
// of one engine fetches the other engine's before opening WebTransport to it.
func (a *App) certHashHandler() http.Handler {
	return control.CORSMiddleware(a.cfg.AllowedOrigins)(http.HandlerFunc(a.handleCertHash))
}

// authHandler serves the sign-in endpoints outside the API's middleware:
// they are reached before the browser holds any token. CORS lets the UI
// refresh its token at the peer engine while its own engine is down.
func (a *App) authHandler() http.Handler {
	return control.CORSMiddleware(a.cfg.AllowedOrigins)(oidcauth.Handler(a.oidcAuth))
}

// startHTTPAPIServer launches an HTTP/1.1 server for curl/scripts and browsers
// that can't directly speak HTTP/3. Enabled via --http-fallback flag.
//
// When TLS cert/key are provided, or --https-self-signed is set, it also
// starts a TCP HTTPS server on the same port as the QUIC listener (e.g., 443)
// with Alt-Svc headers to guide browsers from TCP HTTPS → QUIC WebTransport.
//
// Returns a stop function that gracefully shuts down the server(s), or an error
// if the TCP listener cannot bind.
func (a *App) startHTTPAPIServer(ctx context.Context) (stop func(), err error) {
	apiMux := http.NewServeMux()
	a.api.RegisterOnMux(apiMux)

	// Middleware chain matches ExtraRoutes: CORS -> logger -> metrics -> download ticket -> auth -> operator -> maxbytes
	var apiHandler http.Handler = apiMux
	apiHandler = control.MaxBytesMiddleware(apiHandler)
	unauthenticated := apiHandler
	apiHandler = a.operatorMW(apiHandler)
	apiHandler = a.authMW(apiHandler)
	apiHandler = a.api.DownloadTicketMiddleware(apiHandler, unauthenticated)
	apiHandler = control.RouteMetricsMiddleware(apiMux)(apiHandler)
	apiHandler = control.RouteLoggerMiddleware(slog.Default(), apiMux)(apiHandler)
	apiHandler = control.CORSMiddleware(a.cfg.AllowedOrigins)(apiHandler)

	// Top-level mux: cert-hash (public) + API routes (authenticated) +
	// sign-in (public) + UI files (public).
	topMux := http.NewServeMux()
	topMux.Handle("GET /api/cert-hash", a.certHashHandler())
	topMux.Handle("/api/", apiHandler)
	topMux.Handle("/auth/", a.authHandler())
	if h := uiHandler(); h != nil {
		topMux.Handle("/", h)
	}

	// Extract the QUIC port from the addr flag for Alt-Svc header.
	_, quicPort, _ := net.SplitHostPort(a.cfg.Addr)
	if quicPort == "" {
		quicPort = "443"
	}

	var stops []func()

	// Plain HTTP fallback on --http-addr (e.g., :8081).
	httpSrv := &http.Server{
		Handler: control.SecurityHeadersMiddleware(topMux),
	}
	httpLn, err := net.Listen("tcp", a.cfg.HTTPAddr)
	if err != nil {
		return nil, fmt.Errorf("listen TCP %s: %w", a.cfg.HTTPAddr, err)
	}

	go func() {
		if a.cfg.TLSCert != "" && a.cfg.TLSKey != "" {
			slog.Info("HTTPS fallback server listening", "addr", a.cfg.HTTPAddr)
			if err := httpSrv.ServeTLS(httpLn, a.cfg.TLSCert, a.cfg.TLSKey); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTPS fallback server error", "err", err)
			}
		} else {
			slog.Info("HTTP API server listening", "addr", a.cfg.HTTPAddr)
			if err := httpSrv.Serve(httpLn); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP API server error", "err", err)
			}
		}
	}()
	stops = append(stops, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	})

	// TCP HTTPS on the QUIC port (e.g., :443) — browsers land here first,
	// then upgrade to QUIC via the Alt-Svc header. With --https-self-signed
	// it serves the generated certificate the QUIC listener uses: a page
	// loaded over HTTPS is a secure context once the operator accepts the
	// certificate, which WebTransport needs, while one loaded over plain HTTP
	// from anywhere but localhost is not.
	if (a.cfg.TLSCert != "" && a.cfg.TLSKey != "") || a.cfg.HTTPSSelfSigned {
		cert, loadErr := a.cert.TLSCert, error(nil)
		if a.cfg.TLSCert != "" && a.cfg.TLSKey != "" {
			cert, loadErr = tls.LoadX509KeyPair(a.cfg.TLSCert, a.cfg.TLSKey)
		}
		if loadErr != nil {
			slog.Warn("failed to load TLS cert for HTTPS fallback", "error", loadErr)
		} else {
			httpsHandler := altSvcMiddleware(quicPort, control.SecurityHeadersMiddleware(topMux))
			httpsSrv := &http.Server{
				Handler: httpsHandler,
				TLSConfig: &tls.Config{
					Certificates: []tls.Certificate{cert},
					MinVersion:   tls.VersionTLS12,
				},
			}
			httpsLn, listenErr := net.Listen("tcp", a.cfg.Addr)
			if listenErr != nil {
				slog.Warn("failed to listen TCP on QUIC addr for HTTPS fallback", "addr", a.cfg.Addr, "error", listenErr)
			} else {
				go func() {
					slog.Info("HTTPS fallback server listening", "addr", a.cfg.Addr)
					if serveErr := httpsSrv.ServeTLS(httpsLn, "", ""); serveErr != nil && serveErr != http.ErrServerClosed {
						slog.Error("HTTPS fallback server error", "err", serveErr)
					}
				}()
				stops = append(stops, func() {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = httpsSrv.Shutdown(shutdownCtx)
				})
			}
		}
	}

	return func() {
		for _, s := range stops {
			s()
		}
	}, nil
}
