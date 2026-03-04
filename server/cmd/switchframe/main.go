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

	// Create program relay for switched output.
	programRelay := distribution.NewRelay()

	// Create switcher.
	sw := switcher.New(programRelay)
	defer sw.Close()

	// Create REST API.
	api := control.NewAPI(sw)

	// Create channel-based state publisher for MoQ control track.
	controlPub := control.NewChannelPublisher(16)
	sw.OnStateChange(controlPub.Publish)

	// Start health monitor (checks every second).
	sw.StartHealthMonitor(1 * time.Second)

	addr := ":8080"

	config := distribution.ServerConfig{
		Addr: addr,
		Cert: cert,
		ExtraRoutes: func(mux *http.ServeMux) {
			api.RegisterOnMux(mux)
		},
		OnStreamRegistered: func(key string, relay *distribution.Relay) {
			slog.Info("stream registered, adding source", "key", key)
			sw.RegisterSource(key, relay)
		},
		OnStreamUnregistered: func(key string) {
			slog.Info("stream unregistered, removing source", "key", key)
			sw.UnregisterSource(key)
		},
		ControlCh: controlPub.Ch(),
	}

	server, err := distribution.NewServer(config)
	if err != nil {
		return fmt.Errorf("create distribution server: %w", err)
	}

	// Register the program relay so MoQ viewers can subscribe to "program".
	server.RegisterStream("program")
	// Replace the auto-created relay with our programRelay by wiring
	// the switcher's output into the server's relay for "program".
	// TODO: Prism doesn't expose relay replacement; for now the program
	// relay is separate. Viewers watch "program" via the server's relay,
	// and we bridge frames in a future integration step. This is a known
	// gap documented in tech-debt.md.

	slog.Info("starting Prism distribution server", "addr", addr)
	return server.Start(ctx)
}
