package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanWhisperModels(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ggml-base.bin"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "ggml-medium.en.bin"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "ggml-silero-v6.2.0.bin"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "other.bin"), []byte("x"), 0644)

	models, err := scanWhisperModels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(models), models)
	}
}

func TestScanWhisperModelsEmpty(t *testing.T) {
	dir := t.TempDir()
	models, err := scanWhisperModels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("expected 0 models, got %d", len(models))
	}
}

func TestDefaultWhisperModel(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		want   string
	}{
		{"prefer medium.en", []string{"base", "medium.en", "large"}, "medium.en"},
		{"prefer medium over base", []string{"base", "medium"}, "medium"},
		{"fallback to base.en", []string{"tiny", "base.en"}, "base.en"},
		{"fallback to base", []string{"tiny", "base"}, "base"},
		{"fallback to first", []string{"large-v3", "tiny"}, "large-v3"},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultWhisperModel(tt.models)
			if got != tt.want {
				t.Errorf("defaultWhisperModel(%v) = %q, want %q", tt.models, got, tt.want)
			}
		})
	}
}
