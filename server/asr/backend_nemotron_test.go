//go:build !cgo || !cuda

package asr

import (
	"errors"
	"testing"
)

func TestNemotronBackend_StubReturnsError(t *testing.T) {
	_, err := NewNemotronBackend(NemotronConfig{ModelDir: "/tmp/nonexistent"})
	if err == nil {
		t.Fatal("expected error from stub nemotron backend")
	}
	if !errors.Is(err, ErrNemotronNotAvailable) {
		t.Fatalf("expected ErrNemotronNotAvailable, got: %v", err)
	}
}
