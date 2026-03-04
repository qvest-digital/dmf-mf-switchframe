// server/cmd/switchframe/main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/switcher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("switchframe starting")

	// Generate self-signed TLS certificate for WebTransport (≤14 days validity).
	cert, err := certs.Generate(14 * 24 * time.Hour)
	if err != nil {
		return fmt.Errorf("generate certificate: %w", err)
	}
	slog.Info("certificate generated",
		"fingerprint", cert.FingerprintBase64(),
		"expires", cert.NotAfter.Format(time.RFC3339))

	// Deferred pointer: closures below capture sw before it's assigned.
	// Safe because OnStreamRegistered is only called when external SRT
	// sources connect (after server.Start()), by which time sw is set.
	var sw *switcher.Switcher

	// Create channel-based state publisher for MoQ control track.
	controlPub := control.NewChannelPublisher(16)

	// Create REST API (captures sw pointer; called during server.Start()
	// mux setup, after sw is initialized below).
	var api *control.API

	addr := ":8080"

	config := distribution.ServerConfig{
		Addr: addr,
		Cert: cert,
		ExtraRoutes: func(mux *http.ServeMux) {
			api.RegisterOnMux(mux)
		},
		OnStreamRegistered: func(key string, relay *distribution.Relay) {
			// RegisterStream("program") triggers this callback immediately,
			// before sw is initialized. Guard against that.
			if key == "program" {
				return
			}
			slog.Info("stream registered, adding source", "key", key)
			sw.RegisterSource(key, relay)
		},
		OnStreamUnregistered: func(key string) {
			if key == "program" {
				return
			}
			slog.Info("stream unregistered, removing source", "key", key)
			sw.UnregisterSource(key)
		},
		ControlCh: controlPub.Ch(),
	}

	server, err := distribution.NewServer(config)
	if err != nil {
		return fmt.Errorf("create distribution server: %w", err)
	}

	// Get Prism's relay for "program" — MoQ viewers subscribe to this.
	programRelay := server.RegisterStream("program")

	// Create switcher with Prism's relay so frames reach MoQ viewers.
	sw = switcher.New(programRelay)
	defer sw.Close()

	// Create REST API now that switcher exists.
	api = control.NewAPI(sw)

	// Wire state publisher and health monitor.
	sw.OnStateChange(controlPub.Publish)
	sw.StartHealthMonitor(1 * time.Second)

	slog.Info("starting Prism distribution server", "addr", addr)
	return server.Start(ctx)
}
