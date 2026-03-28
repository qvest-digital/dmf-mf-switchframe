// listenersidecar is a test sidecar that creates its own listener socket,
// simulating the CEF sidecar pattern (sidecar listens, host connects).
//
// Usage: listenersidecar <socket-dir>
//
// It creates a listener at <socket-dir>/control.sock, prints "READY",
// then waits for the host to connect and reads newline-delimited JSON.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

type msg struct {
	Cmd  string `json:"cmd,omitempty"`
	Type string `json:"type,omitempty"`
	Data any    `json:"data,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: listenersidecar <socket-dir>")
		os.Exit(2)
	}
	socketDir := os.Args[1]
	controlPath := filepath.Join(socketDir, "control.sock")

	// Create listener (sidecar creates, host connects).
	ln, err := net.Listen("unix", controlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	fmt.Println("READY")

	// Accept one connection from host.
	conn, err := ln.Accept()
	if err != nil {
		fmt.Fprintf(os.Stderr, "accept: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Handle signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		os.Exit(0)
	}()

	// Read newline-delimited JSON (like CEF protocol).
	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		var m msg
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		switch m.Cmd {
		case "crash":
			os.Exit(1)
		case "shutdown":
			os.Exit(0)
		case "echo":
			_ = encoder.Encode(msg{Type: "echo", Data: m.Data})
		}
	}
}
