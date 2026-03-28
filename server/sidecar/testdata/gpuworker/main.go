// gpuworker is a test binary that verifies CUDA IPC handle import works
// cross-process. It connects to the host's socket, receives a GPU pool
// handshake, imports the IPC handles, writes a marker to the shared
// control region, and reports success.
//
// Usage: gpuworker <socket-path>
//
//go:build cgo && cuda

package main

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zsiec/switchframe/server/sidecar"
)

func initCUDA() error {
	if rc := C.cuInit(0); rc != C.CUDA_SUCCESS {
		return fmt.Errorf("cuInit: error %d", rc)
	}
	if rc := C.cudaSetDevice(0); rc != C.cudaSuccess {
		return fmt.Errorf("cudaSetDevice: error %d", rc)
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gpuworker <socket-path>")
		os.Exit(2)
	}

	// Initialize CUDA runtime before importing IPC handles.
	if err := initCUDA(); err != nil {
		fmt.Fprintf(os.Stderr, "cuda init: %v\n", err)
		os.Exit(1)
	}

	w, err := sidecar.NewWorker(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	w.Ready()

	// Read the GPU pool handshake from the host.
	hs, err := sidecar.ReadGPUPoolHandshake(w.Conn())
	if err != nil {
		fmt.Fprintf(os.Stderr, "read handshake: %v\n", err)
		_ = w.Send(&sidecar.Message{Type: "error", Error: fmt.Sprintf("read handshake: %v", err)})
		os.Exit(1)
	}

	// Import the handles.
	pool, err := sidecar.OpenCUDAGPUPoolWorker(
		hs.FrameHandles,
		hs.ResultHandles,
		hs.ReadyEvents,
		hs.DoneEvents,
		hs.Config,
		hs.ShmName,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open pool: %v\n", err)
		_ = w.Send(&sidecar.Message{Type: "error", Error: fmt.Sprintf("open pool: %v", err)})
		os.Exit(1)
	}
	defer pool.Close()

	// Verify device pointers are non-zero.
	for i := 0; i < pool.SlotCount(); i++ {
		if pool.FrameDevPtr(i) == 0 {
			_ = w.Send(&sidecar.Message{Type: "error", Error: fmt.Sprintf("FrameDevPtr(%d) = 0", i)})
			os.Exit(1)
		}
	}

	// Verify control region: check that slot 0 is in Ready state
	// (host marks it ready before sending handshake in the test).
	ctrl := pool.Control()
	if ctrl == nil {
		_ = w.Send(&sidecar.Message{Type: "error", Error: "control region is nil"})
		os.Exit(1)
	}

	state := ctrl.LoadState(0)
	pts := ctrl.LoadPTS(0)

	// Mark slot 0 as processing (worker acknowledges).
	ctrl.MarkProcessing(0)

	// Mark slot 0 as done.
	ctrl.MarkDone(0)

	// Report success with the state we observed.
	_ = w.Send(&sidecar.Message{
		Type: "status",
		Data: map[string]any{
			"slot0_state": state.String(),
			"slot0_pts":   pts,
			"slot_count":  pool.SlotCount(),
		},
	})

	// Wait for shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	for {
		msg, err := w.Recv()
		if err != nil {
			return
		}
		if msg.Cmd == "shutdown" {
			return
		}
	}
}
