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

// defaultWhisperModel picks the best default from available models for
// live broadcast captioning. Order: large-v3-turbo (any quantization,
// preferring smaller q variants for speed) -> large-v3 -> large-v2 ->
// medium.en -> medium -> base.en -> base. Falls through to the first
// available if none of the preferred names match. Quantization suffixes
// (-q5_0, -q5_1, -q8_0, etc.) are matched alongside the unquantized
// variant.
func defaultWhisperModel(models []string) string {
	if len(models) == 0 {
		return ""
	}

	// Each preference is a base name; we accept it directly or with any
	// of the common GGML quantization suffixes appended. Within a base
	// name, smaller (faster) quantizations are preferred over the full
	// precision file -- they're broadcast-grade in practice and noticeably
	// quicker.
	bases := []string{
		"large-v3-turbo",
		"large-v3",
		"large-v2",
		"medium.en",
		"medium",
		"base.en",
		"base",
		"small.en",
		"small",
	}
	suffixOrder := []string{"-q5_0", "-q5_1", "-q8_0", "-q4_0", "-q4_1", ""}

	for _, base := range bases {
		for _, suf := range suffixOrder {
			candidate := base + suf
			for _, m := range models {
				if m == candidate {
					return m
				}
			}
		}
	}
	return models[0]
}
