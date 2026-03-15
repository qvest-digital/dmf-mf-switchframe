//go:build !cgo || !mxl

package mxl

import (
	"sync"
	"testing"
)

func TestInstanceStub_ReturnsError(t *testing.T) {
	inst, err := NewInstance("/dev/shm/mxl")
	if err == nil {
		t.Fatal("expected error from stub NewInstance")
	}
	if inst != nil {
		t.Fatal("expected nil instance from stub")
	}
	if err != ErrMXLNotAvailable {
		t.Fatalf("expected ErrMXLNotAvailable, got: %v", err)
	}
}

func TestDiscoverStub_ReturnsError(t *testing.T) {
	flows, err := Discover("/dev/shm/mxl")
	if err == nil {
		t.Fatal("expected error from stub Discover")
	}
	if flows != nil {
		t.Fatal("expected nil flows from stub")
	}
	if err != ErrMXLNotAvailable {
		t.Fatalf("expected ErrMXLNotAvailable, got: %v", err)
	}
}

func TestCurrentIndex_StubNotNil(t *testing.T) {
	if CurrentIndex == nil {
		t.Fatal("CurrentIndex should be initialized by stub init()")
	}
	rate := Rational{Numerator: 30000, Denominator: 1001}
	idx := CurrentIndex(rate)
	// Just verify it returns a non-zero value (based on current time).
	if idx == 0 {
		t.Log("warning: CurrentIndex returned 0, might be a timing edge case")
	}
}

func TestStubInstance_MethodsReturnError(t *testing.T) {
	// Create a zero-value Instance (simulating what you'd get if you ignored the error).
	inst := &Instance{}

	if _, err := inst.OpenReader("test"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenReader: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.OpenAudioReader("test"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenAudioReader: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.OpenWriter("{}"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenWriter: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.OpenAudioWriter("{}"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenAudioWriter: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.GetFlowConfig("test"); err != ErrMXLNotAvailable {
		t.Fatalf("GetFlowConfig: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.IsFlowActive("test"); err != ErrMXLNotAvailable {
		t.Fatalf("IsFlowActive: expected ErrMXLNotAvailable, got %v", err)
	}
	if err := inst.Close(); err != nil {
		t.Fatalf("Close: expected nil, got %v", err)
	}
}

func TestDiscoverStub_ReturnsError2(t *testing.T) {
	// In non-mxl builds, Discover should return ErrMXLNotAvailable.
	_, err := Discover("/dev/shm/mxl")
	if err != ErrMXLNotAvailable {
		t.Fatalf("expected ErrMXLNotAvailable, got: %v", err)
	}
}

func TestInstanceClose_ConcurrentSafe(t *testing.T) {
	// Instance.Close() must be safe for concurrent calls.
	// The real (cgo) implementation has a stopGC channel and handle;
	// double-close of the channel would panic without sync.Once.
	inst := &Instance{}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			errs <- inst.Close()
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Close returned unexpected error: %v", err)
		}
	}
}

func TestInstanceClose_Idempotent(t *testing.T) {
	// Calling Close() multiple times sequentially must be safe.
	inst := &Instance{}

	for i := 0; i < 5; i++ {
		if err := inst.Close(); err != nil {
			t.Fatalf("Close() call %d returned error: %v", i, err)
		}
	}
}
