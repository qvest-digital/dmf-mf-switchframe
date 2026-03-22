//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContextSyncCloseConcurrent(t *testing.T) {
	// This test validates that Sync() and Close() can run concurrently
	// without racing on the stream/queue resources.
	ctx, err := NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: call Sync repeatedly.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = ctx.Sync()
		}
	}()

	// Goroutine 2: close the context mid-flight.
	go func() {
		defer wg.Done()
		_ = ctx.Close()
	}()

	wg.Wait()

	// After close, Sync should be a no-op.
	err = ctx.Sync()
	require.NoError(t, err)
}

func TestContextSyncAfterClose(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}

	require.NoError(t, ctx.Close())

	// Sync after close must not crash or error.
	err = ctx.Sync()
	require.NoError(t, err)
}
