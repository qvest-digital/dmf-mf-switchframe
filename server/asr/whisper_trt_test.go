package asr

import "testing"

func TestWhisperTRT_StubReturnsError(t *testing.T) {
	_, err := NewWhisperTRT(WhisperTRTConfig{ModelDir: "/nonexistent"})
	if err == nil {
		t.Fatal("expected error from stub or missing model")
	}
}

func TestWhisperTRT_StubEncodeReturnsError(t *testing.T) {
	w := &WhisperTRT{}
	_, err := w.Encode(nil)
	if err == nil {
		t.Fatal("expected error from stub Encode")
	}
}

func TestWhisperTRT_StubDecodeReturnsError(t *testing.T) {
	w := &WhisperTRT{}
	_, err := w.Decode(nil, nil)
	if err == nil {
		t.Fatal("expected error from stub Decode")
	}
}

func TestWhisperTRT_StubCloseNoOp(t *testing.T) {
	w := &WhisperTRT{}
	w.Close() // should not panic
}
