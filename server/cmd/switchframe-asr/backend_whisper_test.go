package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanWhisperModels(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "ggml-base.bin"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "ggml-medium.en.bin"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "ggml-silero-v6.2.0.bin"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "other.bin"), []byte("x"), 0644)

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
		{
			"prefer large-v3-turbo-q5_0 over everything",
			[]string{"base.en", "medium.en", "large-v3", "large-v3-turbo-q5_0"},
			"large-v3-turbo-q5_0",
		},
		{
			"prefer turbo non-quantized when quant absent",
			[]string{"base.en", "medium.en", "large-v3", "large-v3-turbo"},
			"large-v3-turbo",
		},
		{
			"prefer turbo q8_0 over large-v3",
			[]string{"large-v3", "large-v3-turbo-q8_0"},
			"large-v3-turbo-q8_0",
		},
		{
			"fall to large-v3 when no turbo",
			[]string{"medium.en", "large-v3"},
			"large-v3",
		},
		{
			"fall to large-v2 when no large-v3 family present",
			[]string{"base", "medium.en", "large-v2"},
			"large-v2",
		},
		{
			"medium.en preferred when no large family at all",
			[]string{"base", "medium.en", "small"},
			"medium.en",
		},
		{"prefer medium over base", []string{"base", "medium"}, "medium"},
		{"fallback to base.en", []string{"tiny", "base.en"}, "base.en"},
		{"fallback to base", []string{"tiny", "base"}, "base"},
		{"fallback to first when nothing matches", []string{"tiny.en", "tiny"}, "tiny.en"},
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
