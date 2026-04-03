package main

import (
	"os"
	"strings"
)

// scanWhisperModels scans dir for ggml-*.bin files, excluding Silero VAD models.
// Returns a list of model names (e.g., "base", "medium.en") extracted from filenames.
func scanWhisperModels(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var models []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "ggml-") || !strings.HasSuffix(name, ".bin") {
			continue
		}
		// Extract model name: ggml-base.bin -> "base"
		model := strings.TrimPrefix(name, "ggml-")
		model = strings.TrimSuffix(model, ".bin")

		// Skip Silero VAD models.
		if strings.Contains(strings.ToLower(model), "silero") {
			continue
		}

		models = append(models, model)
	}

	return models, nil
}

// defaultWhisperModel picks the best default from available models.
// Prefers medium.en, then medium, then base.en, then base, then first available.
func defaultWhisperModel(models []string) string {
	preference := []string{"medium.en", "medium", "base.en", "base"}
	for _, pref := range preference {
		for _, m := range models {
			if m == pref {
				return m
			}
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	return ""
}
