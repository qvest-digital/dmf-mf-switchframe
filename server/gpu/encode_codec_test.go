//go:build cgo && darwin

package gpu

import (
	"testing"

	"github.com/zsiec/switchframe/server/switcher"
)

func TestNewGPUEncoderForCodec_NilContext(t *testing.T) {
	_, err := NewGPUEncoderForCodec(nil, 1920, 1080, 30, 1, 8_000_000, switcher.CodecAVC, switcher.ColorYUV420_8bit)
	if err != ErrGPUNotAvailable {
		t.Fatalf("expected ErrGPUNotAvailable, got %v", err)
	}
}

func TestNewGPUEncoderForCodec_AVCRoutesToExisting(t *testing.T) {
	// Can't test with nil context, but verify it returns ErrGPUNotAvailable correctly
	_, err := NewGPUEncoderForCodec(nil, 1920, 1080, 30, 1, 8_000_000, switcher.CodecAVC, switcher.ColorYUV420_8bit)
	if err != ErrGPUNotAvailable {
		t.Fatalf("expected ErrGPUNotAvailable for AVC with nil ctx, got %v", err)
	}
}

func TestNewGPUEncoderForCodec_HEVCNilContext(t *testing.T) {
	_, err := NewGPUEncoderForCodec(nil, 1920, 1080, 30, 1, 8_000_000, switcher.CodecHEVC, switcher.ColorYUV420_8bit)
	if err != ErrGPUNotAvailable {
		t.Fatalf("expected ErrGPUNotAvailable for HEVC with nil ctx, got %v", err)
	}
}

func TestNewGPUEncoderForCodec_HEVC10bitNilContext(t *testing.T) {
	_, err := NewGPUEncoderForCodec(nil, 1920, 1080, 30, 1, 12_000_000, switcher.CodecHEVC, switcher.ColorYUV422_10bit)
	if err != ErrGPUNotAvailable {
		t.Fatalf("expected ErrGPUNotAvailable for HEVC 10-bit with nil ctx, got %v", err)
	}
}

func TestGPUEncoder_IsHEVC_Default(t *testing.T) {
	// A zero-value GPUEncoder should report IsHEVC() == false (AVC default)
	enc := &GPUEncoder{}
	if enc.IsHEVC() {
		t.Fatal("expected IsHEVC() == false for zero-value encoder")
	}
}
