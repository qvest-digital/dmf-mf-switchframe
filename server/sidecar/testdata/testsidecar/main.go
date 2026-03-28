// testsidecar is a minimal sidecar process for testing the Host.
//
// Usage: testsidecar <socket-path>
//
// It connects to the host's Unix socket, prints "READY" to stdout,
// then loops reading control messages. Supported commands:
//   - cmd="echo":     sends back Message{Type: "echo", Data: msg.Data}
//   - cmd="crash":    os.Exit(1)
//   - cmd="shutdown": handled by Worker.Run (returns nil)
//
// On SIGTERM/SIGINT it exits cleanly.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zsiec/switchframe/server/sidecar"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: testsidecar <socket-path>")
		os.Exit(2)
	}

	w, err := sidecar.NewWorker(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	w.Ready()

	// Cancel on termination signals for clean shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	err = w.Run(ctx, func(msg *sidecar.Message) *sidecar.Message {
		switch msg.Cmd {
		case "crash":
			os.Exit(1)
		case "echo":
			return &sidecar.Message{Type: "echo", Data: msg.Data}
		}
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}
