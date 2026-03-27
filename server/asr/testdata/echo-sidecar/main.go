// echo-sidecar is a minimal test helper that mimics the ASR sidecar binary.
// It reads PCM from shared memory and responds with canned transcript messages.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/zsiec/switchframe/server/asr/asripc"
	"github.com/zsiec/switchframe/server/asr/shmring"
)

func main() {
	shmName := flag.String("shm-name", "", "POSIX shared memory name")
	socketPath := flag.String("socket", "", "Unix socket path")
	_ = flag.String("model-dir", "", "Model directory (unused in echo)")
	_ = flag.Int("device", 0, "GPU device ID (unused in echo)")
	flag.Parse()

	if *shmName == "" || *socketPath == "" {
		fmt.Fprintln(os.Stderr, "echo-sidecar: --shm-name and --socket are required")
		os.Exit(1)
	}

	// Open the shared memory ring buffer (reader side).
	reader, err := shmring.NewReader(*shmName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "echo-sidecar: open shm: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	// Print READY to stdout — the parent watches for this.
	fmt.Println("READY")

	// Connect to the Unix socket.
	conn, err := net.Dial("unix", *socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "echo-sidecar: connect socket: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send initial status.
	_ = asripc.WriteMessage(conn, &asripc.Message{
		Type:  "status",
		State: "ready",
		Model: "echo-test-model",
	})

	// Start command reader goroutine.
	cmdCh := make(chan *asripc.Message, 8)
	go func() {
		for {
			msg, err := asripc.ReadMessage(conn)
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
			cmdCh <- msg
		}
	}()

	// Poll shared memory and respond with transcripts.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case msg := <-cmdCh:
			switch msg.Cmd {
			case "shutdown":
				os.Exit(0)
			case "reset":
				// Reset reader state by reading and discarding all available data.
				reader.Read()
			case "stop":
				// No-op for echo sidecar.
			case "set_config":
				// No-op for echo sidecar.
			}
		case <-ticker.C:
			data := reader.Read()
			if len(data) > 0 {
				// Send a canned transcript for any audio received.
				err := asripc.WriteMessage(conn, &asripc.Message{
					Type:    "transcript",
					IsFinal: true,
					Words: []asripc.Word{
						{Text: "echo", Confidence: 0.99, GapBefore: 0},
					},
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "echo-sidecar: write transcript: %v\n", err)
					return
				}
			}
		}
	}
}
