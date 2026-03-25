package neuralkey

import (
	"errors"
	"testing"
	"unsafe"
)

func TestStubNewEngineReturnsError(t *testing.T) {
	eng, err := NewEngine("/nonexistent.onnx", 1920, 1080)
	if eng != nil {
		t.Fatal("expected nil engine from stub")
	}
	if !errors.Is(err, ErrNeuralKeyNotAvailable) {
		t.Fatalf("expected ErrNeuralKeyNotAvailable, got %v", err)
	}
}

func TestStubInferReturnsError(t *testing.T) {
	var eng Engine
	err := eng.Infer(unsafe.Pointer(nil), unsafe.Pointer(nil), 1920, 1080, 2048,
		unsafe.Pointer(nil), unsafe.Pointer(nil), unsafe.Pointer(nil))
	if !errors.Is(err, ErrNeuralKeyNotAvailable) {
		t.Fatalf("expected ErrNeuralKeyNotAvailable, got %v", err)
	}
}

func TestStubWarmupDoneReturnsFalse(t *testing.T) {
	var eng Engine
	if eng.WarmupDone() {
		t.Fatal("expected WarmupDone() == false from stub")
	}
}

func TestStubCloseIsNoop(t *testing.T) {
	var eng Engine
	eng.Close() // should not panic
}
